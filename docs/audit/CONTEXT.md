# Audit

The internal-audit vertical built on the Multica platform. It covers state-owned-enterprise internal audit and separation-of-office audit, not public accounting firm external audit.

Terms below are the contract. Where a term maps onto a platform entity, the platform entity is the thing that exists; the audit term is what users call it. Chinese renderings are overridden at the i18n layer only — code identifiers stay platform identifiers (ADR-0002).

## Language

### Structure

**被审计单位 (Auditee)**:
The organization under audit. Is a platform workspace. The unit of access isolation: seeing an auditee's data means being a member of its workspace.
_Avoid_: 客户, 工作区, client, tenant

**审计项目 (Engagement)**:
One audit of one auditee covering one period. Is a platform project. An auditee accumulates engagements over the years and they share its documents and configuration.
_Avoid_: 项目, 审计任务, audit project

**工作底稿 (Workpaper)**:
The record of executing one audit procedure and the conclusion drawn from it. Is a platform issue that belongs to an engagement. Belonging to an engagement is what makes an issue a workpaper — there is no other kind of issue inside one.
_Avoid_: 任务, issue, 底稿附件

**审计程序 (Procedure)**:
A planned piece of examination work, identified by a procedure code. One-to-one with a workpaper; when one procedure needs splitting, child workpapers inherit its code. Not an entity of its own.
_Avoid_: 步骤, task, run

**审计资料 (Document)**:
Original material belonging to an auditee — its policies, contracts, vouchers,
confirmations — filed under a classification tree two levels deep. A particular
workpaper's own supporting scan is not one of these: it is an attachment on that
workpaper, where the test it supports is. Vouchers, policy documents and contracts are categories of it, not separate kinds. Uses a platform attachment as its binary carrier.
_Avoid_: 附件, 凭证 (a voucher is one category of document, not a synonym)

### People

**编制人 (Preparer)**:
The person who owned a workpaper at the moment it was submitted for review. Distinct from its creator and from whoever currently owns it — once review starts, ownership moves to the reviewer.
_Avoid_: 作者, 创建者, 负责人, author

**复核人 (Reviewer)**:
Someone seated at one of an engagement's review levels: 主审 (l1), 项目经理 (l2), 部门负责人 (l3). A preparer may never review their own workpaper, and no person may hold two levels on the same engagement. Which levels exist is the engagement's 复核级数, so a rank above that depth cannot be seated.
_Avoid_: 审核人, 批准人, approver

**复核级数 (Review Depth)**:
How many review levels an engagement runs: one, two or three, default two. A property of the engagement, decided when it is set up and rarely corrected afterwards — never a property of an individual workpaper, and never a global setting.
_Avoid_: 复核层级, 审批层级, 级别

### Process

**复核 (Review)**:
A reviewer's ruling on a workpaper: pass, or reject. Rejection always returns the workpaper to its preparer, never to the level below.
_Avoid_: 审核, 审批, 批准

**归档 (Archived)**:
The terminal state a workpaper reaches after passing every review level its engagement runs. Immutable: a correction is a new version, never an edit. Unrelated to a status definition being retired from the catalog.
_Avoid_: 完成, 锁定, 封存, locked


**审计阶段 (Engagement Phase)**:
Where an engagement is in its own lifecycle: 准备 (preparation), 现场 (fieldwork), 报告 (reporting), 后续跟踪 (follow_up). Descriptive, not a gate — it records where the work is, and no transition is refused because of it. Distinct from 项目状态, which is the platform's own project status.
_Avoid_: 项目状态, 进度, stage

### Findings

**疑点 (Observation)**:
An anomaly reported by an agent or a person that has not been verified yet. Carries a business dedup key, so the same anomaly reported twice is one observation.
_Avoid_: 风险, 发现, 问题, 线索, exception

**审计发现 (Finding)**:
A problem confirmed to be real. Not a stored entity: a finding is the remediation item that one or more confirmed observations point at.
_Avoid_: 疑点, 风险

**整改事项 (Remediation Item)**:
Something the auditee is required to fix, with a 责任部门, a 整改责任人 and a 整改期限. Is a platform issue that belongs to no engagement — it outlives the audit that found it — and records the engagement that raised it. Reaches 已关闭 only through a 验证.
_Avoid_: 问题, 发现

**整改台账 (Remediation Ledger)**:
Every remediation item an auditee owes, with its department, deadline and state. Not a separate store: it is the view over the items, which is why an item can be filtered, assigned and commented on like any other work.
_Avoid_: 整改清单, 问题清单, backlog

**责任部门 (Responsible Department)**:
The department that owes a fix. Chosen from the auditee's own list, never typed free-hand — counting items by department is the reason the field exists, and two spellings of one department make every count wrong. Distinct from 整改责任人: the person can leave, the department still owes it.
_Avoid_: 部门, 责任人, owner

**整改期限 (Remediation Deadline)**:
When the fix is due. Is the platform issue's due date. An item past it and not closed is 逾期, which is read from the date and never stored — a stored flag is only as true as the last time a job ran.
_Avoid_: 截止日期, deadline (as a separate field)

**验证 (Verification)**:
The audit function checking that a fix actually happened, and recording what was checked. Performed by someone holding a 复核人 rank on the engagement that raised the item, never by the person responsible for the fix. Closure is a verification; there is no other way to reach 已关闭.
_Avoid_: 确认, 复核 (which is about workpapers), 关闭

**后续审计 (Follow-Up Audit)**:
Checking later whether remediation actually held. Reads the ledger of a past engagement; it is why an item points at the engagement that raised it rather than belonging to one.
_Avoid_: 回访, 复查

**风险 (Risk)**:
Keeps its audit meaning only: the planning-stage assessment of where material misstatement is likely, used to decide what to examine and how deeply. Never a word for an observation or a finding.
_Avoid_: using it for anything a procedure discovers

