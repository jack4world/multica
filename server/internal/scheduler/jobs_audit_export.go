package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditexport"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNameAuditTrailExport is persisted in sys_cron_executions and must remain
// stable across releases.
const JobNameAuditTrailExport = "audit_trail_export"

// AuditTrailExportJob copies each auditee's trail for the previous UTC day out
// of the database.
//
// WHY IT EXISTS. The trail lives in activity_log, which workspace teardown
// deletes. Without a copy outside the database, the record of how a workpaper
// was approved dies with the workspace — including by mistake. The export is
// the durable half of "append-only": tampering has to happen in two places to
// go unnoticed, and one of them can sit under different credentials entirely.
//
// It COPIES. It never prunes, and it never writes to activity_log. A job that
// could lose a record while making it durable would be self-defeating.
//
// The destination is whatever storage the deployment configures. Pointing it at
// a separate bucket under a separate account is an operations decision, so it
// is not expressed in code.
func AuditTrailExportJob(queries *db.Queries, store storage.Storage) JobSpec {
	return JobSpec{
		Name: JobNameAuditTrailExport,
		// A whole day at a time, run well after the day closes so late-arriving
		// rows are already in. Catch-up replays every missed plan rather than
		// only the latest: a skipped day is a hole in the record, and "we were
		// down for two days" must not silently become one exported day.
		Cadence:           24 * time.Hour,
		ScheduleDelay:     time.Hour,
		CatchUpMode:       CatchUpEveryPlan,
		CatchUpWindow:     14 * 24 * time.Hour,
		MaxPlansPerTick:   14,
		RunTimeout:        20 * time.Minute,
		StaleTimeout:      30 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		// Safe to steal: a re-run of the same day overwrites the same keys with
		// byte-identical content.
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff: []time.Duration{
			5 * time.Minute,
			30 * time.Minute,
			2 * time.Hour,
		},
		Scopes:  StaticScopes(ScopeGlobal),
		Handler: makeAuditTrailExportHandler(queries, store),
	}
}

func makeAuditTrailExportHandler(queries *db.Queries, store storage.Storage) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if store == nil {
			// No storage configured is a deployment state, not a failure: the
			// vertical is off and there is nothing to copy anywhere.
			return HandlerResult{Result: map[string]any{"skipped": "no storage configured"}}, nil
		}

		// The plan time names the day being exported: the day that has just
		// closed, not the one in progress.
		dayEnd := in.PlanTime.UTC().Truncate(24 * time.Hour)
		dayStart := dayEnd.Add(-24 * time.Hour)

		workspaceIDs, err := queries.ListAuditeeWorkspaceIDs(ctx)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("list auditee workspaces: %w", err)
		}
		if len(workspaceIDs) == 0 {
			// The overwhelmingly common case on any deployment that is not
			// running the audit vertical.
			return HandlerResult{Result: map[string]any{"auditees": 0}}, nil
		}

		var exported int64
		for _, workspaceID := range workspaceIDs {
			if err := in.Heartbeat(ctx); err != nil {
				return HandlerResult{RowsAffected: exported}, err
			}
			rows, err := queries.ListWorkspaceActivityForDay(ctx, db.ListWorkspaceActivityForDayParams{
				WorkspaceID: workspaceID,
				DayStart:    pgtype.Timestamptz{Time: dayStart, Valid: true},
				DayEnd:      pgtype.Timestamptz{Time: dayEnd, Valid: true},
			})
			if err != nil {
				return HandlerResult{RowsAffected: exported}, fmt.Errorf("read trail for %s: %w", util.UUIDToString(workspaceID), err)
			}

			records := make([]auditexport.Record, 0, len(rows))
			for _, row := range rows {
				records = append(records, auditexport.Record{
					ID:        util.UUIDToString(row.ID),
					IssueID:   util.UUIDToString(row.IssueID),
					ActorType: row.ActorType.String,
					ActorID:   util.UUIDToString(row.ActorID),
					Action:    row.Action,
					Details:   row.Details,
					At:        row.CreatedAt.Time.UTC(),
				})
			}

			bundle := auditexport.Build(util.UUIDToString(workspaceID), dayStart, records)
			// The manifest goes LAST. A reader treats its absence as "this day
			// did not finish", so writing it first would let a crash between
			// the two uploads present a truncated day as a complete one.
			if _, err := store.Upload(ctx, bundle.DataKey, bundle.Data, "application/x-ndjson", "records.jsonl"); err != nil {
				return HandlerResult{RowsAffected: exported}, fmt.Errorf("upload trail for %s: %w", util.UUIDToString(workspaceID), err)
			}
			if _, err := store.Upload(ctx, bundle.ManifestKey, bundle.Manifest, "application/json", "manifest.json"); err != nil {
				return HandlerResult{RowsAffected: exported}, fmt.Errorf("upload manifest for %s: %w", util.UUIDToString(workspaceID), err)
			}
			exported += int64(len(records))
		}

		slog.Info("audit trail exported",
			"day", dayStart.Format("2006-01-02"), "auditees", len(workspaceIDs), "records", exported)
		return HandlerResult{
			RowsAffected: exported,
			Result: map[string]any{
				"day":      dayStart.Format("2006-01-02"),
				"auditees": len(workspaceIDs),
				"records":  exported,
			},
		}, nil
	}
}
