package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// Audit mode turns a workspace into an auditee: its projects become
// engagements and the issues inside them become workpapers. Enabling seeds the
// review chain into the workspace's OWN status catalog and the audit fields
// into its property catalog, so no parallel state machine exists to keep in
// sync. The catalog's shape is asserted in internal/auditmode; this file covers
// seeding, idempotency, the collision rules and the permission gate.

func newAuditWorkspace(t *testing.T) string {
	t.Helper()
	slug := "audit-" + uuid.NewString()[:8]
	wsID := dbfx.Workspace(t, "Auditee "+slug, slug)
	dbfx.Member(t, wsID, testUserID, "owner")
	// Production seeds the 7 built-ins inside the workspace-create transaction
	// (and migration 339 backfilled every workspace that predates the catalog),
	// so a fixture without them would test a workspace shape that cannot exist
	// — and would hide the fact that a seeded status has to sort AFTER the
	// built-in of its category.
	if err := issuestatus.Ensure(context.Background(), testHandler.Queries, parseUUID(wsID)); err != nil {
		t.Fatalf("seed built-in statuses: %v", err)
	}
	// issue_status and issue_property carry no foreign key to workspace (the
	// project forbids them), so deleting the workspace row would strand them.
	dbfx.Cleanup(t, `DELETE FROM issue_status WHERE workspace_id = $1`, wsID)
	dbfx.Cleanup(t, `DELETE FROM issue_property WHERE workspace_id = $1`, wsID)
	return wsID
}

func auditRequest(method, path, wsID string, body any) *http.Request {
	req := newRequest(method, path, body)
	req.Header.Set("X-Workspace-ID", wsID)
	return req
}

func enableAuditMode(t *testing.T, wsID string, body any) *testutil.Response {
	t.Helper()
	return testutil.Call(t, testHandler.EnableAuditMode, auditRequest(http.MethodPost, "/api/audit-mode", wsID, body))
}

func TestEnableAuditModeSeedsTheReviewChainAndAuditFields(t *testing.T) {
	wsID := newAuditWorkspace(t)

	var out AuditModeResponse
	enableAuditMode(t, wsID, nil).Want(http.StatusOK).JSON(&out)
	if !out.Enabled {
		t.Fatalf("enabled = false, want true (body said %+v)", out)
	}
	if out.EnabledAt == nil || *out.EnabledAt == "" {
		t.Error("enabled_at is empty; the flag records WHEN a workspace became an auditee")
	}

	for _, want := range auditmode.Statuses() {
		var category, name, color string
		var position float64
		dbfx.QueryRow(t,
			`SELECT category, name, color, position FROM issue_status WHERE workspace_id = $1 AND key = $2`,
			wsID, want.Key,
		).Scan(&category, &name, &color, &position)
		if category != want.Category {
			t.Errorf("status %q category = %q, want %q", want.Key, category, want.Category)
		}
		if name != want.Names[auditmode.LocaleZhHans] {
			t.Errorf("status %q name = %q, want the zh-Hans name %q", want.Key, name, want.Names[auditmode.LocaleZhHans])
		}
		if color != want.Color {
			t.Errorf("status %q color = %q, want %q", want.Key, color, want.Color)
		}
		// Built-ins sit at position 0 in their category, so a seeded status
		// must sort after the built-in it shares a category with.
		if position <= 0 {
			t.Errorf("status %q position = %v, want > 0 so it sorts after the built-in of its category", want.Key, position)
		}
	}

	// Within in_review, the four waiting states must sort in review order —
	// a board that lists 三级复核 before 一级复核 misrepresents the workflow.
	var l1, l2, l3 float64
	dbfx.QueryRow(t, `SELECT position FROM issue_status WHERE workspace_id = $1 AND key = $2`, wsID, auditmode.StatusReviewL1).Scan(&l1)
	dbfx.QueryRow(t, `SELECT position FROM issue_status WHERE workspace_id = $1 AND key = $2`, wsID, auditmode.StatusReviewL2).Scan(&l2)
	dbfx.QueryRow(t, `SELECT position FROM issue_status WHERE workspace_id = $1 AND key = $2`, wsID, auditmode.StatusReviewL3).Scan(&l3)
	if !(l1 < l2 && l2 < l3) {
		t.Errorf("in_review positions = l1 %v, l2 %v, l3 %v; want strictly increasing", l1, l2, l3)
	}

	for _, want := range auditmode.Properties() {
		var propType string
		var config []byte
		dbfx.QueryRow(t,
			`SELECT type, config FROM issue_property WHERE workspace_id = $1 AND name = $2`,
			wsID, want.Names[auditmode.LocaleZhHans],
		).Scan(&propType, &config)
		if propType != want.Type {
			t.Errorf("property %q type = %q, want %q", want.Key, propType, want.Type)
		}
		var cfg PropertyConfig
		if len(config) > 0 {
			if err := json.Unmarshal(config, &cfg); err != nil {
				t.Fatalf("property %q config is not decodable: %v", want.Key, err)
			}
		}
		if len(cfg.Options) != len(want.Options) {
			t.Errorf("property %q has %d options, want %d", want.Key, len(cfg.Options), len(want.Options))
		}
		for _, opt := range cfg.Options {
			if opt.ID == "" {
				t.Errorf("property %q option %q has no id; values reference option ids, so a rename would orphan them", want.Key, opt.Name)
			}
		}
	}
}

func TestEnableAuditModeIsIdempotent(t *testing.T) {
	wsID := newAuditWorkspace(t)

	var first AuditModeResponse
	enableAuditMode(t, wsID, nil).Want(http.StatusOK).JSON(&first)
	var second AuditModeResponse
	enableAuditMode(t, wsID, nil).Want(http.StatusOK).JSON(&second)

	if first.EnabledAt == nil || second.EnabledAt == nil || *first.EnabledAt != *second.EnabledAt {
		t.Errorf("enabled_at moved on re-enable: %v then %v; when a workspace became an auditee is a fact, not a counter", first.EnabledAt, second.EnabledAt)
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != len(auditmode.Statuses()) {
		t.Errorf("issue_status rows = %d, want %d", n, len(auditmode.Statuses()))
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_property WHERE workspace_id = $1`, wsID); n != len(auditmode.Properties()) {
		t.Errorf("issue_property rows = %d, want %d", n, len(auditmode.Properties()))
	}
}

// A workspace that already owns one of the audit keys must be refused rather
// than seeded around it: the review gate built on top of this catalog assumes
// `review_l1` behaves the way the audit catalog defines it, and silently
// keeping someone else's status of that name would build the gate on a lie.
func TestEnableAuditModeRefusesAStatusKeyCollision(t *testing.T) {
	wsID := newAuditWorkspace(t)
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": wsID,
		"key":          auditmode.StatusReviewL1,
		"name":         "Someone Else's Status",
		"category":     issuestatus.Backlog,
		"color":        "#123456",
	})

	enableAuditMode(t, wsID, nil).Want(http.StatusConflict)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM workspace WHERE id = $1 AND audit_mode_enabled_at IS NOT NULL`, wsID); n != 0 {
		t.Error("audit mode was enabled despite the collision; the seed must be all-or-nothing")
	}
	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != 1 {
		t.Errorf("issue_status rows = %d, want 1: the rejected seed must not have written anything", n)
	}
}

// idx_issue_status_workspace_name_active is unique on (workspace_id,
// lower(name)) among active rows, so a display-name clash is as fatal to the
// seed as a key clash — and it is reachable without anyone choosing an audit
// key: DeriveKey slugifies, a CJK name slugifies to nothing, and the fallback
// key is `<category>_2`. A firm that hand-built its review chain before
// enabling audit mode holds "一级复核" under key `in_review_2`, which the key
// check waves through.
func TestEnableAuditModeRefusesAStatusNameCollision(t *testing.T) {
	wsID := newAuditWorkspace(t)
	reviewL1, _ := auditmode.StatusByKey(auditmode.StatusReviewL1)
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": wsID,
		"key":          "in_review_2",
		"name":         reviewL1.Names[auditmode.LocaleZhHans],
		"category":     issuestatus.InReview,
		"color":        "#123456",
	})

	enableAuditMode(t, wsID, nil).Want(http.StatusConflict)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != 1 {
		t.Errorf("issue_status rows = %d, want 1: the rejected seed must not have written anything", n)
	}
}

// The 409 names what an admin has to go and rename. Keys are machine handles
// the settings UI does not show, so reporting a key under the word "named"
// sends them looking for a string that is not on their screen.
func TestStatusCollisionMessageNamesWhatTheAdminCanSee(t *testing.T) {
	wsID := newAuditWorkspace(t)
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": wsID,
		"key":          auditmode.StatusReviewL1,
		"name":         "Someone Else's Status",
		"category":     issuestatus.Backlog,
		"color":        "#123456",
	})

	body := enableAuditMode(t, wsID, nil).Want(http.StatusConflict).Body.String()
	if !strings.Contains(body, "Someone Else's Status") {
		t.Errorf("409 body = %s; want it to name the display name the admin sees", body)
	}
}

func TestEnableAuditModeRefusesAPropertyNameCollisionCaseInsensitively(t *testing.T) {
	wsID := newAuditWorkspace(t)
	// idx_issue_property_ws_name is on LOWER(name), so a differently-cased
	// duplicate would fail the insert rather than create a second definition.
	clash := auditmode.Properties()[0].Names[auditmode.LocaleZhHans]
	dbfx.Insert(t, "issue_property", testutil.Cols{
		"workspace_id": wsID,
		"name":         clash,
		"type":         "text",
		"description":  "",
	})

	enableAuditMode(t, wsID, nil).Want(http.StatusConflict)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != 0 {
		t.Errorf("issue_status rows = %d, want 0: a property collision must roll the status seed back too", n)
	}
}

func TestEnableAuditModeIsOwnerAdminOnlyAndNeverAnAgent(t *testing.T) {
	wsID := newAuditWorkspace(t)

	t.Run("plain member", func(t *testing.T) {
		userID := dbfx.User(t, "Audit Plain Member", fmt.Sprintf("audit-member-%s@multica.ai", uuid.NewString()[:8]))
		dbfx.Member(t, wsID, userID, "member")
		req := auditRequest(http.MethodPost, "/api/audit-mode", wsID, nil)
		req.Header.Set("X-User-ID", userID)
		testutil.Call(t, testHandler.EnableAuditMode, req).Want(http.StatusForbidden)
	})

	t.Run("agent actor", func(t *testing.T) {
		// An agent inherits its runtime owner's credentials. Without this gate
		// an owner's agent could put the workspace into audit mode on its own.
		req := auditRequest(http.MethodPost, "/api/audit-mode", wsID, nil)
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Agent-ID", uuid.NewString())
		testutil.Call(t, testHandler.EnableAuditMode, req).Want(http.StatusForbidden)
	})

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != 0 {
		t.Errorf("issue_status rows = %d, want 0: neither rejected caller may seed", n)
	}
}

func TestEnableAuditModeSeedsTheRequestedLocaleAndRejectsUnsupportedOnes(t *testing.T) {
	wsID := newAuditWorkspace(t)

	enableAuditMode(t, wsID, map[string]string{"locale": "en"}).Want(http.StatusOK)

	filed, _ := auditmode.StatusByKey(auditmode.StatusFiled)
	var name string
	dbfx.QueryRow(t, `SELECT name FROM issue_status WHERE workspace_id = $1 AND key = $2`, wsID, auditmode.StatusFiled).Scan(&name)
	if name != filed.Names[auditmode.LocaleEn] {
		t.Errorf("filed name = %q, want the en name %q", name, filed.Names[auditmode.LocaleEn])
	}

	other := newAuditWorkspace(t)
	enableAuditMode(t, other, map[string]string{"locale": "fr"}).Want(http.StatusBadRequest)
}

// A body that is present but malformed is rejected rather than treated as
// "enable with defaults": a typo in the locale field would otherwise seed the
// wrong language into a production workspace, and the names are stored rows
// that no later request restates.
// Seeded statuses append after whatever the workspace already holds. Pinning
// them to fixed positions 1..n instead would tie with an existing custom status
// of the same category, and the catalog's tie-break on key would drop that
// unrelated status into the middle of the review chain.
func TestSeededStatusesAppendAfterExistingCustomStatuses(t *testing.T) {
	wsID := newAuditWorkspace(t)
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": wsID,
		"key":          "triage",
		"name":         "Triage",
		"category":     issuestatus.InReview,
		"color":        "#123456",
		"position":     1,
	})

	enableAuditMode(t, wsID, nil).Want(http.StatusOK)

	var existing, firstReview float64
	dbfx.QueryRow(t, `SELECT position FROM issue_status WHERE workspace_id = $1 AND key = 'triage'`, wsID).Scan(&existing)
	dbfx.QueryRow(t, `SELECT position FROM issue_status WHERE workspace_id = $1 AND key = $2`, wsID, auditmode.StatusReviewL1).Scan(&firstReview)
	if firstReview <= existing {
		t.Errorf("review_l1 position = %v, existing in_review status = %v; the chain must append after it", firstReview, existing)
	}
}

func TestEnableAuditModeRejectsAMalformedBody(t *testing.T) {
	wsID := newAuditWorkspace(t)

	req := httptest.NewRequest(http.MethodPost, "/api/audit-mode", strings.NewReader(`{"locale": `))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", wsID)
	testutil.Call(t, testHandler.EnableAuditMode, req).Want(http.StatusBadRequest)

	if n := dbfx.Count(t, `SELECT COUNT(*) FROM issue_status WHERE workspace_id = $1 AND is_system = FALSE`, wsID); n != 0 {
		t.Errorf("issue_status rows = %d, want 0", n)
	}
}

func TestGetAuditModeReflectsWhetherTheWorkspaceIsAnAuditee(t *testing.T) {
	wsID := newAuditWorkspace(t)

	var before AuditModeResponse
	testutil.Call(t, testHandler.GetAuditMode, auditRequest(http.MethodGet, "/api/audit-mode", wsID, nil)).
		Want(http.StatusOK).JSON(&before)
	if before.Enabled {
		t.Error("enabled = true on a fresh workspace")
	}
	if before.EnabledAt != nil {
		t.Errorf("enabled_at = %v on a fresh workspace, want null", *before.EnabledAt)
	}

	enableAuditMode(t, wsID, nil).Want(http.StatusOK)

	var after AuditModeResponse
	testutil.Call(t, testHandler.GetAuditMode, auditRequest(http.MethodGet, "/api/audit-mode", wsID, nil)).
		Want(http.StatusOK).JSON(&after)
	if !after.Enabled {
		t.Error("enabled = false after enabling")
	}
}
