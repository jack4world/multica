import { useAuditMode } from "@multica/core/audit";

/**
 * Which audit noun an issue renders under, per the vocabulary contract in
 * `apps/docs/content/docs/developers/conventions.mdx` §4 (ADR-0002).
 *
 * `issue` cannot be overridden globally: inside an engagement an issue is a
 * 工作底稿, outside every engagement it is a 整改事项 (ADR-0004 — a
 * remediation item is an issue with a ledger row and no engagement). The
 * region is decided by exactly that fact — does the issue belong to a project
 * — because in an auditee workspace every project is an engagement
 * (docs/audit/CONTEXT.md). Outside audit mode there is no region and the base
 * glossary applies.
 *
 * Pure so the matrix is tested once; the hook below only supplies the mode.
 */
export type AuditIssueRegion = "workpaper" | "remediation" | null;

export function auditIssueRegion(
  auditModeEnabled: boolean,
  projectId: string | null | undefined,
): AuditIssueRegion {
  if (!auditModeEnabled) return null;
  return projectId ? "workpaper" : "remediation";
}

export function useAuditIssueRegion(
  wsId: string,
  projectId: string | null | undefined,
): AuditIssueRegion {
  const { data: auditMode } = useAuditMode(wsId);
  return auditIssueRegion(auditMode?.enabled === true, projectId);
}
