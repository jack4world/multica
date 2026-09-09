-- Backing index for audit_workpaper's primary key, attached in 463. One row per
-- issue: a workpaper has exactly one preparer at a time, and a resubmission
-- replaces it rather than appending.
CREATE UNIQUE INDEX CONCURRENTLY audit_workpaper_pkey_uidx
    ON audit_workpaper (issue_id);
