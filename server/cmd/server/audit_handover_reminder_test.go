package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/scheduler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// The reminder job. A delivered draft is visible nowhere — not in a review
// queue, not in anyone's assigned work — so this is the one stall in the chain
// that needs a job to notice it.

func runHandoverReminder(t *testing.T) int64 {
	t.Helper()
	spec := scheduler.AuditHandoverReminderJob(db.New(testPool))
	res, err := spec.Handler(context.Background(), scheduler.HandlerInput{
		Job:       &spec,
		PlanTime:  time.Now().UTC(),
		Heartbeat: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("reminder job: %v", err)
	}
	return res.RowsAffected
}

// deliveredDraft creates an auditee holding one workpaper in 待采纳, last
// touched `age` ago, created by a person who could adopt it.
func deliveredDraft(t *testing.T, age time.Duration) (workspaceID, issueID, userID string) {
	t.Helper()
	ctx := context.Background()
	slug := "handover-" + uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix, audit_mode_enabled_at)
		 VALUES ($1, $1, '', 'HO', now()) RETURNING id::text`, slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create auditee: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ('Preparer', $1) RETURNING id::text`,
		"ho-"+uuid.NewString()[:8]+"@multica.ai").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1, 'Engagement') RETURNING id::text`,
		workspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create engagement: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, updated_at)
		 VALUES ($1, $2, 'Drafted workpaper', $3, 'member', $4, now() - $5::interval)
		 RETURNING id::text`,
		workspaceID, projectID, auditmode.StatusAgentDelivered, userID, age.String()).Scan(&issueID); err != nil {
		t.Fatalf("create workpaper: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM inbox_item WHERE workspace_id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM audit_workpaper WHERE workspace_id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM issue WHERE workspace_id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM project WHERE workspace_id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return workspaceID, issueID, userID
}

func inboxCountFor(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_item WHERE issue_id = $1 AND type = 'audit_handover_waiting'`,
		issueID).Scan(&n); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	return n
}

func TestAForgottenDraftPutsAnItemInFrontOfSomeone(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	_, issueID, userID := deliveredDraft(t, 48*time.Hour)

	runHandoverReminder(t)

	if got := inboxCountFor(t, issueID); got != 1 {
		t.Fatalf("inbox items = %d, want 1", got)
	}
	var recipient string
	if err := testPool.QueryRow(context.Background(),
		`SELECT recipient_id::text FROM inbox_item WHERE issue_id = $1`, issueID).Scan(&recipient); err != nil {
		t.Fatalf("read recipient: %v", err)
	}
	if recipient != userID {
		t.Errorf("recipient = %s, want the person who could adopt the draft (%s)", recipient, userID)
	}
}

// THE test about something not happening. The handover exists so a human takes
// authorship; adopting on their behalf after a timeout would forge exactly the
// signature the chain secures. Nothing else in the system would notice if a
// later change quietly turned this into an auto-adopt.
func TestTheReminderNeverAdoptsTheDraft(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	_, issueID, _ := deliveredDraft(t, 48*time.Hour)

	runHandoverReminder(t)

	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != auditmode.StatusAgentDelivered {
		t.Errorf("status = %q after the reminder, want %q — a reminder escalates attention, never state",
			status, auditmode.StatusAgentDelivered)
	}
}

// A forgotten draft that generates an item every hour trains people to ignore
// the inbox, which costs more than the forgotten draft did.
func TestTheReminderIsSentOnce(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	_, issueID, _ := deliveredDraft(t, 48*time.Hour)

	runHandoverReminder(t)
	runHandoverReminder(t)
	runHandoverReminder(t)

	if got := inboxCountFor(t, issueID); got != 1 {
		t.Errorf("inbox items = %d after three runs, want 1", got)
	}
}

func TestADraftInsideTheWindowIsLeftAlone(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	_, issueID, _ := deliveredDraft(t, time.Hour)

	runHandoverReminder(t)

	if got := inboxCountFor(t, issueID); got != 0 {
		t.Errorf("inbox items = %d for a draft an hour old, want 0", got)
	}
}

func TestAWorkspaceThatIsNotAnAuditeeIsIgnored(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	slug := "plain-ho-" + uuid.NewString()[:8]
	var wsID, userID, projectID, issueID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $1, '', 'PL') RETURNING id::text`,
		slug).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_ = testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('P', $1) RETURNING id::text`,
		"pl-"+uuid.NewString()[:8]+"@multica.ai").Scan(&userID)
	_ = testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'P') RETURNING id::text`,
		wsID).Scan(&projectID)
	_ = testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, updated_at)
		 VALUES ($1, $2, 'Not a workpaper', $3, 'member', $4, now() - interval '48 hours') RETURNING id::text`,
		wsID, projectID, auditmode.StatusAgentDelivered, userID).Scan(&issueID)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM inbox_item WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM issue WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM project WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM workspace WHERE id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, userID)
	})

	runHandoverReminder(t)

	if got := inboxCountFor(t, issueID); got != 0 {
		t.Errorf("inbox items = %d in a workspace that is not an auditee, want 0", got)
	}
}

// A reminder with no recipient is not a reason to invent one — but it must not
// make the job re-examine the same draft every hour either.
func TestADraftWithNoPersonToAdoptItIsMarkedNotSkippedForever(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	slug := "orphan-" + uuid.NewString()[:8]
	var wsID, projectID, issueID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix, audit_mode_enabled_at)
		 VALUES ($1, $1, '', 'OR', now()) RETURNING id::text`, slug).Scan(&wsID); err != nil {
		t.Fatalf("create auditee: %v", err)
	}
	_ = testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, 'E') RETURNING id::text`,
		wsID).Scan(&projectID)
	// Created by an agent, assigned to nobody: no person to tell.
	agentIDPlaceholder := dbid.NewV7()
	_ = testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, updated_at)
		 VALUES ($1, $2, 'Orphan draft', $3, 'agent', $4, now() - interval '48 hours') RETURNING id::text`,
		wsID, projectID, auditmode.StatusAgentDelivered, agentIDPlaceholder).Scan(&issueID)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM audit_workpaper WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM issue WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM project WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(bg, `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	runHandoverReminder(t)

	if got := inboxCountFor(t, issueID); got != 0 {
		t.Errorf("inbox items = %d with nobody to tell, want 0", got)
	}
	var marked int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_workpaper WHERE issue_id = $1 AND handover_reminded_at IS NOT NULL`,
		issueID).Scan(&marked); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if marked != 1 {
		t.Error("an unreachable draft was not marked, so the job will re-examine it every hour forever")
	}
}
