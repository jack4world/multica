-- name: GetWorkspaceAuditMode :one
-- Row lock on the workspace: enabling is idempotent by way of this read, so two
-- concurrent enables must not both see NULL and both seed the catalog.
SELECT audit_mode_enabled_at FROM workspace
WHERE id = $1
FOR UPDATE;

-- name: EnableWorkspaceAuditMode :one
-- Only ever sets the flag from NULL. A workspace already in audit mode keeps
-- its original timestamp, so the "when did this become an audit workspace"
-- answer cannot be rewritten by re-running enable.
UPDATE workspace SET
    audit_mode_enabled_at = now(),
    updated_at = now()
WHERE id = $1
  AND audit_mode_enabled_at IS NULL
RETURNING audit_mode_enabled_at;

-- name: SeedAuditIssueStatusEntry :exec
-- MAX(position)+1 within the category, exactly as CreateIssueStatusEntry does.
-- Seeding the chain at fixed positions 1..n instead would TIE with a custom
-- status the workspace already has in that category, and the catalog's
-- tie-break would drop that unrelated status into the middle of the review
-- chain. Called in chain order, so each insert sees its predecessor.
INSERT INTO issue_status (workspace_id, key, name, description, category, color, position)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('key')::text,
    sqlc.arg('name')::text,
    sqlc.arg('description')::text,
    sqlc.arg('category')::text,
    sqlc.arg('color')::text,
    COALESCE(
        (SELECT MAX(position) + 1 FROM issue_status
         WHERE workspace_id = sqlc.arg('workspace_id')::uuid
           AND category = sqlc.arg('category')::text),
        0
    )
);

-- name: SeedAuditIssueProperty :exec
INSERT INTO issue_property (workspace_id, name, type, description, icon, config, position)
SELECT sqlc.arg('workspace_id')::uuid,
       sqlc.arg('name')::text,
       sqlc.arg('type')::text,
       sqlc.arg('description')::text,
       '',
       sqlc.arg('config')::jsonb,
       COALESCE((SELECT MAX(position) FROM issue_property WHERE workspace_id = sqlc.arg('workspace_id')::uuid), 0) + 1;

-- name: LockIssuePropertyCatalog :exec
-- The same advisory lock every property-definition write takes (see
-- withPropertyLock in internal/handler/property.go). The seed needs it for the
-- same reasons those writes do: its active-definition census and its inserts
-- must not interleave with a concurrent CreateProperty.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg('lock_key')::text, 0));

-- name: ListIssueStatusNamesInSet :many
-- Display names of ACTIVE statuses colliding with a candidate set.
-- idx_issue_status_workspace_name_active is unique on (workspace_id,
-- lower(name)) where archived_at IS NULL, so this is a second collision axis
-- independent of the key one — and the reachable one, since DeriveKey slugifies
-- a CJK name to nothing and falls back to `<category>_2`.
SELECT name FROM issue_status
WHERE workspace_id = $1
  AND archived_at IS NULL
  AND LOWER(name) = ANY(sqlc.arg('names')::text[]);

-- name: ListIssueStatusKeysInSet :many
-- Which of a candidate key set does this workspace's catalog already own?
-- Archived rows count: idx_issue_status_workspace_key is not partial, so a
-- retired status still owns its key.
SELECT key, name FROM issue_status
WHERE workspace_id = $1
  AND key = ANY(sqlc.arg('keys')::text[]);

-- name: ListIssuePropertyNamesInSet :many
-- Case-insensitive to match idx_issue_property_ws_name, which is on LOWER(name).
SELECT name FROM issue_property
WHERE workspace_id = $1
  AND LOWER(name) = ANY(sqlc.arg('names')::text[]);
