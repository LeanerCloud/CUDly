data "aws_caller_identity" "current" {}

resource "aws_iam_role" "cudly_deploy" {
  name = "cudly-terraform-deploy"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      # Optional: allow a human IAM principal for local deployments
      var.trust_principal != "" ? [{
        Effect    = "Allow"
        Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:${var.trust_principal}" }
        Action    = "sts:AssumeRole"
      }] : [],
      # Optional: allow GitHub Actions via OIDC
      var.github_repo != "" ? [{
        Effect = "Allow"
        Principal = {
          Federated = aws_iam_openid_connect_provider.github[0].arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
            # Restrict to protected branches and named deployment environments.
            # Any-branch wildcard (repo:org/repo:*) would let a developer push to
            # an unprotected feature branch and mint valid deploy credentials.
            #
            # An OIDC `sub` is EITHER `...:ref:refs/heads/<branch>` OR
            # `...:environment:<name>` -- never both -- so every job in every
            # AWS-assuming workflow that binds to an `environment:` needs its
            # exact subject listed here, or it cannot authenticate at all. This
            # list is derived from grepping every `role-to-assume: ${{
            # vars.AWS_ROLE_TO_ASSUME }}` step across .github/workflows/ and
            # tracing each job's `environment:` value to its source (see #1648
            # and the PR that added this comment for the full per-job table).
            # `StringEquals` (exact match, not `StringLike`) on purpose: every
            # value below is enumerable from a `type: choice` input, so a
            # pattern would only widen what this policy admits, never narrow
            # it usefully.
            #
            # THIS LIST DOES NOT BY ITSELF RESTRICT DEPLOYS TO `main`. Owner
            # constraint is "only main may deploy". Three DIFFERENT controls
            # keep getting conflated in this repo's issues; this list is only
            # the first of them:
            #   1. Allowlist membership (THIS list): can the job authenticate
            #      to AWS at all? Nothing else below matters if this says no.
            #   2. Deployment branch policy (GitHub environment setting,
            #      manual, NOT configured by this module): which BRANCH may
            #      trigger a deploy to that environment. This is what "only
            #      main may deploy" actually requires, and nothing here sets
            #      it.
            #   3. Required reviewers / protection rules (GitHub environment
            #      setting, manual, NOT configured by this module): WHO must
            #      approve before a job bound to that environment proceeds.
            #      The subject of #1591/#1674/#1660, not this file.
            #
            # `ref:refs/heads/main` enforces (2) on its own, for an unbound
            # job: no environment, no policy needed, the ref check IS the
            # restriction. `environment:<name>` subjects enforce NEITHER (2)
            # nor (3) on their own: they are ref-agnostic (the subject encodes
            # which environment the job bound to, not which branch triggered
            # the run), so a `workflow_dispatch` from ANY branch against an
            # environment-bound job presents the identical subject a `main`
            # run would. Until a deployment branch policy exists on that
            # environment, this allowlist delivers "only these environments
            # deploy, from any branch", not "only main deploys" -- and per a
            # live check against the GitHub API, none of the environments
            # below have one, or any protection rules, today. See the PR that
            # added this comment for the full list of environments needing
            # that policy, and which of them exist yet at all.
            #
            # Deliberately NOT listed, and why:
            #   - `pull_request` subjects: no job that assumes THIS role
            #     (cudly_deploy) runs on `pull_request`. `aws_sanity.yml` is the
            #     only pull_request-triggered workflow that calls
            #     configure-aws-credentials, and it assumes a separate,
            #     already-read-only role via `secrets.AWS_CICD_READONLY_ROLE_ARN`,
            #     not this one. Also note GitHub does not mint OIDC tokens for
            #     `pull_request` runs from forked repos by default, independent
            #     of this policy.
            #   - `environment:aws-db-<anything>` from database-migration.yml's
            #     `workflow_call` trigger: that trigger declares `environment`
            #     as an unconstrained `type: string`, but nothing in this repo
            #     currently calls this workflow via `workflow_call` (grepped
            #     `.github/workflows/` for `uses:.*database-migration.yml`: no
            #     hits). Only the `workflow_dispatch` path, constrained to
            #     dev/staging/prod, is reachable today -- covered below. If a
            #     caller is ever added, this must be revisited before that
            #     caller can authenticate.
            "token.actions.githubusercontent.com:sub" = [
              "repo:${var.github_repo}:ref:refs/heads/main",

              # Bare deployment environments. Bound directly by
              # deploy-aws-lambda.yml's build-and-deploy / test-deployment jobs
              # (environment: ${{ needs.prepare.outputs.environment }}, always
              # dev/staging/prod per that job's own resolution logic), and --
              # once #1674 merges -- by cleanup-staging.yml's two AWS destroy
              # jobs (environment: staging) and destroy-fargate-dev.yml's
              # destroy job (environment: dev).
              # Live check (`gh api repos/.../environments`): `dev` exists,
              # `staging`/`prod` do not. None has a deployment branch policy or
              # protection rules -- see the top-of-list note.
              "repo:${var.github_repo}:environment:dev",
              "repo:${var.github_repo}:environment:staging",
              "repo:${var.github_repo}:environment:prod",

              # deploy-aws-fargate.yml's `deploy` job binds to
              # `aws-fargate-${{ needs.prepare.outputs.environment }}`, and that
              # output is always dev/staging/prod (workflow_dispatch choice
              # input, workflow_call from deploy-all.yml which is itself
              # choice-constrained, or the untriggered-input "dev" fallback).
              # Live check: `aws-fargate-dev` and `aws-fargate-staging` exist,
              # `aws-fargate-prod` does not. Same "no branch policy, no
              # protection rules" caveat as above applies to all three.
              "repo:${var.github_repo}:environment:aws-fargate-dev",
              "repo:${var.github_repo}:environment:aws-fargate-staging",
              "repo:${var.github_repo}:environment:aws-fargate-prod",

              # database-migration.yml's `migrate-aws` job binds to
              # `aws-db-${{ inputs.environment }}` on its `workflow_dispatch`
              # trigger, where `inputs.environment` is a choice input
              # constrained to dev/staging/prod. See the workflow_call caveat
              # above for what is deliberately excluded. Live check: none of
              # the three exist yet.
              "repo:${var.github_repo}:environment:aws-db-dev",
              "repo:${var.github_repo}:environment:aws-db-staging",
              "repo:${var.github_repo}:environment:aws-db-prod",

              # rollback.yml's rollback-aws-lambda / rollback-aws-fargate jobs
              # bind to `aws-lambda-${{ inputs.environment }}-rollback` /
              # `aws-fargate-${{ inputs.environment }}-rollback`; inputs.environment
              # is a workflow_dispatch choice input constrained to
              # dev/staging/prod. Listing the subject here only fixes control
              # (1) above (this issue, #1648); it does nothing for controls
              # (2) or (3). Live check: none of these six exist yet -- first
              # dispatch auto-creates them bare, with neither a branch policy
              # nor reviewers, unless #1660's provisioning work lands first.
              "repo:${var.github_repo}:environment:aws-lambda-dev-rollback",
              "repo:${var.github_repo}:environment:aws-lambda-staging-rollback",
              "repo:${var.github_repo}:environment:aws-lambda-prod-rollback",
              "repo:${var.github_repo}:environment:aws-fargate-dev-rollback",
              "repo:${var.github_repo}:environment:aws-fargate-staging-rollback",
              "repo:${var.github_repo}:environment:aws-fargate-prod-rollback",
            ]
          }
        }
      }] : []
    )
  })

  tags = {
    Project   = "CUDly"
    ManagedBy = "terraform"
  }
}

resource "aws_iam_role_policy_attachment" "networking" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.networking.arn
}

resource "aws_iam_role_policy_attachment" "compute" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.compute.arn
}

resource "aws_iam_role_policy_attachment" "compute_b" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.compute_b.arn
}

resource "aws_iam_role_policy_attachment" "data" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.data.arn
}

resource "aws_iam_role_policy_attachment" "iam" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.iam.arn
}
