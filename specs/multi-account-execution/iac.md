<!-- markdownlint-disable MD040 MD060 -->
# Infrastructure as Code Changes

All Terraform changes follow the existing project conventions: modules under `terraform/modules/`, environment configs under `terraform/environments/{aws,azure,gcp}/`.

---

## AWS Environment (`terraform/environments/aws/`)

### New secret: credential encryption key

The existing project creates all secrets through the `modules/secrets/aws` module (invoked in `terraform/environments/aws/secrets.tf` with `create_jwt_secret = true`, etc.). The credential encryption key follows the same pattern.

**Step 1 — Add module variable to `terraform/modules/secrets/aws/variables.tf`:**

```hcl
variable "create_credential_encryption_key" {
  description = "Whether to create a Secrets Manager secret for the credential encryption key"
  type        = bool
  default     = false  # Opt-in; environments without multi-account can skip
}

variable "credential_encryption_key" {
  description = "AES-256-GCM key for account credential encryption (64-char hex = 32 bytes). Generate: openssl rand -hex 32"
  type        = string
  sensitive   = true
  default     = ""
}
```

**Step 2 — Add resource to `terraform/modules/secrets/aws/main.tf`** (following existing `aws_secretsmanager_secret` patterns in that file):

```hcl
resource "aws_secretsmanager_secret" "credential_encryption_key" {
  count                   = var.create_credential_encryption_key ? 1 : 0
  name_prefix             = "${var.stack_name}-credential-enc-key-"
  description             = "AES-256-GCM key for encrypting cloud account credentials"
  recovery_window_in_days = var.secret_recovery_window_days

  tags = merge(var.common_tags, {
    Name      = "${var.stack_name}-credential-enc-key"
    ManagedBy = "terraform"
  })
}

resource "aws_secretsmanager_secret_version" "credential_encryption_key" {
  count         = var.create_credential_encryption_key ? 1 : 0
  secret_id     = aws_secretsmanager_secret.credential_encryption_key[0].id
  secret_string = var.credential_encryption_key

  lifecycle {
    ignore_changes = [secret_string]  # Allow out-of-band rotation without Terraform drift
  }
}
```

**Step 3 — Add output to `terraform/modules/secrets/aws/outputs.tf`** (following the existing `secret_env_vars` output pattern):

```hcl
output "credential_encryption_key_secret_arn" {
  value       = var.create_credential_encryption_key ? aws_secretsmanager_secret.credential_encryption_key[0].arn : ""
  description = "ARN of the credential encryption key secret; empty string if not created"
}
```

**Step 4 — Enable in `terraform/environments/aws/secrets.tf`** (update the existing `module "secrets"` invocation):

```hcl
module "secrets" {
  source = "../../modules/secrets/aws"
  # ... existing args ...
  create_credential_encryption_key = true
  credential_encryption_key        = var.credential_encryption_key
}
```

**Step 5 — Add `credential_encryption_key` variable to `terraform/environments/aws/variables.tf`:**

```hcl
variable "credential_encryption_key" {
  description = "AES-256-GCM key for account credential encryption (64-char hex = 32 bytes). Generate: openssl rand -hex 32"
  type        = string
  sensitive   = true
  # No default — must be explicitly set; for local dev use CREDENTIAL_ENCRYPTION_KEY env var directly
}
```

Pass the secret ARN as an env var to the Lambda module in `terraform/environments/aws/compute.tf`:

```hcl
additional_env_vars = merge(
  {
    STATIC_DIR                           = "/app/static"
    DASHBOARD_URL                        = local.dashboard_url
    CORS_ALLOWED_ORIGIN                  = local.dashboard_url != "" ? local.dashboard_url : "*"
    FROM_EMAIL                           = "noreply@${var.subdomain_zone_name}"
    CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN = module.secrets.credential_encryption_key_secret_arn
    CUDLY_MAX_ACCOUNT_PARALLELISM        = var.max_account_parallelism
  },
  var.additional_env_vars
)
```

Add `max_account_parallelism` variable to `variables.tf`:

```hcl
variable "max_account_parallelism" {
  description = "Maximum number of cloud accounts to process in parallel during plan fan-out"
  type        = number
  default     = 10
}
```

---

## Lambda IAM Policy Updates (`terraform/modules/compute/aws/lambda/main.tf`)

### 1. Add encryption key secret to `secretsmanager:GetSecretValue`

Update the existing `aws_iam_role_policy.secrets_access` to include the new secret ARN. Add a new variable:

```hcl
variable "credential_encryption_key_secret_arn" {
  description = "ARN of the credential encryption key secret in Secrets Manager"
  type        = string
  default     = ""
}
```

Update the policy resource:

```hcl
resource "aws_iam_role_policy" "secrets_access" {
  ...
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue"]
        Resource = compact([
          var.database_password_secret_arn,
          "${var.database_password_secret_arn}*",
          var.admin_password_secret_arn,
          var.admin_password_secret_arn != "" ? "${var.admin_password_secret_arn}*" : "",
          var.credential_encryption_key_secret_arn,             # ← NEW
          var.credential_encryption_key_secret_arn != "" ? "${var.credential_encryption_key_secret_arn}*" : "",
        ])
      },
      ...
    ]
  })
}
```

### 2. Add `sts:AssumeRole` for cross-account credential resolution

New IAM policy block in `terraform/modules/compute/aws/lambda/main.tf`:

```hcl
variable "enable_cross_account_sts" {
  description = "Allow Lambda to assume roles in remote AWS accounts (required for multi-account support)"
  type        = bool
  default     = false
}

resource "aws_iam_role_policy" "cross_account_sts" {
  count = var.enable_cross_account_sts ? 1 : 0

  name_prefix = "${var.stack_name}-cross-account-sts-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["sts:AssumeRole"]
        Resource = "arn:aws:iam::*:role/*"
        # Recommended: scope to a naming convention in production to reduce blast radius:
        # Resource = "arn:aws:iam::*:role/CUDly*"
        # Note: No ExternalId condition is applied at the IAM level because Lambda assumes
        # roles on behalf of many target accounts, each with its own ExternalId.
        # ExternalId validation is enforced at the application layer in resolver.go:
        # the stored aws_external_id is always passed in AssumeRoleInput.ExternalId,
        # so only the configured target account (whose trust policy requires that ExternalId)
        # can be assumed.
      }
    ]
  })
}
```

Enable in `terraform/environments/aws/compute.tf` (hardcoded `true` for now — both capabilities are required for multi-account support; set to `false` to disable in environments that don't need it):

```hcl
module "compute_lambda" {
  ...
  enable_cross_account_sts             = true
  credential_encryption_key_secret_arn = module.secrets.credential_encryption_key_secret_arn
}
```

### 3. Add `organizations:ListAccounts` for org discovery

New IAM policy block — only needed when org discovery is enabled:

```hcl
variable "enable_org_discovery" {
  description = "Allow Lambda to call AWS Organizations ListAccounts for member account discovery"
  type        = bool
  default     = false
}

resource "aws_iam_role_policy" "org_discovery" {
  count = var.enable_org_discovery ? 1 : 0

  name_prefix = "${var.stack_name}-org-discovery-"
  role        = aws_iam_role.lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["organizations:ListAccounts", "organizations:DescribeOrganization"]
        Resource = "*"  # Organizations API does not support resource-level restrictions
      }
    ]
  })
}
```

Enable in `terraform/environments/aws/compute.tf` (alongside `enable_cross_account_sts`):

```hcl
module "compute_lambda" {
  ...
  enable_org_discovery = true
}
```

---

## Azure Environment (`terraform/environments/azure/`)

Add to `terraform/environments/azure/secrets.tf`:

```hcl
resource "azurerm_key_vault_secret" "credential_encryption_key" {
  name         = "${local.stack_name}-credential-enc-key"
  value        = var.credential_encryption_key  # 64-char hex
  key_vault_id = module.secrets.key_vault_id

  tags = local.common_tags
}
```

**Key loading for Azure**: The application's `KeyFromEnv()` only handles `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` (AWS Secrets Manager). For Azure, the container entrypoint or init container retrieves the Key Vault secret and injects it as the `CREDENTIAL_ENCRYPTION_KEY` environment variable before starting the app. Pass the Key Vault secret URI to the container so the startup script can resolve it, or use Azure's [Key Vault references in App Service/Container Apps](https://learn.microsoft.com/en-us/azure/app-service/app-service-key-vault-references) to surface it as an env var automatically.

---

## GCP Environment (`terraform/environments/gcp/`)

Add to GCP secrets module invocation:

```hcl
additional_secrets = {
  "credential-enc-key" = {
    value       = var.credential_encryption_key
    description = "AES-256-GCM key for account credential encryption"
  }
}
```

**Key loading for GCP**: Same pattern as Azure — `KeyFromEnv()` does not call GCP Secret Manager directly. The container entrypoint retrieves the secret (via `gcloud secrets versions access latest --secret=credential-enc-key`) and sets it as `CREDENTIAL_ENCRYPTION_KEY` before the app starts, or use GCP's [Secret Manager env var injection](https://cloud.google.com/run/docs/configuring/secrets) for Cloud Run to surface it automatically as `CREDENTIAL_ENCRYPTION_KEY`.

---

## Application Change: Secret Manager–aware key loading

`internal/credentials/cipher.go:KeyFromEnv()` must be updated to support the ARN-based pattern:

```
Load order:
1. If CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN is set:
     → call secretsmanager.GetSecretValue(arn) → use returned string as hex key
     → cache in memory for the Lambda lifetime (loaded once at cold start)
2. Else if CREDENTIAL_ENCRYPTION_KEY is set:
     → use directly (hex key; for local dev / non-AWS environments)
3. Else:
     → use hardcoded dev key; log WARN "using insecure dev credential key"
```

This preserves local-dev ergonomics (no Secrets Manager needed) while mandating SM in production.

---

## `dev.tfvars.example` Additions

Document new required variables in `terraform/environments/aws/dev.tfvars.example`:

```hcl
# Multi-account credential encryption key (generate with: openssl rand -hex 32)
# In CI/CD, set this from a secrets vault rather than committing to tfvars.
credential_encryption_key = "REPLACE_WITH_64_CHAR_HEX_STRING"

# Maximum concurrent account connections during plan fan-out (default: 10)
max_account_parallelism = 10
```

Note: `enable_cross_account_sts` and `enable_org_discovery` are **not** environment-level variables. They are hardcoded as `true` in `compute.tf` (passed directly to the Lambda module). To disable them for a specific environment, edit `compute.tf` for that environment.

---

## Summary of new env vars (all environments)

| Env var | Set by | Purpose |
|---------|--------|---------|
| `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` | Terraform → Lambda env | ARN of SM secret holding the 32-byte AES key |
| `CREDENTIAL_ENCRYPTION_KEY` | Direct (local dev only) | 64-char hex key, bypasses SM for local use |
| `CUDLY_MAX_ACCOUNT_PARALLELISM` | Terraform → Lambda env | Fan-out goroutine cap (default: 10) |
