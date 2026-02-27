#!/usr/bin/env bash
# Cross-provider deployment test harness for CUDly
# Validates that AWS, Azure, and GCP deployments are functionally equivalent
#
# Usage:
#   ./scripts/test-deployment.sh                    # test all providers
#   ./scripts/test-deployment.sh gcp                # test single provider
#   ./scripts/test-deployment.sh aws gcp            # test specific providers
#
# Environment variables:
#   GCP_URL=https://<gcp-lb-ip>               (defaults to https://)
#   AWS_URL=https://<aws-domain>               (defaults to https://fargate-dev.cudly.leanercloud.com)
#   AZURE_URL=https://<azure-domain>           (no default - set to test Azure)
#   TEST_EMAIL=user@example.com                (required for auth tests)
#   TEST_PASSWORD=secret                       (required for auth tests)

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

# Default endpoints
GCP_URL="${GCP_URL:-https://}"
AWS_URL="${AWS_URL:-https://fargate-dev.cudly.leanercloud.com}"
AZURE_URL="${AZURE_URL:-}"
TEST_EMAIL="${TEST_EMAIL:-}"
TEST_PASSWORD="${TEST_PASSWORD:-}"

# Determine which providers to test
PROVIDERS=()
if [[ $# -gt 0 ]]; then
  PROVIDERS=("$@")
else
  PROVIDERS=(aws gcp azure)
fi

get_url() {
  local provider="$1"
  case "$provider" in
    aws)   echo "$AWS_URL" ;;
    gcp)   echo "$GCP_URL" ;;
    azure) echo "$AZURE_URL" ;;
  esac
}

# Whether the provider serves frontend through CDN (vs direct container access)
has_cdn_frontend() {
  local provider="$1"
  case "$provider" in
    aws)   return 0 ;;  # CloudFront
    gcp)   return 0 ;;  # Cloud CDN
    azure) return 1 ;;  # Direct Container Apps (Front Door not fully wired)
  esac
}

# Whether the provider uses /api prefix for API routes through CDN
uses_api_prefix() {
  local provider="$1"
  case "$provider" in
    aws)   return 0 ;;  # CloudFront routes /api/* to backend
    gcp)   return 0 ;;  # URL map routes /api/* to Cloud Run
    azure) return 1 ;;  # Direct Container Apps access, no /api prefix
  esac
}

api_path() {
  local provider="$1"
  local path="$2"
  if uses_api_prefix "$provider"; then
    echo "/api${path}"
  else
    echo "${path}"
  fi
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
  local provider="$1" url="$2"
  local status
  status=$(curl -sk -o /dev/null -w "%{http_code}" -L --connect-timeout 10 --max-time 15 "$url/" 2>/dev/null || echo "000")
  # Take last 3 chars only (final status code after redirects)
  status="${status: -3}"

  if [[ "$status" == "000" ]]; then
    log_fail "connectivity: $url unreachable (connection failed)"
    return 1
  elif [[ "$status" =~ ^[2345] ]]; then
    log_pass "connectivity: $url reachable (HTTP $status)"
    return 0
  else
    log_fail "connectivity: $url returned HTTP $status"
    return 1
  fi
}

test_health_endpoint() {
  local provider="$1" url="$2"
  local health_path
  health_path=$(api_path "$provider" "/health")

  local response
  response=$(curl -sk --connect-timeout 10 --max-time 15 "${url}${health_path}" 2>/dev/null)
  local status=$?

  if [[ $status -ne 0 ]]; then
    log_fail "health: ${health_path} - connection failed"
    return 1
  fi

  # Check JSON response has expected fields
  local health_status
  health_status=$(echo "$response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)

  if [[ "$health_status" == "healthy" ]]; then
    log_pass "health: ${health_path} -> status=healthy"
  elif [[ "$health_status" == "degraded" ]]; then
    log_warn "health: ${health_path} -> status=degraded"
  elif [[ -n "$health_status" ]]; then
    log_fail "health: ${health_path} -> status=$health_status"
  else
    log_fail "health: ${health_path} -> invalid JSON response"
  fi

  # Verify expected JSON structure
  local has_checks
  has_checks=$(echo "$response" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'checks' in d else 'no')" 2>/dev/null)
  if [[ "$has_checks" == "yes" ]]; then
    log_pass "health: response contains 'checks' object"
  else
    log_fail "health: response missing 'checks' object"
  fi

  local has_timestamp
  has_timestamp=$(echo "$response" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'timestamp' in d else 'no')" 2>/dev/null)
  if [[ "$has_timestamp" == "yes" ]]; then
    log_pass "health: response contains 'timestamp'"
  else
    log_fail "health: response missing 'timestamp'"
  fi
}

test_https() {
  local provider="$1" url="$2"

  # Test that HTTPS works
  local status
  status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}/" 2>/dev/null)

  if [[ "$status" =~ ^[23] ]]; then
    log_pass "https: HTTPS connection successful (HTTP $status)"
  else
    log_fail "https: HTTPS connection failed (HTTP $status)"
  fi

  # Test TLS version
  local tls_version
  tls_version=$(curl -skv "${url}/" 2>&1 | grep -oE 'TLS [0-9.]+' | head -1)
  if [[ -n "$tls_version" ]]; then
    if [[ "$tls_version" =~ "TLS 1.2" || "$tls_version" =~ "TLS 1.3" ]]; then
      log_pass "https: $tls_version"
    else
      log_fail "https: weak TLS version ($tls_version)"
    fi
  else
    log_warn "https: could not determine TLS version"
  fi
}

test_http_redirect() {
  local provider="$1" url="$2"

  # Extract host from URL
  local host
  host=$(echo "$url" | sed 's|https://||')
  local http_url="http://${host}/"

  local status redirect_url
  status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "$http_url" 2>/dev/null)
  redirect_url=$(curl -sk -o /dev/null -w "%{redirect_url}" --connect-timeout 10 "$http_url" 2>/dev/null)

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

test_security_headers() {
  local provider="$1" url="$2"
  local health_path
  health_path=$(api_path "$provider" "/health")

  local headers
  headers=$(curl -sk -D - -o /dev/null --connect-timeout 10 "${url}${health_path}" 2>/dev/null)

  # Check critical security headers
  local header_checks=(
    "strict-transport-security:HSTS"
    "x-content-type-options:X-Content-Type-Options"
    "x-frame-options:X-Frame-Options"
  )

  for check in "${header_checks[@]}"; do
    local header_name="${check%%:*}"
    local header_label="${check##*:}"
    if echo "$headers" | grep -qi "^${header_name}:"; then
      log_pass "security-header: $header_label present"
    else
      log_fail "security-header: $header_label missing"
    fi
  done

  # Optional but recommended headers
  local optional_checks=(
    "referrer-policy:Referrer-Policy"
    "content-security-policy:Content-Security-Policy"
    "permissions-policy:Permissions-Policy"
  )

  for check in "${optional_checks[@]}"; do
    local header_name="${check%%:*}"
    local header_label="${check##*:}"
    if echo "$headers" | grep -qi "^${header_name}:"; then
      log_pass "security-header: $header_label present (optional)"
    else
      log_warn "security-header: $header_label missing (optional)"
    fi
  done
}

test_cors() {
  local provider="$1" url="$2"
  local health_path
  health_path=$(api_path "$provider" "/health")

  local headers
  headers=$(curl -sk -D - -o /dev/null --connect-timeout 10 \
    -X OPTIONS \
    -H "Origin: https://example.com" \
    -H "Access-Control-Request-Method: POST" \
    "${url}${health_path}" 2>/dev/null)

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

  # Check if origin is reflected or wildcard
  local acao
  acao=$(echo "$headers" | grep -i "access-control-allow-origin" | head -1 | tr -d '\r')
  if [[ -n "$acao" ]]; then
    log_info "cors: $acao"
  fi
}

test_frontend_serving() {
  local provider="$1" url="$2"

  if ! has_cdn_frontend "$provider"; then
    log_skip "frontend: $provider doesn't serve frontend via CDN"
    return
  fi

  # Test index.html
  local status content_type
  status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}/" 2>/dev/null)
  content_type=$(curl -sk -D - -o /dev/null --connect-timeout 10 "${url}/" 2>/dev/null | grep -i "content-type:" | head -1 | tr -d '\r')

  if [[ "$status" == "200" ]]; then
    log_pass "frontend: / returns 200"
  else
    log_fail "frontend: / returns $status (expected 200)"
  fi

  if echo "$content_type" | grep -qi "text/html"; then
    log_pass "frontend: / serves text/html"
  else
    log_warn "frontend: / content-type is '$content_type'"
  fi

  # Test static JS asset
  local js_status
  js_status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}/js/app.20b499b8.js" 2>/dev/null)
  if [[ "$js_status" == "200" ]]; then
    log_pass "frontend: JS asset returns 200"
  elif [[ "$js_status" == "404" ]]; then
    log_warn "frontend: JS asset returns 404 (may have different hash)"
  else
    log_fail "frontend: JS asset returns $js_status"
  fi

  # Test cache headers on static assets
  local cache_header
  cache_header=$(curl -sk -D - -o /dev/null --connect-timeout 10 "${url}/js/app.20b499b8.js" 2>/dev/null | grep -i "cache-control:" | head -1 | tr -d '\r')
  if echo "$cache_header" | grep -qi "max-age"; then
    log_pass "frontend: JS asset has cache-control with max-age"
    log_info "frontend: $cache_header"
  elif [[ "$js_status" == "200" ]]; then
    log_warn "frontend: JS asset missing cache-control header"
  fi
}

test_spa_routing() {
  local provider="$1" url="$2"

  if ! has_cdn_frontend "$provider"; then
    log_skip "spa-routing: $provider doesn't serve frontend via CDN"
    return
  fi

  # Test known SPA routes - these should return 200 with index.html, not 404
  local routes=("/settings" "/users" "/recommendations" "/groups")

  for route in "${routes[@]}"; do
    local status
    status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${route}" 2>/dev/null)
    if [[ "$status" == "200" ]]; then
      log_pass "spa-routing: ${route} returns 200 (index.html fallback)"
    elif [[ "$status" == "404" ]]; then
      log_fail "spa-routing: ${route} returns 404 (SPA routing broken)"
    else
      log_warn "spa-routing: ${route} returns $status"
    fi
  done
}

test_api_routing() {
  local provider="$1" url="$2"
  local health_path
  health_path=$(api_path "$provider" "/health")

  # Test that API routes reach the backend
  local status body
  body=$(curl -sk --connect-timeout 10 "${url}${health_path}" 2>/dev/null)
  status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${health_path}" 2>/dev/null)

  if [[ "$status" == "200" ]]; then
    log_pass "api-routing: ${health_path} proxied to backend (200)"
  elif [[ "$status" == "302" ]]; then
    log_fail "api-routing: ${health_path} got redirect (302) - should be proxied, not redirected"
  else
    log_fail "api-routing: ${health_path} returned $status"
  fi

  # Test auth endpoint exists
  local auth_path
  auth_path=$(api_path "$provider" "/auth/login")
  status=$(curl -sk -o /dev/null -w "%{http_code}" -X POST \
    -H "Content-Type: application/json" -d '{}' \
    --connect-timeout 10 "${url}${auth_path}" 2>/dev/null)

  if [[ "$status" =~ ^[4] ]]; then
    log_pass "api-routing: ${auth_path} reached backend (HTTP $status - expected for empty body)"
  elif [[ "$status" == "302" ]]; then
    log_fail "api-routing: ${auth_path} got redirect instead of proxy"
  else
    log_warn "api-routing: ${auth_path} returned $status"
  fi
}

test_auth_flow() {
  local provider="$1" url="$2"

  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "auth: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "$provider" "/auth/login")

  # Attempt login
  local response status
  response=$(curl -sk -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null)

  status=$(echo "$response" | tail -1)
  local body
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "200" ]]; then
    # Check for token/session in response
    local has_token
    has_token=$(echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); print('yes' if 'token' in d or 'session' in d or 'user' in d else 'no')" 2>/dev/null || echo "no")
    if [[ "$has_token" == "yes" ]]; then
      log_pass "auth: login succeeded with valid credentials"
    else
      log_warn "auth: login returned 200 but unexpected response format"
      log_info "auth: response: $(echo "$body" | head -c 200)"
    fi
  elif [[ "$status" == "400" ]]; then
    log_warn "auth: login returned 400 - $(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('error','unknown'))" 2>/dev/null)"
  elif [[ "$status" == "401" ]]; then
    log_warn "auth: login returned 401 (credentials may be wrong or password encoding differs)"
  else
    log_fail "auth: login returned $status"
  fi

  # Test that unauthenticated API calls are properly rejected
  local protected_path
  protected_path=$(api_path "$provider" "/configs")
  status=$(curl -sk -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${protected_path}" 2>/dev/null)

  if [[ "$status" == "401" || "$status" == "403" ]]; then
    log_pass "auth: protected endpoint ${protected_path} returns $status without token"
  elif [[ "$status" == "200" ]]; then
    log_fail "auth: protected endpoint ${protected_path} accessible without auth (returned 200)"
  else
    log_info "auth: protected endpoint ${protected_path} returned $status"
  fi
}

test_response_time() {
  local provider="$1" url="$2"
  local health_path
  health_path=$(api_path "$provider" "/health")

  local time_total
  time_total=$(curl -sk -o /dev/null -w "%{time_total}" --connect-timeout 10 "${url}${health_path}" 2>/dev/null)

  if (( $(echo "$time_total < 1.0" | bc -l 2>/dev/null || echo 0) )); then
    log_pass "response-time: ${health_path} in ${time_total}s"
  elif (( $(echo "$time_total < 3.0" | bc -l 2>/dev/null || echo 0) )); then
    log_warn "response-time: ${health_path} in ${time_total}s (slow)"
  else
    log_fail "response-time: ${health_path} in ${time_total}s (very slow, >3s)"
  fi
}

# ============================================================
# Main
# ============================================================

echo -e "${BOLD}CUDly Cross-Provider Deployment Test${NC}"
echo "========================================"
echo ""

for provider in "${PROVIDERS[@]}"; do
  url=$(get_url "$provider")
  echo -e "${BOLD}${CYAN}[$provider]${NC} ${url:-"(no URL configured)"}"
  echo "----------------------------------------"

  if [[ -z "$url" ]]; then
    log_skip "no URL configured for $provider"
    echo ""
    continue
  fi

  # Connectivity check first - skip remaining tests if unreachable
  if ! test_connectivity "$provider" "$url"; then
    log_skip "skipping remaining tests (provider unreachable)"
    echo ""
    continue
  fi

  test_https "$provider" "$url"
  test_http_redirect "$provider" "$url"
  test_health_endpoint "$provider" "$url"
  test_security_headers "$provider" "$url"
  test_cors "$provider" "$url"
  test_api_routing "$provider" "$url"
  test_auth_flow "$provider" "$url"
  test_response_time "$provider" "$url"

  # Frontend-specific tests (only for providers with CDN frontend)
  test_frontend_serving "$provider" "$url"
  test_spa_routing "$provider" "$url"

  echo ""
done

# ============================================================
# Summary
# ============================================================

echo "========================================"
echo -e "${BOLD}Summary${NC}"
echo "========================================"
echo -e "  ${GREEN}Passed:${NC}  $PASS"
echo -e "  ${RED}Failed:${NC}  $FAIL"
echo -e "  ${YELLOW}Warned:${NC}  $WARN"
echo -e "  ${YELLOW}Skipped:${NC} $SKIP"
echo ""

if [[ $FAIL -gt 0 ]]; then
  echo -e "${RED}${BOLD}RESULT: FAILURES DETECTED${NC}"
  exit 1
else
  echo -e "${GREEN}${BOLD}RESULT: ALL TESTS PASSED${NC}"
  exit 0
fi
