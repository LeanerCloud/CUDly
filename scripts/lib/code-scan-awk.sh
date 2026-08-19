#!/usr/bin/env bash
# code-scan-awk.sh
#
# Shared awk helper functions for the guard suites that scan workflow and shell
# sources for what a step actually runs: test-ecr-delete-selection.sh,
# test-rds-deletion-protection-scope.sh and test-aws-tfstate-platform-key.sh.
# Sourced, not executed; it defines one variable, AWK_CODE_FUNCS, to be
# prepended to an awk program.
#
# Shared rather than copied because these functions encode the rule that
# separates code that RUNS a command from prose that only mentions it, and every
# suite is wrong in the same way if that rule drifts in one of them. A guard
# that fires on a comment constrains what may be WRITTEN about a command, which
# is the "the string is present somewhere" mistake the selector these suites
# guard exists to remove, one level up.
#
# Callers must pass -v SQ="'": the single quote cannot be written inside a
# single-quoted awk program, so it reaches awk as a variable.
#
#   code_of(line)             drops a trailing comment. A `#` that starts a word
#                             ends the line in both YAML and shell, and in
#                             neither is the `#` of `a#b` a comment, so the
#                             word-start rule is the one rule both languages
#                             already use.
#   invokes(line, cmdre)      code_of() with quoted string literals emptied,
#                             matched against cmdre. A command named inside
#                             `"..."` or `'...'` is prose, not an invocation.
#   pipes_to_selector(line,   whether the line pipes into
#                     argre)  scripts/select-owned-name.sh with an argument
#                             matching argre. Runs on code_of() alone, because
#                             it asserts the literal text of the selector's
#                             quoted argument and emptying literals would erase
#                             it. Running it on the raw line instead would let a
#                             commented-out selector stage mask an unguarded
#                             destructive call in the same step, which fails
#                             open.
#
#                             The path is matched as "anything with no pipe or
#                             space in it, ending in a slash" so both call forms
#                             are recognised: a workflow's relative
#                             `./scripts/select-owned-name.sh` and a sibling
#                             script's `"${SCRIPT_DIR}/select-owned-name.sh"`,
#                             which resolves from BASH_SOURCE rather than from
#                             the caller's working directory. Requiring the
#                             slash immediately before the file name keeps
#                             `| cat select-owned-name.sh "$X"` from counting.
#
# `[|]` and `[$]` rather than `\|` and `\$` in the regexes here and in the
# callers: escaping those is undefined in POSIX ERE, and CI's awk is mawk rather
# than the awk this was written on. `^#` and ` #` as two subs rather than one
# `(^|[[:space:]])#` alternation, for the same reason: anchors inside a group
# are not portable across awk implementations.

# build_swept_scripts SCRIPTS_DIR
#
# Sets SWEPT_SCRIPTS to every `*.sh` anywhere under SCRIPTS_DIR, at any depth,
# excluding the three guard suites themselves.
#
# Discovered rather than named file by file, in every suite, because naming the
# scripts already known to be guarded is the same defect the suites exist to
# catch, one level up: a NEW script running the dangerous command without the
# selector is invisible to a sweep that only ever opens the files someone
# remembered to list, which is how a guard fails to reach a sibling site.
#
# RECURSIVE, via `find`, and this is the second half of that same defect. The
# original form globbed `$dir/*.sh` and `$dir/lib/*.sh`, which names two
# directories the way the thing above names two files: a script added at
# `scripts/aws/rollback.sh` was never opened by ANY of the three suites, while
# each went on reporting coverage of "every script under scripts/". Raised by
# review on this suite and fixed here in the shared helper rather than locally,
# because the RDS and ECR guards call this same function and carried the
# identical blind spot.
#
# `find -print0` with `read -d ''` rather than a `**` glob: `globstar` is bash 4,
# and this must run on the bash 3.2 that ships with macOS. `sort -z` so the swept
# order is deterministic across platforms, since `find` order is not defined.
# The loop runs in the current shell (process substitution, not a pipe), because
# a pipeline subshell would build the array and then discard it.
#
# The guard suites are excluded by basename because each carries the very
# pattern it looks for as fixture data and inside awk programs, so sweeping them
# reports a suite as a violation of itself. Matching on basename rather than on
# a path fragment keeps the exclusion from exempting a real script that merely
# sits beside them.
#
# A directory with no `*.sh` yields an empty SWEPT_SCRIPTS rather than a
# nonexistent path. Callers must still assert SWEPT_SCRIPTS is non-empty and
# contains the script that actually runs their command: an empty swept set
# satisfies every "no violations" reading.
#
# Returns through a global because bash 3.2, which this must run on, has no
# namerefs.
build_swept_scripts() {
  local dir="$1" candidate
  SWEPT_SCRIPTS=()
  while IFS= read -r -d '' candidate; do
    case "$(basename "$candidate")" in
      test-rds-deletion-protection-scope.sh | test-ecr-delete-selection.sh | \
        test-aws-tfstate-platform-key.sh) continue ;;
    esac
    SWEPT_SCRIPTS+=("$candidate")
  done < <(find "$dir" -type f -name '*.sh' -print0 2>/dev/null | sort -z)
}

# shellcheck disable=SC2034  # read by the suites that source this file
AWK_CODE_FUNCS='
  function code_of(line) {
    sub(/^#.*$/, "", line)
    sub(/[[:space:]]#.*$/, "", line)
    return line
  }
  function invokes(line, cmdre) {
    line = code_of(line)
    gsub(/"[^"]*"/, "", line)
    gsub(SQ "[^" SQ "]*" SQ, "", line)
    return line ~ cmdre
  }
  function pipes_to_selector(line, argre) {
    return code_of(line) ~ ("[|][[:space:]]*\"?[^|[:space:]]*/select-owned-name\\.sh\"?[[:space:]]+" argre)
  }
'
