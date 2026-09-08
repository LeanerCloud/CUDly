#!/bin/bash
# Self-test for the git-secrets allowlist (#1972). Exercises the real
# scripts/setup-git-secrets.sh and .gitallowed in a throwaway repo, through
# the pre-commit hook scan path, in both directions: fixtures that must be
# caught, and fixtures that must scan clean.
#
# Fixture safety: every positive fixture below is a secret-shaped string
# that this script itself, and the repo's detect-private-key / git-secrets
# pre-commit hooks, would flag if it appeared as a contiguous literal. Each
# is assembled at runtime from adjacent string literals so this file's own
# source never contains a scannable token. Where a fixture shows \n below,
# it is the two literal characters backslash and n, as in a Go string
# literal, never an actual newline.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v git-secrets >/dev/null 2>&1; then
    echo "git-secrets not found on PATH; install it before running this test" >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/home"
# Isolate HOME: --register-aws reads ~/.aws/credentials, and git-secrets
# echoes the whole joined pattern list on a regex error, which would print
# real credentials into this log.
export HOME="$tmp/home"

git init -q "$tmp/repo"
mkdir -p "$tmp/repo/scripts"
cp "$REPO_ROOT/scripts/setup-git-secrets.sh" "$tmp/repo/scripts/setup-git-secrets.sh"
cp "$REPO_ROOT/.gitallowed" "$tmp/repo/.gitallowed"
cd "$tmp/repo"

if ! bash scripts/setup-git-secrets.sh > "$tmp/setup.log" 2>&1; then
    echo "scripts/setup-git-secrets.sh failed in the throwaway repo:" >&2
    cat "$tmp/setup.log" >&2
    exit 1
fi
# Same HOME-isolation reason: drop the provider before any further scan.
git config --unset-all secrets.providers

pass=0
fail=0

# Writes $content to $relpath, stages it, scans it through the hook path,
# compares the exit code to $expected, then unstages and removes it.
run_case() {
    local label=$1 expected=$2 relpath=$3 content=$4
    mkdir -p "$(dirname "$relpath")"
    printf '%s\n' "$content" >"$relpath"
    git add "$relpath"
    local actual=0
    git secrets --scan --cached "$relpath" >/dev/null 2>&1 || actual=$?
    git rm -q --cached "$relpath"
    rm -f "$relpath"
    if [ "$actual" -eq "$expected" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL: $label (expected exit $expected, got $actual)" >&2
    fi
}

# Same as run_case, for a file already on disk (the copied setup script and
# .gitallowed), left staged afterward like any other tracked file would be.
run_staged_case() {
    local label=$1 expected=$2 relpath=$3
    git add "$relpath"
    local actual=0
    git secrets --scan --cached "$relpath" >/dev/null 2>&1 || actual=$?
    if [ "$actual" -eq "$expected" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL: $label (expected exit $expected, got $actual)" >&2
    fi
}

# Fixtures, assembled from adjacent literals so this file scans clean.
KEY="AKIA""0123456789ABCDEF"
PEM="-----BEGIN ""PRIVATE KEY-----"
AIZA="AIza$(head -c 35 /dev/zero | tr '\0' X)"

# Must be caught (exit 1): the #1972 hole and the coverage this PR adds.
run_case "terraform resource with a real-shaped key" 1 a.tf \
    'resource "aws_iam_access_key" "x" { key = "'"$KEY"'" }'
run_case "terraform var reference alongside a key" 1 b.tf \
    'secret = var.x # '"$KEY"
run_case "go test const with a key" 1 c_test.go \
    'const k = "'"$KEY"'"'
run_case "testdata fixture with a key" 1 testdata/creds.txt \
    'key='"$KEY"
run_case "placeholder line with a key" 1 d.txt \
    'placeholder '"$KEY"
run_case "untruncated PKCS8 body" 1 e.json \
    '"private_key": "'"$PEM"'\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC\n"'
run_case "corrected GCP API key range" 1 f.txt \
    "$AIZA"

# Must scan clean (exit 0): what the tree contains, and what the allowlist
# must keep clean.
run_staged_case "setup script's own self-matching lines" 0 scripts/setup-git-secrets.sh
run_case "GCP type marker alone, no key material" 0 g.json \
    '{"type": "service_account", "project_id": "p"}'
run_case "Go format-string DSN" 0 h.go \
    '"postgres://%s:%s@%s:%d/%s?sslmode=%s",'
run_case "truncated PEM in a struct literal" 0 i.go \
    'PrivateKey: "'"$PEM"'\n...",'
run_case "truncated PEM in a JSON literal" 0 j.go \
    '"private_key": "'"$PEM"'\nMIIEvQIBADANBg...\n",'
run_case "PEM word fragments are not a real header" 0 k.txt \
    'this is PRIVATE data
algo EC here
-----BEGIN CERTIFICATE-----'
run_staged_case ".gitallowed self-scan" 0 .gitallowed

echo "$pass PASS, $fail FAIL"
[ "$fail" -eq 0 ]
