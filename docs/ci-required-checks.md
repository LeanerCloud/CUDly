# CI Required Status Checks

This document records the canonical set of required status checks for the
CUDly repository and the admin commands needed to enforce them.

## Context

PR #232 (merged 2026-05-03) added `.github/workflows/frontend-build.yml`,
which runs `npm ci`, `npm run typecheck`, and `npm run build` on every PR
touching `frontend/**`. The workflow shipped, but the corresponding
`Build frontend` check was never added to the branch-protection
required-checks list. A PR that breaks the frontend build can currently be
merged without a block (issue #377).

> **Note on context naming**: GitHub branch-protection contexts are derived
> from the **job `name:`** field, not the workflow `name:` field. The workflow
> is named `Frontend build (PR)` but its single job is named `Build frontend`,
> so the context GitHub reports (and that must be entered here) is
> `Build frontend`.

## Required checks -- canonical list

Both `feat/multicloud-web-frontend` and `main` should require:

- `Build frontend`
- Any other checks already present at the time an admin applies this runbook
  (retrieve the live list first; see Step 1 below).

## Admin steps

These commands require repo-admin permission on LeanerCloud/CUDly.

### Step 1 -- retrieve the current required checks for each branch

```bash
gh api repos/LeanerCloud/CUDly/branches/feat/multicloud-web-frontend/protection \
  --jq '.required_status_checks.contexts'

gh api repos/LeanerCloud/CUDly/branches/main/protection \
  --jq '.required_status_checks.contexts'
```

If either returns `"Branch not protected"` (HTTP 404), the protection rule
must be created from scratch; use the `gh api -X PUT` form in Step 3.

### Step 2 -- add the check to an already-protected branch

Replace `EXISTING_CHECK_1` etc. with the contexts returned by Step 1:

```bash
gh api -X PATCH \
  repos/LeanerCloud/CUDly/branches/feat/multicloud-web-frontend/protection/required_status_checks \
  -F 'contexts[]=EXISTING_CHECK_1' \
  -F 'contexts[]=Build frontend'

gh api -X PATCH \
  repos/LeanerCloud/CUDly/branches/main/protection/required_status_checks \
  -F 'contexts[]=EXISTING_CHECK_1' \
  -F 'contexts[]=Build frontend'
```

### Step 3 -- create protection from scratch (if Step 1 returned 404)

Adjust `required_pull_request_reviews` and `enforce_admins` to taste:

```bash
gh api -X PUT \
  repos/LeanerCloud/CUDly/branches/feat/multicloud-web-frontend/protection \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Build frontend"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null
}
EOF

gh api -X PUT \
  repos/LeanerCloud/CUDly/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Build frontend"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null
}
EOF
```

### Step 4 -- verify

```bash
gh api repos/LeanerCloud/CUDly/branches/feat/multicloud-web-frontend/protection \
  --jq '.required_status_checks.contexts'

gh api repos/LeanerCloud/CUDly/branches/main/protection \
  --jq '.required_status_checks.contexts'
```

Both should include `"Build frontend"`.

### Step 5 -- smoke test

Open a draft PR with a deliberate TypeScript error in `frontend/` and confirm
that the `Build frontend` check turns red and the merge button is blocked.

## Notes

- The `strict` flag in Step 3 requires the branch to be up to date before
  merging; adjust if that is too strict for the current workflow.
- The GitHub UI path for the same change is: Settings > Branches >
  Branch protection rules > Edit rule > "Require status checks to pass" >
  search for `Build frontend` > save.
