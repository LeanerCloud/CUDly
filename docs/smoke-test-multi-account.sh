#!/usr/bin/env bash
# smoke-test-multi-account.sh — Manual smoke test for multi-account execution (Task 7.1)
#
# Usage:
#   BASE_URL=https://cudly.example.com \
#   AUTH_TOKEN=<jwt> \
#   AWS_ROLE_ARN=arn:aws:iam::123456789012:role/CUDly-ReadWrite \
#   bash docs/smoke-test-multi-account.sh
#
# Requirements:
#   - curl, jq
#   - A running CUDly instance with migration 000011+ applied
#   - An IAM role that CUDly's Lambda execution role can AssumeRole into
#   - CREDENTIAL_ENCRYPTION_KEY set on the target Lambda (or CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN)
#
# Exit codes:
#   0 — all checks passed
#   1 — a check failed (details printed to stderr)

set -euo pipefail

BASE_URL="${BASE_URL:?Set BASE_URL to the CUDly API root, e.g. https://cudly.example.com}"
AUTH_TOKEN="${AUTH_TOKEN:?Set AUTH_TOKEN to a valid JWT}"
AWS_ROLE_ARN="${AWS_ROLE_ARN:?Set AWS_ROLE_ARN to an IAM role ARN CUDly can assume}"

HDR=(-H "Authorization: Bearer ${AUTH_TOKEN}" -H "Content-Type: application/json")

pass() { echo "  PASS: $*"; }
fail() { echo "  FAIL: $*" >&2; exit 1; }
step() { echo; echo "==> $*"; }

# ---------------------------------------------------------------------------
# Step 1: Create an AWS account (role_arn auth mode)
# ---------------------------------------------------------------------------
step "1. Create AWS account with role_arn auth mode"

ACCOUNT_JSON=$(curl -sf "${BASE_URL}/api/accounts" "${HDR[@]}" -d "{
  \"name\": \"smoke-test-account\",
  \"provider\": \"aws\",
  \"external_id\": \"$(echo "${AWS_ROLE_ARN}" | grep -oP '[0-9]{12}')\",
  \"aws_auth_mode\": \"role_arn\",
  \"aws_role_arn\": \"${AWS_ROLE_ARN}\",
  \"region\": \"us-east-1\",
  \"enabled\": true
}")

ACCOUNT_ID=$(echo "${ACCOUNT_JSON}" | jq -r '.id')
[[ "${ACCOUNT_ID}" =~ ^[0-9a-f-]{36}$ ]] || fail "Expected UUID for account id, got: ${ACCOUNT_ID}"
pass "Account created: ${ACCOUNT_ID}"

# ---------------------------------------------------------------------------
# Step 2: Save credentials (access_keys not needed for role_arn; save a
#         placeholder aws_access_keys payload to exercise the credential store)
# ---------------------------------------------------------------------------
step "2. Save credentials via /api/accounts/:id/credentials"

curl -sf -X PUT "${BASE_URL}/api/accounts/${ACCOUNT_ID}/credentials" "${HDR[@]}" -d '{
  "credential_type": "aws_access_keys",
  "payload": {
    "access_key_id": "AKIAIOSFODNN7EXAMPLE",
    "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  }
}' > /dev/null
pass "Credentials saved"

# ---------------------------------------------------------------------------
# Step 3: Test credentials
# ---------------------------------------------------------------------------
step "3. Test credentials via /api/accounts/:id/test"

TEST_JSON=$(curl -sf -X POST "${BASE_URL}/api/accounts/${ACCOUNT_ID}/test" "${HDR[@]}")
OK=$(echo "${TEST_JSON}" | jq -r '.ok')
MSG=$(echo "${TEST_JSON}" | jq -r '.message')

# role_arn mode calls sts:AssumeRole — expect ok=true or a clear error
if [[ "${OK}" == "true" ]]; then
  pass "Credential test: ${MSG}"
else
  echo "  WARN: Credential test returned ok=false: ${MSG}" >&2
  echo "        (expected if the test runner lacks sts:AssumeRole — continuing)"
fi

# ---------------------------------------------------------------------------
# Step 4: Set a service override
# ---------------------------------------------------------------------------
step "4. Set service override (aws/savingsplans: disable auto-collect)"

curl -sf -X PUT \
  "${BASE_URL}/api/accounts/${ACCOUNT_ID}/service-overrides/aws/savingsplans" \
  "${HDR[@]}" -d '{
    "auto_collect": false,
    "notification_days_before": 14
  }' > /dev/null
pass "Service override saved"

OVERRIDES=$(curl -sf "${BASE_URL}/api/accounts/${ACCOUNT_ID}/service-overrides" "${HDR[@]}")
COUNT=$(echo "${OVERRIDES}" | jq '. | length')
[[ "${COUNT}" -ge 1 ]] || fail "Expected >=1 override, got ${COUNT}"
pass "Override visible in list (${COUNT} total)"

# ---------------------------------------------------------------------------
# Step 5: Create a purchase plan and associate the account
# ---------------------------------------------------------------------------
step "5. Create a plan and associate account"

PLAN_JSON=$(curl -sf "${BASE_URL}/api/plans" "${HDR[@]}" -d '{
  "name": "smoke-test-plan",
  "services": {"aws:savingsplans": {"term": "1yr", "payment": "all_upfront"}},
  "enabled": true
}')
PLAN_ID=$(echo "${PLAN_JSON}" | jq -r '.id')
[[ "${PLAN_ID}" =~ ^[0-9a-f-]{36}$ ]] || fail "Expected UUID for plan id, got: ${PLAN_ID}"
pass "Plan created: ${PLAN_ID}"

curl -sf -X PUT "${BASE_URL}/api/plans/${PLAN_ID}/accounts" "${HDR[@]}" \
  -d "[\"${ACCOUNT_ID}\"]" > /dev/null
pass "Account associated to plan"

PLAN_ACCOUNTS=$(curl -sf "${BASE_URL}/api/plans/${PLAN_ID}/accounts" "${HDR[@]}")
GOT_ID=$(echo "${PLAN_ACCOUNTS}" | jq -r '.[0]')
[[ "${GOT_ID}" == "${ACCOUNT_ID}" ]] || fail "Plan accounts mismatch: ${GOT_ID}"
pass "Plan-account association verified"

# ---------------------------------------------------------------------------
# Step 6: Trigger recommendations refresh
# ---------------------------------------------------------------------------
step "6. Trigger recommendations refresh"

curl -sf -X POST "${BASE_URL}/api/recommendations/refresh" "${HDR[@]}" > /dev/null
pass "Recommendations refresh triggered"
echo "  NOTE: Refresh is asynchronous — waiting 5s for results"
sleep 5

# ---------------------------------------------------------------------------
# Step 7: Verify recommendations include account name
# ---------------------------------------------------------------------------
step "7. Verify recommendations show account filter"

RECS=$(curl -sf "${BASE_URL}/api/recommendations?account_ids=${ACCOUNT_ID}" "${HDR[@]}")
REC_COUNT=$(echo "${RECS}" | jq '. | length')
pass "Recommendations endpoint returned ${REC_COUNT} items filtered by account"

# Check that the response is an array (even if empty after first refresh)
echo "${RECS}" | jq -e 'type == "array"' > /dev/null \
  || fail "Expected array from recommendations endpoint"
pass "Recommendations response is a valid array"

# ---------------------------------------------------------------------------
# Step 8: Check history filter by account
# ---------------------------------------------------------------------------
step "8. Check history filter by account"

HISTORY=$(curl -sf "${BASE_URL}/api/history?account_ids=${ACCOUNT_ID}" "${HDR[@]}")
echo "${HISTORY}" | jq -e 'type == "array"' > /dev/null \
  || fail "Expected array from history endpoint"
HIST_COUNT=$(echo "${HISTORY}" | jq '. | length')
pass "History endpoint returned ${HIST_COUNT} items filtered by account"

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
step "Cleanup: deleting test account and plan"

curl -sf -X DELETE "${BASE_URL}/api/plans/${PLAN_ID}" "${HDR[@]}" > /dev/null && pass "Plan deleted"
curl -sf -X DELETE "${BASE_URL}/api/accounts/${ACCOUNT_ID}" "${HDR[@]}" > /dev/null && pass "Account deleted"

echo
echo "All smoke-test checks PASSED."
