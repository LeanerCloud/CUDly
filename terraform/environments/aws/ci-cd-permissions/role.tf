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
          StringLike = {
            "token.actions.githubusercontent.com:sub" = "repo:${var.github_repo}:*"
          }
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
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

resource "aws_iam_role_policy_attachment" "data" {
  role       = aws_iam_role.cudly_deploy.name
  policy_arn = aws_iam_policy.data.arn
}
