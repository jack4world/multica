-- Audit-only facts about one workpaper, keyed by the issue it extends.
--
-- WHY NOT A COLUMN ON issue. Two reasons. `issue` is the hottest table in the
-- schema and this row is read only inside auditee workspaces, on the small
-- fraction of writes the review gate governs. And audit-only fields will accrue
-- here (rejection origin, version lineage) without touching the core table
-- again each time — which is what "the audit vertical adds tables only for
-- concepts the platform has no equivalent of" means in practice.
--
-- preparer_id is a PERMISSION INPUT: the no-self-review rule reads it. It is
-- written by the review gate at the moment of submission and is not exposed as
-- a writable API field, for the same reason the workpaper marker lives in
-- project membership rather than in a user-editable label or property.
--
-- The preparer cannot be recovered from the issue after the fact: ownership
-- moves as the workpaper passes through review, so submission is the only
-- moment the identity exists to be captured.
--
-- No primary key inline; see 462 and 463, per the repo convention.
CREATE TABLE audit_workpaper (
    issue_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    preparer_id UUID NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
