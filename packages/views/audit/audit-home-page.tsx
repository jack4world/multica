"use client";

import { useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Sparkles } from "lucide-react";
import {
  useAuditMode,
  useRemediationLedger,
  useReviewQueue,
} from "@multica/core/audit";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { projectListOptions } from "@multica/core/projects/queries";
import type { Project } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../navigation/app-link";
import { PAGE_GUTTER, PAGE_RAIL } from "../layout/page-header";
import { useT } from "../i18n";
import { useAskAgent } from "./ask-agent";
import { AskAgentMenu } from "./ask-agent-menu";

/** The remediation status the ledger considers finished (server catalog key). */
const REMEDIATION_CLOSED = "remediation_closed";

/**
 * The 审计台: the page an auditor opens the workspace on.
 *
 * It answers one question — what is waiting on me, and where do I go to do it
 * — with a handful of large cards that each carry one number and one button.
 * An auditor who has never seen the product should be able to read this page
 * and act on it without learning the navigation; the rest of the sidebar is
 * for the day they want to go somewhere on their own.
 *
 * Every number here is the server's, read through the same queries the pages
 * behind the buttons use, so the count on a card is the count on the page it
 * opens. The page computes nothing the server did not already say.
 */
export function AuditHomePage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const slug = useWorkspaceSlug() ?? "";
  const p = paths.workspace(slug);
  const user = useAuthStore((s) => s.user);
  const { data: auditMode } = useAuditMode(wsId);
  const enabled = auditMode?.enabled === true;
  const ask = useAskAgent();

  const { data: queue = [], isPending: queuePending } = useReviewQueue(wsId, enabled);
  const mineFilters = useMemo(
    () => (user ? { assignee_id: user.id } : {}),
    [user],
  );
  const { data: mine = [], isPending: minePending } = useRemediationLedger(
    wsId,
    mineFilters,
    enabled && !!user,
  );
  const { data: overdue = [], isPending: overduePending } = useRemediationLedger(
    wsId,
    { overdue: true },
    enabled,
  );
  const { data: projects = [], isPending: projectsPending } = useQuery({
    ...projectListOptions(wsId),
    enabled,
  });

  if (!enabled) return null;

  const mineOpen = mine.filter((item) => item.status !== REMEDIATION_CLOSED);
  const mineOverdue = mineOpen.filter((item) => item.overdue === true);
  const engagements = projects.filter((project) => !project.audit_archived_at);
  const name = user?.name?.trim() ?? "";

  return (
    <div className={cn("flex flex-col gap-8 py-6", PAGE_GUTTER, PAGE_RAIL, "max-w-[960px]")}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <h1 className="text-title font-semibold text-balance">
          {name
            ? t(($) => $.audit.home.greeting, { name })
            : t(($) => $.audit.home.greeting_anonymous)}
        </h1>
        <AskAgentMenu
          prompts={[
            { id: "what_now", question: t(($) => $.audit.home.starters.what_now) },
            { id: "how_review", question: t(($) => $.audit.home.starters.how_review) },
            { id: "how_remediation", question: t(($) => $.audit.home.starters.how_remediation) },
          ]}
        />
      </header>

      <Section title={t(($) => $.audit.home.mine_title)}>
        <HomeCard
          title={t(($) => $.audit.home.review.title)}
          count={queue.length}
          loading={queuePending}
          summary={
            queue.length > 0
              ? t(($) => $.audit.home.review.count, { count: queue.length })
              : t(($) => $.audit.home.review.empty)
          }
          href={p.reviewQueue()}
          cta={t(($) => $.audit.home.review.cta)}
          urgent={queue.length > 0}
        />
        <HomeCard
          title={t(($) => $.audit.home.remediation.title)}
          count={mineOpen.length}
          loading={minePending}
          summary={
            mineOpen.length > 0
              ? t(($) => $.audit.home.remediation.count, { count: mineOpen.length })
              : t(($) => $.audit.home.remediation.empty)
          }
          detail={
            mineOverdue.length > 0
              ? t(($) => $.audit.home.remediation.overdue, { count: mineOverdue.length })
              : undefined
          }
          href={p.remediation()}
          cta={t(($) => $.audit.home.remediation.cta)}
          urgent={mineOverdue.length > 0}
        />
      </Section>

      <Section title={t(($) => $.audit.home.unit_title)}>
        <HomeCard
          title={t(($) => $.audit.home.overdue.title)}
          count={overdue.length}
          loading={overduePending}
          summary={
            overdue.length > 0
              ? t(($) => $.audit.home.overdue.count, { count: overdue.length })
              : t(($) => $.audit.home.overdue.empty)
          }
          href={p.remediation()}
          cta={t(($) => $.audit.home.overdue.cta)}
          urgent={overdue.length > 0}
        />
        <HomeCard
          title={t(($) => $.audit.home.documents.title)}
          summary={t(($) => $.audit.home.documents.hint)}
          href={p.auditDocuments()}
          cta={t(($) => $.audit.home.documents.cta)}
        />
        <div className="md:col-span-2">
          <EngagementList
            projects={engagements}
            loading={projectsPending}
            slug={slug}
          />
        </div>
      </Section>

      <section className="flex flex-col gap-3 rounded-xl bg-surface-raised p-5 ring-1 ring-surface-border">
        <div className="flex items-center gap-2">
          <Sparkles className="size-4 text-muted-foreground" />
          <h2 className="text-body font-medium">{t(($) => $.audit.home.ask_title)}</h2>
        </div>
        <ul className="flex flex-col gap-2">
          {(["what_now", "how_review", "how_remediation"] as const).map((key) => {
            const question = t(($) => $.audit.home.starters[key]);
            return (
              <li key={key}>
                <button
                  type="button"
                  onClick={() => ask(question)}
                  className="flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2.5 text-left text-body hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/70"
                >
                  <span>{question}</span>
                  <ArrowRight className="size-4 shrink-0 text-muted-foreground" />
                </button>
              </li>
            );
          })}
        </ul>
        <p className="text-caption text-muted-foreground">{t(($) => $.audit.home.ask_hint)}</p>
      </section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-caption font-medium uppercase tracking-wide text-muted-foreground">
        {title}
      </h2>
      <div className="grid gap-3 md:grid-cols-2">{children}</div>
    </section>
  );
}

/**
 * One card, one number, one button. The number is large because it is the
 * answer; the button says where it is acted on. `urgent` colours the number,
 * not the card — a red card reads as an error, a red number reads as "look".
 */
function HomeCard({
  title,
  count,
  loading,
  summary,
  detail,
  href,
  cta,
  urgent = false,
}: {
  title: string;
  count?: number;
  loading?: boolean;
  summary: string;
  detail?: string;
  href: string;
  cta: string;
  urgent?: boolean;
}) {
  const { t } = useT("issues");
  return (
    <div className="flex flex-col gap-4 rounded-xl bg-surface-raised p-5 ring-1 ring-surface-border">
      <div className="flex flex-col gap-1">
        <h3 className="text-body font-medium">{title}</h3>
        {loading ? (
          <p className="text-caption text-muted-foreground">{t(($) => $.audit.home.loading)}</p>
        ) : (
          <>
            {count !== undefined ? (
              <p
                className={cn(
                  "text-[2rem] font-semibold leading-tight tabular-nums",
                  urgent ? "text-destructive" : "text-foreground",
                )}
              >
                {count}
              </p>
            ) : null}
            <p className="text-body text-muted-foreground">{summary}</p>
            {detail ? <p className="text-caption text-destructive">{detail}</p> : null}
          </>
        )}
      </div>
      <Button
        variant={urgent ? "default" : "outline"}
        size="lg"
        className="mt-auto self-start"
        render={<AppLink href={href} />}
      >
        {cta}
        <ArrowRight data-icon="inline-end" />
      </Button>
    </div>
  );
}

function EngagementList({
  projects,
  loading,
  slug,
}: {
  projects: Project[];
  loading: boolean;
  slug: string;
}) {
  const { t } = useT("issues");
  const p = paths.workspace(slug);
  return (
    <div className="flex flex-col gap-3 rounded-xl bg-surface-raised p-5 ring-1 ring-surface-border">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-body font-medium">{t(($) => $.audit.home.engagements.title)}</h3>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.audit.home.engagements.archived_hidden)}
        </span>
      </div>
      {loading ? (
        <p className="text-caption text-muted-foreground">{t(($) => $.audit.home.loading)}</p>
      ) : projects.length === 0 ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.home.engagements.empty)}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-border">
          {projects.map((project) => (
            <li key={project.id} className="flex items-center justify-between gap-3 py-2.5">
              <div className="flex min-w-0 flex-col gap-0.5">
                <AppLink
                  href={p.projectDetail(project.id)}
                  className="truncate text-body font-medium hover:underline"
                >
                  {project.title}
                </AppLink>
                <span className="text-caption text-muted-foreground">
                  {phaseLabel(t, project.audit_phase)}
                </span>
              </div>
              <Button variant="ghost" size="sm" render={<AppLink href={p.projectReport(project.id)} />}>
                {t(($) => $.audit.home.engagements.report)}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** The phase the way an auditor says it; an unknown value is named, not hidden. */
function phaseLabel(
  t: ReturnType<typeof useT<"issues">>["t"],
  phase: string | null | undefined,
): string {
  switch (phase) {
    case "preparation": return t(($) => $.audit.home.phase.preparation);
    case "fieldwork": return t(($) => $.audit.home.phase.fieldwork);
    case "reporting": return t(($) => $.audit.home.phase.reporting);
    case "follow_up": return t(($) => $.audit.home.phase.follow_up);
    default: return t(($) => $.audit.home.phase.unset);
  }
}
