import type { ReviewQueueItem } from "@multica/core/types";
import { auditIssueRegion } from "./issue-region";

/** The nav items an issue route can belong to in an auditee workspace. */
export type AuditIssueNavKey = "reviewQueue" | "remediation" | null;

/**
 * Which sidebar item an open issue belongs to in audit mode.
 *
 * The platform highlights by path prefix, so `/issues/:id` lights up "任务"
 * whatever the issue is. An auditor reading a workpaper that is waiting on
 * them came from 待我复核; one reading a remediation item came from 整改台账.
 * The two use different evidence because the two nav items mean different
 * things: 整改台账 is a region — every remediation item belongs to it, so the
 * region decides (auditIssueRegion) — while 待我复核 is a state, so only a
 * workpaper actually in the viewer's queue belongs to it. A workpaper not in
 * the queue keeps the platform's answer (null → prefix rule), because it is
 * not "waiting on me" and saying so would be a lie.
 *
 * The queue is matched on id and identifier because the route carries
 * whichever the link used.
 */
export function auditIssueNavKey(
  auditModeEnabled: boolean,
  routeIssueId: string | null,
  queue: readonly ReviewQueueItem[],
  issue: { project_id?: string | null } | undefined,
): AuditIssueNavKey {
  if (!auditModeEnabled || !routeIssueId) return null;
  if (queue.some((q) => q.issue.id === routeIssueId || q.issue.identifier === routeIssueId)) {
    return "reviewQueue";
  }
  if (issue && auditIssueRegion(true, issue.project_id) === "remediation") return "remediation";
  return null;
}
