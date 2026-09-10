// Package auditmode holds the catalog an audit workspace is seeded with: the
// workpaper review chain expressed as custom issue statuses, and the audit
// fields expressed as custom issue property definitions.
//
// NOTHING HERE IS A NEW MECHANISM. The review chain is issue_status rows
// (migration 332) and the audit fields are issue_property rows (migration 191),
// so the platform's existing catalog, board, filter, CLI and agent-instruction
// paths render the audit workflow without knowing it exists. The alternative —
// a parallel review_status column and a parallel field set on issue — would
// have forced every one of those paths to learn a second source of truth.
//
// The definitions are data, and the seeding that writes them is in the handler
// layer, so the shape of the workflow can be asserted without a database.
package auditmode

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/issuestatus"
)

// Locale selects the language of the seeded names. These are stored rows, not
// i18n strings: a workspace is seeded once, in one language, and the names it
// gets are the names its users edit from then on.
type Locale string

const (
	LocaleZhHans Locale = "zh-Hans"
	LocaleEn     Locale = "en"
)

// Locales lists the languages the catalog can be seeded in. ja/ko are absent
// deliberately — the vertical has no business in those markets yet, and a
// half-translated catalog is worse than an English one.
func Locales() []Locale { return []Locale{LocaleZhHans, LocaleEn} }

// ParseLocale maps a request value onto a supported locale, defaulting to
// Simplified Chinese for the empty string.
func ParseLocale(v string) Locale {
	if strings.TrimSpace(v) == "" {
		return LocaleZhHans
	}
	loc, err := ValidateLocale(v)
	if err != nil {
		return LocaleZhHans
	}
	return loc
}

// ValidateLocale rejects a locale the catalog has no names for.
func ValidateLocale(v string) (Locale, error) {
	for _, loc := range Locales() {
		if strings.EqualFold(v, string(loc)) {
			return loc, nil
		}
	}
	names := make([]string, 0, len(Locales()))
	for _, loc := range Locales() {
		names = append(names, string(loc))
	}
	return "", fmt.Errorf("locale must be one of: %s", strings.Join(names, ", "))
}

// The workpaper review chain. Keys are the machine handles stored in
// issue.status; the display names live in StatusDef.Names.
const (
	// StatusDrafting is the preparer writing, or an agent running.
	StatusDrafting = "drafting"
	StatusReviewL1 = "review_l1"
	StatusReviewL2 = "review_l2"
	StatusReviewL3 = "review_l3"
	// StatusFiled is the terminal state: passed every level the engagement
	// runs, immutable.
	// NOT keyed "archived" — issue_status.archived_at already means "this
	// status definition was retired", and one table cannot carry two senses of
	// the word.
	StatusFiled = "filed"
)

// StatusDef is one row to seed into a workspace's issue status catalog.
type StatusDef struct {
	Key string
	// Category is the behavior equivalence class this status inherits whole.
	Category     string
	Color        string
	Names        map[Locale]string
	Descriptions map[Locale]string
}

// Statuses returns the review chain in board order. All three review levels
// are seeded whatever depth an engagement runs: the catalog is the auditee's,
// depth is the engagement's, and an auditee with a three-level engagement and a
// two-level one needs the same catalog for both. Which levels a given workpaper
// can reach is the gate's ruling, not the catalog's (see docs/adr/0003).
//
// Every status a workpaper WAITS in is in_review, and that is load-bearing
// rather than cosmetic.
//
// There is no status between drafting and review. An agent that finishes a
// draft leaves its account as a COMMENT and the workpaper stays in 编制中; a
// person submits it when they judge it ready. A workflow state is the most
// expensive way to represent something — it appears in the picker, the board,
// the filters and the vocabulary an auditor has to learn — so it has to earn
// its place in the AUDITOR's model, not only in ours. in_review is the only category that both finalizes the
// autopilot run (service/autopilot.go) and is skipped by the stuck-issue
// sweeper (service/task.go, which resets in_progress). A workpaper parked in an
// in_progress-category status while it waits for its reviewer would be reset by
// that sweeper as a wedged agent run.
func Statuses() []StatusDef {
	return []StatusDef{
		{
			Key:      StatusDrafting,
			Category: issuestatus.InProgress,
			Color:    "#f59e0b",
			Names: map[Locale]string{
				LocaleZhHans: "编制中",
				LocaleEn:     "Drafting",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "编制人正在编写，或 agent 正在执行。",
				LocaleEn:     "The preparer is writing, or an agent is running.",
			},
		},
		{
			Key:      StatusReviewL1,
			Category: issuestatus.InReview,
			Color:    "#22c55e",
			Names: map[Locale]string{
				LocaleZhHans: "一级复核",
				LocaleEn:     "Review L1",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "待主审复核。",
				LocaleEn:     "Waiting on the lead auditor.",
			},
		},
		{
			Key:      StatusReviewL2,
			Category: issuestatus.InReview,
			Color:    "#14b8a6",
			Names: map[Locale]string{
				LocaleZhHans: "二级复核",
				LocaleEn:     "Review L2",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "待项目经理复核。",
				LocaleEn:     "Waiting on the engagement manager.",
			},
		},
		{
			Key:      StatusReviewL3,
			Category: issuestatus.InReview,
			Color:    "#0ea5e9",
			Names: map[Locale]string{
				LocaleZhHans: "三级复核",
				LocaleEn:     "Review L3",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "待部门负责人复核。",
				LocaleEn:     "Waiting on the audit department head.",
			},
		},
		{
			Key:      StatusFiled,
			Category: issuestatus.Done,
			Color:    "#3b82f6",
			Names: map[Locale]string{
				LocaleZhHans: "已归档",
				LocaleEn:     "Filed",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "通过全部复核级次并归档。此后不可修改，修正只能出新版本。",
				LocaleEn:     "Passed every review level and filed. Immutable: a correction is a new version, never an edit.",
			},
		},
	}
}

// StatusByKey looks one status up in the catalog.
func StatusByKey(key string) (StatusDef, bool) {
	for _, s := range Statuses() {
		if s.Key == key {
			return s, true
		}
	}
	return StatusDef{}, false
}

// StatusKeys returns the seeded keys in board order.
func StatusKeys() []string {
	keys := make([]string, 0, len(Statuses()))
	for _, s := range Statuses() {
		keys = append(keys, s.Key)
	}
	return keys
}

// Property definition types, mirroring issue_property.type.
const (
	PropertyTypeText   = "text"
	PropertyTypeSelect = "select"
)

// Audit property handles. These are internal identifiers for the seeder and
// tests; issue_property itself has no key column, so a definition's stored
// identity is its name.
const (
	PropertyProcedureCode = "procedure_code"
	PropertyProcedureType = "procedure_type"
	PropertyConclusion    = "conclusion"
)

// PropertyOption is one choice of a select definition.
type PropertyOption struct {
	Key   string
	Color string
	Names map[Locale]string
}

// PropertyDef is one row to seed into a workspace's issue property catalog.
type PropertyDef struct {
	Key          string
	Type         string
	Names        map[Locale]string
	Descriptions map[Locale]string
	Options      []PropertyOption
}

// Properties returns the audit fields seeded onto every workpaper.
//
// The narrative conclusion is NOT here: issue.properties is a bounded JSONB bag
// (16KB per issue, 20 active definitions per workspace) meant for short typed
// values, and a workpaper's reasoning belongs in its description. What is here
// is the conclusion's CLASSIFICATION, which is what gets filtered and counted.
func Properties() []PropertyDef {
	return []PropertyDef{
		{
			Key:  PropertyProcedureCode,
			Type: PropertyTypeText,
			Names: map[Locale]string{
				LocaleZhHans: "审计程序编码",
				LocaleEn:     "Procedure Code",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "审计程序的编码，如 A1-01。拆分出的子底稿继承父底稿的编码。",
				LocaleEn:     "The procedure's code, e.g. A1-01. Child workpapers inherit their parent's.",
			},
		},
		{
			Key:  PropertyProcedureType,
			Type: PropertyTypeSelect,
			Names: map[Locale]string{
				LocaleZhHans: "程序类型",
				LocaleEn:     "Procedure Type",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "本条底稿执行的审计程序类别。",
				LocaleEn:     "The kind of audit procedure this workpaper records.",
			},
			Options: []PropertyOption{
				{Key: "sampling", Color: "#3b82f6", Names: map[Locale]string{LocaleZhHans: "抽样检查", LocaleEn: "Sampling"}},
				{Key: "interview", Color: "#8b5cf6", Names: map[Locale]string{LocaleZhHans: "访谈穿透", LocaleEn: "Interview"}},
				{Key: "data_analysis", Color: "#14b8a6", Names: map[Locale]string{LocaleZhHans: "数据分析", LocaleEn: "Data Analysis"}},
				{Key: "walkthrough", Color: "#f59e0b", Names: map[Locale]string{LocaleZhHans: "穿行测试", LocaleEn: "Walkthrough"}},
				{Key: "analytical", Color: "#6b7280", Names: map[Locale]string{LocaleZhHans: "分析性复核", LocaleEn: "Analytical Review"}},
			},
		},
		{
			Key:  PropertyConclusion,
			Type: PropertyTypeSelect,
			Names: map[Locale]string{
				LocaleZhHans: "审计结论",
				LocaleEn:     "Conclusion",
			},
			Descriptions: map[Locale]string{
				LocaleZhHans: "本条底稿的结论定性。详细论述写在描述里。",
				LocaleEn:     "How this workpaper concluded. The reasoning belongs in the description.",
			},
			Options: []PropertyOption{
				{Key: "clean", Color: "#22c55e", Names: map[Locale]string{LocaleZhHans: "无异常", LocaleEn: "No Exception"}},
				{Key: "exception", Color: "#ef4444", Names: map[Locale]string{LocaleZhHans: "存在问题", LocaleEn: "Exception Found"}},
				{Key: "inconclusive", Color: "#6b7280", Names: map[Locale]string{LocaleZhHans: "无法确定", LocaleEn: "Inconclusive"}},
			},
		},
	}
}
