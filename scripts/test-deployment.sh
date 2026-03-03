#!/usr/bin/env bash
# Deployment test harness for CUDly
#
# Called by Terraform local-exec provisioner after deployment. Covers tests
# that Terraform check blocks cannot handle: TLS introspection, HTTP->HTTPS
# redirect detection, multi-step auth flows, response time measurement, and
# HTTP methods not supported by data "http" (DELETE/PUT/OPTIONS).
#
# Usage: ./scripts/test-deployment.sh <provider>
#
# Environment variables (set by Terraform local-exec):
#   AWS_URL   - API base URL for AWS deployments
#   GCP_URL   - API base URL for GCP deployments
#   AZURE_URL - API base URL for Azure deployments
#
# Optional:
#   TEST_EMAIL    - Email for auth flow tests (skipped if unset)
#   TEST_PASSWORD - Password for auth flow tests (base64-encoded, skipped if unset)

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'
BOLD='\033[1m'

# Counters
PASS=0
FAIL=0
SKIP=0
WARN=0

# Auth test credentials
TEST_EMAIL="${TEST_EMAIL:-}"
TEST_PASSWORD="${TEST_PASSWORD:-}"

# Prefer OpenSSL-based curl over macOS SecureTransport curl.
# macOS system curl (SecureTransport) disables SNI with --insecure, breaking
# connections to hosts that need SNI.
CURL_BIN="curl"
for candidate in /opt/homebrew/opt/curl/bin/curl /usr/local/opt/curl/bin/curl; do
  if [[ -x "$candidate" ]] && "$candidate" --version 2>/dev/null | grep -q OpenSSL; then
    CURL_BIN="$candidate"
    break
  fi
done

# Parse arguments
if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <provider>"
  exit 1
fi

PROVIDER="$1"

# Get URL from environment variable (set by Terraform local-exec)
case "$PROVIDER" in
  aws)   URL="${AWS_URL:-}" ;;
  gcp)   URL="${GCP_URL:-}" ;;
  azure) URL="${AZURE_URL:-}" ;;
  *)     echo "Unknown provider: $PROVIDER"; exit 1 ;;
esac

# Strip trailing slash
URL="${URL%/}"

if [[ -z "$URL" ]]; then
  echo "No URL set for $PROVIDER"
  exit 0
fi

# Determine protocol from URL
if [[ "$URL" =~ ^https:// ]]; then
  DEFAULT_PROTOCOL="https"
else
  DEFAULT_PROTOCOL="http"
fi

# TLS options for bare-IP HTTPS URLs (self-signed certs)
CURL_K=""
if [[ "$URL" =~ ^https://[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+ ]]; then
  CURL_K="-k"
fi

# ============================================================
# Helper functions
# ============================================================

api_path() {
  echo "/api${1}"
}

log_pass() { ((PASS++)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { ((FAIL++)); echo -e "  ${RED}FAIL${NC} $1"; }
log_skip() { ((SKIP++)); echo -e "  ${YELLOW}SKIP${NC} $1"; }
log_warn() { ((WARN++)); echo -e "  ${YELLOW}WARN${NC} $1"; }
log_info() { echo -e "  ${CYAN}INFO${NC} $1"; }

# ============================================================
# Test functions
# ============================================================

test_connectivity() {
  local status
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" -L --connect-timeout 10 --max-time 15 "$URL/" 2>/dev/null || echo "000")
  status="${status: -3}"

  if [[ "$status" == "000" ]]; then
    log_fail "connectivity: $URL unreachable (connection failed)"
    return 1
  elif [[ "$status" =~ ^[2345] ]]; then
    log_pass "connectivity: $URL reachable (HTTP $status)"
    return 0
  else
    log_fail "connectivity: $URL returned HTTP $status"
    return 1
  fi
}

test_https() {
  local status
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" --connect-timeout 10 "${URL}/" 2>/dev/null || true)

  if [[ "$status" =~ ^[23] ]]; then
    log_pass "https: HTTPS connection successful (HTTP $status)"
  else
    log_fail "https: HTTPS connection failed (HTTP $status)"
  fi

  local tls_version
  tls_version=$($CURL_BIN -s $CURL_K -v "${URL}/" 2>&1 | grep -oE 'TLSv?[0-9.]+' | head -1 || true)
  if [[ -n "$tls_version" ]]; then
    if [[ "$tls_version" =~ 1\.[23] ]]; then
      log_pass "https: $tls_version"
    else
      log_fail "https: weak TLS version ($tls_version)"
    fi
  else
    log_warn "https: could not determine TLS version"
  fi
}

test_http_redirect() {
  local host
  host=$(echo "$URL" | sed 's|https://||')
  local http_url="http://${host}/"

  local status
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" --connect-timeout 10 "$http_url" 2>/dev/null || true)

  if [[ "$status" == "301" || "$status" == "308" ]]; then
    log_pass "http-redirect: HTTP->HTTPS redirect ($status)"
  elif [[ "$status" == "302" || "$status" == "307" ]]; then
    log_warn "http-redirect: temporary redirect ($status), should be 301"
  elif [[ "$status" == "000" ]]; then
    log_skip "http-redirect: HTTP port not reachable (may be blocked)"
  else
    log_fail "http-redirect: expected 301, got $status"
  fi
}

test_cors() {
  local cors_path
  cors_path=$(api_path "/auth/login")

  local headers
  headers=$($CURL_BIN -s $CURL_K -D - -o /dev/null --connect-timeout 10 \
    -X OPTIONS \
    -H "Origin: https://example.com" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: Content-Type, Authorization" \
    "${URL}${cors_path}" 2>/dev/null || true)

  local status
  status=$(echo "$headers" | head -1 | grep -oE '[0-9]{3}')

  if [[ "$status" == "200" || "$status" == "204" ]]; then
    log_pass "cors: OPTIONS preflight returns $status"
  else
    log_fail "cors: OPTIONS preflight returns $status (expected 200/204)"
  fi

  if echo "$headers" | grep -qi "access-control-allow-methods"; then
    log_pass "cors: Access-Control-Allow-Methods present"
  else
    log_fail "cors: Access-Control-Allow-Methods missing"
  fi

  if echo "$headers" | grep -qi "access-control-allow-headers"; then
    log_pass "cors: Access-Control-Allow-Headers present"
  else
    log_fail "cors: Access-Control-Allow-Headers missing"
  fi

  local acao
  acao=$(echo "$headers" | grep -i "access-control-allow-origin" | head -1 | tr -d '\r')
  if [[ -n "$acao" ]]; then
    log_info "cors: $acao"
  fi
}

test_method_enforcement() {
  local login_path
  login_path=$(api_path "/auth/login")
  local status

  # DELETE /api/auth/login -> 404 or 405
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" -X DELETE \
    --connect-timeout 10 "${URL}${login_path}" 2>/dev/null || true)

  if [[ "$status" == "404" || "$status" == "405" ]]; then
    log_pass "method-enforcement: DELETE ${login_path} -> $status"
  else
    log_fail "method-enforcement: DELETE ${login_path} -> $status (expected 404/405)"
  fi

  # PUT /health -> 404 or 405
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" -X PUT \
    --connect-timeout 10 "${URL}/health" 2>/dev/null || true)

  if [[ "$status" == "404" || "$status" == "405" ]]; then
    log_pass "method-enforcement: PUT /health -> $status"
  else
    log_fail "method-enforcement: PUT /health -> $status (expected 404/405)"
  fi
}

test_api_docs() {
  local docs_base="/api/docs"
  local status

  # GET /api/docs/openapi.yaml -> 200
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" --connect-timeout 10 "${URL}${docs_base}/openapi.yaml" 2>/dev/null || true)

  if [[ "$status" == "200" ]]; then
    log_pass "api-docs: ${docs_base}/openapi.yaml -> 200"
  else
    log_fail "api-docs: ${docs_base}/openapi.yaml -> $status (expected 200)"
  fi

  # GET /api/docs -> 200, HTML (Swagger UI)
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" --connect-timeout 10 "${URL}${docs_base}" 2>/dev/null || true)
  local content_type
  content_type=$($CURL_BIN -s $CURL_K -D - -o /dev/null --connect-timeout 10 "${URL}${docs_base}" 2>/dev/null | grep -i "content-type:" | head -1 | tr -d '\r')

  if [[ "$status" == "200" ]]; then
    log_pass "api-docs: ${docs_base} -> 200"
  else
    log_fail "api-docs: ${docs_base} -> $status (expected 200)"
  fi

  if echo "$content_type" | grep -qi "text/html"; then
    log_pass "api-docs: ${docs_base} serves text/html"
  else
    log_warn "api-docs: ${docs_base} content-type is '$content_type'"
  fi
}

test_response_time() {
  local health_path="/health"

  local time_total
  time_total=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{time_total}" --connect-timeout 10 "${URL}${health_path}" 2>/dev/null || true)

  if (( $(echo "$time_total < 1.0" | bc -l 2>/dev/null || echo 0) )); then
    log_pass "response-time: ${health_path} in ${time_total}s"
  elif (( $(echo "$time_total < 3.0" | bc -l 2>/dev/null || echo 0) )); then
    log_warn "response-time: ${health_path} in ${time_total}s (slow)"
  else
    log_fail "response-time: ${health_path} in ${time_total}s (very slow, >3s)"
  fi
}

test_auth_flow() {
  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "auth: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "/auth/login")

  local response status body
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${URL}${login_path}" 2>/dev/null || true)

  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "200" ]]; then
    local has_token
    has_token=$(echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'token' in d or 'session' in d or 'user' in d else 'no')" 2>/dev/null || echo "no")
    if [[ "$has_token" == "yes" ]]; then
      log_pass "auth: login succeeded with valid credentials"
    else
      log_warn "auth: login returned 200 but unexpected response format"
      log_info "auth: response: $(echo "$body" | head -c 200)"
    fi
  elif [[ "$status" == "400" ]]; then
    log_warn "auth: login returned 400 - $(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('error','unknown'))" 2>/dev/null || true)"
  elif [[ "$status" == "401" ]]; then
    log_warn "auth: login returned 401 (credentials may be wrong or password encoding differs)"
  elif [[ "$status" == "429" ]]; then
    log_warn "auth: login returned 429 (rate limited)"
  else
    log_fail "auth: login returned $status"
  fi

  local protected_path
  protected_path=$(api_path "/configs")
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" --connect-timeout 10 "${URL}${protected_path}" 2>/dev/null || true)

  if [[ "$status" == "401" || "$status" == "403" ]]; then
    log_pass "auth: protected endpoint ${protected_path} returns $status without token"
  elif [[ "$status" == "200" ]]; then
    log_fail "auth: protected endpoint ${protected_path} accessible without auth (returned 200)"
  else
    log_info "auth: protected endpoint ${protected_path} returned $status"
  fi
}

test_csrf_protection() {
  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "csrf: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "/auth/login")

  local response status body
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${URL}${login_path}" 2>/dev/null || true)

  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" != "200" ]]; then
    log_warn "csrf: skipped (login failed with $status)"
    return
  fi

  local token
  token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)

  if [[ -z "$token" ]]; then
    log_warn "csrf: skipped (could not extract token from login response)"
    return
  fi

  # POST /api/auth/logout with Bearer token but NO X-CSRF-Token -> 403
  local logout_path
  logout_path=$(api_path "/auth/logout")
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${URL}${logout_path}" 2>/dev/null || true)

  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "403" ]]; then
    log_pass "csrf: POST ${logout_path} without CSRF token -> 403"
    local mentions_csrf
    mentions_csrf=$(echo "$body" | python3 -c "import sys,json; e=json.load(sys.stdin).get('error',''); print('yes' if 'csrf' in e.lower() else 'no')" 2>/dev/null || echo "no")
    if [[ "$mentions_csrf" == "yes" ]]; then
      log_pass "csrf: error message mentions CSRF"
    else
      log_warn "csrf: 403 response does not mention CSRF in error field"
    fi
  else
    log_fail "csrf: POST ${logout_path} without CSRF token -> $status (expected 403)"
  fi

  # Clean up: re-login and logout properly
  local csrf_token
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${URL}${login_path}" 2>/dev/null || true)
  body=$(echo "$response" | sed '$d')
  token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)
  csrf_token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('csrf_token',''))" 2>/dev/null || true)
  if [[ -n "$token" && -n "$csrf_token" ]]; then
    $CURL_BIN -s $CURL_K -o /dev/null -X POST \
      -H "Authorization: Bearer ${token}" \
      -H "X-CSRF-Token: ${csrf_token}" \
      --connect-timeout 10 "${URL}${logout_path}" 2>/dev/null || true
  fi
}

test_authenticated_api() {
  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "auth-api: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "/auth/login")

  local response status body
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${URL}${login_path}" 2>/dev/null || true)

  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" != "200" ]]; then
    log_warn "auth-api: skipped (login failed with $status)"
    return
  fi

  local token csrf_token
  token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)
  csrf_token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('csrf_token',''))" 2>/dev/null || true)

  if [[ -z "$token" ]]; then
    log_warn "auth-api: skipped (could not extract token)"
    return
  fi

  # GET /api/auth/me -> 200, has user email
  local me_path
  me_path=$(api_path "/auth/me")
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${URL}${me_path}" 2>/dev/null || true)
  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "200" ]]; then
    local has_email
    has_email=$(echo "$body" | python3 -c "import sys,json; print('yes' if 'email' in json.load(sys.stdin) else 'no')" 2>/dev/null || echo "no")
    if [[ "$has_email" == "yes" ]]; then
      log_pass "auth-api: GET ${me_path} -> 200 with email"
    else
      log_fail "auth-api: GET ${me_path} -> 200 but missing email field"
    fi
  else
    log_fail "auth-api: GET ${me_path} -> $status (expected 200)"
  fi

  # GET /api/dashboard/summary -> 200, valid JSON
  local summary_path
  summary_path=$(api_path "/dashboard/summary")
  response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${URL}${summary_path}" 2>/dev/null || true)
  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "200" ]]; then
    local valid_json
    valid_json=$(echo "$body" | python3 -c "import sys,json; json.load(sys.stdin); print('yes')" 2>/dev/null || echo "no")
    if [[ "$valid_json" == "yes" ]]; then
      log_pass "auth-api: GET ${summary_path} -> 200 with valid JSON"
    else
      log_fail "auth-api: GET ${summary_path} -> 200 but invalid JSON"
    fi
  else
    log_fail "auth-api: GET ${summary_path} -> $status (expected 200)"
  fi

  # GET /api/config -> 200
  local config_path
  config_path=$(api_path "/config")
  status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${URL}${config_path}" 2>/dev/null || true)

  if [[ "$status" == "200" ]]; then
    log_pass "auth-api: GET ${config_path} -> 200"
  else
    log_fail "auth-api: GET ${config_path} -> $status (expected 200)"
  fi

  # POST /api/auth/logout with Bearer + CSRF -> 200
  local logout_path
  logout_path=$(api_path "/auth/logout")

  if [[ -n "$csrf_token" ]]; then
    response=$($CURL_BIN -s $CURL_K -w "\n%{http_code}" -X POST \
      -H "Authorization: Bearer ${token}" \
      -H "X-CSRF-Token: ${csrf_token}" \
      --connect-timeout 10 "${URL}${logout_path}" 2>/dev/null || true)
    status=$(echo "$response" | tail -1)

    if [[ "$status" == "200" ]]; then
      log_pass "auth-api: POST ${logout_path} with CSRF -> 200"
    else
      log_fail "auth-api: POST ${logout_path} with CSRF -> $status (expected 200)"
    fi

    # GET /api/auth/me with old token after logout -> 401
    status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" \
      -H "Authorization: Bearer ${token}" \
      --connect-timeout 10 "${URL}${me_path}" 2>/dev/null || true)

    if [[ "$status" == "401" ]]; then
      log_pass "auth-api: GET ${me_path} after logout -> 401 (token invalidated)"
    else
      log_fail "auth-api: GET ${me_path} after logout -> $status (expected 401)"
    fi
  else
    log_warn "auth-api: skipped logout/post-logout tests (no csrf_token in login response)"
  fi
}

# ============================================================
# Main
# ============================================================

echo -e "${BOLD}CUDly Deployment Test [${PROVIDER}]${NC}"
echo "========================================"
if [[ "$CURL_BIN" != "curl" ]]; then
  log_info "using OpenSSL curl: $CURL_BIN"
fi
echo -e "  ${CYAN}URL${NC}  $URL"
echo ""

# Warm up containers that may have scaled to zero
warmup_status=$($CURL_BIN -s $CURL_K -o /dev/null -w "%{http_code}" \
  --connect-timeout 60 --max-time 90 "${URL}/health" 2>/dev/null || echo "000")
warmup_status="${warmup_status: -3}"
if [[ "$warmup_status" != "000" ]]; then
  log_info "warm-up: container ready (HTTP $warmup_status)"
fi

# Connectivity check - skip remaining tests if unreachable
if ! test_connectivity; then
  log_skip "skipping remaining tests (target unreachable)"
  echo ""
  echo "========================================"
  echo -e "PASS: $PASS  FAIL: $FAIL  WARN: $WARN  SKIP: $SKIP"
  exit 1
fi

# HTTPS tests - only for HTTPS targets
if [[ "$DEFAULT_PROTOCOL" == "https" ]]; then
  test_https || true
  test_http_redirect || true
else
  log_skip "https: skipped (HTTP-only target)"
  log_skip "http-redirect: skipped (HTTP-only target)"
fi

# CORS and method enforcement (require OPTIONS/DELETE/PUT)
test_cors || true
test_method_enforcement || true

# API docs
test_api_docs || true

# Response time
test_response_time || true

# Auth tests
test_auth_flow || true
test_csrf_protection || true
test_authenticated_api || true

# ============================================================
# Summary
# ============================================================

echo "========================================"
echo -e "PASS: $PASS  FAIL: $FAIL  WARN: $WARN  SKIP: $SKIP"
echo ""

if [[ $FAIL -gt 0 ]]; then
  echo -e "${RED}${BOLD}RESULT: FAILURES DETECTED${NC}"
  exit 1
else
  echo -e "${GREEN}${BOLD}RESULT: ALL TESTS PASSED${NC}"
  exit 0
fi
