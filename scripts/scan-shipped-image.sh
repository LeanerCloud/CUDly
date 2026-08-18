#!/usr/bin/env bash
# scan-shipped-image.sh
#
# Scans the Go binaries inside a built container image, rather than the source
# tree they were built from.
#
# Every scanner in this repo's CI reads the repository: govulncheck runs in
# source mode over the six modules, Trivy runs with scan-type fs and config.
# None of them can see the artifact. Twice in a row a toolchain/CVE fix went
# green while the image still shipped the vulnerability (#1829/#1832 bumped the
# toolchain CI reads but not the one the image built on; #1833's first fix
# cleaned /app/cudly while /usr/local/bin/migrate stayed an upstream prebuilt
# release carrying go1.25.4 and 69 advisories, executed on every container start
# by scripts/entrypoint.sh with DB_AUTO_MIGRATE=true). That is issue #1836.
#
# WHAT IT SCANS. Every Go binary in the image, discovered by exporting the
# image filesystem and asking `go version` about every regular file in it.
# It deliberately does not take a list of paths: a third binary added later is
# exactly the thing that must not become invisible again. As a tripwire against
# a silently empty enumeration, /app/cudly must be among the binaries found.
#
# WHAT IT FAILS ON. Any advisory that has a published fixed version, in any of
# those binaries, at any severity. That is the whole class that actually
# occurred: a binary built on a superseded Go toolchain carries stdlib
# advisories fixed in a later patch release, and a stale third-party binary
# carries module advisories fixed in a later release. Both are actionable by
# rebuilding or bumping.
#
# WHAT IT TOLERATES, AND WHY. An advisory with no published fix. Today
# /app/cudly carries exactly one, GO-2026-5932 (golang.org/x/crypto/openpgp is
# unmaintained), introduced at "0" with no fixed version now or ever, and
# unreachable in this binary. Failing on it would make the gate permanently red
# with no available action, which is how gates get disabled. Tolerated
# advisories are still printed on every run, so they cannot go unnoticed. This
# is a property of the finding, not an allowlist of advisory IDs: nothing here
# has to be edited when the set of unfixable advisories changes, and an
# advisory becomes gating the moment upstream publishes a fix.
#
# WHAT IT DOES NOT COVER. OS package CVEs in the base image (musl, openssl,
# zlib, curl). govulncheck only knows about Go modules and the Go standard
# library. A Trivy scan-type: image step would cover those; it is not added
# here because the pinned alpine:3.21.3 runtime base already carries fixable
# CRITICAL/HIGH openssl, musl and zlib advisories, so such a gate would land
# red. Tracked separately.
#
# Exit 0 = every Go binary found, and no advisory with a published fix.
# Exit 1 = at least one fixable advisory, or the enumeration came back empty.
# Exit 2 = usage error or a missing prerequisite.
#
# Usage:
#   scripts/scan-shipped-image.sh IMAGE_REF
#
# Requires: docker, go, govulncheck, jq, tar.

set -euo pipefail

# The image is built by the Dockerfile's final stage, so this path is a build
# invariant, not a guess. Its absence means the enumeration below silently read
# the wrong filesystem, which is the failure mode that makes a scanner report a
# clean run it never performed.
readonly REQUIRED_BINARY="/app/cudly"

# classify_findings JSON_FILE LABEL
#
# Reads a `govulncheck -format json` stream and reports one line per advisory,
# split by whether upstream has published a fix. Returns 0 when every advisory
# is unfixable (including when there are none), 1 when at least one is fixable.
#
# Sourceable so scripts/test-scan-shipped-image.sh can exercise the verdict
# against recorded govulncheck output without docker or a built image.
classify_findings() {
  if [[ $# -ne 2 ]]; then
    echo "ERROR: classify_findings needs JSON_FILE and LABEL" >&2
    return 2
  fi
  local json="$1" label="$2"

  if [[ ! -f "$json" ]]; then
    echo "ERROR: no govulncheck output at $json" >&2
    return 2
  fi

  # The stream is concatenated JSON objects, hence -s. One advisory produces
  # several finding records (one per trace), so group by OSV id and keep the
  # first published fixed version seen for it.
  #
  # Every jq result is checked. errexit is suppressed inside a function whose
  # status is being tested by the caller, so an unchecked failure here would
  # leave the counts empty, make the comparisons below error out, and land in
  # the "clean" branch -- a scan reporting a verdict it never computed.
  local summary
  if ! summary="$(jq -s '
    [ .[] | select(has("finding")) | .finding ]
    | group_by(.osv)
    | map({
        osv: .[0].osv,
        fixed: (map(.fixed_version // empty) | first),
        modules: (map(.trace[0].module // "unknown") | unique | join(", "))
      })
    | sort_by(.osv)
  ' "$json")"; then
    echo "ERROR: could not parse govulncheck output at $json" >&2
    return 2
  fi

  local fixable unfixable
  if ! fixable="$(jq -r '[.[] | select(.fixed != null)] | length' <<<"$summary")" ||
    ! unfixable="$(jq -r '[.[] | select(.fixed == null)] | length' <<<"$summary")"; then
    echo "ERROR: could not summarise the advisories in $json" >&2
    return 2
  fi
  if ! [[ "$fixable" =~ ^[0-9]+$ && "$unfixable" =~ ^[0-9]+$ ]]; then
    echo "ERROR: unexpected advisory counts from $json" \
      "(fixable='$fixable', unfixable='$unfixable')" >&2
    return 2
  fi

  if [[ "$fixable" -gt 0 ]]; then
    jq -r '.[] | select(.fixed != null)
           | "    FIXABLE  \(.osv)  in \(.modules)  fixed in \(.fixed)"' <<<"$summary"
  fi
  if [[ "$unfixable" -gt 0 ]]; then
    jq -r '.[] | select(.fixed == null)
           | "    no fix   \(.osv)  in \(.modules)  (tolerated: no published fix)"' <<<"$summary"
  fi

  if [[ "$fixable" -gt 0 ]]; then
    echo "    => $label: $fixable fixable advisory/advisories, $unfixable without a fix"
    return 1
  fi
  echo "    => $label: clean ($unfixable advisory/advisories without a published fix)"
  return 0
}

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR: $1 is required but not on PATH" >&2
    exit 2
  fi
}

# Set by main() before anything that can fail, and only read by cleanup().
WORKDIR=""
CONTAINER_ID=""

cleanup() {
  if [[ -n "$CONTAINER_ID" ]]; then
    docker rm -f "$CONTAINER_ID" >/dev/null 2>&1 || true
  fi
  # Created by mktemp -d in this run, so nothing pre-existing is removed.
  if [[ -n "$WORKDIR" ]]; then
    rm -rf "$WORKDIR"
  fi
}

main() {
  if [[ $# -ne 1 ]]; then
    echo "Usage: $0 IMAGE_REF" >&2
    exit 2
  fi
  local image="$1"

  require_tool docker
  require_tool go
  require_tool govulncheck
  require_tool jq
  require_tool tar

  WORKDIR="$(mktemp -d)"
  trap cleanup EXIT
  local workdir="$WORKDIR"

  local rootfs="$workdir/rootfs"
  mkdir -p "$rootfs"

  echo "==> exporting the filesystem of $image"
  CONTAINER_ID="$(docker create "$image")"
  # /dev, /proc and /sys carry entries an unprivileged extract cannot recreate.
  docker export "$CONTAINER_ID" |
    tar -x -C "$rootfs" --exclude='dev/*' --exclude='proc/*' --exclude='sys/*'
  docker rm -f "$CONTAINER_ID" >/dev/null
  CONTAINER_ID=""

  # `go version FILE...` prints "<path>: go<version>" for each Go binary and
  # reports everything else on stderr, exiting non-zero because most files in
  # an image are not Go binaries. Its exit status is therefore not a verdict;
  # the empty-result check below is.
  #
  # Every regular file is offered, not only the executable ones: a Go binary
  # shipped without its execute bit, or chmod'd at runtime, is still a Go
  # binary that runs. Asking about all 1500-odd files in this image costs under
  # a second, so there is nothing to buy by narrowing the question.
  local file_list="$workdir/files.nul"
  find "$rootfs" -type f -print0 >"$file_list"
  local raw="$workdir/go-version.txt"
  if ! xargs -0 go version <"$file_list" >"$raw" 2>/dev/null; then
    : # expected, see above
  fi

  local binaries="$workdir/go-binaries.tsv"
  awk '
    /: go[0-9]/ {
      i = index($0, ": go")
      printf "%s\t%s\n", substr($0, 1, i - 1), substr($0, i + 2)
    }
  ' "$raw" | sort >"$binaries"

  if [[ ! -s "$binaries" ]]; then
    echo "ERROR: no Go binary found in $image." >&2
    echo "       An image that ships no Go binary is not this project's image," >&2
    echo "       so treat this as a broken scan, not a clean one." >&2
    exit 1
  fi

  if ! awk -v want="$rootfs$REQUIRED_BINARY" -F'\t' '$1 == want { found = 1 } END { exit !found }' "$binaries"; then
    echo "ERROR: $REQUIRED_BINARY is not among the Go binaries found in $image:" >&2
    sed "s|^$rootfs|         |" "$binaries" >&2
    echo "       The enumeration read something other than the shipped image." >&2
    exit 1
  fi

  local count
  count="$(wc -l <"$binaries" | tr -d ' ')"
  echo "==> $count Go binary/binaries in $image"

  local status=0
  local binpath toolchain rel out rc
  while IFS=$'\t' read -r binpath toolchain; do
    rel="${binpath#"$rootfs"}"
    echo "  -> $rel (built with $toolchain)"
    out="$workdir/govulncheck$(printf '%s' "$rel" | tr '/' '-').json"
    set +e
    govulncheck -mode=binary -format json "$binpath" >"$out" 2>"$out.err"
    rc=$?
    set -e
    # 0 is what JSON output returns even with findings; 3 is govulncheck's
    # documented "vulnerabilities found". Anything else is a scan error, and a
    # scan that did not run must not be reported as a binary with no findings.
    if [[ "$rc" -ne 0 && "$rc" -ne 3 ]]; then
      echo "ERROR: govulncheck failed on $rel (exit $rc)" >&2
      cat "$out.err" >&2
      status=1
      continue
    fi
    if ! classify_findings "$out" "$rel"; then
      status=1
    fi
  done <"$binaries"

  if [[ "$status" -ne 0 ]]; then
    echo "" >&2
    echo "FAILED: the shipped image carries at least one advisory with a published fix." >&2
    echo "        Rebuild the image on the current toolchain, or bump the dependency /" >&2
    echo "        third-party binary the advisory names above. A source-mode scan of" >&2
    echo "        this repository cannot see any of it." >&2
    exit 1
  fi

  echo ""
  echo "OK: $count Go binary/binaries scanned in $image; no advisory with a published fix."
  echo "    Advisories without a fix are listed above and are not gated; OS package CVEs"
  echo "    in the base image are out of scope for govulncheck (see the header)."
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
