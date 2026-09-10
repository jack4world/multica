package auditmode

import (
	"regexp"
	"testing"

	"github.com/multica-ai/multica/server/internal/issuestatus"
)

// The audit workflow's shape is asserted here rather than in the handler suite:
// these are pure facts about the catalog, and a DB round-trip would not make
// them any truer. The handler test covers seeding and the collision rules.

func TestStatusesFormAReviewChainEndingInAnImmutableState(t *testing.T) {
	got := make([]string, 0, len(Statuses()))
	for _, s := range Statuses() {
		got = append(got, s.Key)
	}
	want := []string{
		StatusDrafting,
		StatusReviewL1,
		StatusReviewL2,
		StatusReviewL3,
		StatusFiled,
	}
	if len(got) != len(want) {
		t.Fatalf("status count = %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status order = %v, want %v", got, want)
		}
	}
}

func TestEveryStatusCategoryIsCanonical(t *testing.T) {
	for _, s := range Statuses() {
		if !issuestatus.IsCategory(s.Category) {
			t.Errorf("status %q category %q is not one of the 7 canonical categories", s.Key, s.Category)
		}
	}
}

// A workpaper waiting on a human must not look like an agent is still working
// on it. in_review is the only category that both finalizes the autopilot run
// (service/autopilot.go) and is left alone by the stuck-issue sweeper
// (service/task.go) — in_progress is swept, which would reset a workpaper that
// is merely waiting for its reviewer.
func TestWaitingStatusesAreInReviewSoTheSweeperLeavesThemAlone(t *testing.T) {
	waiting := []string{StatusReviewL1, StatusReviewL2, StatusReviewL3}
	for _, key := range waiting {
		s, ok := StatusByKey(key)
		if !ok {
			t.Fatalf("status %q missing from catalog", key)
		}
		if s.Category != issuestatus.InReview {
			t.Errorf("status %q category = %q, want %q", key, s.Category, issuestatus.InReview)
		}
	}
}

// Every status in the chain has to be a stage an auditor would recognise. A
// state that exists for the software's benefit — as 待采纳 did, to give an
// agent's output somewhere to sit — shows up in the picker, the board, the
// filters and the vocabulary, and is paid for by every person who uses the
// product rather than by the code that needed it.
func TestEveryStatusIsAStageOfAnAudit(t *testing.T) {
	stages := map[string]bool{
		StatusDrafting: true, StatusReviewL1: true, StatusReviewL2: true,
		StatusReviewL3: true, StatusFiled: true,
	}
	for _, s := range Statuses() {
		if !stages[s.Key] {
			t.Errorf("status %q is not a stage of an audit; if the software needs a place to "+
				"put something, a comment or a field is cheaper than a workflow state", s.Key)
		}
	}
}

func TestDraftingIsInProgressAndFiledIsDone(t *testing.T) {
	if s, _ := StatusByKey(StatusDrafting); s.Category != issuestatus.InProgress {
		t.Errorf("drafting category = %q, want %q", s.Category, issuestatus.InProgress)
	}
	if s, _ := StatusByKey(StatusFiled); s.Category != issuestatus.Done {
		t.Errorf("filed category = %q, want %q", s.Category, issuestatus.Done)
	}
}

// The terminal state is keyed `filed`, not `archived`: issue_status already has
// an archived_at column meaning "this status definition was retired", and one
// table cannot carry two senses of the same word (ADR-0002 round, Q22).
func TestTerminalStatusIsNotKeyedArchived(t *testing.T) {
	for _, s := range Statuses() {
		if s.Key == "archived" {
			t.Fatal("terminal status must not be keyed \"archived\": it collides with issue_status.archived_at")
		}
	}
}

func TestStatusKeysAndColorsAreStorable(t *testing.T) {
	colorRe := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	seen := map[string]bool{}
	for _, s := range Statuses() {
		if _, err := issuestatus.ValidateKey(s.Key); err != nil {
			t.Errorf("status key %q rejected by the catalog validator: %v", s.Key, err)
		}
		if seen[s.Key] {
			t.Errorf("duplicate status key %q", s.Key)
		}
		seen[s.Key] = true
		if !colorRe.MatchString(s.Color) {
			t.Errorf("status %q color %q does not match the storage CHECK", s.Key, s.Color)
		}
	}
}

func TestPropertiesAreWithinTheActiveDefinitionCap(t *testing.T) {
	// issue_property caps a workspace at 20 active definitions. Seeding must
	// leave room for the workspace's own.
	if n := len(Properties()); n > 5 {
		t.Errorf("seeded property count = %d; keep the audit catalog small, the cap is 20 per workspace", n)
	}
}

func TestSelectPropertiesCarryOptionsAndTextPropertiesDoNot(t *testing.T) {
	for _, p := range Properties() {
		switch p.Type {
		case PropertyTypeSelect:
			if len(p.Options) == 0 {
				t.Errorf("select property %q has no options", p.Key)
			}
		case PropertyTypeText:
			if len(p.Options) != 0 {
				t.Errorf("text property %q must not declare options", p.Key)
			}
		default:
			t.Errorf("property %q has unsupported type %q", p.Key, p.Type)
		}
	}
}

// Seeded names are stored rows, not i18n strings, so the locale is chosen once
// at enable time and every supported locale has to be complete — a missing
// entry would seed an empty name and fail the storage CHECK.
func TestEveryLocaleNamesEveryStatusPropertyAndOption(t *testing.T) {
	for _, loc := range Locales() {
		for _, s := range Statuses() {
			if s.Names[loc] == "" {
				t.Errorf("status %q has no %s name", s.Key, loc)
			}
		}
		for _, p := range Properties() {
			if p.Names[loc] == "" {
				t.Errorf("property %q has no %s name", p.Key, loc)
			}
			for _, o := range p.Options {
				if o.Names[loc] == "" {
					t.Errorf("property %q option %q has no %s name", p.Key, o.Key, loc)
				}
			}
		}
	}
}

func TestParseLocaleDefaultsToSimplifiedChinese(t *testing.T) {
	// The vertical's market is mainland internal audit (Q2), so an unspecified
	// locale seeds Chinese rather than English.
	if got := ParseLocale(""); got != LocaleZhHans {
		t.Errorf("ParseLocale(\"\") = %q, want %q", got, LocaleZhHans)
	}
	if got := ParseLocale("en"); got != LocaleEn {
		t.Errorf("ParseLocale(\"en\") = %q, want %q", got, LocaleEn)
	}
	if _, err := ValidateLocale("fr"); err == nil {
		t.Error("ValidateLocale(\"fr\") = nil error, want rejection")
	}
}
