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
