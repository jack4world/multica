import { useAuditMode, useReviewQueue } from "@multica/core/audit";
import { useWorkspaceId } from "@multica/core/hooks";
import { AppLink } from "../navigation/app-link";
import { useT } from "../i18n";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { AskAgentMenu } from "./ask-agent-menu";
import { reviewLevelLabel } from "./level-label";

/**
 * The workpapers waiting on this reviewer.
 *
 * A destination rather than a saved filter. "Waiting on me" is a different kind
 * of thing from a filter over all issues, and the reason this page exists is
 * that a reviewer previously had no way to learn there was work at all — the
 * workpaper stays owned by its preparer while it is being reviewed, so it
 * appears in no other list.
 *
 * The rank shown per row is the rank held on THAT engagement: the same person
 * can be 主审 on one and 项目经理 on another, and the server pairs each
 * engagement with the rank held there rather than returning the cross product.
 */
export function ReviewQueuePage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const slug = useWorkspaceSlug() ?? "";
  const { data: auditMode } = useAuditMode(wsId);
  const enabled = auditMode?.enabled === true;
  const { data: items = [], isPending } = useReviewQueue(wsId, enabled);

  if (!enabled) return null;

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-title font-semibold">{t(($) => $.audit.queue_title)}</h1>
          <p className="text-body text-muted-foreground">{t(($) => $.audit.queue_subtitle)}</p>
        </div>
        <AskAgentMenu
          prompts={[
            { id: "what_to_check", question: t(($) => $.audit.ask.queue.what_to_check) },
            { id: "return_reason", question: t(($) => $.audit.ask.queue.return_reason) },
          ]}
        />
      </header>

      {isPending ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.queue_loading)}</p>
      ) : items.length === 0 ? (
        // "Nothing waiting" and "still loading" must not look the same, or a
        // reviewer cannot tell whether they are done.
        <p className="text-body text-muted-foreground">{t(($) => $.audit.queue_empty)}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-border">
          {items.map((item) => (
            <li key={item.issue.id} className="flex items-center justify-between gap-4 py-3">
              <div className="flex min-w-0 flex-col gap-0.5">
                <AppLink
                  href={paths.workspace(slug).issueDetail(item.issue.id)}
                  className="truncate text-body font-medium hover:underline"
                >
                  {item.issue.title}
                </AppLink>
                <span className="text-caption text-muted-foreground">
                  {reviewLevelLabel(t, item.level)}
                </span>
              </div>
              <time
                className="shrink-0 text-caption text-muted-foreground tabular-nums"
                dateTime={item.waiting_since}
              >
                {item.waiting_since.slice(0, 10)}
              </time>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
