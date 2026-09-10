import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type {
  AuditAction,
  AuditDepartment,
  AuditReport,
  AuditMode,
  RemediationItem,
  RemediationLedgerFilters,
  ReviewQueueItem,
} from "../types";

/**
 * Server state for the audit review interface.
 *
 * Every key carries `wsId`, per the repo's rule for workspace-scoped queries:
 * switching workspaces does not remount the app, so a key without it would
 * serve one auditee's queue to another.
 */
export const auditKeys = {
  all: (wsId: string) => ["audit", wsId] as const,
  mode: (wsId: string) => ["audit", wsId, "mode"] as const,
  reviewQueue: (wsId: string) => ["audit", wsId, "review-queue"] as const,
  actions: (wsId: string, issueId: string) => ["audit", wsId, "actions", issueId] as const,
  departments: (wsId: string) => ["audit", wsId, "departments"] as const,
  // The filters are part of the key: two filtered ledgers are two different
  // server answers, and serving one under the other's key is how a department
  // head ends up looking at another department's list.
  ledger: (wsId: string, filters: RemediationLedgerFilters) =>
    ["audit", wsId, "remediation", filters] as const,
  reports: (wsId: string, projectId: string) => ["audit", wsId, "reports", projectId] as const,
  report: (wsId: string, reportId: string) => ["audit", wsId, "report", reportId] as const,
};

export function auditModeOptions(wsId: string) {
  return queryOptions({
    queryKey: auditKeys.mode(wsId),
    queryFn: (): Promise<AuditMode> => api.getAuditMode(),
    // Audit mode is turned on once and never off, so this is as static as
    // workspace state gets. Refetching it on every focus would cost a request
    // per navigation to learn what cannot have changed.
    staleTime: 5 * 60 * 1000,
  });
}

export function reviewQueueOptions(wsId: string) {
  return queryOptions({
    queryKey: auditKeys.reviewQueue(wsId),
    queryFn: (): Promise<ReviewQueueItem[]> => api.listReviewQueue(),
  });
}

export function auditActionsOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: auditKeys.actions(wsId, issueId),
    queryFn: (): Promise<AuditAction[]> => api.listAuditActions(issueId),
  });
}

export function auditDepartmentsOptions(wsId: string) {
  return queryOptions({
    queryKey: auditKeys.departments(wsId),
    queryFn: (): Promise<AuditDepartment[]> => api.listAuditDepartments(),
  });
}

export function remediationLedgerOptions(wsId: string, filters: RemediationLedgerFilters) {
  return queryOptions({
    queryKey: auditKeys.ledger(wsId, filters),
    queryFn: (): Promise<RemediationItem[]> => api.listRemediation(filters),
  });
}

export function auditReportsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: auditKeys.reports(wsId, projectId),
    queryFn: (): Promise<AuditReport[]> => api.listAuditReports(projectId),
  });
}

export function auditReportOptions(wsId: string, reportId: string) {
  return queryOptions({
    queryKey: auditKeys.report(wsId, reportId),
    queryFn: (): Promise<AuditReport> => api.getAuditReport(reportId),
  });
}
