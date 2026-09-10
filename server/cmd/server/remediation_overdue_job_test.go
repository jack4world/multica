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

// The overdue reminder against a real database. What needs one is the job's own
// behaviour: which items it picks up, who hears about it, and that it says so
// once.

type overdueFixture struct {
	workspaceID string
	projectID   string
	issueID     string
	assignee    string
	leadAuditor string
}

// newOverdueItem builds an auditee with one remediation item, due `daysAgo`
// days ago, owned by a member and raised by an engagement with a seated 主审.
func newOverdueItem(t *testing.T, daysAgo int, status string) overdueFixture {
	t.Helper()
	ctx := context.Background()
	slug := "overdue-" + uuid.NewString()[:8]
	var f overdueFixture
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix, audit_mode_enabled_at)
		 VALUES ($1, $1, '', '', now()) RETURNING id::text`, slug).Scan(&f.workspaceID); err != nil {
		t.Fatalf("create auditee: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM audit_remediation WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM audit_department WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM audit_role WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM project WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1`, f.workspaceID)
		_, _ = testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, f.workspaceID)
	})

	mustExec := func(query string, args ...any) {
		if _, err := testPool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	newMember := func() string {
		var userID, memberID string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO "user" (email, name) VALUES ($1, 'Overdue Tester') RETURNING id::text`,
			"overdue-"+uuid.NewString()[:8]+"@multica.ai").Scan(&userID); err != nil {
			t.Fatalf("create user: %v", err)
		}
		if err := testPool.QueryRow(ctx,
			`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member') RETURNING id::text`,
			f.workspaceID, userID).Scan(&memberID); err != nil {
			t.Fatalf("create member: %v", err)
		}
		return memberID
	}
	f.assignee = newMember()
	f.leadAuditor = newMember()

	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1, 'Engagement') RETURNING id::text`,
		f.workspaceID).Scan(&f.projectID); err != nil {
		t.Fatalf("create engagement: %v", err)
	}
	mustExec(`INSERT INTO audit_role (workspace_id, project_id, member_id, level)
	          VALUES ($1, $2, $3, 'reviewer_l1')`, f.workspaceID, f.projectID, f.leadAuditor)

	var departmentID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO audit_department (workspace_id, name) VALUES ($1, $2) RETURNING id::text`,
		f.workspaceID, "财务部-"+uuid.NewString()[:6]).Scan(&departmentID); err != nil {
		t.Fatalf("create department: %v", err)
	}

	issueID := dbid.NewV7()
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (id, workspace_id, number, title, status, priority,
		                    assignee_type, assignee_id, creator_type, creator_id, position, due_date)
		 VALUES ($1, $2, 1, '整改事项', $3, 'medium',
		         'member', $4, 'member', $4, 0, CURRENT_DATE - $5::int)
		 RETURNING id::text`,
		issueID, f.workspaceID, status, f.assignee, daysAgo).Scan(&f.issueID); err != nil {
		t.Fatalf("create item: %v", err)
	}
	mustExec(`INSERT INTO audit_remediation (issue_id, workspace_id, source_project_id, department_id)
	          VALUES ($1, $2, $3, $4)`, f.issueID, f.workspaceID, f.projectID, departmentID)
	return f
}

func runOverdueSweep(t *testing.T) {
	t.Helper()
	spec := scheduler.RemediationOverdueJob(db.New(testPool))
	if _, err := spec.Handler(context.Background(), scheduler.HandlerInput{
		Job:       &spec,
		PlanTime:  time.Now().UTC(),
		Heartbeat: func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("overdue sweep: %v", err)
	}
}

func overdueNotices(t *testing.T, issueID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM inbox_item WHERE issue_id = $1 AND type = 'remediation_overdue'`,
		issueID).Scan(&n); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	return n
}

// Both the person who owes the fix and the auditor who has to chase it. Telling
// only the first makes the deadline a private matter between one person and a
// database.
func TestALateItemTellsTheResponsiblePersonAndTheLeadAuditor(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	f := newOverdueItem(t, 3, auditmode.StatusRemediating)

	runOverdueSweep(t)

	if n := overdueNotices(t, f.issueID); n != 2 {
		t.Fatalf("notices = %d, want 2 (the responsible person and the lead auditor)", n)
	}
	for _, recipient := range []string{f.assignee, f.leadAuditor} {
		var n int
		if err := testPool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM inbox_item WHERE issue_id = $1 AND recipient_id = $2`,
			f.issueID, recipient).Scan(&n); err != nil {
			t.Fatalf("count for recipient: %v", err)
		}
		if n != 1 {
			t.Errorf("recipient %s got %d notices, want 1", recipient, n)
		}
	}
}

// ONCE. A deadline that generates a notification every morning trains everyone
// to ignore the notification, and then the one item that mattered is ignored
// with the rest.
func TestALateItemIsNotRemindedTwice(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	f := newOverdueItem(t, 3, auditmode.StatusRemediating)

	runOverdueSweep(t)
	runOverdueSweep(t)

	if n := overdueNotices(t, f.issueID); n != 2 {
		t.Errorf("notices = %d after two sweeps, want the same 2 the first sweep wrote", n)
	}
}

func TestAnItemDueTodayIsNotLate(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	f := newOverdueItem(t, 0, auditmode.StatusRemediating)

	runOverdueSweep(t)

	if n := overdueNotices(t, f.issueID); n != 0 {
		t.Errorf("notices = %d for an item due today, want 0: it is not late until the day has passed", n)
	}
}

func TestAClosedItemIsNeverReportedLate(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	f := newOverdueItem(t, 30, auditmode.StatusRemediationClosed)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE audit_remediation SET verified_at = now() WHERE issue_id = $1`, f.issueID); err != nil {
		t.Fatalf("close item: %v", err)
	}

	runOverdueSweep(t)

	if n := overdueNotices(t, f.issueID); n != 0 {
		t.Errorf("notices = %d for a closed item, want 0: it is finished, whenever it finished", n)
	}
}
