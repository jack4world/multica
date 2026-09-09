-- Reviewer roles for the three-level workpaper review chain, scoped to one
-- engagement (a project inside an auditee workspace).
--
-- SCOPE. Governs status transitions ONLY. Nothing may read this table from a
-- list, search, board, inbox or dispatch query: access isolation is auditee
-- membership and nothing else (ADR-0001), and reading roles into a visibility
-- filter would rebuild the cross-cutting access surface that ADR avoids.
--
-- No foreign keys and no cascades, per the project's database rules; the
-- application resolves workspace, project and member, and cleans up.
--
-- id is NOT declared inline as PRIMARY KEY: the repo convention (migrations
-- 332-334) is to build the table without one, create the backing unique index
-- CONCURRENTLY in its own single-statement migration (458), then attach it
-- (459). An inline PRIMARY KEY would build its index non-concurrently.
CREATE TABLE audit_role (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    member_id UUID NOT NULL,

    -- The three levels of the chain. A preparer is not a level: preparing is
    -- what the workpaper's own submitter does, recorded per workpaper in
    -- audit_workpaper, not granted per engagement.
    level TEXT NOT NULL CHECK (level IN ('reviewer_l1', 'reviewer_l2', 'reviewer_l3')),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID
);
