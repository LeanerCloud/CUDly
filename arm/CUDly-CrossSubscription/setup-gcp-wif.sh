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
  # Matches AWS's own role-name charset [\w+=,.@-]{1,64}. The comma is safe here:
  # the role name reaches --attribute-condition (a plain string flag), never the
  # comma-delimited --attribute-mapping dict.
  validate_principal_value "--aws-role-name"  "$AWS_ROLE_NAME"  '^[A-Za-z0-9_+=,.@-]{1,64}$'
  # Standard AWS partition. A GovCloud or China-partition caller presents a
  # different ARN prefix, so the condition below simply would not match: the
  # token exchange is refused rather than admitted on a partition mismatch.
  AWS_ROLE_ARN="arn:aws:sts::${AWS_ACCOUNT_ID}:assumed-role/${AWS_ROLE_NAME}"
  EXPECTED_CONDITION="attribute.aws_role == '${AWS_ROLE_ARN}'"
  # attribute.aws_role normalises the session ARN
  # (arn:aws:sts::<acct>:assumed-role/<role>/<session>) down to the role ARN,
  # so both EXPECTED_CONDITION above and the IAM grant below can match it
  # exactly instead of relying on a substring test. Kept in a variable (not
  # inlined at the create-provider call below) so the reuse path can validate
  # an existing provider against the exact same expression instead of a
  # second hand-copied literal that could silently drift from this one.
  EXPECTED_MAPPING="google.subject=assertion.arn,attribute.aws_role=assertion.arn.contains('assumed-role') ? assertion.arn.extract('{account_arn}assumed-role/') + 'assumed-role/' + assertion.arn.extract('assumed-role/{role_name}/') : assertion.arn"
  # google.subject is the full session ARN here and GCP caps it at 127 bytes,
  # not characters (127 chars is used below only because both charset
  # regexes constraining this value -- --aws-role-name above and
  # --oidc-subject below -- are ASCII-only, so chars == bytes; this budget
  # would silently under/over-count if that charset were ever widened).
  # "arn:aws:sts::<12 digits>:assumed-role/" is 39 characters, and the session
  # name is preceded by a "/", so the role name leaves 127 - 39 - len(role) - 1
  # for a session name that AWS allows to reach 64.
  AWS_SESSION_BUDGET=$((127 - 39 - ${#AWS_ROLE_NAME} - 1))
  if [[ $AWS_SESSION_BUDGET -lt 64 ]]; then
    echo "Note: role name '${AWS_ROLE_NAME}' leaves only ${AWS_SESSION_BUDGET} characters" >&2
    echo "      for the session name before google.subject exceeds GCP's 127-character" >&2
    echo "      limit. Longer session names will be rejected at token-exchange time." >&2
  fi
else
  [[ -n "$ISSUER_URI" ]] || die "--issuer-uri is required for --provider-type oidc"
  # Mandatory: public issuers such as token.actions.githubusercontent.com will
  # mint a token for any workload on the platform, so without a pinned subject
  # any repository in the world can federate as the service account.
  [[ -n "$OIDC_SUBJECT" ]] || die "--oidc-subject is required for --provider-type oidc. Without it the provider has no attribute condition and any subject issued by ${ISSUER_URI} can federate as ${SA_EMAIL}."
  validate_principal_value "--issuer-uri"   "$ISSUER_URI"   '^https://[a-zA-Z0-9.-]+(:[0-9]+)?(/[-a-zA-Z0-9._~/]*)?$'
  # '|' is permitted because Auth0/Okta-style subjects use it (google-oauth2|123).
  # It is inert in both destinations: quotes are rejected above, so it cannot
  # escape the CEL string literal, and it carries no meaning in a principal path.
  validate_principal_value "--oidc-subject" "$OIDC_SUBJECT" '^[A-Za-z0-9][-A-Za-z0-9._:/@=+~|]*$'
  # google.subject is capped at 127 characters by GCP; a longer value would be
  # rejected at token-exchange time, long after this script reported success.
  [[ ${#OIDC_SUBJECT} -le 127 ]] || die "--oidc-subject must be at most 127 characters (google.subject limit); got ${#OIDC_SUBJECT}"
  EXPECTED_CONDITION="google.subject == '${OIDC_SUBJECT}'"
  EXPECTED_MAPPING="google.subject=assertion.sub"
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
fi

# GCP principal identifiers are pool-scoped, not provider-scoped: the grant below
# names an attribute value, and any provider in this pool that can mint that
# attribute satisfies it. A pool dedicated to this provider keeps the trust
# boundary equal to the attribute condition set below.
#
# Printed unconditionally, not only when reusing a pool: a pool this run creates
# fresh is equally exposed the moment a second provider is added to it later, and
# that operator would otherwise never have seen the warning. No script can close
# this - GCP has no provider-scoped principal form - so a notice is the ceiling.
echo "Note: principal identifiers are pool-scoped, not provider-scoped. Another" >&2
echo "      provider in pool '${POOL_ID}' that maps the same attribute value would" >&2
echo "      also satisfy the grant below. Keep this pool dedicated to CUDly" >&2
echo "      (--pool-id) unless you intend to share it." >&2

# gcloud's `value(...)` printer renders a map field (attributeMapping) as its
# entries sorted by key and delimiter-joined with ';' by CsvPrinter._AddRecord
# (the same code path ValuePrinter inherits from), not the comma-joined
# "key=value,key=value" form the --attribute-mapping flag takes as input.
# Comparing the raw describe output against EXPECTED_MAPPING as strings would
# therefore report every correctly-configured provider as mismatched. This
# splits each side on its own pair separator, re-sorts, and compares parsed
# key/value pairs so neither the input-vs-output shape difference nor a
# hypothetical future change to gcloud's own key ordering can produce a false
# positive; a mapping that actually differs still compares unequal.
normalize_mapping() {
  local mapping="$1" pair_sep="$2"
  local -a pairs
  IFS="$pair_sep" read -ra pairs <<<"$mapping"
  printf '%s\n' "${pairs[@]}" | sort
}

# ── Idempotent provider creation ───────────────────────────────────────────────
if ! gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
     --project="$PROJECT" --location=global \
     --workload-identity-pool="$POOL_ID" &>/dev/null; then
  echo "Creating ${PROVIDER_TYPE} provider '${PROVIDER_ID}'..."
  if [[ "$PROVIDER_TYPE" == "aws" ]]; then
    gcloud iam workload-identity-pools providers create-aws "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --account-id="$AWS_ACCOUNT_ID" \
      --attribute-mapping="$EXPECTED_MAPPING" \
      --attribute-condition="$EXPECTED_CONDITION" \
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
      --attribute-mapping="$EXPECTED_MAPPING" \
      --attribute-condition="$EXPECTED_CONDITION" \
      --quiet
  fi
else
  # An existing provider keeps whatever attribute condition it was created with.
  # Checking only that the condition is non-empty would grandfather exactly the
  # values --subject-condition was removed to prevent: "true", or
  # "assertion.sub != ''", both non-empty and both admitting every identity. The
  # condition is therefore compared to the one this script would have written.
  DELETE_HINT="  gcloud iam workload-identity-pools providers delete ${PROVIDER_ID} --project=${PROJECT} --location=global --workload-identity-pool=${POOL_ID}"
  EXISTING_CONDITION=$(gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
    --project="$PROJECT" --location=global \
    --workload-identity-pool="$POOL_ID" \
    --format='value(attributeCondition)')
  # A provider of the other type would emit a credential config that does not
  # match it and mint an attribute the grant below never names. Compared as an
  # exact match on the single relevant field, not a prefix/suffix test against
  # the account-id/issuer-uri pair gcloud tab-joins under `value(a,b)`: a
  # suffix test here would accept any issuer URI merely ENDING in the expected
  # one (e.g. an attacker-hosted https://evil.example.com/<expected-issuer>,
  # whose /.well-known/openid-configuration GCP would then fetch from the
  # attacker, who can mint a token with any subject), and a prefix test would
  # accept any account ID merely STARTING with the expected one.
  if [[ "$PROVIDER_TYPE" == "aws" ]]; then
    EXISTING_ACCOUNT_ID=$(gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --format='value(aws.accountId)')
    if [[ "$EXISTING_ACCOUNT_ID" != "$AWS_ACCOUNT_ID" ]]; then
      die "provider '${PROVIDER_ID}' already exists but is not an AWS provider for account ${AWS_ACCOUNT_ID} (describe reports account: ${EXISTING_ACCOUNT_ID:-none}). Delete it and re-run:
${DELETE_HINT}"
    fi
  else
    EXISTING_ISSUER=$(gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
      --project="$PROJECT" --location=global \
      --workload-identity-pool="$POOL_ID" \
      --format='value(oidc.issuerUri)')
    if [[ "$EXISTING_ISSUER" != "$ISSUER_URI" ]]; then
      die "provider '${PROVIDER_ID}' already exists but is not an OIDC provider for issuer ${ISSUER_URI} (describe reports issuer: ${EXISTING_ISSUER:-none}). Delete it and re-run:
${DELETE_HINT}"
    fi
  fi
  if [[ "$EXISTING_CONDITION" != "$EXPECTED_CONDITION" ]]; then
    die "provider '${PROVIDER_ID}' already exists with a different attribute condition, so this script cannot vouch for which identities enter pool '${POOL_ID}'.
  found   : ${EXISTING_CONDITION:-(none)}
  expected: ${EXPECTED_CONDITION}
A condition that is merely non-empty is not a restriction: 'true' and
\"assertion.sub != ''\" both admit every identity the issuer will vouch for.
Delete the provider and re-run:
${DELETE_HINT}"
  fi
  # The condition alone does not decide who gets in: it is evaluated against
  # whatever value the mapping produces, so a provider can carry the exact
  # expected condition while a different mapping feeds it a normalised value
  # derived from an identity this script never authorised (e.g. one where
  # attribute.aws_role is built from a caller-controlled ARN path segment
  # rather than the actual assumed-role name). Validating the condition
  # without also validating the mapping only checks half of that property.
  EXISTING_MAPPING=$(gcloud iam workload-identity-pools providers describe "$PROVIDER_ID" \
    --project="$PROJECT" --location=global \
    --workload-identity-pool="$POOL_ID" \
    --format='value(attributeMapping)')
  if [[ "$(normalize_mapping "$EXISTING_MAPPING" ';')" != "$(normalize_mapping "$EXPECTED_MAPPING" ',')" ]]; then
    die "provider '${PROVIDER_ID}' already exists with a different attribute mapping, so this script cannot vouch for which identities enter pool '${POOL_ID}'.
  found   : ${EXISTING_MAPPING:-(none)}
  expected: ${EXPECTED_MAPPING}
A matching attribute condition is not sufficient on its own: the mapping
decides what value the condition is evaluated against, so a mismatched
mapping can satisfy the condition while admitting an identity this script
never authorised.
Delete the provider and re-run:
${DELETE_HINT}"
  fi
  echo "Reusing existing provider '${PROVIDER_ID}' (attribute condition and mapping match expected values)"
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

# ── Retire pool-wide grants left by earlier versions of this script ───────────
# Scanned across every pool and every role, not just this run's --pool-id and
# roles/iam.workloadIdentityUser. Scoping the scan to the current pool would let
# a customer hide the original grant simply by re-running with a different
# --pool-id (which the pool-reuse note above actively recommends), and scoping it
# to one role would miss the same wildcard under roles/iam.serviceAccountTokenCreator,
# which confers the same impersonation power via generateAccessToken.
#
# Emitted one "role<TAB>member" line per member so the role owning each wildcard
# is known; a bare policy grep cannot tell which role to remove it from.
#
# The fetch is deliberately kept out of the grep pipeline. Piping gcloud straight
# into `grep ... || true` would let a failed get-iam-policy produce empty output
# and read as "no wildcard grants found", reporting success over the very grant
# this check exists to catch. As a plain assignment it aborts under `set -e`.
sa_bindings() {
  gcloud iam service-accounts get-iam-policy "$SA_EMAIL" \
    --project="$PROJECT" --flatten="bindings[].members" \
    --format="value(bindings.role,bindings.members)"
}
pool_wide_grants() {
  grep -E $'\t''principalSet://iam\.googleapis\.com/projects/[0-9]+/locations/[^/]+/workloadIdentityPools/[^/]+/\*$' <<<"$1" || true
}

SA_BINDINGS=$(sa_bindings)
LEGACY_GRANTS=$(pool_wide_grants "$SA_BINDINGS")
if [[ -n "$LEGACY_GRANTS" ]]; then
  if [[ "$REMOVE_LEGACY_POOL_BINDING" == "true" ]]; then
    while IFS=$'\t' read -r legacy_role legacy_member; do
      [[ -n "$legacy_member" ]] || continue
      echo "Removing pool-wide grant: ${legacy_role} -> ${legacy_member}"
      gcloud iam service-accounts remove-iam-policy-binding "$SA_EMAIL" \
        --role="$legacy_role" \
        --member="$legacy_member" \
        --project="$PROJECT" \
        --quiet
    done <<<"$LEGACY_GRANTS"
    # Re-read the policy: removing one role's wildcard says nothing about the
    # others, and reporting success here is the whole point of the check.
    SA_BINDINGS=$(sa_bindings)
    REMAINING=$(pool_wide_grants "$SA_BINDINGS")
    [[ -z "$REMAINING" ]] || die "pool-wide grants survived removal on ${SA_EMAIL}:
${REMAINING}"
  else
    die "${SA_EMAIL} still grants impersonation to every identity in a workload identity pool:
${LEGACY_GRANTS}
Grants of this shape were created by earlier versions of this script and make the
narrow grant added above irrelevant: anything admitted to that pool can still
impersonate the service account. Re-run with --remove-legacy-pool-binding, or
remove each one yourself with:
  gcloud iam service-accounts remove-iam-policy-binding ${SA_EMAIL} \\
    --role='<role>' --member='<member>' --project=${PROJECT}"
  fi
fi

# Narrow grants for identities other than this run's are not removed (they may
# belong to a second legitimate CUDly account), but they are surfaced: a re-run
# with a corrected role or subject otherwise leaves the previous one impersonating.
# The member is compared as a whole field, not with `grep -vF`. A substring
# exclusion drops every line CONTAINING this run's member, so a sibling grant
# whose member merely starts with it is suppressed: pinning CUDly-Execution would
# hide an existing grant to CUDly-Execution-Admin, in exactly the "re-ran with a
# corrected role" case this notice exists to surface.
OTHER_GRANTS=$(awk -F'\t' -v pool="iam.googleapis.com/${POOL_RESOURCE}/" -v m="$MEMBER" \
  'index($2, pool) && $2 != m' <<<"$SA_BINDINGS" || true)
if [[ -n "$OTHER_GRANTS" ]]; then
  echo "Note: ${SA_EMAIL} is also impersonable by other identities in pool '${POOL_ID}':" >&2
  while IFS= read -r grant_line; do
    echo "      ${grant_line}" >&2
  done <<<"$OTHER_GRANTS"
  echo "      Remove any that are no longer expected." >&2
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
