-- The facts an auditee and an engagement record about themselves.
--
-- SPLIT ON PURPOSE. client_name and confidentiality describe the ENTITY under
-- audit, which outlives any single audit of it, so they sit on the workspace.
-- The period and type describe ONE audit, so they sit on the project. Putting
-- all four on the project — as the vertical's first design did — would mean
-- re-entering the client's name for every year's audit and letting the copies
-- drift apart.
--
-- confidentiality is a MARKING, not a permission. Access isolation is auditee
-- membership and nothing else (docs/adr/0001), and nothing may read this column
-- into a query that decides what a person can see. A label that also filtered
-- would be a second, drifting answer to "who can see this". It is stored,
-- displayed, and used for nothing else.
--
-- No index on any of them: they are read with the row they sit on, never
-- filtered by. Adding one would cost every workspace and project write to serve
-- a query nobody makes.
ALTER TABLE workspace ADD COLUMN client_name TEXT;
ALTER TABLE workspace ADD COLUMN confidentiality TEXT
    CHECK (confidentiality IS NULL OR confidentiality IN ('normal', 'restricted', 'secret'));

ALTER TABLE project ADD COLUMN audit_period_start DATE;
ALTER TABLE project ADD COLUMN audit_period_end DATE;
ALTER TABLE project ADD COLUMN audit_type TEXT
    CHECK (audit_type IS NULL OR audit_type IN (
        'separation_of_office', 'internal_control', 'special', 'annual', 'compliance', 'other'
    ));

-- An end before a start is a period that cannot exist, and recording one would
-- put every workpaper's evidence window in doubt. Checked at the boundary too;
-- this is the half a handler bug cannot get past.
ALTER TABLE project ADD CONSTRAINT project_audit_period_ordered
    CHECK (audit_period_start IS NULL OR audit_period_end IS NULL OR audit_period_end >= audit_period_start) NOT VALID;
ALTER TABLE project VALIDATE CONSTRAINT project_audit_period_ordered;
