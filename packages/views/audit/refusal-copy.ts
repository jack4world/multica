import { ApiError } from "@multica/core/api";

/**
 * Reads the machine-readable code out of a gate refusal.
 *
 * The server sends a stable code alongside its English sentence precisely so a
 * client can translate the failure — a Chinese-locale auditor should not be
 * shown `this step needs reviewer_l1 on this engagement`.
 */
export function refusalCode(err: unknown): AuditRefusalCode | undefined {
  if (!(err instanceof ApiError)) return undefined;
  const body = err.body;
  if (body && typeof body === "object" && "code" in body) {
    const code = (body as { code?: unknown }).code;
    if (typeof code === "string" && isAuditRefusalCode(code)) return code;
  }
  return undefined;
}

/**
 * The codes the gate classifies its refusals with. Listed rather than accepted
 * as any string so a code added on the server without copy here falls through
 * to the server's own sentence instead of rendering an empty toast.
 */
export const AUDIT_REFUSAL_CODES = [
  // The review chain.
  "level_required",
  "self_review",
  "same_reviewer",
  "filed",
  "agent_not_permitted",
  "illegal_transition",
  "leaves_chain",
  "reason_required",
  // The 整改台账.
  "verifier_required",
  "self_verification",
  "not_responsible",
  "remediation_closed",
  "note_required",
  "not_a_remediation_item",
  // The 审计报告 and its archive.
  "rank_required",
  "report_issued",
  "report_required",
  "workpapers_unfinished",
  "archive_not_configured",
  // Both chains, once the engagement's file is closed.
  "engagement_archived",
] as const;

export type AuditRefusalCode = (typeof AUDIT_REFUSAL_CODES)[number];

function isAuditRefusalCode(value: string): value is AuditRefusalCode {
  return (AUDIT_REFUSAL_CODES as readonly string[]).includes(value);
}

/**
 * The server's own sentence, when it is worth showing.
 *
 * A 4xx message is written for the user and is the right fallback for a code
 * this client has no copy for yet. A 5xx message is internal detail and must
 * never reach a toast.
 */
export function refusalFallback(err: unknown): string | undefined {
  if (err instanceof ApiError && err.status >= 400 && err.status < 500 && err.message) {
    return err.message;
  }
  return undefined;
}
