import { useState } from "react";
import { useAuditActions, useReviewAction } from "@multica/core/audit";
import type { AuditAction } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../i18n";
import { refusalCode, refusalFallback } from "./refusal-copy";

/**
 * The review actions on one workpaper.
 *
 * The buttons come from the SERVER. This component never decides that
 * 一级复核 is followed by 二级复核, or that a preparer may not review their own
 * work — a client that knew those things would be a second copy of a control,
 * and two copies drift. It renders what it is offered; a button that is not
 * here is a step this person cannot take.
 */
export function ReviewActions({ wsId, issueId }: { wsId: string; issueId: string }) {
  const { t } = useT("issues");
  const { data: actions = [], isPending } = useAuditActions(wsId, issueId);
  const reviewAction = useReviewAction(wsId);
  const [returning, setReturning] = useState(false);
  const [reason, setReason] = useState("");

  // Nothing offered is an ANSWER, not an empty state: a filed workpaper,
  // someone else's level, one's own work. Rendering a heading over no buttons
  // would read as a broken panel rather than as the rule it is.
  if (isPending || actions.length === 0) return null;

  const run = (action: AuditAction, note?: string) => {
    reviewAction.mutate(
      { issueId, to: action.to, reason: note },
      {
        onSuccess: () => {
          setReturning(false);
          setReason("");
        },
        onError: (err: unknown) => {
          toast.error(refusalMessage(err));
        },
      },
    );
  };

  // Localized copy for a refusal, falling back to the server's own sentence
  // for a code this client does not know yet — degrading to something true
  // beats degrading to silence.
  const refusalMessage = (err: unknown): string => {
    switch (refusalCode(err)) {
      case "level_required": return t(($) => $.audit.refusal.level_required);
      case "self_review": return t(($) => $.audit.refusal.self_review);
      case "filed": return t(($) => $.audit.refusal.filed);
      case "agent_not_permitted": return t(($) => $.audit.refusal.agent_not_permitted);
      case "illegal_transition": return t(($) => $.audit.refusal.illegal_transition);
      case "leaves_chain": return t(($) => $.audit.refusal.leaves_chain);
      case "reason_required": return t(($) => $.audit.refusal.reason_required);
      default: return refusalFallback(err) ?? t(($) => $.audit.refusal.unknown);
    }
  };

  const returnAction = actions.find((a) => a.requires_reason);
  const stepActions = actions.filter((a) => !a.requires_reason);

  return (
    <section className="flex flex-col gap-3" aria-label={t(($) => $.audit.actions_label)}>
      <div className="flex flex-wrap gap-2">
        {stepActions.map((action) => (
          <Button
            key={action.to}
            size="sm"
            variant={action.event === "workpaper_filed" ? "default" : "secondary"}
            disabled={reviewAction.isPending}
            onClick={() => run(action)}
          >
            {actionLabel(t, action.event)}
          </Button>
        ))}
        {returnAction && !returning && (
          <Button
            size="sm"
            variant="outline"
            disabled={reviewAction.isPending}
            onClick={() => setReturning(true)}
          >
            {t(($) => $.audit.action.workpaper_review_rejected)}
          </Button>
        )}
      </div>

      {returnAction && returning && (
        <div className="flex flex-col gap-2">
          <Textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t(($) => $.audit.reason_placeholder)}
            aria-label={t(($) => $.audit.reason_label)}
            rows={3}
          />
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="destructive"
              // Blocked here as well as refused by the server, so the reviewer
              // learns the requirement before pressing rather than after.
              disabled={reason.trim().length === 0 || reviewAction.isPending}
              onClick={() => run(returnAction, reason.trim())}
            >
              {t(($) => $.audit.confirm_return)}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setReturning(false);
                setReason("");
              }}
            >
              {t(($) => $.audit.cancel_return)}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}

/**
 * The label for one offered action. A switch rather than an index, so a new
 * event on the server is a compile-time prompt to write copy for it instead of
 * a silently missing string.
 */
function actionLabel(t: ReturnType<typeof useT<"issues">>["t"], event: string): string {
  switch (event) {
    case "workpaper_submitted": return t(($) => $.audit.action.workpaper_submitted);
    case "workpaper_review_passed": return t(($) => $.audit.action.workpaper_review_passed);
    case "workpaper_review_rejected": return t(($) => $.audit.action.workpaper_review_rejected);
    case "workpaper_filed": return t(($) => $.audit.action.workpaper_filed);
    case "workpaper_cancelled": return t(($) => $.audit.action.workpaper_cancelled);
    default: return t(($) => $.audit.action.fallback);
  }
}
