#!/usr/bin/env bash
# setup-gcp-wif.sh — Configure a GCP Workload Identity Pool and Provider so that
# CUDly (running on AWS or with an OIDC token) can access GCP without a service
# account key file.  Outputs the external-account credential config JSON that
# should be stored as gcp_workload_identity_config in CUDly.
#
# SECURITY: the provider is always created with an --attribute-condition and the
# service account impersonation grant is always scoped to a single principal.
# Both require an explicit identity to pin (--aws-role-name / --oidc-subject);
# there is no permissive default, and the script exits nonzero if one is missing.
#
# Prerequisites:
#   gcloud auth login (with roles/iam.workloadIdentityPoolAdmin + roles/iam.serviceAccountAdmin
#   on the target project, and roles/iam.serviceAccountTokenCreator on the SA)
#
# Usage (AWS provider):
#   ./setup-gcp-wif.sh \
#       --project my-gcp-project \
#       --pool-id cudly-pool \
#       --provider-id cudly-aws \
#       --provider-type aws \
#       --aws-account-id 123456789012 \
#       --aws-role-name CUDly-Execution \
#       --sa-email cudly@my-gcp-project.iam.gserviceaccount.com
#
# Usage (OIDC provider):
#   ./setup-gcp-wif.sh \
#       --project my-gcp-project \
#       --pool-id cudly-pool \
#       --provider-id cudly-oidc \
#       --provider-type oidc \
#       --issuer-uri https://token.actions.githubusercontent.com \
#       --oidc-subject 'repo:my-org/my-repo:ref:refs/heads/main' \
#       --sa-email cudly@my-gcp-project.iam.gserviceaccount.com
#
# Upgrading from an earlier run: versions of this script before the
# --aws-role-name / --oidc-subject requirement granted
# roles/iam.workloadIdentityUser to every identity in the pool
# (principalSet://.../workloadIdentityPools/<pool>/*). Re-running this script
# detects that binding and refuses to report success while it exists; pass
# --remove-legacy-pool-binding to delete it once the narrow grant is in place.

set -euo pipefail

PROJECT=""
POOL_ID="cudly-pool"
PROVIDER_ID="cudly-provider"
PROVIDER_TYPE=""   # "aws" or "oidc"
SA_EMAIL=""
AWS_ACCOUNT_ID=""
AWS_ROLE_NAME=""
ISSUER_URI=""
OIDC_SUBJECT=""
REMOVE_LEGACY_POOL_BINDING="false"

die() { echo "Error: $*" >&2; exit 1; }

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --project)           PROJECT="$2";           shift 2 ;;
    --pool-id)           POOL_ID="$2";           shift 2 ;;
    --provider-id)       PROVIDER_ID="$2";       shift 2 ;;
    --provider-type)     PROVIDER_TYPE="$2";     shift 2 ;;
    --sa-email)          SA_EMAIL="$2";          shift 2 ;;
    --aws-account-id)    AWS_ACCOUNT_ID="$2";    shift 2 ;;
    --aws-role-name)     AWS_ROLE_NAME="$2";     shift 2 ;;
    --issuer-uri)        ISSUER_URI="$2";        shift 2 ;;
    --oidc-subject)      OIDC_SUBJECT="$2";      shift 2 ;;
    --remove-legacy-pool-binding) REMOVE_LEGACY_POOL_BINDING="true"; shift ;;
    # Removed on purpose: --subject-condition took a raw CEL expression, so a
    # value such as "true" or "assertion.sub != ''" looked like a restriction
    # while admitting every subject from the issuer. --oidc-subject takes the
    # subject itself and this script builds the equality condition around it.
    --subject-condition)
      die "--subject-condition has been removed; pass --oidc-subject '<exact sub claim>' instead (this script builds the attribute condition, so a hand-written CEL expression can no longer silently admit every subject)" ;;
    *) die "Unknown argument: $1" ;;
  esac
done

# ── Value validation ───────────────────────────────────────────────────────────
# Every value below is interpolated into either the provider's CEL
# --attribute-condition or the IAM principal identifier of the impersonation
# grant. Reject the forms that fail OPEN there rather than merely looking wrong:
#   '*'      would widen an IAM principal identifier to match every identity;
#   '$'      catches pasted ${...} placeholders, which IAM does not expand here
#            (they yield a binding that matches nothing, or in a context that
#            does expand them, one that matches everything);
#   ' " \ `  would terminate the CEL string literal in --attribute-condition and
#            let the rest of the value rewrite the condition (e.g. "|| true").
validate_principal_value() {
  local flag="$1" value="$2" pattern="$3"
  [[ -n "$value" ]] || die "$flag must not be empty"
  case "$value" in
    *'*'*) die "$flag must not contain '*' (got: $value): a wildcard would match every identity and defeat the restriction this flag exists to apply" ;;
    *'$'*) die "$flag must not contain '\$' (got: $value): it looks like an unexpanded \${...} placeholder, which is not substituted here" ;;
    *\'*|*\"*|*\\*|*'`'*) die "$flag must not contain quotes or backslashes (got: $value): they would break out of the CEL string literal in the provider's attribute condition" ;;
  esac
  [[ "$value" =~ $pattern ]] || die "$flag has an unexpected format (got: $value)"
}

if [[ -z "$PROJECT" || -z "$SA_EMAIL" || -z "$PROVIDER_TYPE" ]]; then
  die "--project, --sa-email, and --provider-type are required"
fi
if [[ "$PROVIDER_TYPE" != "aws" && "$PROVIDER_TYPE" != "oidc" ]]; then
  die "--provider-type must be 'aws' or 'oidc'"
fi

validate_principal_value "--project"     "$PROJECT"     '^[a-z][-a-z0-9]{4,28}[a-z0-9]$'
validate_principal_value "--pool-id"     "$POOL_ID"     '^[a-z0-9][-a-z0-9]{2,30}[a-z0-9]$'
validate_principal_value "--provider-id" "$PROVIDER_ID" '^[a-z0-9][-a-z0-9]{2,30}[a-z0-9]$'
validate_principal_value "--sa-email"    "$SA_EMAIL"    '^[a-zA-Z0-9][-a-zA-Z0-9._]*@[a-z0-9.-]+\.gserviceaccount\.com$'

if [[ "$PROVIDER_TYPE" == "aws" ]]; then
  [[ -n "$AWS_ACCOUNT_ID" ]] || die "--aws-account-id is required for --provider-type aws"
  # Mandatory: without a role to pin, the provider has no attribute condition and
  # every IAM principal in the AWS account can federate as the service account.
  [[ -n "$AWS_ROLE_NAME" ]] || die "--aws-role-name is required for --provider-type aws. Without it the provider has no attribute condition and any IAM role in account ${AWS_ACCOUNT_ID} can federate as ${SA_EMAIL}."
  validate_principal_value "--aws-account-id" "$AWS_ACCOUNT_ID" '^[0-9]{12}$'
  validate_principal_value "--aws-role-name"  "$AWS_ROLE_NAME"  '^[A-Za-z0-9_+=.@-]{1,64}$'
  # Standard AWS partition. A GovCloud or China-partition caller presents a
  # different ARN prefix, so the condition below simply would not match: the
  # token exchange is refused rather than admitted on a partition mismatch.
  AWS_ROLE_ARN="arn:aws:sts::${AWS_ACCOUNT_ID}:assumed-role/${AWS_ROLE_NAME}"
else
  [[ -n "$ISSUER_URI" ]] || die "--issuer-uri is required for --provider-type oidc"
  # Mandatory: public issuers such as token.actions.githubusercontent.com will
  # mint a token for any workload on the platform, so without a pinned subject
  # any repository in the world can federate as the service account.
  [[ -n "$OIDC_SUBJECT" ]] || die "--oidc-subject is required for --provider-type oidc. Without it the provider has no attribute condition and any subject issued by ${ISSUER_URI} can federate as ${SA_EMAIL}."
  validate_principal_value "--issuer-uri"   "$ISSUER_URI"   '^https://[a-zA-Z0-9.-]+(:[0-9]+)?(/[-a-zA-Z0-9._~/]*)?$'
  validate_principal_value "--oidc-subject" "$OIDC_SUBJECT" '^[A-Za-z0-9][-A-Za-z0-9._:/@=+~]*$'
  # google.subject is capped at 127 characters by GCP; a longer value would be
  # rejected at token-exchange time, long after this script reported success.
  [[ ${#OIDC_SUBJECT} -le 127 ]] || die "--oidc-subject must be at most 127 characters (google.subject limit); got ${#OIDC_SUBJECT}"
fi

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
[[ "$PROJECT_NUMBER" =~ ^[0-9]+$ ]] || die "could not resolve a numeric project number for '$PROJECT' (got: '$PROJECT_NUMBER')"

echo "Project       : $PROJECT (number: $PROJECT_NUMBER)"
echo "Pool          : $POOL_ID"
echo "Provider      : $PROVIDER_ID ($PROVIDER_TYPE)"
echo "Service Acct  : $SA_EMAIL"
if [[ "$PROVIDER_TYPE" == "aws" ]]; then
  echo "Trusted role  : $AWS_ROLE_ARN"
else
  echo "Trusted subj  : $OIDC_SUBJECT (issuer: $ISSUER_URI)"
fi
echo ""

# ── Idempotent pool creation ───────────────────────────────────────────────────
if ! gcloud iam workload-identity-pools describe "$POOL_ID" \
     --project="$PROJECT" --location=global &>/dev/null; then
  echo "Creating workload identity pool '${POOL_ID}'..."
  gcloud iam workload-identity-pools create "$POOL_ID" \
    --project="$PROJECT" --location=global \
    --display-name="CUDly WIF pool" --quiet
else
  echo "Reusing existing pool '${POOL_ID}'"
  # GCP principal identifiers are pool-scoped, not provider-scoped: the grant
  # below names an attribute value, and any provider in this pool that can mint
  # that attribute satisfies it. A pool dedicated to this provider keeps the
  # trust boundary equal to the attribute condition set below.
  echo "Note: principal identifiers are pool-scoped, not provider-scoped. Another" >&2
  echo "      provider in this pool that maps the same attribute value would also" >&2
  echo "      satisfy the grant below. Use a pool dedicated to CUDly (--pool-id)" >&2
  echo "      unless you intend to share it." >&2
fi

# ── Idempotent provider creation ───────────────────────────────────────────────
if ! gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
     --project="$PROJECT" --location=global \
     --workload-identity-pool="$POOL_ID" &>/dev/null; then
  echo "Creating ${PROVIDER_TYPE} provider '${PROVIDER_ID}'..."
  if [[ "$PROVIDER_TYPE" == "aws" ]]; then
    # attribute.aws_role normalises the session ARN
    # (arn:aws:sts::<acct>:assumed-role/<role>/<session>) down to the role ARN,
    # so both the condition below and the IAM grant can match it exactly instead
    # of relying on a substring test.
    gcloud iam workload-identity-pools providers create-aws "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --account-id="$AWS_ACCOUNT_ID" \
      --attribute-mapping="google.subject=assertion.arn,attribute.aws_role=assertion.arn.contains('assumed-role') ? assertion.arn.extract('{account_arn}assumed-role/') + 'assumed-role/' + assertion.arn.extract('assumed-role/{role_name}/') : assertion.arn" \
      --attribute-condition="attribute.aws_role == '${AWS_ROLE_ARN}'" \
      --quiet
  else
    # --attribute-mapping is required; without it no subject claims are mapped and
    # all token exchanges are rejected at runtime despite a successful setup.
    # --attribute-condition is built here from --oidc-subject so that only that
    # exact subject is admitted to the pool.
    gcloud iam workload-identity-pools providers create-oidc "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --issuer-uri="$ISSUER_URI" \
      --attribute-mapping="google.subject=assertion.sub" \
      --attribute-condition="google.subject == '${OIDC_SUBJECT}'" \
      --quiet
  fi
else
  # An existing provider keeps whatever attribute condition it was created with,
  # including none at all. Do not report success over a provider this script
  # cannot vouch for.
  EXISTING_CONDITION=$(gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
    --project="$PROJECT" --location=global \
    --workload-identity-pool="$POOL_ID" \
    --format='value(attributeCondition)')
  if [[ -z "$EXISTING_CONDITION" ]]; then
    die "provider '${PROVIDER_ID}' already exists with no attribute condition, so every identity it accepts can enter pool '${POOL_ID}'. Delete it and re-run:
  gcloud iam workload-identity-pools providers delete ${PROVIDER_ID} --project=${PROJECT} --location=global --workload-identity-pool=${POOL_ID}"
  fi
  echo "Reusing existing provider '${PROVIDER_ID}' (attribute condition: ${EXISTING_CONDITION})"
fi

# ── Grant service account impersonation to one principal ──────────────────────
POOL_RESOURCE="projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL_ID}"
if [[ "$PROVIDER_TYPE" == "aws" ]]; then
  # Session ARNs carry a per-session suffix, so the grant names the normalised
  # role attribute rather than an exact subject.
  MEMBER="principalSet://iam.googleapis.com/${POOL_RESOURCE}/attribute.aws_role/${AWS_ROLE_ARN}"
else
  MEMBER="principal://iam.googleapis.com/${POOL_RESOURCE}/subject/${OIDC_SUBJECT}"
fi
echo "Granting roles/iam.workloadIdentityUser on ${SA_EMAIL} to:"
echo "  ${MEMBER}"
gcloud iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
  --role=roles/iam.workloadIdentityUser \
  --member="$MEMBER" \
  --project="$PROJECT" \
  --quiet

# ── Retire the pool-wide grant left by earlier versions of this script ────────
LEGACY_MEMBER="principalSet://iam.googleapis.com/${POOL_RESOURCE}/*"
# Fetched into a variable first: a pipeline straight into grep would let a failed
# get-iam-policy read as "no legacy binding found" and report success over one.
SA_POLICY=$(gcloud iam service-accounts get-iam-policy "$SA_EMAIL" \
  --project="$PROJECT" --format=json)
if grep -qF -- "$LEGACY_MEMBER" <<<"$SA_POLICY"; then
  if [[ "$REMOVE_LEGACY_POOL_BINDING" == "true" ]]; then
    echo "Removing legacy pool-wide impersonation grant..."
    gcloud iam service-accounts remove-iam-policy-binding "$SA_EMAIL" \
      --role=roles/iam.workloadIdentityUser \
      --member="$LEGACY_MEMBER" \
      --project="$PROJECT" \
      --quiet
  else
    die "${SA_EMAIL} still grants roles/iam.workloadIdentityUser to every identity in pool '${POOL_ID}':
  ${LEGACY_MEMBER}
That grant was created by an earlier version of this script and makes the narrow
grant added above irrelevant: anything admitted to the pool can still impersonate
the service account. Re-run this command with --remove-legacy-pool-binding, or
remove it yourself with:
  gcloud iam service-accounts remove-iam-policy-binding ${SA_EMAIL} \\
    --role=roles/iam.workloadIdentityUser \\
    --member='${LEGACY_MEMBER}' \\
    --project=${PROJECT}"
  fi
fi

# ── Generate external account credential config (no secrets) ──────────────────
PROVIDER_RESOURCE="${POOL_RESOURCE}/providers/${PROVIDER_ID}"
echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  External account credential config (store in CUDly as"
echo "  gcp_workload_identity_config — contains no secrets)"
echo "══════════════════════════════════════════════════════════════"
if [[ "$PROVIDER_TYPE" == "aws" ]]; then
  gcloud iam workload-identity-pools create-cred-config \
    "$PROVIDER_RESOURCE" \
    --service-account="$SA_EMAIL" \
    --aws \
    --output-file=/dev/stdout
else
  gcloud iam workload-identity-pools create-cred-config \
    "$PROVIDER_RESOURCE" \
    --service-account="$SA_EMAIL" \
    --output-file=/dev/stdout
fi
echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  CUDly account registration values:"
echo "    provider       : gcp"
echo "    gcp_auth_mode  : workload_identity_federation"
echo "    gcp_project_id : ${PROJECT}"
echo "    gcp_client_email (optional) : ${SA_EMAIL}"
echo "══════════════════════════════════════════════════════════════"
