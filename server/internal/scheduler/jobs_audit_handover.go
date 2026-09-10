package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditmode"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// JobNameAuditHandoverReminder is persisted in sys_cron_executions and must
// remain stable across releases.
const JobNameAuditHandoverReminder = "audit_handover_reminder"

const (
	// handoverReminderAfter is how long a delivered draft may wait before
	// somebody is told about it. One sensible default beats a setting nobody
	// tunes; it can become one when a customer asks.
	handoverReminderAfter = 24 * time.Hour
	handoverReminderBatch = 200
)

// AuditHandoverReminderJob nudges a person when an agent's draft has been
// waiting too long to be adopted.
//
// WHY IT EXISTS. A delivered draft is visible NOWHERE. It is not in a review
// queue, because it is not in review. It is not in anyone's assigned work,
// because the agent still owns it. Every other stall in the chain shows up
// somewhere; this one does not, which is why it is the one that gets a job.
//
// IT NEVER ADOPTS. The handover exists so that a human takes authorship of an
// agent's draft; adopting on their behalf after a timeout would forge exactly
// the signature the chain exists to secure. This escalates attention, never
// state — the workpaper's status is the same after this job as before it.
//
// IT REMINDS ONCE. A forgotten draft that generates an inbox item every morning
// trains people to ignore the inbox, which costs more than the forgotten draft.
func AuditHandoverReminderJob(queries *db.Queries) JobSpec {
	return JobSpec{
		Name:    JobNameAuditHandoverReminder,
		Cadence: time.Hour,
		// Latest-only: this is a sweep over current state, not a per-period
		// computation. Replaying twelve missed hours would find the same drafts
		// twelve times, and the reminded flag would make eleven of them no-ops.
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        5 * time.Minute,
		StaleTimeout:      10 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{time.Minute, 5 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeAuditHandoverReminderHandler(queries),
	}
}

func makeAuditHandoverReminderHandler(queries *db.Queries) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		// One read decides the whole run. An ordinary deployment has no
		// auditees and stops here.
		workspaceIDs, err := queries.ListAuditeeWorkspaceIDs(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("list auditee workspaces: %w", err)
		}
		if len(workspaceIDs) == 0 {
			return HandlerResult{Result: map[string]any{"auditees": 0}}, nil
		}

		stale, err := queries.ListStaleHandovers(ctx, db.ListStaleHandoversParams{
			WorkspaceIds: workspaceIDs,
			Status:       auditmode.StatusAgentDelivered,
			OlderThan:    pgtype.Timestamptz{Time: in.PlanTime.UTC().Add(-handoverReminderAfter), Valid: true},
			Lim:          handoverReminderBatch,
		})
		if err != nil {
			return HandlerResult{}, fmt.Errorf("list stale handovers: %w", err)
		}

		var reminded int64
		for _, row := range stale {
			if err := in.Heartbeat(ctx); err != nil {
				return HandlerResult{RowsAffected: reminded}, err
			}
			recipient, ok := adoptionRecipient(row)
			if !ok {
				// A reminder with no recipient is not a reason to invent one.
				// Marked anyway, so the job does not re-examine it every hour.
				if err := markReminded(ctx, queries, row); err != nil {
					return HandlerResult{RowsAffected: reminded}, err
				}
				continue
			}

			details, _ := json.Marshal(map[string]any{
				"waiting_since": row.UpdatedAt.Time.UTC().Format(time.RFC3339),
			})
			if _, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
				ID:            dbid.NewV7(),
				WorkspaceID:   row.WorkspaceID,
				RecipientType: "member",
				RecipientID:   recipient,
				Type:          "audit_handover_waiting",
				// attention, not action_required: nothing is broken and no
				// deadline has passed. Reserving the loudest severity for
				// things that are actually urgent is what keeps it meaning
				// something.
				Severity: "attention",
				IssueID:  row.ID,
				Title:    "A drafted workpaper is waiting to be adopted",
				Body:     pgtype.Text{String: row.Title, Valid: row.Title != ""},
				Details:  details,
			}); err != nil {
				return HandlerResult{RowsAffected: reminded}, fmt.Errorf("create inbox item: %w", err)
			}
			if err := markReminded(ctx, queries, row); err != nil {
				return HandlerResult{RowsAffected: reminded}, err
			}
			reminded++
		}

		if reminded > 0 {
			slog.Info("audit handover reminders sent", "count", reminded, "auditees", len(workspaceIDs))
		}
		return HandlerResult{
			RowsAffected: reminded,
			Result:       map[string]any{"auditees": len(workspaceIDs), "reminded": reminded},
		}, nil
	}
}

// adoptionRecipient picks the person who should adopt the draft.
//
// The assignee where that is a member, otherwise the creator where that is one.
// After an agent run the assignee is usually the agent itself, so the creator is
// the more common answer — but an explicitly assigned human outranks it, since
// somebody put them there on purpose.
//
// Deliberately NOT addressed by the agent: letting an agent choose a recipient
// would make the handover a delegation, which is a different thing with
// different rules.
func adoptionRecipient(row db.ListStaleHandoversRow) (pgtype.UUID, bool) {
	if row.AssigneeType.String == "member" && row.AssigneeID.Valid {
		return row.AssigneeID, true
	}
	if row.CreatorType == "member" && row.CreatorID.Valid {
		return row.CreatorID, true
	}
	return pgtype.UUID{}, false
}

func markReminded(ctx context.Context, queries *db.Queries, row db.ListStaleHandoversRow) error {
	return queries.MarkHandoverReminded(ctx, db.MarkHandoverRemindedParams{
		IssueID:     row.ID,
		WorkspaceID: row.WorkspaceID,
	})
}
