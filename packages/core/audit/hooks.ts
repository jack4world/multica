import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "../issues/queries";
import { auditActionsOptions, auditKeys, auditModeOptions, reviewQueueOptions } from "./queries";

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
