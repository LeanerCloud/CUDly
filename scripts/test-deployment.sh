#!/usr/bin/env bash
# Cross-provider, multi-platform deployment test harness for CUDly
# Validates that AWS, Azure, and GCP deployments are functionally equivalent
# across all compute platforms (Lambda, Fargate, Container Apps, AKS, Cloud Run, GKE).
#
# Usage:
#   ./scripts/test-deployment.sh                    # test all providers, all configured platforms
#   ./scripts/test-deployment.sh gcp                # test all GCP platforms + CDN
#   ./scripts/test-deployment.sh aws/lambda         # test specific platform only
#   ./scripts/test-deployment.sh aws/lambda gcp     # mix of specific + all
#
# Environment variables:
#   # CDN/frontend URLs (legacy, also used for CDN target when platform URLs are set)
#   AWS_URL=https://<aws-domain>               (defaults to https://lambda-dev.cudly.leanercloud.com)
#   GCP_URL=https://cudly-dev.local            (defaults to https://cudly-dev.local)
#   AZURE_URL=https://<azure-domain>           (no default - set to test Azure)
#
#   # Platform-specific compute URLs (set to test individual platforms)
#   AWS_LAMBDA_URL=https://<lambda-domain>
#   AWS_FARGATE_URL=http://<alb-domain>
#   AZURE_CONTAINER_APPS_URL=https://<ca-domain>
#   AZURE_AKS_URL=http://<aks-domain>
#   GCP_CLOUD_RUN_URL=https://<cr-domain>
#   GCP_GKE_URL=http://<gke-domain>
#
#   # DNS resolve overrides (for self-signed certs / dev without DNS)
#   GCP_RESOLVE=               (applies to GCP CDN URL)
#   GCP_CLOUD_RUN_RESOLVE=<ip>               (falls back to GCP_RESOLVE if unset)
#   GCP_GKE_RESOLVE=<ip>
#
#   # Auth test credentials
#   TEST_EMAIL=user@example.com                (required for auth tests)
#   TEST_PASSWORD=secret                       (required for auth tests, base64-encoded)
#
# When no platform-specific URLs (*_LAMBDA_URL, *_FARGATE_URL, etc.) are set for
# a provider, the script behaves identically to the legacy single-URL mode: tests
# the CDN URL only, labeled as "frontend".
#
# When platform-specific URLs ARE set, each configured platform is tested
# separately, plus the CDN URL (if set) is tested as a "cdn" target.
# HTTP-only platforms (Fargate ALB, AKS, GKE) skip HTTPS and redirect tests.
# No-frontend platforms skip SPA routing and static file tests.

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'
BOLD='\033[1m'

# Counters (global, incremented by log_* functions)
PASS=0
FAIL=0
SKIP=0
WARN=0

# Per-target summary tracking (parallel indexed arrays for bash 3 compat)
TARGET_LABELS=()
TARGET_PASS=()
TARGET_FAIL=()
TARGET_WARN=()
TARGET_SKIP=()

# Platform definitions: cloud|platform|env_prefix|has_frontend|default_protocol
PLATFORM_DEFS=(
  "aws|lambda|AWS_LAMBDA|yes|https"
  "aws|fargate|AWS_FARGATE|no|http"
  "azure|container-apps|AZURE_CONTAINER_APPS|yes|https"
  "azure|aks|AZURE_AKS|no|http"
  "gcp|cloud-run|GCP_CLOUD_RUN|yes|https"
  "gcp|gke|GCP_GKE|no|http"
)

# Auto-detect frontend URL from terraform output for a given provider
detect_frontend_url() {
  local provider="$1"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  local env_dir="${script_dir}/../terraform/environments/${provider}"
  if [[ -d "$env_dir" ]]; then
    local url
    if [[ "$provider" == "gcp" ]]; then
      url=$(cd "$env_dir" && GOOGLE_APPLICATION_CREDENTIALS="" terraform output -raw frontend_url 2>/dev/null) || true
    else
      url=$(cd "$env_dir" && terraform output -raw frontend_url 2>/dev/null) || true
    fi
    [[ -n "$url" ]] && echo "$url"
  fi
}

# Strip trailing slash from a URL
strip_trailing_slash() {
  local url="$1"
  echo "${url%/}"
}

# Default endpoints - auto-detect from terraform outputs if not set via env
AWS_URL=$(strip_trailing_slash "${AWS_URL:-$(detect_frontend_url aws)}")
GCP_URL=$(strip_trailing_slash "${GCP_URL:-$(detect_frontend_url gcp)}")
GCP_RESOLVE="${GCP_RESOLVE:-}"
AZURE_URL=$(strip_trailing_slash "${AZURE_URL:-$(detect_frontend_url azure)}")

# Platform-specific URLs (empty = not configured)
AWS_LAMBDA_URL="${AWS_LAMBDA_URL:-}"
AWS_FARGATE_URL="${AWS_FARGATE_URL:-}"
AZURE_CONTAINER_APPS_URL="${AZURE_CONTAINER_APPS_URL:-}"
AZURE_AKS_URL="${AZURE_AKS_URL:-}"
GCP_CLOUD_RUN_URL="${GCP_CLOUD_RUN_URL:-}"
GCP_GKE_URL="${GCP_GKE_URL:-}"

# Platform-specific resolve IPs (with fallbacks)
GCP_CLOUD_RUN_RESOLVE="${GCP_CLOUD_RUN_RESOLVE:-$GCP_RESOLVE}"
GCP_GKE_RESOLVE="${GCP_GKE_RESOLVE:-}"

# Auth test credentials
TEST_EMAIL="${TEST_EMAIL:-}"
TEST_PASSWORD="${TEST_PASSWORD:-}"

# Prefer OpenSSL-based curl over macOS SecureTransport curl.
# macOS system curl (SecureTransport) disables SNI with --insecure, breaking
# connections to hosts that need SNI (e.g., GCP load balancer with self-signed cert).
# Homebrew curl with OpenSSL handles --cacert and --insecure correctly.
CURL_BIN="curl"
for candidate in /opt/homebrew/opt/curl/bin/curl /usr/local/opt/curl/bin/curl; do
  if [[ -x "$candidate" ]] && "$candidate" --version 2>/dev/null | grep -q OpenSSL; then
    CURL_BIN="$candidate"
    break
  fi
done

# Per-target curl options, set by setup_curl_options.
# CURL_K: TLS verification override (--cacert or --insecure)
# CURL_RESOLVE: --resolve flag to map hostname to IP (for dev without DNS)
CURL_K=""
CURL_RESOLVE=""
CERT_FILE=""

# ============================================================
# CLI argument parsing - supports provider and provider/platform
# ============================================================

PROVIDERS=()
PLATFORM_FILTERS=()
HAS_PLATFORM_FILTERS=false

if [[ $# -gt 0 ]]; then
  for arg in "$@"; do
    if [[ "$arg" == */* ]]; then
      # provider/platform syntax
      _provider="${arg%%/*}"
      PLATFORM_FILTERS+=("$arg")
      HAS_PLATFORM_FILTERS=true
      # Add provider if not already present
      _found=false
      for p in "${PROVIDERS[@]+"${PROVIDERS[@]}"}"; do
        [[ "$p" == "$_provider" ]] && _found=true && break
      done
      [[ "$_found" == "false" ]] && PROVIDERS+=("$_provider")
    else
      # Bare provider
      _found=false
      for p in "${PROVIDERS[@]+"${PROVIDERS[@]}"}"; do
        [[ "$p" == "$arg" ]] && _found=true && break
      done
      [[ "$_found" == "false" ]] && PROVIDERS+=("$arg")
    fi
  done
else
  PROVIDERS=(aws gcp azure)
fi

# ============================================================
# Helper functions
# ============================================================

# Get the CDN/legacy URL for a provider
get_cdn_url() {
  local provider="$1"
  case "$provider" in
    aws)   echo "$AWS_URL" ;;
    gcp)   echo "$GCP_URL" ;;
    azure) echo "$AZURE_URL" ;;
  esac
}

# Get the CDN resolve IP for a provider (empty if not needed)
get_cdn_resolve_ip() {
  local provider="$1"
  case "$provider" in
    gcp) echo "$GCP_RESOLVE" ;;
    *)   echo "" ;;
  esac
}

# Get platform URL from env_prefix (e.g., AWS_LAMBDA -> $AWS_LAMBDA_URL)
get_platform_url() {
  local env_prefix="$1"
  eval "echo \"\${${env_prefix}_URL:-}\""
}

# Get platform resolve IP from env_prefix (e.g., GCP_CLOUD_RUN -> $GCP_CLOUD_RUN_RESOLVE)
get_platform_resolve_ip() {
  local env_prefix="$1"
  eval "echo \"\${${env_prefix}_RESOLVE:-}\""
}

# API path helper - container always uses /api/ prefix for API routes
# Health endpoint is at /health (dedicated handler, not behind /api/)
api_path() {
  local provider="$1"
  local path="$2"
  echo "/api${path}"
}

log_pass() { ((PASS++)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { ((FAIL++)); echo -e "  ${RED}FAIL${NC} $1"; }
log_skip() { ((SKIP++)); echo -e "  ${YELLOW}SKIP${NC} $1"; }
log_warn() { ((WARN++)); echo -e "  ${YELLOW}WARN${NC} $1"; }
log_info() { echo -e "  ${CYAN}INFO${NC} $1"; }

# Check if a provider/platform pair is allowed by CLI filters
is_platform_allowed() {
  local provider="$1"
  local platform="$2"

  # No filters -> allow everything
  [[ "$HAS_PLATFORM_FILTERS" == "false" ]] && return 0

  # Check if this exact provider/platform is in the filter list
  for filter in "${PLATFORM_FILTERS[@]}"; do
    [[ "$filter" == "${provider}/${platform}" ]] && return 0
  done

  # Check if the provider was specified as a bare arg (no platform-specific filter)
  local provider_has_filter=false
  for filter in "${PLATFORM_FILTERS[@]}"; do
    [[ "$filter" == "${provider}/"* ]] && provider_has_filter=true && break
  done

  # If no platform-specific filters exist for this provider, allow all its platforms
  [[ "$provider_has_filter" == "false" ]] && return 0

  return 1
}

# Check if any platform-specific URLs are configured for a provider
has_platform_urls() {
  local provider="$1"
  for def in "${PLATFORM_DEFS[@]}"; do
    IFS='|' read -r cloud platform env_prefix has_frontend default_protocol <<< "$def"
    if [[ "$cloud" == "$provider" ]]; then
      local url
      url=$(get_platform_url "$env_prefix")
      [[ -n "$url" ]] && return 0
    fi
  done
  return 1
}

# ============================================================
# setup_curl_options - configure CURL_K, CURL_RESOLVE, CERT_FILE
# ============================================================

setup_curl_options() {
  local url="$1"
  local resolve_ip="$2"

  CURL_K=""
  CURL_RESOLVE=""
  CERT_FILE=""

  if [[ -n "$resolve_ip" ]]; then
    # Extract hostname from URL for --resolve
    local hostname
    hostname=$(echo "$url" | sed 's|https\?://||; s|/.*||')
    CURL_RESOLVE="--resolve ${hostname}:443:${resolve_ip} --resolve ${hostname}:80:${resolve_ip}"

    # For self-signed certs: extract the server cert via openssl and use --cacert.
    # This works with OpenSSL-based curl. macOS SecureTransport curl ignores --cacert
    # for trust and also disables SNI with --insecure, so OpenSSL curl is required.
    CERT_FILE=$(mktemp /tmp/curl-cert.XXXXXX)
    if echo | openssl s_client -connect "${resolve_ip}:443" -servername "${hostname}" 2>/dev/null \
         | openssl x509 -outform PEM > "$CERT_FILE" 2>/dev/null && [[ -s "$CERT_FILE" ]]; then
      if "$CURL_BIN" --version 2>/dev/null | grep -q OpenSSL; then
        CURL_K="--cacert ${CERT_FILE}"
        log_info "using --resolve ${hostname} -> ${resolve_ip} (cert pinned via --cacert)"
      else
        CURL_K="-k"
        rm -f "$CERT_FILE"
        CERT_FILE=""
        log_warn "using --resolve ${hostname} -> ${resolve_ip} with --insecure (no OpenSSL curl; SNI may break)"
      fi
    else
      CURL_K="-k"
      rm -f "$CERT_FILE"
      CERT_FILE=""
      log_warn "server at ${resolve_ip} returned no cert, falling back to --insecure"
    fi
  elif [[ "$url" =~ ^https://[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+ ]]; then
    CURL_K="-k"
    log_info "using --insecure for bare IP (self-signed cert expected)"
  fi
}

cleanup_cert_file() {
  [[ -n "${CERT_FILE:-}" && -f "${CERT_FILE}" ]] && rm -f "$CERT_FILE"
  CERT_FILE=""
}

# ============================================================
# Test functions
# ============================================================

test_connectivity() {
  local provider="$1" url="$2"
  local status
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" -L --connect-timeout 10 --max-time 15 "$url/" 2>/dev/null || echo "000")
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

test_https() {
  local provider="$1" url="$2"

  # Test that HTTPS works
  local status
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}/" 2>/dev/null || true)

  if [[ "$status" =~ ^[23] ]]; then
    log_pass "https: HTTPS connection successful (HTTP $status)"
  else
    log_fail "https: HTTPS connection failed (HTTP $status)"
  fi

  # Test TLS version
  local tls_version
  tls_version=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -v "${url}/" 2>&1 | grep -oE 'TLSv?[0-9.]+' | head -1 || true)
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
  local provider="$1" url="$2"

  # Extract host from URL
  local host
  host=$(echo "$url" | sed 's|https://||')
  local http_url="http://${host}/"

  local status redirect_url
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" --connect-timeout 10 "$http_url" 2>/dev/null || true)
  redirect_url=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{redirect_url}" --connect-timeout 10 "$http_url" 2>/dev/null || true)

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
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null || true)

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
    log_warn "auth: login returned 400 - $(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('error','unknown'))" 2>/dev/null || true)"
  elif [[ "$status" == "401" ]]; then
    log_warn "auth: login returned 401 (credentials may be wrong or password encoding differs)"
  elif [[ "$status" == "429" ]]; then
    log_warn "auth: login returned 429 (rate limited)"
  else
    log_fail "auth: login returned $status"
  fi

  # Test that unauthenticated API calls are properly rejected
  local protected_path
  protected_path=$(api_path "$provider" "/configs")
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${protected_path}" 2>/dev/null || true)

  if [[ "$status" == "401" || "$status" == "403" ]]; then
    log_pass "auth: protected endpoint ${protected_path} returns $status without token"
  elif [[ "$status" == "200" ]]; then
    log_fail "auth: protected endpoint ${protected_path} accessible without auth (returned 200)"
  else
    log_info "auth: protected endpoint ${protected_path} returned $status"
  fi
}

test_csrf_protection() {
  local provider="$1" url="$2"

  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "csrf: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "$provider" "/auth/login")

  # Login to get Bearer token
  local response status body
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null || true)

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
  logout_path=$(api_path "$provider" "/auth/logout")
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${url}${logout_path}" 2>/dev/null || true)

  status=$(echo "$response" | tail -1)
  body=$(echo "$response" | sed '$d')

  if [[ "$status" == "403" ]]; then
    log_pass "csrf: POST ${logout_path} without CSRF token -> 403"
    # Verify response mentions CSRF
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

  # Clean up: logout with CSRF token so the session is invalidated
  local csrf_token
  csrf_token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('csrf_token',''))" 2>/dev/null || true)
  # Re-login since the CSRF test may have consumed the session state
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null || true)
  body=$(echo "$response" | sed '$d')
  token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || true)
  csrf_token=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('csrf_token',''))" 2>/dev/null || true)
  if [[ -n "$token" && -n "$csrf_token" ]]; then
    $CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -X POST \
      -H "Authorization: Bearer ${token}" \
      -H "X-CSRF-Token: ${csrf_token}" \
      --connect-timeout 10 "${url}${logout_path}" 2>/dev/null || true
  fi
}

test_authenticated_api() {
  local provider="$1" url="$2"

  if [[ -z "$TEST_EMAIL" || -z "$TEST_PASSWORD" ]]; then
    log_warn "auth-api: skipped (set TEST_EMAIL and TEST_PASSWORD to enable)"
    return
  fi

  local login_path
  login_path=$(api_path "$provider" "/auth/login")

  # Login
  local response status body
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}" \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null || true)

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
  me_path=$(api_path "$provider" "/auth/me")
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${url}${me_path}" 2>/dev/null || true)
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
  summary_path=$(api_path "$provider" "/dashboard/summary")
  response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${url}${summary_path}" 2>/dev/null || true)
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
  config_path=$(api_path "$provider" "/config")
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${token}" \
    --connect-timeout 10 "${url}${config_path}" 2>/dev/null || true)

  if [[ "$status" == "200" ]]; then
    log_pass "auth-api: GET ${config_path} -> 200"
  else
    log_fail "auth-api: GET ${config_path} -> $status (expected 200)"
  fi

  # POST /api/auth/logout with Bearer token + CSRF -> 200
  local logout_path
  logout_path=$(api_path "$provider" "/auth/logout")

  if [[ -n "$csrf_token" ]]; then
    response=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -w "\n%{http_code}" -X POST \
      -H "Authorization: Bearer ${token}" \
      -H "X-CSRF-Token: ${csrf_token}" \
      --connect-timeout 10 "${url}${logout_path}" 2>/dev/null || true)
    status=$(echo "$response" | tail -1)

    if [[ "$status" == "200" ]]; then
      log_pass "auth-api: POST ${logout_path} with CSRF -> 200"
    else
      log_fail "auth-api: POST ${logout_path} with CSRF -> $status (expected 200)"
    fi

    # GET /api/auth/me with old token after logout -> 401
    status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" \
      -H "Authorization: Bearer ${token}" \
      --connect-timeout 10 "${url}${me_path}" 2>/dev/null || true)

    if [[ "$status" == "401" ]]; then
      log_pass "auth-api: GET ${me_path} after logout -> 401 (token invalidated)"
    else
      log_fail "auth-api: GET ${me_path} after logout -> $status (expected 401)"
    fi
  else
    log_warn "auth-api: skipped logout/post-logout tests (no csrf_token in login response)"
  fi
}

test_cors() {
  local provider="$1" url="$2"
  # Test CORS on API endpoint (not /health which is diagnostic and doesn't need CORS)
  local cors_path
  cors_path=$(api_path "$provider" "/auth/login")

  local headers
  headers=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -D - -o /dev/null --connect-timeout 10 \
    -X OPTIONS \
    -H "Origin: https://example.com" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: Content-Type, Authorization" \
    "${url}${cors_path}" 2>/dev/null || true)

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

test_method_enforcement() {
  local provider="$1" url="$2"

  # DELETE /api/auth/login -> 404 (no route match, router has no 405)
  local login_path
  login_path=$(api_path "$provider" "/auth/login")
  local status
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" -X DELETE \
    --connect-timeout 10 "${url}${login_path}" 2>/dev/null || true)

  if [[ "$status" == "404" || "$status" == "405" ]]; then
    log_pass "method-enforcement: DELETE ${login_path} -> $status"
  else
    log_fail "method-enforcement: DELETE ${login_path} -> $status (expected 404/405)"
  fi

  # PUT /health -> 404
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" -X PUT \
    --connect-timeout 10 "${url}/health" 2>/dev/null || true)

  if [[ "$status" == "404" || "$status" == "405" ]]; then
    log_pass "method-enforcement: PUT /health -> $status"
  else
    log_fail "method-enforcement: PUT /health -> $status (expected 404/405)"
  fi
}

test_api_docs() {
  local provider="$1" url="$2"
  local docs_base="/api/docs"

  # GET /api/docs/openapi.yaml -> 200, YAML content
  local status content_type
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${docs_base}/openapi.yaml" 2>/dev/null || true)

  if [[ "$status" == "200" ]]; then
    log_pass "api-docs: ${docs_base}/openapi.yaml -> 200"
  else
    log_fail "api-docs: ${docs_base}/openapi.yaml -> $status (expected 200)"
  fi

  # GET /api/docs -> 200, HTML content (Swagger UI)
  status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" --connect-timeout 10 "${url}${docs_base}" 2>/dev/null || true)
  content_type=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -D - -o /dev/null --connect-timeout 10 "${url}${docs_base}" 2>/dev/null | grep -i "content-type:" | head -1 | tr -d '\r')

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
  local provider="$1" url="$2"
  local health_path="/health"

  local time_total
  time_total=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{time_total}" --connect-timeout 10 "${url}${health_path}" 2>/dev/null || true)

  if (( $(echo "$time_total < 1.0" | bc -l 2>/dev/null || echo 0) )); then
    log_pass "response-time: ${health_path} in ${time_total}s"
  elif (( $(echo "$time_total < 3.0" | bc -l 2>/dev/null || echo 0) )); then
    log_warn "response-time: ${health_path} in ${time_total}s (slow)"
  else
    log_fail "response-time: ${health_path} in ${time_total}s (very slow, >3s)"
  fi
}

# ============================================================
# run_tests_for_target - run the full test suite for one target
# ============================================================

run_tests_for_target() {
  local provider="$1"
  local platform="$2"
  local url="$3"
  local has_frontend="$4"
  local default_protocol="$5"
  local resolve_ip="$6"

  local label="${provider}/${platform}"

  echo -e "${BOLD}${CYAN}[${label}]${NC} ${url}"
  echo "----------------------------------------"

  # Save counters for per-target delta tracking
  local saved_pass=$PASS saved_fail=$FAIL saved_warn=$WARN saved_skip=$SKIP

  # Set up curl options for this target
  setup_curl_options "$url" "$resolve_ip"

  # Warm up containers that may have scaled to zero (e.g. Azure Container Apps)
  local warmup_status
  warmup_status=$($CURL_BIN -s $CURL_K $CURL_RESOLVE -o /dev/null -w "%{http_code}" \
    --connect-timeout 60 --max-time 90 "${url}/health" 2>/dev/null || echo "000")
  warmup_status="${warmup_status: -3}"
  if [[ "$warmup_status" != "000" ]]; then
    log_info "warm-up: container ready (HTTP $warmup_status)"
  fi

  # Connectivity check first - skip remaining tests if unreachable
  if ! test_connectivity "$provider" "$url"; then
    log_skip "skipping remaining tests (target unreachable)"
    TARGET_LABELS+=("$label")
    TARGET_PASS+=($((PASS - saved_pass)))
    TARGET_FAIL+=($((FAIL - saved_fail)))
    TARGET_WARN+=($((WARN - saved_warn)))
    TARGET_SKIP+=($((SKIP - saved_skip)))
    cleanup_cert_file
    echo ""
    return
  fi

  # HTTPS tests - only for HTTPS targets
  if [[ "$default_protocol" == "https" ]]; then
    test_https "$provider" "$url" || true
    test_http_redirect "$provider" "$url" || true
  else
    log_skip "https: skipped (HTTP-only target)"
    log_skip "http-redirect: skipped (HTTP-only target)"
  fi

  # CORS and method enforcement (require OPTIONS/DELETE/PUT not supported by TF data "http")
  test_cors "$provider" "$url" || true
  test_method_enforcement "$provider" "$url" || true

  # API docs endpoint (public, always available)
  test_api_docs "$provider" "$url" || true

  # Response time
  test_response_time "$provider" "$url" || true

  # Auth tests - require TEST_EMAIL and TEST_PASSWORD
  test_auth_flow "$provider" "$url" || true
  test_csrf_protection "$provider" "$url" || true
  test_authenticated_api "$provider" "$url" || true

  # Record per-target results
  TARGET_LABELS+=("$label")
  TARGET_PASS+=($((PASS - saved_pass)))
  TARGET_FAIL+=($((FAIL - saved_fail)))
  TARGET_WARN+=($((WARN - saved_warn)))
  TARGET_SKIP+=($((SKIP - saved_skip)))

  cleanup_cert_file
  echo ""
}

# ============================================================
# Main
# ============================================================

echo -e "${BOLD}CUDly Cross-Provider Deployment Test${NC}"
echo "========================================"
if [[ "$CURL_BIN" != "curl" ]]; then
  echo -e "  ${CYAN}INFO${NC} using OpenSSL curl: $CURL_BIN"
fi
echo ""

for provider in "${PROVIDERS[@]}"; do
  if has_platform_urls "$provider"; then
    # Multi-platform mode: test each configured platform separately
    for def in "${PLATFORM_DEFS[@]}"; do
      IFS='|' read -r cloud platform env_prefix has_frontend default_protocol <<< "$def"
      [[ "$cloud" != "$provider" ]] && continue

      # Check CLI filter
      is_platform_allowed "$provider" "$platform" || continue

      _url=$(get_platform_url "$env_prefix")
      [[ -z "$_url" ]] && continue

      _resolve=$(get_platform_resolve_ip "$env_prefix")
      run_tests_for_target "$provider" "$platform" "$_url" "$has_frontend" "$default_protocol" "$_resolve"
    done

    # Also test CDN URL if configured and allowed by filter
    _cdn_url=$(get_cdn_url "$provider")
    if [[ -n "$_cdn_url" ]] && is_platform_allowed "$provider" "cdn"; then
      _cdn_resolve=$(get_cdn_resolve_ip "$provider")
      run_tests_for_target "$provider" "cdn" "$_cdn_url" "yes" "https" "$_cdn_resolve"
    fi
  else
    # Legacy single-URL mode: identical to original behavior
    # If user specified provider/platform filters for this provider, skip it
    # (no platform URLs configured, so specific platform requests can't be fulfilled)
    if [[ "$HAS_PLATFORM_FILTERS" == "true" ]]; then
      _provider_has_filter=false
      for filter in "${PLATFORM_FILTERS[@]}"; do
        [[ "$filter" == "${provider}/"* ]] && _provider_has_filter=true && break
      done
      [[ "$_provider_has_filter" == "true" ]] && continue
    fi

    _url=$(get_cdn_url "$provider")

    if [[ -z "$_url" ]]; then
      echo -e "${BOLD}${CYAN}[${provider}]${NC} (no URL configured)"
      echo "----------------------------------------"
      log_skip "no URL configured for $provider"
      TARGET_LABELS+=("$provider")
      TARGET_PASS+=(0)
      TARGET_FAIL+=(0)
      TARGET_WARN+=(0)
      TARGET_SKIP+=(1)
      echo ""
      continue
    fi

    _resolve=$(get_cdn_resolve_ip "$provider")
    run_tests_for_target "$provider" "frontend" "$_url" "yes" "https" "$_resolve"
  fi
done

# ============================================================
# Summary
# ============================================================

echo "========================================"
echo -e "${BOLD}Summary${NC}"
echo "========================================"
for i in $(seq 0 $((${#TARGET_LABELS[@]} - 1))); do
  _label="${TARGET_LABELS[$i]}"
  _p="${TARGET_PASS[$i]}"
  _f="${TARGET_FAIL[$i]}"
  _w="${TARGET_WARN[$i]}"
  _s="${TARGET_SKIP[$i]}"
  printf "  %-24s PASS: %-4s FAIL: %-4s WARN: %-4s SKIP: %-4s\n" "[${_label}]" "$_p" "$_f" "$_w" "$_s"
done

if [[ ${#TARGET_LABELS[@]} -gt 1 ]]; then
  echo "  ----------------------------------------"
  printf "  %-24s PASS: %-4s FAIL: %-4s WARN: %-4s SKIP: %-4s\n" "Total" "$PASS" "$FAIL" "$WARN" "$SKIP"
fi

echo ""

if [[ $FAIL -gt 0 ]]; then
  echo -e "${RED}${BOLD}RESULT: FAILURES DETECTED${NC}"
  exit 1
else
  echo -e "${GREEN}${BOLD}RESULT: ALL TESTS PASSED${NC}"
  exit 0
fi
