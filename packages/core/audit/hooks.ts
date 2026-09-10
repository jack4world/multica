import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "../issues/queries";
import { projectKeys } from "../projects/queries";
import type {
  RaiseRemediationRequest,
  RemediationLedgerFilters,
  UpdateAuditReportRequest,
} from "../types";
import {
  auditActionsOptions,
  auditDepartmentsOptions,
  auditKeys,
  auditCategoriesOptions,
  auditDocumentsOptions,
  auditReportOptions,
  auditReportsOptions,
  auditModeOptions,
  remediationLedgerOptions,
  reviewQueueOptions,
} from "./queries";

/** Whether this workspace is an auditee. Gates every audit surface. */
export function useAuditMode(wsId: string) {
  return useQuery({ ...auditModeOptions(wsId), enabled: Boolean(wsId) });
}

/** The workpapers waiting on the viewer's rank. */
export function useReviewQueue(wsId: string, enabled = true) {
  return useQuery({ ...reviewQueueOptions(wsId), enabled: Boolean(wsId) && enabled });
}

/**
 * What the viewer may do with one workpaper.
 *
 * Fetched rather than computed. The buttons a reviewer sees come from the same
 * matrix that enforces the chain, so the interface cannot offer a step the
 * server would refuse.
 */
export function useAuditActions(wsId: string, issueId: string, enabled = true) {
  return useQuery({
    ...auditActionsOptions(wsId, issueId),
    enabled: Boolean(wsId) && Boolean(issueId) && enabled,
  });
}

/**
 * Take one review action.
 *
 * Not optimistic. A review decision is the moment a control is exercised, and
 * showing it as done before the server has agreed would show the one thing the
 * user must not be misled about. The platform's optimistic status patch is for
 * ordinary work; this is not that.
 */
export function useReviewAction(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { issueId: string; to: string; reason?: string }) => {
      return api.updateIssue(input.issueId, {
        status: input.to,
        ...(input.reason ? { review_note: input.reason } : {}),
      });
    },
    onSettled: (_data, _error, input) => {
      // The workpaper leaves the queue, its available actions change, and its
      // timeline gains an entry. Invalidate rather than patch: what the next
      // actions are is the server's answer, not something to guess at.
      void queryClient.invalidateQueries({ queryKey: auditKeys.reviewQueue(wsId) });
      void queryClient.invalidateQueries({ queryKey: auditKeys.actions(wsId, input.issueId) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.detail(wsId, input.issueId) });
    },
  });
}

/** The auditee's 责任部门 list. */
export function useAuditDepartments(wsId: string, enabled = true) {
  return useQuery({ ...auditDepartmentsOptions(wsId), enabled: Boolean(wsId) && enabled });
}

/** The 整改台账, filtered. */
export function useRemediationLedger(
  wsId: string,
  filters: RemediationLedgerFilters = {},
  enabled = true,
) {
  return useQuery({ ...remediationLedgerOptions(wsId, filters), enabled: Boolean(wsId) && enabled });
}

/**
 * Add a department to the auditee's list.
 *
 * Not optimistic: the server refuses a name that already exists
 * case-insensitively, and showing the row before it has agreed would show two
 * departments where there is one — which is the exact failure the closed list
 * exists to prevent.
 */
export function useCreateAuditDepartment(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.createAuditDepartment(name),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.departments(wsId) });
    },
  });
}

/** Remove a department that owes nothing. */
export function useDeleteAuditDepartment(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteAuditDepartment(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.departments(wsId) });
    },
  });
}

/** Raise a 整改事项 from the engagement that found the problem. */
export function useRaiseRemediation(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { projectId: string; data: RaiseRemediationRequest }) =>
      api.raiseRemediation(input.projectId, input.data),
    onSuccess: () => {
      // Every filtered view of the ledger may now be wrong, and so is the
      // department's item count.
      void queryClient.invalidateQueries({ queryKey: auditKeys.all(wsId) });
    },
  });
}

/** Move a mis-routed item to the department that owns the fix. */
export function useReassignRemediation(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { issueId: string; departmentId: string }) =>
      api.updateRemediationDepartment(input.issueId, input.departmentId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.all(wsId) });
    },
  });
}

/** An engagement's reports, newest version first. */
export function useAuditReports(wsId: string, projectId: string, enabled = true) {
  return useQuery({
    ...auditReportsOptions(wsId, projectId),
    enabled: Boolean(wsId) && Boolean(projectId) && enabled,
  });
}

/** One report. */
export function useAuditReport(wsId: string, reportId: string, enabled = true) {
  return useQuery({
    ...auditReportOptions(wsId, reportId),
    enabled: Boolean(wsId) && Boolean(reportId) && enabled,
  });
}

/** Start the engagement's next report. */
export function useCreateAuditReport(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { projectId: string; title?: string }) =>
      api.createAuditReport(input.projectId, input.title),
    onSuccess: (_report, input) => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.reports(wsId, input.projectId) });
    },
  });
}

/**
 * Write an unsigned report, or move it through its chain.
 *
 * Not optimistic, and the reason is sharper here than elsewhere: the write that
 * matters is 签发, and showing a report as issued before the server has agreed
 * would show the one state a reader acts on — sending it out — for a document
 * that may have been refused.
 */
export function useUpdateAuditReport(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { reportId: string; data: UpdateAuditReportRequest }) =>
      api.updateAuditReport(input.reportId, input.data),
    onSuccess: (report, input) => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.report(wsId, input.reportId) });
      void queryClient.invalidateQueries({ queryKey: auditKeys.reports(wsId, report.project_id) });
    },
  });
}

/**
 * Close the engagement's file.
 *
 * Invalidates the whole audit namespace and the project: archiving changes what
 * the engagement will accept from then on, and a stale "open" elsewhere in the
 * interface would offer work the server now refuses.
 */
export function useArchiveEngagement(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (projectId: string) => api.archiveEngagement(projectId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.all(wsId) });
      void queryClient.invalidateQueries({ queryKey: projectKeys.all(wsId) });
    },
  });
}

/** The auditee's filing scheme, in filing order. */
export function useAuditCategories(wsId: string, enabled = true) {
  return useQuery({ ...auditCategoriesOptions(wsId), enabled: Boolean(wsId) && enabled });
}

/** What is filed under one drawer, at any depth beneath it. */
export function useAuditDocuments(wsId: string, categoryPath: string, enabled = true) {
  return useQuery({
    ...auditDocumentsOptions(wsId, categoryPath),
    enabled: Boolean(wsId) && Boolean(categoryPath) && enabled,
  });
}

/** Add a drawer this auditee needs. */
export function useCreateAuditCategory(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { path: string; name: string }) =>
      api.createAuditCategory(input.path, input.name),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.categories(wsId) });
    },
  });
}

/** Remove a drawer that holds nothing. */
export function useDeleteAuditCategory(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (path: string) => api.deleteAuditCategory(path),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.categories(wsId) });
    },
  });
}

/**
 * File material in a drawer.
 *
 * Two steps, because the bytes go through the platform's own upload path: the
 * file becomes an attachment, and this records where it is filed. A second
 * storage path for audit material would be a second thing to secure.
 */
export function useFileAuditDocument(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: { file: File; categoryPath: string; title?: string }) => {
      const attachment = await api.uploadFile(input.file);
      return api.fileAuditDocument({
        attachment_id: attachment.id,
        category_path: input.categoryPath,
        title: (input.title ?? "").trim() || input.file.name,
      });
    },
    onSuccess: (_doc, input) => {
      // Every ancestor drawer also lists this document, so the whole document
      // namespace goes rather than one key.
      void queryClient.invalidateQueries({ queryKey: auditKeys.all(wsId) });
      void queryClient.invalidateQueries({ queryKey: auditKeys.documents(wsId, input.categoryPath) });
    },
  });
}

/**
 * Withdraw filed material, with the reason that goes into the trail.
 *
 * People only, owner/admin only, and never silent: 审计资料 is the evidence,
 * and taking a piece of it out of the file is itself an auditable act.
 */
export function useWithdrawAuditDocument(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { id: string; reason: string }) =>
      api.withdrawAuditDocument(input.id, input.reason),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: auditKeys.all(wsId) });
    },
  });
}
