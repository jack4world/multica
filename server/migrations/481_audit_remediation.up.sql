-- Audit-only facts about one 整改事项, keyed by the issue it extends.
--
-- WHY NOT COLUMNS ON issue. The same reasoning as audit_workpaper: `issue` is
-- the hottest table in the schema, these fields are read only inside auditee
-- workspaces, and the ledger will accrue fields (verification evidence,
-- deadline history) without touching the core table again each time.
--
-- source_project_id IS NOT DECORATION. It is the engagement that raised the
-- item, and it is where the verifier's rank is read from: an item with no
-- source has nobody qualified to close it. It is also what makes the glossary's
-- claim true — a remediation item outlives the audit that found it, and next
-- year's 后续审计 finds last year's items through this column.
--
-- THE DEADLINE IS issue.due_date. The platform already has it, on every list,
-- filter and board; a second date column would be a second answer to "when is
-- this due" and the two would disagree within a month.
--
-- OVERDUE IS NOT A COLUMN AND NOT A STATUS. It is due_date < today on an item
-- that is not closed. A stored flag is only as true as the last time a job ran.
--
-- No primary key inline; see 482 and 483, per the repo convention.
CREATE TABLE audit_remediation (
    issue_id UUID NOT NULL,
    workspace_id UUID NOT NULL,

    -- The engagement that raised it. NOT NULL: see above.
    source_project_id UUID NOT NULL,
    -- The workpaper the problem was found in, when it was found in one.
    source_issue_id UUID,

    -- 责任部门. NOT NULL: an item with no owning department is precisely the
    -- item nobody fixes.
    department_id UUID NOT NULL,

    -- Written together at closure, by the verification, never by the person
    -- responsible for the fix.
    verified_by UUID,
    verified_at TIMESTAMPTZ,
    verification_note TEXT,

    -- What makes "remind once" true across restarts and catch-up runs. A
    -- deadline that generates a notification every morning trains people to
    -- ignore the notification, which costs more than the missed deadline did.
    overdue_reminded_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
