-- The auditee's list of 责任部门: who a remediation item belongs to.
--
-- WHY A LIST AND NOT FREE TEXT. The reason a 整改台账 exists is to be counted —
-- "本期整改事项按责任部门分布" is the line every 后续审计 report carries. Free
-- text produces 财务部 and 财务处 and 财务部门 as three departments, and the
-- count is then wrong in a way nobody notices.
--
-- NO HIERARCHY AND NO MEMBERSHIP. A tree would immediately raise "does 财务部
-- include 财务共享中心" without helping anyone answer it, and a department is
-- not an access boundary: separation is a separate auditee (docs/adr/0001).
--
-- Belongs to the WORKSPACE, like the classification tree: the same auditee's
-- departments span every engagement, and per-engagement copies would drift.
--
-- Seeded EMPTY. Every organization's department list is its own, and a guessed
-- one is a list people work around rather than correct.
--
-- No primary key inline; see 478 and 479, per the repo convention.
CREATE TABLE audit_department (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    position DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
