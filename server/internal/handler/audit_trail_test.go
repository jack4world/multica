package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// The trail's wiring. The vocabulary itself is a table in internal/auditgate
// and is not replayed here; what this file proves is that the entry commits
// with the change it describes, carries what an auditor needs, and does not
// double up on the timeline.

func (f auditFixture) trailFor(t *testing.T, issueID string) []trailEntry {
	t.Helper()
	rows, err := testPool.Query(t.Context(),
		`SELECT action, actor_type, details FROM activity_log WHERE issue_id = $1 ORDER BY created_at ASC, id ASC`,
		issueID)
	if err != nil {
		t.Fatalf("read trail: %v", err)
	}
	defer rows.Close()
	var out []trailEntry
	for rows.Next() {
		var e trailEntry
		var raw []byte
		if err := rows.Scan(&e.Action, &e.ActorType, &raw); err != nil {
			t.Fatalf("scan trail: %v", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Details)
		}
		out = append(out, e)
	}
	return out
}

type trailEntry struct {
	Action    string
	ActorType string
	Details   map[string]any
}

func TestTheTrailRecordsEveryStepOfTheChainInOrder(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL3, f.reviewerUsers[auditgate.LevelL2]).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusFiled, f.reviewerUsers[auditgate.LevelL3]).Want(http.StatusOK)

	want := []string{
		string(auditgate.EventSubmitted),
		string(auditgate.EventReviewPassed),
		string(auditgate.EventReviewPassed),
		string(auditgate.EventFiled),
	}
	got := f.trailFor(t, wp)
	if len(got) != len(want) {
		t.Fatalf("trail has %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Action != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i].Action, want[i])
		}
	}
}

// One entry per thing that happened. The platform's own listener also writes a
// status entry on issue:updated; without the gate telling it to stand down,
// every review step would appear twice and the second copy would be the weaker
// of the two.
func TestAReviewStepAppearsOnceNotTwice(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	for _, e := range f.trailFor(t, wp) {
		if e.Action == "status_changed" {
			t.Error("the timeline shows a second, weaker entry for the same review step")
		}
	}
}

// An auditor checking independence reads one entry, not the whole history.
func TestAnEntryCarriesTheLevelThePreparerAndBothStatuses(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)
	f.setStatus(t, wp, auditmode.StatusReviewL2, f.reviewerUsers[auditgate.LevelL1]).Want(http.StatusOK)

	entries := f.trailFor(t, wp)
	pass := entries[len(entries)-1]
	if pass.Details["level"] != string(auditgate.LevelL1) {
		t.Errorf("level = %v, want %q", pass.Details["level"], auditgate.LevelL1)
	}
	if pass.Details["from"] != auditmode.StatusReviewL1 || pass.Details["to"] != auditmode.StatusReviewL2 {
		t.Errorf("from/to = %v/%v, want %s/%s",
			pass.Details["from"], pass.Details["to"], auditmode.StatusReviewL1, auditmode.StatusReviewL2)
	}
	if pass.Details["preparer_id"] == nil {
		t.Error("no preparer on the entry; an auditor checking independence would have to scan backwards for it")
	}
}

// A rejection SHOULD carry a reason and the trail records one when given. It is
// not yet REQUIRED: no client can send `review_note` today, so enforcing it
// server-side would not improve rejections, it would strand every workpaper
// that fails review with no way back to its preparer. The requirement lands
// with the input that lets a reviewer type one.
func TestReturningAWorkpaperRecordsTheReasonWhenGiven(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	req := auditRequest(http.MethodPatch, "/api/issues/"+wp, f.workspaceID, map[string]any{
		"status":      auditmode.StatusDrafting,
		"review_note": "sampling basis is not documented",
	})
	req.Header.Set("X-User-ID", f.reviewerUsers[auditgate.LevelL1])
	testutil.Call(t, testHandler.UpdateIssue, withURLParam(req, "id", wp)).Want(http.StatusOK)

	entries := f.trailFor(t, wp)
	last := entries[len(entries)-1]
	if last.Action != string(auditgate.EventReviewRejected) {
		t.Fatalf("action = %q, want %q", last.Action, auditgate.EventReviewRejected)
	}
	if last.Details["reason"] != "sampling basis is not documented" {
		t.Errorf("reason = %v, want the note the reviewer wrote", last.Details["reason"])
	}
}

// The rule is enforced through the API, not only in the form. The form is one
// caller; the CLI and every future client are the others.
//
// This test exists because the requirement was once shipped as a disabled
// button alone, while the change that shipped it claimed the rule was back in
// the gate. It was not, and a rejection with no reason went through.
func TestReturningAWorkpaperWithoutAReasonIsRefusedThroughTheAPI(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusDrafting)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)
	f.setStatus(t, wp, auditmode.StatusReviewL1, testUserID).Want(http.StatusOK)

	f.setStatus(t, wp, auditmode.StatusDrafting, f.reviewerUsers[auditgate.LevelL1]).
		Want(http.StatusBadRequest)

	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL1 {
		t.Errorf("status = %q, want the refused write to have changed nothing", got)
	}
	for _, e := range f.trailFor(t, wp) {
		if e.Action == string(auditgate.EventReviewRejected) {
			t.Error("a rejection explaining nothing was written to the trail")
		}
	}
}

// The whole reason the trail moved out of the event listener: the entry and the
// change it describes share a fate. A refused transition writes neither.
func TestARefusedTransitionLeavesNoTrailEntry(t *testing.T) {
	f := newAuditFixture(t)
	wp := f.workpaper(t, auditmode.StatusReviewL1)
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, wp)

	// No rank on this engagement.
	f.setStatus(t, wp, auditmode.StatusReviewL2, testUserID).Want(http.StatusForbidden)

	if entries := f.trailFor(t, wp); len(entries) != 0 {
		t.Errorf("trail has %d entries after a refusal: %+v. The trail says what happened, "+
			"not what was attempted", len(entries), entries)
	}
	if got := f.statusOf(t, wp); got != auditmode.StatusReviewL1 {
		t.Errorf("status = %q, want unchanged", got)
	}
}

func TestOrdinaryIssuesGetNoAuditEntries(t *testing.T) {
	f := newAuditFixture(t)
	remediation := dbfx.Issue(t, "Remediation", testutil.Cols{
		"workspace_id": f.workspaceID,
		"status":       "todo",
	})
	dbfx.Cleanup(t, `DELETE FROM activity_log WHERE issue_id = $1`, remediation)

	f.setStatus(t, remediation, "done", testUserID).Want(http.StatusOK)

	for _, e := range f.trailFor(t, remediation) {
		if e.Action != "status_changed" {
			t.Errorf("an issue outside an engagement produced an audit entry %q", e.Action)
		}
	}
}
