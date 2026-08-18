#!/usr/bin/env bash
# code-scan-awk.sh
#
# Shared awk helper functions for the guard suites that scan workflow and shell
# sources for destructive commands: test-ecr-delete-selection.sh and
# test-rds-deletion-protection-scope.sh. Sourced, not executed; it defines one
# variable, AWK_CODE_FUNCS, to be prepended to an awk program.
#
# Shared rather than copied because these functions encode the rule that
# separates code that RUNS a command from prose that only mentions it, and both
# suites are wrong in the same way if that rule drifts in one of them. A guard
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
