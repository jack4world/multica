# The audit report is a document with a signature, not an issue with a status

The deliverable of an internal audit is a report; the workpapers are the supporting material behind it. Before this the vertical could prove a workpaper passed two levels of review and could not produce the document anyone outside the audit function will ever read — so that document was written in Word, by hand, by copying findings out of the system, and the report and the ledger disagreed within a week.

`audit_report` is its own table. A workpaper is an issue because it is a unit of work with an assignee and a status; a report is a document. Modelling it as an issue would have put it in the board, the backlog and the assignment model, and would have put the workpaper review chain — whose rules are about a preparer and a rank per level — in charge of something it was not designed for.

Its five sections are columns, not a JSONB bag. An internal audit report's structure is not user-configurable: 基本情况 / 审计依据 / 审计范围 / 审计意见 / 整改要求 is what one has, and a closed list is a schema rather than a limitation.

## Consequences

- **Three states, not a level-by-level chain.** `drafting → reviewing → issued`, plus a send-back that requires a reason. A report is signed; whether each level saw the evidence is answered upstream, on the workpapers.
- **The signer is the engagement's TOP rank**, from `auditgate.LevelsUpTo(depth)`. At depth two that is 项目经理, at depth three 部门负责人 — so a two-level engagement is never blocked waiting for a level it does not run. This reuses ADR-0003 rather than adding a second answer to "who is senior here".
- **Workspace admin is not a way around the signature.** Admin is an administrative role; signing the department's report is not an administrative act. Admin can start a report and edit one; only the rank can issue it.
- **There is no rule against signing your own draft.** In a five-person department the 部门负责人 often writes the report, and demanding a second signature there is the invented-reviewer mistake ADR-0003 exists to avoid. Independence lives on the findings, in the workpaper chain.
- **Issuing snapshots.** The report stores the items it cited and the workpaper counts behind it at the moment of signature, and renders from the snapshot thereafter. An item closed in March must not rewrite a report issued in January — which is precisely the document 后续审计 reads. A draft renders live, because it has asserted nothing yet.
- **One unsigned report per engagement**, enforced by a partial unique index. Two people drafting two reports nobody reconciles is how an audit issues the wrong one; a correction is made by issuing this one and starting version 2.
- **Markdown and HTML only.** Word and PDF are a rendering chain plus a template negotiation with each organization, and neither changes what the report says. Rendering is a pure function over a document value, so the output is pinned by a golden test and a change to the report's shape is a change someone chose.
- **A draft always says it is a draft.** The rendered header carries 未签发（草稿，不得对外）, because the failure mode of an unmarked draft is that someone sends it.
