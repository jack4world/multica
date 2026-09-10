import { useState } from "react";
import { useAuditActions, useReviewAction } from "@multica/core/audit";
import type { AuditAction } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../i18n";
import { refusalCode, refusalFallback } from "./refusal-copy";

/**
 * The audit actions on one issue — a workpaper in the review chain, or a
 * 整改事项 on the ledger.
 *
 * The buttons come from the SERVER. This component never decides that
 * 一级复核 is followed by 二级复核, that a preparer may not review their own
 * work, or that a fix is verified by someone else — a client that knew those
 * things would be a second copy of a control, and two copies drift. It renders
 * what it is offered; a button that is not here is a step this person cannot
 * take.
 *
 * One component for both chains because the shape is identical: a list of
 * offered steps, some of which need free text first. Which chain an issue is on
 * is the server's business, and asking it twice would be two ways to be wrong.
 */
export function ReviewActions({ wsId, issueId }: { wsId: string; issueId: string }) {
  const { t } = useT("issues");
  const { data: actions = [], isPending } = useAuditActions(wsId, issueId);
  const reviewAction = useReviewAction(wsId);
  // The action waiting on its note, or null. Held as the action rather than a
  // boolean: at 待验证 a verifier is offered TWO steps that need one — closing
  // and sending back — and a boolean could not tell them apart.
  const [pending, setPending] = useState<AuditAction | null>(null);
  const [note, setNote] = useState("");

  // Nothing offered is an ANSWER, not an empty state: a filed workpaper,
  // someone else's level, one's own work. Rendering a heading over no buttons
  // would read as a broken panel rather than as the rule it is.
  if (isPending || actions.length === 0) return null;

  const close = () => {
    setPending(null);
    setNote("");
  };

  const run = (action: AuditAction, text?: string) => {
    reviewAction.mutate(
      { issueId, to: action.to, reason: text },
      {
        onSuccess: close,
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
      case "verifier_required": return t(($) => $.audit.refusal.verifier_required);
      case "self_verification": return t(($) => $.audit.refusal.self_verification);
      case "not_responsible": return t(($) => $.audit.refusal.not_responsible);
      case "remediation_closed": return t(($) => $.audit.refusal.remediation_closed);
      case "note_required": return t(($) => $.audit.refusal.note_required);
      case "not_a_remediation_item": return t(($) => $.audit.refusal.not_a_remediation_item);
      case "engagement_archived": return t(($) => $.audit.refusal.engagement_archived);
      default: return refusalFallback(err) ?? t(($) => $.audit.refusal.unknown);
    }
  };

  // While one action is collecting its note, the others are hidden rather than
  // disabled: a row of dead buttons around an open form reads as a stuck
  // screen, and the way out is the cancel button right there.
  if (pending) {
    return (
      <section className="flex flex-col gap-2" aria-label={t(($) => $.audit.actions_label)}>
        <Textarea
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder={notePlaceholder(t, pending.event)}
          aria-label={noteLabel(t, pending.event)}
          rows={3}
        />
        <div className="flex gap-2">
          <Button
            size="sm"
            variant={destructiveEvent(pending.event) ? "destructive" : "default"}
            // Blocked here as well as refused by the server, so the reviewer
            // learns the requirement before pressing rather than after.
            disabled={note.trim().length === 0 || reviewAction.isPending}
            onClick={() => run(pending, note.trim())}
          >
            {actionLabel(t, pending.event)}
          </Button>
          <Button size="sm" variant="ghost" onClick={close}>
            {t(($) => $.audit.cancel_return)}
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="flex flex-wrap gap-2" aria-label={t(($) => $.audit.actions_label)}>
      {actions.map((action) => (
        <Button
          key={action.to + action.event}
          size="sm"
          variant={buttonVariant(action.event)}
          disabled={reviewAction.isPending}
          onClick={() => (action.requires_reason ? setPending(action) : run(action))}
        >
          {actionLabel(t, action.event)}
        </Button>
      ))}
    </section>
  );
}

/** The step that ends the chain gets the emphasis; a send-back gets the warning. */
function buttonVariant(event: string): "default" | "secondary" | "outline" {
  if (event === "workpaper_filed" || event === "remediation_verified") return "default";
  if (destructiveEvent(event)) return "outline";
  return "secondary";
}

function destructiveEvent(event: string): boolean {
  return (
    event === "workpaper_review_rejected" ||
    event === "workpaper_cancelled" ||
    event === "remediation_rejected" ||
    event === "remediation_cancelled"
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
    case "remediation_started": return t(($) => $.audit.action.remediation_started);
    case "remediation_submitted": return t(($) => $.audit.action.remediation_submitted);
    case "remediation_verified": return t(($) => $.audit.action.remediation_verified);
    case "remediation_rejected": return t(($) => $.audit.action.remediation_rejected);
    case "remediation_cancelled": return t(($) => $.audit.action.remediation_cancelled);
    default: return t(($) => $.audit.action.fallback);
  }
}

/**
 * What the free text IS depends on the step: a reviewer's reason for returning
 * a workpaper, the account of what was fixed, or the record of what was
 * checked. One box with one label for all three would ask the wrong question
 * twice out of three times.
 */
function noteLabel(t: ReturnType<typeof useT<"issues">>["t"], event: string): string {
  switch (event) {
    case "remediation_submitted": return t(($) => $.audit.note.submitted_label);
    case "remediation_verified": return t(($) => $.audit.note.verified_label);
    case "remediation_rejected": return t(($) => $.audit.note.rejected_label);
    default: return t(($) => $.audit.reason_label);
  }
}

function notePlaceholder(t: ReturnType<typeof useT<"issues">>["t"], event: string): string {
  switch (event) {
    case "remediation_submitted": return t(($) => $.audit.note.submitted_placeholder);
    case "remediation_verified": return t(($) => $.audit.note.verified_placeholder);
    case "remediation_rejected": return t(($) => $.audit.note.rejected_placeholder);
    default: return t(($) => $.audit.reason_placeholder);
  }
}
