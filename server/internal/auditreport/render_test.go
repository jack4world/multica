package auditreport

import (
	"strings"
	"testing"
)

// The document itself. Pinned, because the shape of an internal audit report is
// something someone chose and not something a refactor should be able to move.

func sampleDocument() Document {
	return Document{
		Title:        "2025 年度经济责任审计报告",
		Auditee:      "华东分公司",
		Engagement:   "2025 年离任审计",
		PeriodStart:  "2025-01-01",
		PeriodEnd:    "2025-12-31",
		Version:      1,
		Status:       StatusIssued,
		IssuedAt:     "2026-03-01T08:00:00Z",
		IssuedBy:     "审计部负责人",
		Background:   "本次审计对华东分公司 2025 年度经营情况进行了检查。",
		Basis:        "《中华人民共和国审计法》及公司内部审计制度。",
		Scope:        "采购、费用报销、合同管理三个循环。",
		Opinion:      "总体内控有效，采购环节存在两项缺陷。",
		Requirements: "请责任部门于 2026 年 6 月 30 日前完成整改并提交材料。",
		Findings: []Finding{
			{Title: "三份采购合同未经审批", Department: "采购部", DueDate: "2026-06-30", Status: "整改中", IssueID: "i-1"},
			{Title: "费用报销缺少附件", Department: "财务部", DueDate: "2026-05-31", Status: "待验证", IssueID: "i-2"},
		},
		Workpapers:      12,
		FiledWorkpapers: 12,
	}
}

const wantMarkdown = `# 2025 年度经济责任审计报告

- 被审计单位：华东分公司
- 审计项目：2025 年离任审计
- 审计期间：2025-01-01 至 2025-12-31
- 报告版本：第 1 版
- 签发：审计部负责人（2026-03-01T08:00:00Z）
- 工作底稿：共 12 份，已归档 12 份

## 一、基本情况

本次审计对华东分公司 2025 年度经营情况进行了检查。

## 二、审计依据

《中华人民共和国审计法》及公司内部审计制度。

## 三、审计范围

采购、费用报销、合同管理三个循环。

## 四、审计发现与整改事项

| # | 事项 | 责任部门 | 整改期限 | 状态 |
| --- | --- | --- | --- | --- |
| 1 | 三份采购合同未经审批 | 采购部 | 2026-06-30 | 整改中 |
| 2 | 费用报销缺少附件 | 财务部 | 2026-05-31 | 待验证 |

## 五、审计意见

总体内控有效，采购环节存在两项缺陷。

## 六、整改要求

请责任部门于 2026 年 6 月 30 日前完成整改并提交材料。
`

func TestTheIssuedReportRendersAsPinned(t *testing.T) {
	got := Markdown(sampleDocument())
	if got != wantMarkdown {
		t.Errorf("the report's shape changed.\n--- got ---\n%s\n--- want ---\n%s", got, wantMarkdown)
	}
}

// A draft that renders like an issued report is a draft someone will send out.
func TestADraftSaysItIsNotIssued(t *testing.T) {
	d := sampleDocument()
	d.Status = StatusDrafting
	md := Markdown(d)
	if !strings.Contains(md, "未签发") {
		t.Error("a draft renders with no sign that it is one")
	}
	if strings.Contains(md, "审计部负责人") {
		t.Error("a draft names a signatory; nobody has signed it")
	}
}

func TestAnEmptySectionIsMarked(t *testing.T) {
	d := sampleDocument()
	d.Opinion = "   "
	if !strings.Contains(Markdown(d), "（本节尚未填写）") {
		t.Error("an unfilled section renders blank; a reader cannot tell it from a rendering fault")
	}
}

func TestAReportWithNoFindingsSaysSo(t *testing.T) {
	d := sampleDocument()
	d.Findings = nil
	md := Markdown(d)
	if !strings.Contains(md, "本次审计未提出整改事项") {
		t.Error("a report with no findings renders an empty table")
	}
	if strings.Contains(md, "| # |") {
		t.Error("a report with no findings still drew the table header")
	}
}

// A pipe in a title would split the cell and shift every column after it — the
// table would look fine and say something else.
func TestAPipeInAFindingDoesNotBreakTheTable(t *testing.T) {
	d := sampleDocument()
	d.Findings = []Finding{{Title: "A|B 未对账", Department: "财务部", Status: "整改中"}}
	line := ""
	for _, l := range strings.Split(Markdown(d), "\n") {
		if strings.Contains(l, "未对账") {
			line = l
		}
	}
	if strings.Count(line, "|")-strings.Count(line, "\\|") != 6 {
		t.Errorf("row %q has the wrong number of cells", line)
	}
}

func TestTheHTMLEscapesWhatPeopleTyped(t *testing.T) {
	d := sampleDocument()
	d.Findings = []Finding{{Title: "<script>alert(1)</script>", Department: "IT", Status: "整改中"}}
	out := HTML(d)
	if strings.Contains(out, "<script>") {
		t.Error("a finding's text reached the HTML unescaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("the escaped text is missing entirely")
	}
}

func TestTheHTMLKeepsParagraphs(t *testing.T) {
	d := sampleDocument()
	d.Opinion = "第一段。\n\n第二段。"
	out := HTML(d)
	if strings.Count(out, "<p>第一段。</p>") != 1 || strings.Count(out, "<p>第二段。</p>") != 1 {
		t.Errorf("two paragraphs did not survive rendering:\n%s", out)
	}
}
