/**
 * The audit review interface's wire types.
 *
 * The server answers what a viewer may do with a workpaper; the client renders
 * buttons from that answer and never computes the review chain itself. A client
 * that knew "after 一级复核 comes 二级复核" would be a second copy of a control,
 * and two copies of a control drift.
 */

import type { Issue } from "./issue";

/** One thing the viewer may do with a workpaper, as the server offers it. */
export interface AuditAction {
  /** What the trail will record. Also what the client keys its label off. */
  event: string;
  /** The status to send through the ordinary issue update. */
  to: string;
  /** Collect a reason BEFORE sending, so the common case never hits a refusal. */
  requires_reason: boolean;
}

/** One workpaper waiting on the viewer's rank. */
export interface ReviewQueueItem {
  issue: Issue;
  /** The rank held on THAT engagement — it can differ between engagements. */
  level: string;
  waiting_since: string;
}

export interface AuditMode {
  enabled: boolean;
  enabled_at: string | null;
}

/** One 责任部门 on the auditee's list. */
export interface AuditDepartment {
  id: string;
  name: string;
  /** How many ledger items this department owns, open or closed. */
  item_count: number;
}

/**
 * One row of the 整改台账.
 *
 * `overdue` and `days_late` are the SERVER's answer, computed from the deadline
 * at read time. The client must not recompute them: a browser's clock, a
 * timezone and a stale cache are three ways to disagree with the ledger about
 * who is late.
 */
export interface RemediationItem {
  issue_id: string;
  title: string;
  status: string;
  due_date?: string;
  overdue: boolean;
  days_late?: number;
  assignee_id?: string;
  department_id: string;
  department_name: string;
  source_project_id: string;
  source_project_title: string;
  source_issue_id?: string;
  verified_by?: string;
  verified_at?: string;
  verification_note?: string;
  created_at: string;
}

/** What raising a 整改事项 from an engagement needs. */
export interface RaiseRemediationRequest {
  title: string;
  description?: string;
  department_id: string;
  /** Required: an item with no deadline is an item nobody chases. */
  due_date: string;
  assignee_id?: string;
  source_issue_id?: string;
}

/** Filters the ledger read accepts. All optional, all AND-ed. */
export interface RemediationLedgerFilters {
  department_id?: string;
  source_project_id?: string;
  assignee_id?: string;
  status?: string;
  overdue?: boolean;
}
