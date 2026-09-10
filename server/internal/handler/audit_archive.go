package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditarchive"
	"github.com/multica-ai/multica/server/internal/auditgate"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/auditreport"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// 归档: closing an engagement's file.
//
// An audit ends with one complete, indexed package — the report, the workpapers
// behind it, the evidence index, the trail — kept for as long as the records
// policy says. The daily trail export is NOT replaced by this and must not be:
// an auditee's trail includes work belonging to no engagement (every
// remediation item, by definition), and the export is what survives the
// workspace being deleted. The two answer different questions.

// ArchiveResponse is what an archival returns: where the file is, and what it
// holds.
type ArchiveResponse struct {
	ProjectID   string `json:"project_id"`
	ArchiveKey  string `json:"archive_key"`
	ArchivedAt  string `json:"archived_at"`
	Workpapers  int    `json:"workpaper_count"`
	TrailCount  int    `json:"trail_count"`
	Remediation int    `json:"remediation_count"`
	Attachments int    `json:"attachment_count"`
}

// ArchiveEngagement writes the engagement's file and closes it.
func (h *Handler) ArchiveEngagement(w http.ResponseWriter, r *http.Request) {
	// The one write that names an engagement in order to close it.
	project, member, ok := h.requireEngagementRank(w, r, true)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, _ := h.resolveActor(r, userID, uuidToString(project.WorkspaceID))
	if actorType == "agent" {
		writeErrorCode(w, http.StatusForbidden, "agent_not_permitted",
			"an agent cannot archive an engagement")
		return
	}

	// Refused rather than warned about, every one of them: an archive is a
	// statement that this file is complete.
	if h.AuditArchiveStorage == nil {
		// The worst possible failure for this feature is reporting success
		// while writing nowhere, so no destination is a 503, not a no-op.
		writeErrorCode(w, http.StatusServiceUnavailable, "archive_not_configured",
			"no archive destination is configured; set AUDIT_EXPORT_DIR before archiving an engagement")
		return
	}
	if project.AuditArchivedAt.Valid {
		writeErrorCode(w, http.StatusConflict, "engagement_archived",
			"this engagement is already archived")
		return
	}
	level, err := h.Queries.GetAuditRoleLevel(r.Context(), db.GetAuditRoleLevelParams{
		ProjectID: project.ID, MemberID: member.ID,
	})
	// Closing the file is the same act as putting the report out, so it takes
	// the same signature. Workspace admin is not a way around it.
	if err != nil || auditgate.Level(level) != auditreport.SigningLevel(int(project.ReviewLevels)) {
		writeErrorCode(w, http.StatusForbidden, "rank_required",
			"archiving this engagement needs "+string(auditreport.SigningLevel(int(project.ReviewLevels))))
		return
	}

	report, err := h.Queries.GetIssuedAuditReport(r.Context(), db.GetIssuedAuditReportParams{
		ProjectID: project.ID, WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeErrorCode(w, http.StatusConflict, "report_required",
			"issue the engagement's report before archiving it; a closed engagement with no report is not an audit anyone can file")
		return
	}
	unfinished, err := h.Queries.CountUnfinishedWorkpapers(r.Context(), db.CountUnfinishedWorkpapersParams{
		ProjectID:   project.ID,
		FiledStatus: auditmode.StatusFiled,
	})
	if err != nil {
		slog.Warn("CountUnfinishedWorkpapers failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to archive the engagement")
		return
	}
	if unfinished > 0 {
		writeErrorCode(w, http.StatusConflict, "workpapers_unfinished",
			"this engagement still has workpapers in the review chain; an archive taken over them would present unfinished work as a closed file")
		return
	}

	in, err := h.archiveInput(r, project, report, member)
	if err != nil {
		slog.Warn("archive input failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to archive the engagement")
		return
	}
	pkg := auditarchive.Build(in)

	// Files first, manifest last. A reader treats a prefix with no manifest as
	// unfinished, so a crash between the two leaves an archive that announces
	// itself as incomplete rather than one that lies about being whole.
	for _, f := range pkg.Files {
		if _, err := h.AuditArchiveStorage.Upload(r.Context(), f.Key, f.Data, "application/json", ""); err != nil {
			slog.Warn("archive upload failed", append(logger.RequestAttrs(r), "error", err, "key", f.Key)...)
			writeError(w, http.StatusInternalServerError, "failed to write the archive")
			return
		}
	}
	if _, err := h.AuditArchiveStorage.Upload(r.Context(), pkg.ManifestKey, pkg.Manifest, "application/json", ""); err != nil {
		slog.Warn("archive manifest upload failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to write the archive")
		return
	}

	// Stamped after the files exist. A crash in between leaves an unarchived
	// engagement beside a complete set of files, and re-running overwrites them
	// with identical bytes — which is what the builder being deterministic is
	// for.
	updated, err := h.Queries.MarkEngagementArchived(r.Context(), db.MarkEngagementArchivedParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		ArchiveKey:  pkg.ManifestKey,
	})
	if err != nil {
		slog.Warn("MarkEngagementArchived failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to close the engagement")
		return
	}
	if err := h.recordArchiveTrail(r, updated, userID, actorType); err != nil {
		// The file is written and the engagement is closed; a trail entry that
		// failed is worth reporting but not worth undoing either of those.
		slog.Warn("archive trail failed", append(logger.RequestAttrs(r), "error", err)...)
	}

	writeJSON(w, http.StatusOK, ArchiveResponse{
		ProjectID:   uuidToString(project.ID),
		ArchiveKey:  pkg.ManifestKey,
		ArchivedAt:  timestampToString(updated.AuditArchivedAt),
		Workpapers:  len(in.Workpapers),
		TrailCount:  len(in.Trail),
		Remediation: len(in.Remediation),
		Attachments: len(in.Attachments),
	})
}

// archiveInput reads everything the file holds.
func (h *Handler) archiveInput(r *http.Request, project db.Project, report db.AuditReport, member db.Member) (auditarchive.Input, error) {
	ctx := r.Context()
	workspaceID := uuidToString(project.WorkspaceID)
	projectID := uuidToString(project.ID)

	in := auditarchive.Input{
		Engagement: auditarchive.Engagement{
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			Title:        project.Title,
			ReviewLevels: int(project.ReviewLevels),
		},
		ArchivedBy: h.memberDisplayName(ctx, member.ID),
		ArchivedAt: time.Now().UTC(),
	}
	if ws, err := h.Queries.GetWorkspace(ctx, project.WorkspaceID); err == nil {
		in.Engagement.Auditee = ws.Name
	}
	if project.AuditType.Valid {
		in.Engagement.AuditType = project.AuditType.String
	}
	if project.AuditPhase.Valid {
		in.Engagement.Phase = project.AuditPhase.String
	}
	if project.AuditPeriodStart.Valid {
		in.Engagement.PeriodStart = project.AuditPeriodStart.Time.Format("2006-01-02")
	}
	if project.AuditPeriodEnd.Valid {
		in.Engagement.PeriodEnd = project.AuditPeriodEnd.Time.Format("2006-01-02")
	}

	in.Report = auditarchive.Report{
		ReportID:     uuidToString(report.ID),
		Version:      int(report.Version),
		Title:        report.Title,
		IssuedAt:     timestampToString(report.IssuedAt),
		IssuedBy:     h.memberDisplayName(ctx, report.IssuedBy),
		Background:   report.Background,
		Basis:        report.Basis,
		Scope:        report.Scope,
		Opinion:      report.Opinion,
		Requirements: report.Requirements,
	}
	doc, err := h.reportDocument(ctx, report)
	if err != nil {
		return in, err
	}
	// The report as it was ISSUED: reportDocument reads the snapshot for an
	// issued report, so the archived rendering is the document that went out
	// rather than one rebuilt from today's rows.
	in.ReportBody = auditreport.Markdown(doc)

	// Property values are stored against definition ids, which mean nothing in
	// a file someone opens in three years. Resolved to the names and option
	// labels an auditor reads.
	resolver, err := h.newPropertyResolver(ctx, project.WorkspaceID)
	if err != nil {
		return in, err
	}
	people, err := h.newPeopleResolver(ctx, project.WorkspaceID, project.ID)
	if err != nil {
		return in, err
	}

	workpapers, err := h.Queries.ListWorkpapersForArchive(ctx, project.ID)
	if err != nil {
		return in, err
	}
	for _, row := range workpapers {
		wp := auditarchive.Workpaper{
			IssueID:   uuidToString(row.ID),
			Number:    int(row.Number),
			Title:     row.Title,
			Status:    row.Status,
			UpdatedAt: row.UpdatedAt.Time.UTC(),
		}
		if row.PreparerID.Valid {
			wp.Preparer = people.member(row.PreparerID)
			// The USER id, matching TrailEntry.ActorID's namespace.
			wp.PreparerID = wp.Preparer.ID
		}
		if row.SubmittedAt.Valid {
			wp.SubmittedAt = timestampToString(row.SubmittedAt)
		}
		wp.Properties = resolver.resolve(row.Properties)
		in.Workpapers = append(in.Workpapers, wp)
	}

	trail, err := h.Queries.ListTrailForProject(ctx, db.ListTrailForProjectParams{
		WorkspaceID:   project.WorkspaceID,
		ProjectID:     project.ID,
		ProjectIDText: projectID,
	})
	if err != nil {
		return in, err
	}
	for _, row := range trail {
		entry := auditarchive.TrailEntry{
			ID:        uuidToString(row.ID),
			ActorType: row.ActorType.String,
			Action:    row.Action,
			Details:   json.RawMessage(row.Details),
			At:        row.CreatedAt.Time.UTC(),
		}
		if row.IssueID.Valid {
			entry.IssueID = uuidToString(row.IssueID)
		}
		if row.ActorID.Valid {
			entry.ActorID = uuidToString(row.ActorID)
			entry.Actor = people.user(row.ActorID)
		}
		in.Trail = append(in.Trail, entry)
	}

	items, err := h.Queries.ListRemediationForReport(ctx, project.ID)
	if err != nil {
		return in, err
	}
	for _, row := range items {
		item := auditarchive.RemediationItem{
			IssueID:    uuidToString(row.IssueID),
			Title:      row.Title,
			Department: row.DepartmentName,
			Status:     row.Status,
		}
		if row.DueDate.Valid {
			item.DueDate = row.DueDate.Time.Format("2006-01-02")
		}
		if row.AssigneeType.String == "member" {
			item.Responsible = people.user(row.AssigneeID)
		}
		if row.VerifiedBy.Valid {
			item.Verifier = people.member(row.VerifiedBy)
		}
		in.Remediation = append(in.Remediation, item)
	}

	attachments, err := h.Queries.ListAttachmentsForProject(ctx, project.ID)
	if err != nil {
		return in, err
	}
	for _, row := range attachments {
		in.Attachments = append(in.Attachments, auditarchive.Attachment{
			AttachmentID: uuidToString(row.ID),
			IssueID:      uuidToString(row.IssueID),
			Filename:     row.Filename,
			ContentType:  row.ContentType,
			SizeBytes:    row.SizeBytes,
		})
	}
	return in, nil
}

// peopleResolver turns the ids a live system uses into the people an archived
// file has to name.
//
// Two id namespaces reach this code: the trail records actors as USER ids, and
// audit_workpaper.preparer_id / audit_remediation.verified_by are MEMBER ids.
// The first version of the archive wrote both through untouched, so the two
// files could not even be matched against each other — the same person appeared
// as two unrelated uuids. Everything leaves here resolved, and the id kept
// alongside is always the user id.
type peopleResolver struct {
	byMember map[string]auditarchive.Person
	byUser   map[string]auditarchive.Person
}

func (h *Handler) newPeopleResolver(ctx context.Context, workspaceID, projectID pgtype.UUID) (peopleResolver, error) {
	res := peopleResolver{
		byMember: map[string]auditarchive.Person{},
		byUser:   map[string]auditarchive.Person{},
	}
	// The rank each person held on THIS engagement, as it stood at archival.
	// "赵六 项目经理" is what makes a signature readable; "赵六" alone leaves the
	// reader asking what standing they had to sign it.
	levels := map[string]string{}
	roles, err := h.Queries.ListAuditRolesForProject(ctx, projectID)
	if err != nil {
		return res, err
	}
	for _, role := range roles {
		levels[uuidToString(role.MemberID)] = role.Level
	}

	rows, err := h.Queries.ListWorkspaceDirectory(ctx, workspaceID)
	if err != nil {
		return res, err
	}
	for _, row := range rows {
		memberID := uuidToString(row.MemberID)
		userID := uuidToString(row.UserID)
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = row.Email
		}
		person := auditarchive.Person{ID: userID, Name: name, Level: levels[memberID]}
		res.byMember[memberID] = person
		res.byUser[userID] = person
	}
	return res, nil
}

// user resolves a trail actor. An id that no longer resolves keeps the id and
// no name — which is itself worth recording, because it says the person was
// already gone when the file closed.
func (r peopleResolver) user(id pgtype.UUID) auditarchive.Person {
	if !id.Valid {
		return auditarchive.Person{}
	}
	key := uuidToString(id)
	if person, ok := r.byUser[key]; ok {
		return person
	}
	return auditarchive.Person{ID: key}
}

// member resolves a preparer or a verifier, and returns them in the USER
// namespace so the archived files can be matched against each other.
func (r peopleResolver) member(id pgtype.UUID) auditarchive.Person {
	if !id.Valid {
		return auditarchive.Person{}
	}
	if person, ok := r.byMember[uuidToString(id)]; ok {
		return person
	}
	// Unresolvable: keep the raw id rather than dropping it, and say nothing
	// about which namespace it was in — that is exactly the confusion this
	// resolver exists to end.
	return auditarchive.Person{ID: uuidToString(id)}
}

// propertyResolver turns an issue's property bag into names and labels.
//
// A stored bag is keyed by definition id and, for a select, holds an option id.
// Both are meaningless to someone opening the archive in three years, and an
// archive nobody can read is not an archive.
type propertyResolver struct {
	names   map[string]string
	options map[string]string
}

func (h *Handler) newPropertyResolver(ctx context.Context, workspaceID pgtype.UUID) (propertyResolver, error) {
	res := propertyResolver{names: map[string]string{}, options: map[string]string{}}
	// Archived definitions included: a workpaper written under a definition
	// that was later retired still carries its value, and an archive that
	// rendered that value as a bare uuid would be unreadable exactly where the
	// history is oldest.
	defs, err := h.Queries.ListIssueProperties(ctx, db.ListIssuePropertiesParams{
		WorkspaceID: workspaceID, IncludeArchived: true,
	})
	if err != nil {
		return res, err
	}
	for _, def := range defs {
		res.names[uuidToString(def.ID)] = def.Name
		var cfg PropertyConfig
		if len(def.Config) > 0 {
			if err := json.Unmarshal(def.Config, &cfg); err != nil {
				continue
			}
		}
		for _, opt := range cfg.Options {
			res.options[opt.ID] = opt.Name
		}
	}
	return res, nil
}

// resolve renders one issue's bag. A value whose definition is gone keeps its
// raw key: dropping it would quietly shorten the record, which is the one thing
// an archive must not do.
func (r propertyResolver) resolve(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var bag map[string]any
	if err := json.Unmarshal(raw, &bag); err != nil {
		return nil
	}
	out := make(map[string]string, len(bag))
	for id, value := range bag {
		name := r.names[id]
		if name == "" {
			name = id
		}
		out[name] = r.renderValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r propertyResolver) renderValue(value any) string {
	switch v := value.(type) {
	case string:
		if label, ok := r.options[v]; ok {
			return label
		}
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func (h *Handler) recordArchiveTrail(r *http.Request, project db.Project, userID, actorType string) error {
	details, err := json.Marshal(map[string]any{
		"project_id":  uuidToString(project.ID),
		"archive_key": project.AuditArchiveKey.String,
	})
	if err != nil {
		return err
	}
	actorUUID, err := util.ParseUUID(userID)
	if err != nil {
		return err
	}
	_, err = h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
		ID:          dbid.NewV7(),
		WorkspaceID: project.WorkspaceID,
		ActorType:   pgtype.Text{String: actorType, Valid: actorType != ""},
		ActorID:     actorUUID,
		Action:      "engagement_archived",
		Details:     details,
	})
	return err
}
