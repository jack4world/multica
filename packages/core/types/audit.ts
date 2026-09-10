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

/** One finding a report cites, as it stood when the report was signed. */
export interface AuditReportFinding {
  title: string;
  department: string;
  due_date?: string;
  status: string;
  issue_id: string;
}

/**
 * One 审计报告.
 *
 * `findings` are the ledger as it stands while the report is a draft, and the
 * snapshot taken at signing once it is issued. The client never has to know
 * which: the server answers with whichever is true for this report's state.
 */
export interface AuditReport {
  id: string;
  project_id: string;
  version: number;
  status: string;
  title: string;
  background: string;
  basis: string;
  scope: string;
  opinion: string;
  requirements: string;
  findings: AuditReportFinding[];
  workpaper_count: number;
  filed_workpaper_count: number;
  issued_by?: string;
  issued_at?: string;
  created_at: string;
  updated_at: string;
}

/** An edit to an unsigned report. An absent key leaves that section alone. */
export interface UpdateAuditReportRequest {
  title?: string;
  background?: string;
  basis?: string;
  scope?: string;
  opinion?: string;
  requirements?: string;
  status?: string;
  reason?: string;
}

/** Where an archived engagement's file was written, and what it holds. */
export interface EngagementArchive {
  project_id: string;
  archive_key: string;
  archived_at: string;
  workpaper_count: number;
  trail_count: number;
  remediation_count: number;
  attachment_count: number;
}

/** One node of the auditee's filing scheme. */
export interface AuditCategory {
  /** The identity: "03", "03/01". Fixed-width segments, so it sorts as filed. */
  path: string;
  name: string;
  /** Marks the seeded scheme, so an interface can show what every auditee shares. */
  is_standard: boolean;
  depth: number;
  parent?: string;
}

/** One piece of filed material. The bytes live in a platform attachment. */
export interface AuditDocument {
  id: string;
  category_path: string;
  title: string;
  attachment_id: string;
  filename: string;
  /**
   * A short-lived signed download capability (~1 minute), minted by the server
   * on every read. NOT a storage URL: do not cache it, persist it, or paste it
   * anywhere — the whole point is that it stops working.
   */
  download_url: string;
  content_type: string;
  size_bytes: number;
  uploader_type: string;
  uploader_id: string;
  created_at: string;
}
