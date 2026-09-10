// Package auditdocs holds the classification tree's path rules and the standard
// filing scheme a new auditee starts from.
//
// A category's identity IS its path — "02", "02/01" — so "everything under
// 账务凭证" is a prefix read on one index rather than a recursive walk.
//
// The usual objection to materialised paths is that moving a subtree rewrites
// every descendant. It does not apply to an audit filing scheme: the scheme is
// defined once and stable for years, while the query it must be fast at — give
// me this section and everything in it — is the one prefix paths are best at.
package auditdocs

import (
	"fmt"
	"strings"
)

const (
	// separator is part of the prefix, not just a display convention. A prefix
	// test that omits it matches 020 as a child of 02, which is the bug this
	// design is most likely to ship with and is invisible until a client has
	// enough categories to collide.
	separator = "/"

	// MaxDepth bounds the tree. A path is a filing scheme, not a filesystem;
	// an uncapped one makes prefix reads slow and paths unreadable.
	MaxDepth = 4

	// segmentLen is fixed at two digits so the tree sorts lexically in the
	// order an auditor expects — "10" after "09", not between "01" and "02".
	segmentLen = 2
)

// ValidatePath checks a classification path.
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("a document must be filed somewhere; the category path is empty")
	}
	segments := strings.Split(path, separator)
	if len(segments) > MaxDepth {
		return fmt.Errorf("category path %q is %d levels deep; the filing scheme allows %d",
			path, len(segments), MaxDepth)
	}
	for _, seg := range segments {
		if len(seg) != segmentLen {
			return fmt.Errorf("category path %q: each level is exactly %d digits, so the tree sorts",
				path, segmentLen)
		}
		for _, r := range seg {
			if r < '0' || r > '9' {
				return fmt.Errorf("category path %q: levels are digits", path)
			}
		}
	}
	return nil
}

// Depth reports how many levels a path has.
func Depth(path string) int {
	if path == "" {
		return 0
	}
	return strings.Count(path, separator) + 1
}

// ParentPath returns the path one level up, or "" for a top-level category.
func ParentPath(path string) string {
	idx := strings.LastIndex(path, separator)
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// DescendantPattern is the SQL LIKE pattern for everything strictly BELOW a
// path. The separator is what keeps 020 from matching as a child of 02.
func DescendantPattern(path string) string {
	return path + separator + "%"
}

// UnderPath reports whether a path is the given one or beneath it. The Go half
// of the same rule the SQL pattern expresses, so both can be tested together.
func UnderPath(root string) func(string) bool {
	prefix := root + separator
	return func(candidate string) bool {
		return candidate == root || strings.HasPrefix(candidate, prefix)
	}
}

// Category is one node of the filing scheme.
type Category struct {
	Path  string
	Names map[string]string
}

// StandardScheme is the classification a new auditee starts from.
//
// Seeded rather than invented per client for two reasons: nobody should be
// designing a taxonomy under time pressure, and anyone moving between clients
// should find the same drawers in the same places. Extending it per client is
// allowed; these nodes are marked as standard so an interface can show which
// parts are.
//
// Parents come before children, so a caller can apply the list in order.
func StandardScheme() []Category {
	return []Category{
		{Path: "01", Names: map[string]string{"zh-Hans": "立项与计划", "en": "Engagement setup and planning"}},
		{Path: "01/01", Names: map[string]string{"zh-Hans": "审计通知书", "en": "Engagement notice"}},
		{Path: "01/02", Names: map[string]string{"zh-Hans": "审计方案", "en": "Audit plan"}},

		{Path: "02", Names: map[string]string{"zh-Hans": "制度与授权", "en": "Policies and delegations"}},
		{Path: "02/01", Names: map[string]string{"zh-Hans": "内控制度", "en": "Internal control policies"}},
		{Path: "02/02", Names: map[string]string{"zh-Hans": "审批权限表", "en": "Approval authority matrix"}},

		{Path: "03", Names: map[string]string{"zh-Hans": "账务凭证", "en": "Accounting vouchers"}},
		{Path: "03/01", Names: map[string]string{"zh-Hans": "记账凭证", "en": "Journal vouchers"}},
		{Path: "03/02", Names: map[string]string{"zh-Hans": "原始单据", "en": "Source documents"}},
		{Path: "03/03", Names: map[string]string{"zh-Hans": "银行对账单", "en": "Bank statements"}},

		{Path: "04", Names: map[string]string{"zh-Hans": "合同与协议", "en": "Contracts and agreements"}},
		{Path: "04/01", Names: map[string]string{"zh-Hans": "采购合同", "en": "Procurement contracts"}},
		{Path: "04/02", Names: map[string]string{"zh-Hans": "销售合同", "en": "Sales contracts"}},

		{Path: "05", Names: map[string]string{"zh-Hans": "函证与外部证据", "en": "Confirmations and external evidence"}},
		{Path: "05/01", Names: map[string]string{"zh-Hans": "银行函证", "en": "Bank confirmations"}},
		{Path: "05/02", Names: map[string]string{"zh-Hans": "往来函证", "en": "Counterparty confirmations"}},

		{Path: "06", Names: map[string]string{"zh-Hans": "访谈与说明", "en": "Interviews and representations"}},
		{Path: "06/01", Names: map[string]string{"zh-Hans": "访谈记录", "en": "Interview records"}},
		{Path: "06/02", Names: map[string]string{"zh-Hans": "管理层声明", "en": "Management representations"}},

		{Path: "07", Names: map[string]string{"zh-Hans": "其他资料", "en": "Other material"}},
	}
}
