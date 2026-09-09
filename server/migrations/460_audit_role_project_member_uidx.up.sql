-- One person holds AT MOST ONE level on an engagement. This is what makes
-- "nobody reviews at two levels of the same workpaper" structural rather than a
-- runtime check the gate could forget: with one level per person per
-- engagement, holding two is unrepresentable.
--
-- It is also the gate's lookup: (project_id, member_id) -> level, on the write
-- path of every governed status change.
CREATE UNIQUE INDEX CONCURRENTLY audit_role_project_member_uidx
    ON audit_role (project_id, member_id);
