// Package migrations: transactional-safety invariant test.
//
// golang-migrate runs each migration file inside one implicit transaction
// (the postgres driver wraps every *.up.sql / *.down.sql in BEGIN ... COMMIT).
// That property is what makes a failed migration roll back atomically: either
// the whole file applies or none of it does, so we never end up with a
// half-applied schema. This test enforces that invariant statically by
// failing if any migration contains a statement that PostgreSQL refuses to
// run inside a transaction block (e.g. CREATE INDEX CONCURRENTLY, VACUUM,
// ALTER SYSTEM, CREATE/DROP DATABASE, CREATE TABLESPACE, REINDEX).
//
// Why this matters: this guard is orthogonal to the dirty-flag auto-heal
// hardening in this PR. Auto-heal recovers a migration whose transaction was
// interrupted; this test closes the *partial-apply* hole, where a
// non-transactional statement could leave the schema half-changed even though
// the migration "failed", because that statement committed outside the
// transaction the rest of the file ran in. Keeping every migration
// transactional means the dirty flag + auto-heal always describe an
// all-or-nothing state.
//
// Opt-out: a migration that genuinely must run non-transactionally may declare
// itself by putting the exact marker comment
//
//	-- migrate:no-transaction
//
// on (one of) the first lines of the file. Such files are SKIPPED by the
// forbidden-statement scan, but the test logs a loud warning naming them.
// These files MUST be applied via the one-shot, deploy-time migration runner
// (follow-up issue #1125), NEVER via the in-request auto-migrate path on the
// hot request handler, because outside a transaction a failure there leaves a
// partially-applied schema with no rollback.
//
// The scanner strips SQL comments and dollar-quoted bodies ($$...$$, $tag$...$tag$)
// before matching, so CONCURRENTLY mentioned in a comment or stored inside a
// CREATE FUNCTION / DO body (which is only executed later, outside the
// migration transaction) does not trip the check. Only top-level statements
// that the migration itself executes are scanned.
package migrations

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// noTransactionMarker, when present in a migration file, declares it as an
// intentional, deploy-time-only non-transactional migration (see issue #1125).
const noTransactionMarker = "-- migrate:no-transaction"

// forbiddenPatterns are statements PostgreSQL cannot run inside a transaction
// block. Each is matched case-insensitively against the comment-and-body-stripped
// SQL with whitespace tolerance and word boundaries.
var forbiddenPatterns = []struct { //nolint:govet // fieldalignment: reorder would break API/readability
	name string
	re   *regexp.Regexp
}{
	{"CREATE INDEX CONCURRENTLY", regexp.MustCompile(`(?is)\bcreate\s+(?:unique\s+)?index\s+concurrently\b`)},
	{"DROP INDEX CONCURRENTLY", regexp.MustCompile(`(?is)\bdrop\s+index\s+concurrently\b`)},
	{"REINDEX", regexp.MustCompile(`(?is)\breindex\b`)},
	{"VACUUM", regexp.MustCompile(`(?is)\bvacuum\b`)},
	{"ALTER SYSTEM", regexp.MustCompile(`(?is)\balter\s+system\b`)},
	{"CREATE DATABASE", regexp.MustCompile(`(?is)\bcreate\s+database\b`)},
	{"DROP DATABASE", regexp.MustCompile(`(?is)\bdrop\s+database\b`)},
	{"CREATE TABLESPACE", regexp.MustCompile(`(?is)\bcreate\s+tablespace\b`)},
	// REFRESH MATERIALIZED VIEW CONCURRENTLY also cannot run in a txn. A bare,
	// top-level one is a hazard; one inside a function/DO body is fine and is
	// already stripped before matching, so this only fires at top level.
	{"REFRESH MATERIALIZED VIEW CONCURRENTLY", regexp.MustCompile(`(?is)\brefresh\s+materialized\s+view\s+concurrently\b`)},
}

// lineCommentRe matches a -- comment to end of line.
var lineCommentRe = regexp.MustCompile(`--[^\n]*`)

// blockCommentRe matches /* ... */ comments (non-greedy, across newlines).
var blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// dollarTagRe matches a dollar-quote opening tag, e.g. $$ or $func$.
var dollarTagRe = regexp.MustCompile(`\$[A-Za-z0-9_]*\$`)

// stripDollarBodies removes the contents of dollar-quoted strings (function
// and DO bodies). These are stored/parsed-later text, not statements the
// migration transaction executes, so CONCURRENTLY etc. inside them is benign.
// Matching is tag-aware: a body opened with $tag$ closes only on the same
// $tag$, per PostgreSQL dollar-quoting rules.
func stripDollarBodies(sql string) string {
	var b strings.Builder
	for {
		loc := dollarTagRe.FindStringIndex(sql)
		if loc == nil {
			b.WriteString(sql)
			break
		}
		b.WriteString(sql[:loc[0]])
		tag := sql[loc[0]:loc[1]]
		rest := sql[loc[1]:]
		end := strings.Index(rest, tag)
		if end == -1 {
			// Unterminated dollar quote: keep the tag and the remainder as-is
			// rather than silently dropping it, so a malformed file is still
			// scanned conservatively.
			b.WriteString(tag)
			b.WriteString(rest)
			break
		}
		// Drop the body (rest[:end]) and the closing tag; continue after it.
		sql = rest[end+len(tag):]
	}
	return b.String()
}

// sanitizeSQL removes comments and dollar-quoted bodies so the forbidden-pattern
// scan only sees top-level statements the migration actually executes.
func sanitizeSQL(sql string) string {
	sql = blockCommentRe.ReplaceAllString(sql, " ")
	sql = lineCommentRe.ReplaceAllString(sql, " ")
	sql = stripDollarBodies(sql)
	return sql
}

func TestMigrationsAreTransactional(t *testing.T) {
	files, err := filepath.Glob("*.sql")
	if err != nil {
		t.Fatalf("globbing migration files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no *.sql migration files found; test is in the wrong directory or migrations vanished")
	}

	var scanned, skipped int
	for _, f := range files {
		name := filepath.Base(f)
		if !strings.HasSuffix(name, ".up.sql") && !strings.HasSuffix(name, ".down.sql") {
			continue
		}

		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		content := string(raw)

		if strings.Contains(content, noTransactionMarker) {
			skipped++
			t.Logf("WARNING: %s is declared non-transactional via %q and is SKIPPED by the "+
				"transactional-safety scan. It MUST be applied via the one-shot deploy-time "+
				"migration runner (issue #1125), never the in-request auto-migrate path.",
				name, noTransactionMarker)
			continue
		}

		scanned++
		sanitized := sanitizeSQL(content)
		for _, p := range forbiddenPatterns {
			if p.re.MatchString(sanitized) {
				t.Errorf("%s contains a non-transactional statement (%s). golang-migrate runs "+
					"each migration in one implicit transaction, so this statement would either "+
					"fail outright or commit outside that transaction and leave a partially-applied "+
					"schema. Move it to a deploy-time runner and mark the file with %q (see #1125), "+
					"or rewrite it transactionally.",
					name, p.name, noTransactionMarker)
			}
		}
	}

	t.Logf("transactional-safety scan: %d migration files scanned, %d skipped (opt-out)", scanned, skipped)
}
