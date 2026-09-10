-- The auditee's classification tree, as materialised paths.
--
-- A category's identity IS its path ("03", "03/01"), so "everything under
-- 账务凭证" is a prefix read on one index rather than a recursive walk. The
-- usual objection — moving a subtree rewrites every descendant — does not apply
-- to an audit filing scheme: the scheme is defined once and stable for years,
-- while the prefix read is the query it has to be fast at.
--
-- Belongs to the WORKSPACE (the auditee), not to a project. The same client's
-- material spans every audit of it; filing per engagement would mean copying it
-- forward every year and letting the copies drift. Access is auditee
-- membership, unchanged (docs/adr/0001).
--
-- is_standard marks the seeded scheme, so an interface can show which parts are
-- the classification every auditee shares and which a client added.
--
-- No primary key inline; see 468 and 469, per the repo convention.
CREATE TABLE audit_document_category (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- Two-digit segments separated by "/", at most four levels. Fixed width so
    -- the tree sorts lexically in the order an auditor expects: "10" after
    -- "09", not between "01" and "02".
    path TEXT NOT NULL CHECK (path ~ '^[0-9]{2}(/[0-9]{2}){0,3}$'),

    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    is_standard BOOLEAN NOT NULL DEFAULT FALSE,
    position DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
