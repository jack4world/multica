-- name: IsWorkspaceAuditMode :one
-- Lean, NON-LOCKING read of the auditee flag for the review gate's write path
-- (the enable path uses GetWorkspaceAuditMode, which locks). Only reached once
-- the status keys have already proven an audit status is involved, so it costs
-- nothing on the ordinary issue write.
SELECT (audit_mode_enabled_at IS NOT NULL)::bool AS enabled
FROM workspace
WHERE id = $1;

-- name: GetEngagementGateFacts :one
-- What the gate needs to know about the engagement: how many review levels it
-- runs, and whether its file has been closed. Read on the write path of every
-- governed transition, by primary key — one row rather than two reads, because
-- the two facts have to come from the same snapshot as each other.
SELECT review_levels, (audit_archived_at IS NOT NULL)::bool AS archived
FROM project
WHERE id = $1 AND workspace_id = $2;

-- name: CountWorkpapersAboveDepth :one
-- Workpapers sitting at a review stage the engagement would no longer have.
-- Lowering the depth under one of them would strand it: a configuration
-- correction must not become a data problem.
SELECT COUNT(*)::bigint FROM issue
WHERE project_id = $1
  AND status = ANY(sqlc.arg('beyond_statuses')::text[]);

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



-- name: SeedAuditDocumentCategory :exec
-- Idempotent, so the same seeding serves audit-mode enable and an explicit
-- action on an auditee that predates the scheme. Only the seeded NAME is
-- refreshed; a client that renamed a standard drawer keeps its own name because
-- position and any later edits are left alone.
INSERT INTO audit_document_category (workspace_id, path, name, is_standard, position)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('path')::text,
    sqlc.arg('name')::text,
    TRUE,
    sqlc.arg('position')::float8
)
ON CONFLICT (workspace_id, path) DO NOTHING;

-- name: CreateAuditDocumentCategory :one
INSERT INTO audit_document_category (workspace_id, path, name, position)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('path')::text,
    sqlc.arg('name')::text,
    sqlc.arg('position')::float8
)
RETURNING *;

-- name: ListAuditDocumentCategories :many
-- The whole tree, in filing order. Fixed-width segments are what make a lexical
-- sort the order an auditor expects.
SELECT * FROM audit_document_category
WHERE workspace_id = $1
ORDER BY path ASC;

-- name: GetAuditDocumentCategory :one
SELECT * FROM audit_document_category
WHERE workspace_id = $1 AND path = $2;

-- name: CountCategoryChildren :one
-- Sub-categories strictly below a path. The pattern carries the separator: a
-- prefix test without it counts 020 as a child of 02.
SELECT COUNT(*)::bigint FROM audit_document_category
WHERE workspace_id = $1 AND path LIKE sqlc.arg('descendant_pattern')::text;

-- name: CountDocumentsUnderCategory :one
-- This category AND everything beneath it, which is what "can I delete this"
-- has to ask: orphaning material by tidying the tree is silent.
SELECT COUNT(*)::bigint FROM audit_document
WHERE workspace_id = $1
  AND withdrawn_at IS NULL
  AND (category_path = sqlc.arg('path')::text
       OR category_path LIKE sqlc.arg('descendant_pattern')::text);

-- name: DeleteAuditDocumentCategory :execrows
DELETE FROM audit_document_category
WHERE workspace_id = $1 AND path = $2;

-- name: CreateAuditDocument :one
INSERT INTO audit_document (workspace_id, attachment_id, category_path, title, uploader_type, uploader_id)
VALUES (
    sqlc.arg('workspace_id')::uuid,
    sqlc.arg('attachment_id')::uuid,
    sqlc.arg('category_path')::text,
    sqlc.arg('title')::text,
    sqlc.arg('uploader_type')::text,
    sqlc.arg('uploader_id')::uuid
)
RETURNING *;

-- name: ListAuditDocumentsUnderCategory :many
-- Asking for a parent returns everything beneath it at any depth: an auditor
-- looking for a voucher should not have to walk the tree to find it.
SELECT d.*, a.filename, a.url, a.content_type, a.size_bytes
FROM audit_document d
JOIN attachment a ON a.id = d.attachment_id
WHERE d.workspace_id = $1
  AND d.withdrawn_at IS NULL
  AND (d.category_path = sqlc.arg('path')::text
       OR d.category_path LIKE sqlc.arg('descendant_pattern')::text)
ORDER BY d.category_path ASC, d.created_at DESC
LIMIT sqlc.arg('lim');

-- name: GetAuditDocument :one
SELECT * FROM audit_document WHERE id = $1 AND workspace_id = $2;

-- name: MoveAuditDocument :one
UPDATE audit_document
SET category_path = sqlc.arg('category_path')::text,
    title = COALESCE(sqlc.narg('title'), title),
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: WithdrawAuditDocument :one
-- Withdrawn, not deleted: the row stays so the file can say the document was
-- here and why it went. Already-withdrawn rows are left alone, so a second
-- withdrawal cannot overwrite who took it down or why.
UPDATE audit_document
SET withdrawn_at = now(),
    withdrawn_by = sqlc.arg('withdrawn_by')::uuid,
    withdrawal_reason = sqlc.arg('withdrawal_reason')::text,
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
  AND withdrawn_at IS NULL
RETURNING *;

-- name: ListWorkpapersForArchive :many
-- Every workpaper in the engagement, with the audit-only facts that make "who
-- checked this" answerable from the archived file alone.
SELECT i.id, i.number, i.title, i.status, i.properties, i.updated_at,
       w.preparer_id, w.submitted_at
FROM issue i
LEFT JOIN audit_workpaper w ON w.issue_id = i.id
WHERE i.project_id = $1
ORDER BY i.number ASC;

-- name: CountUnfinishedWorkpapers :one
-- Workpapers still in the chain. An archive taken over them would be a snapshot
-- of unfinished work presented as a closed file.
SELECT COUNT(*)::bigint FROM issue
WHERE project_id = $1
  AND status <> sqlc.arg('filed_status')::text
  AND status <> 'cancelled';

-- name: ListTrailForProject :many
-- Every trail entry for this engagement: its issues', plus the report's own,
-- which carry no issue_id because a report is not an issue.
SELECT a.id, a.issue_id, a.actor_type, a.actor_id, a.action, a.details, a.created_at
FROM activity_log a
WHERE a.workspace_id = sqlc.arg('workspace_id')::uuid
  AND (
      a.issue_id IN (SELECT id FROM issue WHERE project_id = sqlc.arg('project_id')::uuid)
      OR (a.issue_id IS NULL
          AND a.details->>'project_id' = sqlc.arg('project_id_text')::text)
  )
ORDER BY a.created_at ASC, a.id ASC;

-- name: ListAttachmentsForProject :many
-- The evidence index. The bytes stay in the attachment store; what the archive
-- records is that this file, of this size, hung on that workpaper — which is
-- what makes a later disappearance detectable.
SELECT at.id, at.issue_id, at.filename, at.content_type, at.size_bytes
FROM attachment at
WHERE at.issue_id IN (SELECT id FROM issue WHERE project_id = $1)
ORDER BY at.id ASC;

-- name: MarkEngagementArchived :one
UPDATE project
SET audit_archived_at = now(),
    audit_archive_key = sqlc.arg('archive_key')::text,
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: ListEngagementReviewerUserIDs :many
-- The USER ids of the people holding one rank on an engagement.
--
-- User ids, not member ids, because the callers address a person: an inbox
-- item's recipient_id is a user id (see ListInbox), and writing a member id
-- there produces a notification nobody can ever read.
SELECT m.user_id
FROM audit_role ar
JOIN member m ON m.id = ar.member_id
WHERE ar.project_id = $1 AND ar.level = sqlc.arg('level')::text;

-- name: ListWorkpaperSignatures :many
-- Who has already signed this workpaper, and at which level.
--
-- Read from the TRAIL, not from the seating chart: a reviewer's rank changes
-- mid-flight for ordinary reasons, and "two levels of review" is a claim about
-- two people having looked, not about two ranks having existed. actor_id is a
-- user id here, like every other row in this table.
SELECT actor_id, details->>'level' AS level
FROM activity_log
WHERE issue_id = $1
  AND action IN ('workpaper_review_passed', 'workpaper_filed')
  AND actor_id IS NOT NULL
  AND details->>'level' IS NOT NULL;

-- name: ListWorkspaceDirectory :many
-- Everyone in the auditee, with both ids and their name.
--
-- Read at archival to turn ids into people. The archive is a snapshot, not a
-- set of foreign keys: whoever opens it has no database to join against, and
-- the person may have left long before.
SELECT m.id AS member_id, m.user_id, u.name, u.email
FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1;
