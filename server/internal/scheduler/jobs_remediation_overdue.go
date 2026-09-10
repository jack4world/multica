package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// JobNameRemediationOverdue is persisted in sys_cron_executions and must remain
// stable across releases.
const JobNameRemediationOverdue = "remediation_overdue_reminder"

// remediationOverdueBatch bounds one tick. A deployment with more newly-overdue
// items than this has a problem no notification will solve, and the rest are
// picked up on the next run.
const remediationOverdueBatch = 500

// inboxTypeRemediationOverdue is what clients branch on to render the item.
const inboxTypeRemediationOverdue = "remediation_overdue"

// RemediationOverdueJob tells people once when a 整改事项 passes its deadline.
//
// ONCE, not daily. A deadline that generates a notification every morning
// trains everyone to ignore the notification, and then the one item that
// mattered is ignored with the rest. `overdue_reminded_at` is what makes "once"
// true across restarts and catch-up runs.
//
// Two recipients, deliberately. The 整改责任人 owes the fix; the engagement's
// 主审 raised the item and is the person who has to chase it. Telling only the
// first makes the deadline a private matter between one person and a database.
func RemediationOverdueJob(queries *db.Queries) JobSpec {
	return JobSpec{
		Name: JobNameRemediationOverdue,
		// Once a day, in the morning of the deployment's clock. Catch-up runs
		// the LATEST plan only: the notification says "this is late", and three
		// replays of that sentence for the same item are three copies of one
		// fact — which the once-only stamp would collapse anyway.
		Cadence:           24 * time.Hour,
		ScheduleDelay:     time.Hour,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     3 * 24 * time.Hour,
		MaxPlansPerTick:   1,
		RunTimeout:        10 * time.Minute,
		StaleTimeout:      15 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			5 * time.Minute,
			30 * time.Minute,
			2 * time.Hour,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeRemediationOverdueHandler(queries),
	}
}

func makeRemediationOverdueHandler(queries *db.Queries) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		rows, err := queries.ListRemediationToRemind(ctx, db.ListRemediationToRemindParams{
			ClosedStatus: auditmode.StatusRemediationClosed,
			Lim:          remediationOverdueBatch,
		})
		if err != nil {
			return HandlerResult{}, fmt.Errorf("list overdue remediation items: %w", err)
		}
		if len(rows) == 0 {
			// The overwhelmingly common case on any deployment not running the
			// audit vertical, and the good case on one that is.
			return HandlerResult{Result: map[string]any{"overdue": 0}}, nil
		}

		var notified, undelivered int64
		for _, row := range rows {
			if err := in.Heartbeat(ctx); err != nil {
				return HandlerResult{RowsAffected: notified}, err
			}
			recipients := remediationRecipients(ctx, queries, row)
			for _, recipient := range recipients {
				if err := writeOverdueInboxItem(ctx, queries, row, recipient); err != nil {
					// One undeliverable notification must not stop the rest:
					// the stamp below is what stops a second copy, and it is
					// only written once every recipient has been attempted.
					// Counted into the run's result as well as logged — the
					// item is stamped either way, so a silent failure here is
					// a notification nobody ever gets and nobody can see.
					undelivered++
					slog.Warn("remediation overdue: inbox write failed",
						"issue_id", util.UUIDToString(row.IssueID), "error", err)
				}
			}
			// Stamped even when a recipient failed. The alternative is an item
			// that retries its notification every day forever, which is the
			// daily reminder this job exists not to be.
			if err := queries.MarkRemediationOverdueReminded(ctx, row.IssueID); err != nil {
				return HandlerResult{RowsAffected: notified}, fmt.Errorf("mark reminded: %w", err)
			}
			notified++
		}
		return HandlerResult{
			RowsAffected: notified,
			Result:       map[string]any{"overdue": notified, "undelivered": undelivered},
		}, nil
	}
}

// remediationRecipients is who hears about one late item: the person
// responsible for the fix, and the lead auditor on the engagement that raised
// it. Duplicates are collapsed — one person holding both roles gets one
// notification, not two copies of the same sentence.
func remediationRecipients(ctx context.Context, queries *db.Queries, row db.ListRemediationToRemindRow) []pgtype.UUID {
	seen := map[string]bool{}
	out := make([]pgtype.UUID, 0, 2)
	add := func(id pgtype.UUID) {
		if !id.Valid {
			return
		}
		key := util.UUIDToString(id)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, id)
	}
	if row.AssigneeType.String == "member" {
		add(row.AssigneeID)
	}
	roles, err := queries.ListAuditRolesForProject(ctx, row.SourceProjectID)
	if err != nil {
		slog.Warn("remediation overdue: role read failed",
			"project_id", util.UUIDToString(row.SourceProjectID), "error", err)
		return out
	}
	for _, role := range roles {
		// The first level only. Every reviewer on the engagement hearing about
		// every late item is how an inbox becomes noise.
		if role.Level == "reviewer_l1" {
			add(role.MemberID)
		}
	}
	return out
}

func writeOverdueInboxItem(ctx context.Context, queries *db.Queries, row db.ListRemediationToRemindRow, recipient pgtype.UUID) error {
	daysLate := 0
	if row.DueDate.Valid {
		today := time.Now().UTC().Truncate(24 * time.Hour)
		daysLate = int(today.Sub(row.DueDate.Time.UTC().Truncate(24*time.Hour)).Hours() / 24)
	}
	details, _ := json.Marshal(map[string]any{
		"issue_id":          util.UUIDToString(row.IssueID),
		"department_id":     util.UUIDToString(row.DepartmentID),
		"source_project_id": util.UUIDToString(row.SourceProjectID),
		"days_late":         daysLate,
	})
	_, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   row.WorkspaceID,
		RecipientType: "member",
		RecipientID:   recipient,
		Type:          inboxTypeRemediationOverdue,
		// action_required, not info: a missed remediation deadline is not news
		// to read, it is work someone owes. An inbox that files it beside a
		// mention teaches people it is the same size of event.
		Severity:  "action_required",
		IssueID:   row.IssueID,
		Title:     row.Title,
		Body:      pgtype.Text{},
		ActorType: pgtype.Text{},
		Details:   details,
	})
	return err
}
