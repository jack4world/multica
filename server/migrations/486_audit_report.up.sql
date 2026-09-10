-- The engagement's deliverable.
--
-- A REPORT IS NOT AN ISSUE. A workpaper is an issue because it is a unit of
-- work with an assignee and a status; a report is a document. Making it an
-- issue would put it in the board, the backlog and the assignment model, and
-- would put the review chain in charge of something it was not designed for.
--
-- SECTIONS ARE COLUMNS, NOT A JSONB BAG. An internal audit report's structure
-- is not user-configurable: 基本情况 / 审计依据 / 审计范围 / 审计意见 / 整改要求
-- is what one has. A closed list of five is a schema, not a limitation.
--
-- THE SNAPSHOT IS THE POINT. A signed report renders from what it stored at the
-- moment it was signed, never from live rows: an item closed in March must not
-- silently rewrite a report issued in January, which is exactly the document
-- 后续审计 reads.
--
-- No primary key inline; see 487 and 488, per the repo convention.
CREATE TABLE audit_report (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,

    -- 1, 2, 3 within one engagement. A correction is a new version, never an
    -- edit of an issued one.
    version INT NOT NULL DEFAULT 1 CHECK (version >= 1),

    status TEXT NOT NULL DEFAULT 'drafting'
        CHECK (status IN ('drafting', 'reviewing', 'issued')),

    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    background TEXT NOT NULL DEFAULT '',
    basis TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT '',
    opinion TEXT NOT NULL DEFAULT '',
    requirements TEXT NOT NULL DEFAULT '',

    -- Written at signing, together with issued_by/issued_at: the items this
    -- report cited, as they stood, and how much work was behind it.
    findings_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    workpaper_count INT NOT NULL DEFAULT 0,
    filed_workpaper_count INT NOT NULL DEFAULT 0,

    created_by UUID NOT NULL,
    issued_by UUID,
    issued_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
