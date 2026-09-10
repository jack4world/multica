-- At most one UNSIGNED report per engagement. Two people drafting two reports
-- that nobody reconciles is how an audit issues the wrong one; a correction is
-- made by issuing this one and starting the next.
CREATE UNIQUE INDEX CONCURRENTLY audit_report_open_uidx
    ON audit_report (project_id)
    WHERE status <> 'issued';
