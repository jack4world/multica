package auditreport

import (
	"fmt"
	"html"
	"strings"
)

// Rendering. Pure, so the document a report produces can be pinned by a golden
// test — a change to the shape of an internal audit report is a change someone
// chose, not one that arrived with a refactor.
//
// Markdown and HTML only. Word and PDF are a rendering chain and a template
// negotiation with each organization, and neither changes what the report IS.

// Finding is one item the report cites, as it stood when the report was
// signed. A signed report renders from these, never from live rows: an item
// closed in March must not rewrite a report issued in January.
type Finding struct {
	Title      string `json:"title"`
	Department string `json:"department"`
	DueDate    string `json:"due_date,omitempty"`
	Status     string `json:"status"`
	IssueID    string `json:"issue_id"`
}

// Document is everything the renderer needs.
type Document struct {
	Title        string
	Auditee      string
	Engagement   string
	PeriodStart  string
	PeriodEnd    string
	Version      int
	Status       string
	IssuedAt     string
	IssuedBy     string
	Background   string
	Basis        string
	Scope        string
	Opinion      string
	Requirements string
	Findings     []Finding
	// Workpapers and FiledWorkpapers make the report's claim about the work
	// behind it checkable rather than asserted.
	Workpapers      int
	FiledWorkpapers int
}

// section titles, in the order an internal audit report has them.
var sectionOrder = []struct {
	heading string
	pick    func(Document) string
}{
	{"一、基本情况", func(d Document) string { return d.Background }},
	{"二、审计依据", func(d Document) string { return d.Basis }},
	{"三、审计范围", func(d Document) string { return d.Scope }},
}

// Markdown renders the report.
func Markdown(d Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", d.Title)

	// The header block an auditor checks first: who, what period, which
	// version, and whether this is the issued one.
	fmt.Fprintf(&b, "- 被审计单位：%s\n", fallback(d.Auditee))
	fmt.Fprintf(&b, "- 审计项目：%s\n", fallback(d.Engagement))
	if d.PeriodStart != "" || d.PeriodEnd != "" {
		fmt.Fprintf(&b, "- 审计期间：%s 至 %s\n", fallback(d.PeriodStart), fallback(d.PeriodEnd))
	}
	fmt.Fprintf(&b, "- 报告版本：第 %d 版\n", d.Version)
	if d.Status == StatusIssued {
		fmt.Fprintf(&b, "- 签发：%s（%s）\n", fallback(d.IssuedBy), fallback(d.IssuedAt))
	} else {
		b.WriteString("- 签发：未签发（草稿，不得对外）\n")
	}
	fmt.Fprintf(&b, "- 工作底稿：共 %d 份，已归档 %d 份\n\n", d.Workpapers, d.FiledWorkpapers)

	for _, s := range sectionOrder {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", s.heading, fallbackBody(s.pick(d)))
	}

	b.WriteString("## 四、审计发现与整改事项\n\n")
	if len(d.Findings) == 0 {
		b.WriteString("本次审计未提出整改事项。\n\n")
	} else {
		b.WriteString("| # | 事项 | 责任部门 | 整改期限 | 状态 |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for i, f := range d.Findings {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
				i+1, cell(f.Title), cell(f.Department), cell(f.DueDate), cell(f.Status))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## 五、审计意见\n\n%s\n\n", fallbackBody(d.Opinion))
	fmt.Fprintf(&b, "## 六、整改要求\n\n%s\n", fallbackBody(d.Requirements))
	return b.String()
}

// HTML renders the same document as a self-contained fragment, so it can be
// pasted into whatever the organization actually sends out.
func HTML(d Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(d.Title))
	b.WriteString("<ul>\n")
	fmt.Fprintf(&b, "<li>被审计单位：%s</li>\n", esc(fallback(d.Auditee)))
	fmt.Fprintf(&b, "<li>审计项目：%s</li>\n", esc(fallback(d.Engagement)))
	if d.PeriodStart != "" || d.PeriodEnd != "" {
		fmt.Fprintf(&b, "<li>审计期间：%s 至 %s</li>\n", esc(fallback(d.PeriodStart)), esc(fallback(d.PeriodEnd)))
	}
	fmt.Fprintf(&b, "<li>报告版本：第 %d 版</li>\n", d.Version)
	if d.Status == StatusIssued {
		fmt.Fprintf(&b, "<li>签发：%s（%s）</li>\n", esc(fallback(d.IssuedBy)), esc(fallback(d.IssuedAt)))
	} else {
		b.WriteString("<li>签发：未签发（草稿，不得对外）</li>\n")
	}
	fmt.Fprintf(&b, "<li>工作底稿：共 %d 份，已归档 %d 份</li>\n", d.Workpapers, d.FiledWorkpapers)
	b.WriteString("</ul>\n")

	for _, s := range sectionOrder {
		fmt.Fprintf(&b, "<h2>%s</h2>\n%s\n", esc(s.heading), paragraphs(s.pick(d)))
	}

	b.WriteString("<h2>四、审计发现与整改事项</h2>\n")
	if len(d.Findings) == 0 {
		b.WriteString("<p>本次审计未提出整改事项。</p>\n")
	} else {
		b.WriteString("<table>\n<thead><tr><th>#</th><th>事项</th><th>责任部门</th><th>整改期限</th><th>状态</th></tr></thead>\n<tbody>\n")
		for i, f := range d.Findings {
			fmt.Fprintf(&b, "<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				i+1, esc(f.Title), esc(f.Department), esc(f.DueDate), esc(f.Status))
		}
		b.WriteString("</tbody>\n</table>\n")
	}

	fmt.Fprintf(&b, "<h2>五、审计意见</h2>\n%s\n", paragraphs(d.Opinion))
	fmt.Fprintf(&b, "<h2>六、整改要求</h2>\n%s\n", paragraphs(d.Requirements))
	return b.String()
}

// fallback marks an empty header field rather than rendering a blank, so a
// reader can tell "not recorded" from "rendered wrong".
func fallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return "（未填写）"
	}
	return v
}

func fallbackBody(v string) string {
	if strings.TrimSpace(v) == "" {
		return "（本节尚未填写）"
	}
	return strings.TrimSpace(v)
}

// cell keeps a table row a table row. A pipe inside a title would otherwise
// split the cell and silently shift every column after it.
func cell(v string) string {
	v = strings.ReplaceAll(strings.TrimSpace(v), "|", "\\|")
	return strings.ReplaceAll(v, "\n", " ")
}

func esc(v string) string { return html.EscapeString(v) }

func paragraphs(v string) string {
	body := strings.TrimSpace(v)
	if body == "" {
		return "<p>（本节尚未填写）</p>"
	}
	var b strings.Builder
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		fmt.Fprintf(&b, "<p>%s</p>\n", strings.ReplaceAll(esc(para), "\n", "<br>"))
	}
	return strings.TrimRight(b.String(), "\n")
}
