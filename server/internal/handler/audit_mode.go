package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Audit mode marks a workspace as an auditee: its projects are audit
// engagements and the issues inside them are workpapers subject to the
// three-level review chain.
//
// Enabling seeds the review chain into the workspace's OWN issue status
// catalog (migration 332) and the audit fields into its OWN property catalog
// (migration 191). Nothing here introduces a parallel state machine or a
// parallel field set: every existing board, filter, CLI command and agent
// instruction renders the audit workflow without knowing it exists.
//
// There is no disable. Turning audit mode off would strand workpapers on
// statuses whose review semantics no longer apply, and "this workspace used to
// be an auditee" is not a state an audit product should be able to enter.

// AuditModeResponse reports whether a workspace is an auditee.
type AuditModeResponse struct {
	Enabled   bool    `json:"enabled"`
	EnabledAt *string `json:"enabled_at"`
}

// EnableAuditModeRequest selects the language of the seeded catalog. The names
// are stored rows rather than i18n strings, so the choice is made once.
type EnableAuditModeRequest struct {
	Locale string `json:"locale"`
}

func auditModeResponse(at pgtype.Timestamptz) AuditModeResponse {
	return AuditModeResponse{Enabled: at.Valid, EnabledAt: timestampToPtr(at)}
}

// GetAuditMode reports the workspace's audit mode. Readable by any member:
// every client needs it to decide which vocabulary and which boards to render.
func (h *Handler) GetAuditMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin", "member"); !ok {
		return
	}
	// The catalog read does not need the row lock the enable path takes.
	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		// requireWorkspaceRole already proved the workspace exists and the
		// caller is a member, so anything else here is infrastructure. Reported
		// as 404 a client may act on it by dropping the workspace tab.
		slog.Warn("GetAuditMode failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to read audit mode")
		return
	}
	writeJSON(w, http.StatusOK, auditModeResponse(ws.AuditModeEnabledAt))
}

// EnableAuditMode seeds the audit catalog and marks the workspace an auditee.
//
// Idempotent by way of a row lock on the workspace: a second call returns the
// original timestamp untouched rather than re-seeding, so "when did this become
// an auditee" cannot be rewritten and two concurrent enables cannot both seed.
func (h *Handler) EnableAuditMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// Agents are rejected before the role check, exactly as property
	// definitions are: an agent inherits its runtime owner's credentials, so
	// without this an owner's agent could convert the workspace on its own.
	if actorType, _ := h.resolveActor(r, userID, workspaceID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot enable audit mode")
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}

	// An absent body is the common case (enable with defaults), so EOF is not
	// an error — but a body that is present and malformed is, or a locale typo
	// would silently seed the default language into a production workspace.
	var req EnableAuditModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	locale := auditmode.LocaleZhHans
	if strings.TrimSpace(req.Locale) != "" {
		parsed, err := auditmode.ValidateLocale(req.Locale)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		locale = parsed
	}

	enabledAt, seeded, status, message := h.seedAuditMode(r, workspaceID, wsUUID, locale)
	if status != 0 {
		if status == http.StatusInternalServerError {
			slog.Warn("EnableAuditMode failed", append(logger.RequestAttrs(r), "error", message)...)
			writeError(w, status, "failed to enable audit mode")
			return
		}
		writeError(w, status, message)
		return
	}

	// Only on a real transition. A repeat POST writes nothing, and invalidating
	// every client's catalog for a no-op is pure churn.
	if seeded {
		// Two caches move, not one. The status catalog gains the review chain,
		// and the workspace object gains audit_mode — which is what every
		// client reads to choose its vocabulary and boards, so without this
		// they keep rendering 工作区/项目/任务 until a manual reload.
		h.publishIssueStatusChanged(workspaceID, member, "created")
		if ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err == nil {
			h.publish(protocol.EventWorkspaceUpdated, workspaceID, "member", requestUserID(r),
				map[string]any{"workspace": h.workspaceToResponse(ws)})
		}
	}
	writeJSON(w, http.StatusOK, auditModeResponse(enabledAt))
}

// seedAuditMode does the transactional work. A non-zero status is the HTTP
// status to report; for 500 the message is the internal error, not client copy.
// The bool reports whether this call actually seeded, as opposed to finding the
// workspace already in audit mode.
//
// workspaceID is the raw request-scoped id, not the canonical rendering of
// wsUUID: it is what property.go builds its advisory lock key from, and a lock
// key that does not match byte-for-byte is not the same lock.
func (h *Handler) seedAuditMode(r *http.Request, workspaceID string, wsUUID pgtype.UUID, locale auditmode.Locale) (pgtype.Timestamptz, bool, int, string) {
	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	// FOR UPDATE: the read that decides "already enabled?" and the seed that
	// follows have to be one atomic step, or two concurrent enables both see
	// NULL and the loser fails on a duplicate key halfway through seeding.
	existing, err := qtx.GetWorkspaceAuditMode(ctx, wsUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.Timestamptz{}, false, http.StatusNotFound, "workspace not found"
	}
	if err != nil {
		// The role check already proved the workspace exists, so anything else
		// here is infrastructure and must not be reported as a missing row.
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if existing.Valid {
		// Already an auditee. Still seed the filing scheme: it is idempotent,
		// and this is the ONLY path that reaches an auditee enabled before the
		// document library existed. Returning here without it left those
		// workspaces with no categories and no way to get any, because filing
		// requires a category and creating one requires its parent.
		//
		// `seeded` stays false: nothing about the workspace's audit status
		// changed, so no client cache needs invalidating.
		if err := h.seedAuditDocumentCategories(ctx, qtx, wsUUID); err != nil {
			return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
		}
		// Same reason, same path: an auditee enabled before the remediation
		// chain existed has no catalog entry for 整改中, and no other route by
		// which to get one. Convergent, so re-enabling adds only what is
		// missing.
		if status, msg := h.seedMissingAuditStatuses(ctx, qtx, wsUUID, locale); status != 0 {
			return pgtype.Timestamptz{}, false, status, msg
		}
		if err := tx.Commit(ctx); err != nil {
			return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
		}
		return existing, false, 0, ""
	}

	// This is the only path that holds BOTH catalog locks, so it defines their
	// order: properties first, then statuses. Nothing else takes the pair, so
	// no other path can approach them the other way round and deadlock.
	if err := qtx.LockIssuePropertyCatalog(ctx, "props:"+workspaceID); err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	// The same EXCLUSIVE catalog lock every status create takes, so a seed and
	// a hand-created status cannot interleave between the collision check and
	// the inserts.
	if err := qtx.LockIssueStatusCatalog(ctx, wsUUID); err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}

	// Refuse rather than seed around a collision. The review gate built on this
	// catalog assumes `review_l1` means what the audit catalog says it means;
	// quietly keeping an unrelated status of that name would build the gate on
	// a status with different behavior.
	taken, err := qtx.ListIssueStatusKeysInSet(ctx, db.ListIssueStatusKeysInSetParams{
		WorkspaceID: wsUUID,
		Keys:        auditmode.SeedStatusKeys(),
	})
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if len(taken) > 0 {
		// Report the DISPLAY names: keys are machine handles the settings UI
		// never shows, so an admin told to rename `review_l1` has nothing to
		// search for.
		clashing := make([]string, 0, len(taken))
		for _, row := range taken {
			clashing = append(clashing, row.Name)
		}
		return pgtype.Timestamptz{}, false, http.StatusConflict,
			"this workspace already has statuses occupying the audit keys: " + strings.Join(clashing, ", ") + "; rename or retire them before enabling audit mode"
	}

	// Second collision axis: idx_issue_status_workspace_name_active is unique
	// on (workspace_id, lower(name)) among active rows. A workspace that built
	// its own review chain by hand collides here without owning a single audit
	// key, because DeriveKey slugifies a CJK name to nothing and falls back to
	// `<category>_2`.
	loweredStatusNames := make([]string, 0, len(auditmode.SeedStatuses()))
	for _, st := range auditmode.SeedStatuses() {
		loweredStatusNames = append(loweredStatusNames, strings.ToLower(st.Names[locale]))
	}
	takenStatusNames, err := qtx.ListIssueStatusNamesInSet(ctx, db.ListIssueStatusNamesInSetParams{
		WorkspaceID: wsUUID,
		Names:       loweredStatusNames,
	})
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if len(takenStatusNames) > 0 {
		return pgtype.Timestamptz{}, false, http.StatusConflict,
			"this workspace already has statuses named: " + strings.Join(takenStatusNames, ", ") + "; rename them before enabling audit mode"
	}

	lowered := make([]string, 0, len(auditmode.Properties()))
	for _, p := range auditmode.Properties() {
		lowered = append(lowered, strings.ToLower(p.Names[locale]))
	}
	takenNames, err := qtx.ListIssuePropertyNamesInSet(ctx, db.ListIssuePropertyNamesInSetParams{
		WorkspaceID: wsUUID,
		Names:       lowered,
	})
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if len(takenNames) > 0 {
		return pgtype.Timestamptz{}, false, http.StatusConflict,
			"this workspace already has properties named: " + strings.Join(takenNames, ", ") + "; rename them before enabling audit mode"
	}

	active, err := qtx.CountActiveIssueProperties(ctx, wsUUID)
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if int(active)+len(auditmode.Properties()) > maxActivePropertiesPerWorkspace {
		return pgtype.Timestamptz{}, false, http.StatusBadRequest,
			"not enough room in the property catalog to seed the audit fields; archive some definitions first"
	}

	// In chain order: the query positions each row at MAX+1 within its
	// category, so each insert sees the one before it.
	for _, st := range auditmode.SeedStatuses() {
		if err := qtx.SeedAuditIssueStatusEntry(ctx, db.SeedAuditIssueStatusEntryParams{
			WorkspaceID: wsUUID,
			Key:         st.Key,
			Name:        st.Names[locale],
			Description: st.Descriptions[locale],
			Category:    st.Category,
			Color:       st.Color,
		}); err != nil {
			return pgtype.Timestamptz{}, false, seedWriteStatus(err), err.Error()
		}
	}

	for _, p := range auditmode.Properties() {
		var cfg *PropertyConfig
		if len(p.Options) > 0 {
			cfg = &PropertyConfig{Options: make([]PropertyOption, 0, len(p.Options))}
			for _, o := range p.Options {
				cfg.Options = append(cfg.Options, PropertyOption{Name: o.Names[locale], Color: o.Color})
			}
		}
		// Reuse the definition validator so seeded options get the same
		// server-assigned UUIDs and colour normalization a hand-created
		// definition gets — issue values reference option ids, not names.
		config, err := validatePropertyConfig(p.Type, cfg)
		if err != nil {
			return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
		}
		if err := qtx.SeedAuditIssueProperty(ctx, db.SeedAuditIssuePropertyParams{
			WorkspaceID: wsUUID,
			Name:        p.Names[locale],
			Type:        p.Type,
			Description: p.Descriptions[locale],
			Config:      config,
		}); err != nil {
			return pgtype.Timestamptz{}, false, seedWriteStatus(err), err.Error()
		}
	}

	// The filing scheme, so nobody designs a taxonomy under time pressure and
	// every client's file looks the same to anyone who moves between them.
	// Idempotent, so an auditee that predates it can be given one by calling
	// this path again rather than by a migration that would have to guess.
	if err := h.seedAuditDocumentCategories(ctx, qtx, wsUUID); err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}

	enabledAt, err := qtx.EnableWorkspaceAuditMode(ctx, wsUUID)
	if err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	if err := tx.Commit(ctx); err != nil {
		return pgtype.Timestamptz{}, false, http.StatusInternalServerError, err.Error()
	}
	return enabledAt, true, 0, ""
}

// seedWriteStatus classifies a seed insert failure. The pre-flight checks
// above are what produce the actionable message, but they run under a lock
// this transaction takes, not one the whole cluster takes forever — and a
// catalog is also editable from paths that do not take it at all. A unique
// violation reaching here is still a collision, and reporting it as 500 would
// tell the admin their server is broken when their catalog merely overlaps.
func seedWriteStatus(err error) int {
	if isUniqueViolation(err) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// seedMissingAuditStatuses adds any seeded status this auditee does not have.
//
// THE PATH, not the mechanism. An auditee enabled before a chain existed is
// reached only here: the enable endpoint returns early once a workspace is an
// auditee, so a status added to the catalog in a later release has no other way
// in, and the feature built on it silently does not exist for every workspace
// that adopted the vertical early. Convergent by construction — it adds what is
// absent and touches nothing that is present, so calling it again costs one
// query per status and changes nothing.
//
// A non-zero status is the HTTP status to report.
func (h *Handler) seedMissingAuditStatuses(ctx context.Context, qtx *db.Queries, wsUUID pgtype.UUID, locale auditmode.Locale) (int, string) {
	defs := auditmode.SeedStatuses()
	// The name index is unique on (workspace_id, lower(name)) among active
	// rows, so a workspace that built its own 整改中 by hand would fail the
	// insert. Reported as a conflict naming the row to rename, not as a 500:
	// the admin can fix it, and nothing else can.
	lowered := make([]string, 0, len(defs))
	for _, st := range defs {
		lowered = append(lowered, strings.ToLower(st.Names[locale]))
	}
	takenNames, err := qtx.ListIssueStatusNamesInSet(ctx, db.ListIssueStatusNamesInSetParams{
		WorkspaceID: wsUUID,
		Names:       lowered,
	})
	if err != nil {
		return http.StatusInternalServerError, err.Error()
	}
	existing, err := qtx.ListIssueStatusKeysInSet(ctx, db.ListIssueStatusKeysInSetParams{
		WorkspaceID: wsUUID,
		Keys:        auditmode.SeedStatusKeys(),
	})
	if err != nil {
		return http.StatusInternalServerError, err.Error()
	}
	have := make(map[string]bool, len(existing))
	haveName := make(map[string]bool, len(existing))
	for _, row := range existing {
		have[row.Key] = true
		haveName[strings.ToLower(row.Name)] = true
	}
	// A name held by a row that is NOT one of ours is the collision worth
	// reporting; a name held by our own seeded row is simply already seeded.
	clashing := make([]string, 0, len(takenNames))
	for _, name := range takenNames {
		if !haveName[strings.ToLower(name)] {
			clashing = append(clashing, name)
		}
	}
	if len(clashing) > 0 {
		return http.StatusConflict,
			"this workspace already has statuses named: " + strings.Join(clashing, ", ") + "; rename them so the audit chain can be completed"
	}

	for _, st := range defs {
		if have[st.Key] {
			continue
		}
		if err := qtx.SeedAuditIssueStatusEntryIfAbsent(ctx, db.SeedAuditIssueStatusEntryIfAbsentParams{
			WorkspaceID: wsUUID,
			Key:         st.Key,
			Name:        st.Names[locale],
			Description: st.Descriptions[locale],
			Category:    st.Category,
			Color:       st.Color,
		}); err != nil {
			return seedWriteStatus(err), err.Error()
		}
	}
	return 0, ""
}
