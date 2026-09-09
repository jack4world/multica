-- name: IsWorkspaceAuditMode :one
-- Lean, NON-LOCKING read of the auditee flag for the review gate's write path
-- (the enable path uses GetWorkspaceAuditMode, which locks). Only reached once
-- the status keys have already proven an audit status is involved, so it costs
-- nothing on the ordinary issue write.
SELECT (audit_mode_enabled_at IS NOT NULL)::bool AS enabled
FROM workspace
WHERE id = $1;

-- name: LockIssueForReviewGate :one
-- Re-reads the issue under a row lock INSIDE the gate's transaction, so the
-- status and project the decision rests on are the ones the write lands on.
--
-- Without it the decision comes from a snapshot taken before the transaction
-- opened, and "a filed workpaper refuses every write" is only true when nobody
-- files it concurrently: a description edit could read 三级复核, be allowed,
-- then block on the row lock and commit onto a workpaper that is now filed.
SELECT * FROM issue
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: GetAuditRoleLevel :one
-- The actor's reviewer rank on ONE engagement. Unique on
-- (project_id, member_id), so there is never a second row to choose between.
SELECT level FROM audit_role
WHERE project_id = $1 AND member_id = $2;

-- name: ListAuditRolesForProject :many
SELECT id, workspace_id, project_id, member_id, level, created_at, created_by
FROM audit_role
WHERE project_id = $1
ORDER BY level ASC, created_at ASC;

-- name: SetAuditRole :one
-- Assigning a level to someone who already holds one REPLACES it rather than
-- failing: the unique constraint expresses "at most one level per person per
-- engagement", and a staffing change is the normal way that constraint is met.
INSERT INTO audit_role (workspace_id, project_id, member_id, level, created_by)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('project_id')::uuid,
    sqlc.arg('member_id')::uuid,
    sqlc.arg('level')::text,
    sqlc.narg('created_by')::uuid
)
ON CONFLICT (project_id, member_id) DO UPDATE
SET level = EXCLUDED.level,
    created_by = EXCLUDED.created_by
RETURNING *;

-- name: DeleteAuditRole :execrows
DELETE FROM audit_role
WHERE project_id = $1 AND member_id = $2;

-- name: DeleteAuditRolesForProject :exec
-- Application-layer cleanup: there are no cascades, so deleting an engagement
-- has to take its roles with it. Scoped by workspace for the same reason
-- DeleteIssue is: a bare project_id predicate would reach another tenant's rows
-- if a caller ever passed a foreign id.
DELETE FROM audit_role
WHERE project_id = sqlc.arg('project_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: GetWorkpaperPreparer :one
SELECT preparer_id FROM audit_workpaper
WHERE issue_id = $1;

-- name: RecordWorkpaperPreparer :exec
-- Written by the review gate at submission, inside the same transaction as the
-- status change, so a workpaper can never sit in review with no preparer on
-- record. A resubmission after rejection replaces the previous value.
INSERT INTO audit_workpaper (issue_id, workspace_id, preparer_id)
VALUES (
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('preparer_id')::uuid
)
ON CONFLICT (issue_id) DO UPDATE
SET preparer_id = EXCLUDED.preparer_id,
    submitted_at = now(),
    updated_at = now();

-- name: DeleteWorkpaperRecord :exec
DELETE FROM audit_workpaper WHERE issue_id = $1;

-- name: ListAuditeeWorkspaceIDs :many
-- Workspaces the daily trail export has anything to do. An ordinary deployment
-- has none, so the job costs one indexed scan and stops.
SELECT id FROM workspace
WHERE audit_mode_enabled_at IS NOT NULL
ORDER BY id ASC;

-- name: ListWorkspaceActivityForDay :many
-- One auditee's whole activity for one UTC day, oldest first.
--
-- Deliberately NOT filtered to audit-domain actions. A workpaper's story is the
-- comments, assignments and edits around its review decisions as well as the
-- decisions themselves; an export holding only the approvals would be the wrong
-- artifact to hand an auditor.
SELECT id, issue_id, actor_type, actor_id, action, details, created_at
FROM activity_log
WHERE workspace_id = $1
  AND created_at >= sqlc.arg('day_start')::timestamptz
  AND created_at < sqlc.arg('day_end')::timestamptz
ORDER BY created_at ASC, id ASC;

-- name: ListReviewQueueForMember :many
-- Workpapers waiting on THIS member's rank, across every engagement they hold
-- one on.
--
-- The join is the point. Filtering projects and statuses independently — which
-- is all the ordinary issue list can do — returns the cross product: a viewer
-- who is 主审 on one engagement and 项目经理 on another would be shown the
-- other engagement's 一级复核 workpapers too, which are not theirs to review.
-- Pairing each engagement with the rank held THERE is what makes the queue
-- correct rather than merely filtered.
--
-- The preparer is excluded here as well as refused by the gate: a workpaper
-- nobody may act on has no business sitting in their queue.
SELECT i.*, r.level::text AS reviewer_level, w.preparer_id
FROM audit_role r
JOIN issue i
  ON i.project_id = r.project_id
 AND i.status = CASE r.level
     WHEN 'reviewer_l1' THEN 'review_l1'
     WHEN 'reviewer_l2' THEN 'review_l2'
     WHEN 'reviewer_l3' THEN 'review_l3'
 END
LEFT JOIN audit_workpaper w ON w.issue_id = i.id
WHERE r.workspace_id = $1
  AND r.member_id = $2
  AND (w.preparer_id IS NULL OR w.preparer_id <> $2)
ORDER BY i.updated_at ASC, i.id ASC
LIMIT $3;
