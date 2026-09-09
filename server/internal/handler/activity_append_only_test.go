package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The audit trail's integrity rests on one property: nothing in this codebase
// modifies or removes a row of activity_log. That is TRUE today by accident —
// there is a single insert, no update, and one delete in workspace teardown —
// and accidents do not survive a year of feature work. This test is what turns
// it into a guarantee, in the style the repo already uses for the workspace
// deletion manifest and the concurrent index registry: the hazard is a query
// somebody adds months from now, so the check belongs where it fails at the
// moment that query is written rather than in review.
//
// If you are here because this test failed, the question is not how to make it
// pass. It is whether the trail should still be called a trail.

const activityTable = "activity_log"

// allowedActivityDeletions names the queries permitted to remove trail rows,
// each with the reason it is allowed. Workspace teardown is the only one: a
// deleted workspace takes its history with it, which is why the daily export
// exists as the durable copy.
var allowedActivityDeletions = map[string]string{
	"DeleteWorkspaceLeafData": "workspace teardown; the daily export is the durable copy",
}

var (
	activityUpdatePattern = regexp.MustCompile(`(?is)\bUPDATE\s+` + activityTable + `\b`)
	activityDeletePattern = regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+` + activityTable + `\b`)
	sqlcNamePattern       = regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)
)

func TestNothingUpdatesTheAuditTrail(t *testing.T) {
	for name, body := range sqlcQueriesForTest(t) {
		if activityUpdatePattern.MatchString(stripSQLComments(body)) {
			t.Errorf("query %q updates %s. The audit trail is append-only: a review decision that can be "+
				"rewritten is not evidence of anything. Record a correcting entry instead.", name, activityTable)
		}
	}
}

func TestOnlyWorkspaceTeardownDeletesFromTheAuditTrail(t *testing.T) {
	for name, body := range sqlcQueriesForTest(t) {
		if !activityDeletePattern.MatchString(stripSQLComments(body)) {
			continue
		}
		if _, ok := allowedActivityDeletions[name]; !ok {
			t.Errorf("query %q deletes from %s and is not on the allowed list. Pruning the trail loses the "+
				"record of what happened; if this really is teardown, add it to allowedActivityDeletions with "+
				"its reason.", name, activityTable)
		}
	}
}

// The allowlist has to name queries that exist, or a rename would silently
// widen it: the named query disappears, its replacement is unlisted, and the
// test above starts failing for the wrong reason — or worse, the old name keeps
// blessing nothing while a new deletion slips in under it.
func TestTheDeletionAllowlistNamesRealQueries(t *testing.T) {
	queries := sqlcQueriesForTest(t)
	for name, reason := range allowedActivityDeletions {
		body, ok := queries[name]
		if !ok {
			t.Errorf("allowlist names %q (%s) but no such query exists", name, reason)
			continue
		}
		if !activityDeletePattern.MatchString(stripSQLComments(body)) {
			t.Errorf("allowlist names %q but it no longer deletes from %s; drop the entry", name, activityTable)
		}
	}
}

// A guard nobody has watched fail is a guard nobody knows works.
func TestTheGuardCatchesAPlantedViolation(t *testing.T) {
	planted := "UPDATE " + activityTable + " SET action = 'tidied' WHERE id = $1;"
	if !activityUpdatePattern.MatchString(planted) {
		t.Error("the update guard does not match a plain UPDATE against the trail")
	}
	plantedDelete := "DELETE FROM " + activityTable + " WHERE created_at < now() - interval '1 year';"
	if !activityDeletePattern.MatchString(plantedDelete) {
		t.Error("the delete guard does not match a plain DELETE against the trail")
	}
	// And it must not fire on a read.
	if activityUpdatePattern.MatchString("SELECT * FROM "+activityTable+" WHERE id = $1;") ||
		activityDeletePattern.MatchString("SELECT * FROM "+activityTable+" WHERE id = $1;") {
		t.Error("the guards fire on a plain read")
	}
}

// sqlcQueriesForTest returns every named query in the sqlc query files, keyed
// by name. Splitting on the `-- name:` markers keeps a violation attributable to
// one query rather than to a whole file.
func sqlcQueriesForTest(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "pkg", "db", "queries", "*.sql"))
	if err != nil {
		t.Fatalf("glob query files: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no sqlc query files found; this guard would pass vacuously")
	}
	out := map[string]string{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		markers := sqlcNamePattern.FindAllStringSubmatchIndex(string(body), -1)
		for i, m := range markers {
			end := len(body)
			if i+1 < len(markers) {
				end = markers[i+1][0]
			}
			name := string(body)[m[2]:m[3]]
			out[name] = string(body)[m[0]:end]
		}
	}
	return out
}

// stripSQLComments removes `--` line comments so a violation cannot hide in
// prose, and so an explanatory comment mentioning UPDATE does not trip the
// guard.
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
