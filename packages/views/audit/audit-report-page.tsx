import { useEffect, useState } from "react";
import {
  useArchiveEngagement,
  useAuditReports,
  useCreateAuditReport,
  useUpdateAuditReport,
} from "@multica/core/audit";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useQuery } from "@tanstack/react-query";
import { projectDetailOptions } from "@multica/core/projects";
import type { AuditReport } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../i18n";
import { refusalCode, refusalFallback } from "./refusal-copy";

/**
 * The engagement's 审计报告.
 *
 * A document, not a task: it is written in five fixed sections, signed once,
 * and then immutable. The page is deliberately a full-width reading surface
 * rather than a panel — the report is the only thing anyone outside the audit
 * function ever reads, and it should be edited at the width it is read at.
 *
 * The findings section is NOT editable. It is the ledger — live while the
 * report is a draft, frozen at signing — and a report whose findings could be
 * typed over would be a report that disagrees with the台账 it cites.
 */
export function AuditReportPage({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  const { data: reports = [], isPending } = useAuditReports(wsId, projectId);
  const createReport = useCreateAuditReport(wsId);

  // The newest version. Older ones stay readable through their own history but
  // the page is about the report someone is working on or has just issued.
  const report = reports[0];

  if (isPending) {
    return <p className="p-6 text-body text-muted-foreground">{t(($) => $.audit.report.loading)}</p>;
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-title font-semibold">{t(($) => $.audit.report.title)}</h1>
          <p className="text-body text-muted-foreground">{t(($) => $.audit.report.subtitle)}</p>
        </div>
        {!report && (
          <Button
            size="sm"
            disabled={createReport.isPending}
            onClick={() =>
              createReport.mutate(
                { projectId },
                {
                  onError: (err: unknown) => toast.error(reportRefusal(t, err)),
                },
              )
            }
          >
            {t(($) => $.audit.report.start)}
          </Button>
        )}
      </header>

      {!report ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.report.none)}</p>
      ) : (
        <ReportBody wsId={wsId} report={report} />
      )}

      <ArchiveSection
        wsId={wsId}
        projectId={projectId}
        archivedAt={project?.audit_archived_at ?? null}
      />
    </div>
  );
}

function ReportBody({ wsId, report }: { wsId: string; report: AuditReport }) {
  const { t } = useT("issues");
  const update = useUpdateAuditReport(wsId);
  const [sendingBack, setSendingBack] = useState(false);
  const [reason, setReason] = useState("");
  const issued = report.status === "issued";

  const save = (data: Parameters<typeof update.mutate>[0]["data"]) => {
    update.mutate(
      { reportId: report.id, data },
      { onError: (err: unknown) => toast.error(reportRefusal(t, err)) },
    );
  };

  return (
    <article className="flex max-w-3xl flex-col gap-6">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={issued ? "default" : "secondary"}>{statusLabel(t, report.status)}</Badge>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.audit.report.version).replace("{version}", String(report.version))}
        </span>
        {issued && report.issued_at ? (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.audit.report.issued_at).replace("{at}", report.issued_at.slice(0, 10))}
          </span>
        ) : null}
        <span className="ml-auto flex gap-2">
          <ExportButtons reportId={report.id} />
        </span>
      </div>

      {!issued && (
        // The failure mode of an unmarked draft is that someone sends it. Said
        // on the page as well as in the rendered document.
        <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-caption text-muted-foreground">
          {t(($) => $.audit.report.draft_warning)}
        </p>
      )}

      <Section
        heading={t(($) => $.audit.report.section_background)}
        value={report.background}
        readOnly={issued}
        onCommit={(value) => save({ background: value })}
      />
      <Section
        heading={t(($) => $.audit.report.section_basis)}
        value={report.basis}
        readOnly={issued}
        onCommit={(value) => save({ basis: value })}
      />
      <Section
        heading={t(($) => $.audit.report.section_scope)}
        value={report.scope}
        readOnly={issued}
        onCommit={(value) => save({ scope: value })}
      />

      <FindingsSection report={report} />

      <Section
        heading={t(($) => $.audit.report.section_opinion)}
        value={report.opinion}
        readOnly={issued}
        onCommit={(value) => save({ opinion: value })}
      />
      <Section
        heading={t(($) => $.audit.report.section_requirements)}
        value={report.requirements}
        readOnly={issued}
        onCommit={(value) => save({ requirements: value })}
      />

      {!issued && (
        <div className="flex flex-col gap-2 border-t border-border pt-4">
          {sendingBack ? (
            <>
              <Textarea
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                aria-label={t(($) => $.audit.report.send_back_reason)}
                placeholder={t(($) => $.audit.report.send_back_placeholder)}
                rows={3}
              />
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={reason.trim().length === 0 || update.isPending}
                  onClick={() => {
                    save({ status: "drafting", reason: reason.trim() });
                    setSendingBack(false);
                    setReason("");
                  }}
                >
                  {t(($) => $.audit.report.send_back_confirm)}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setSendingBack(false)}>
                  {t(($) => $.audit.report.cancel)}
                </Button>
              </div>
            </>
          ) : (
            <div className="flex flex-wrap gap-2">
              {report.status === "drafting" && (
                <Button
                  size="sm"
                  disabled={update.isPending}
                  onClick={() => save({ status: "reviewing" })}
                >
                  {t(($) => $.audit.report.submit)}
                </Button>
              )}
              {report.status === "reviewing" && (
                <>
                  <Button
                    size="sm"
                    disabled={update.isPending}
                    onClick={() => save({ status: "issued" })}
                  >
                    {t(($) => $.audit.report.issue)}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setSendingBack(true)}>
                    {t(($) => $.audit.report.send_back)}
                  </Button>
                </>
              )}
            </div>
          )}
        </div>
      )}
    </article>
  );
}

/**
 * One section of the report.
 *
 * Committed on blur rather than on every keystroke: an audit report is written
 * in paragraphs, and a request per character would put the document's history
 * into the network log instead of into the document.
 */
function Section({
  heading,
  value,
  readOnly,
  onCommit,
}: {
  heading: string;
  value: string;
  readOnly: boolean;
  onCommit: (value: string) => void;
}) {
  const { t } = useT("issues");
  const [draft, setDraft] = useState(value);
  // A section the server changed under us — a send-back, another editor —
  // must not keep showing the local draft.
  useEffect(() => setDraft(value), [value]);

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-body font-semibold">{heading}</h2>
      {readOnly ? (
        <p className="whitespace-pre-wrap text-body">
          {value.trim() === "" ? (
            <span className="text-muted-foreground">{t(($) => $.audit.report.placeholder)}</span>
          ) : (
            value
          )}
        </p>
      ) : (
        <Textarea
          value={draft}
          aria-label={heading}
          rows={4}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            if (draft !== value) onCommit(draft);
          }}
        />
      )}
    </section>
  );
}

/** The ledger, read-only, with a line saying which of the two it is. */
function FindingsSection({ report }: { report: AuditReport }) {
  const { t } = useT("issues");
  const issued = report.status === "issued";
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-body font-semibold">{t(($) => $.audit.report.section_findings)}</h2>
      <p className="text-caption text-muted-foreground">
        {issued
          ? t(($) => $.audit.report.findings_snapshot)
          : t(($) => $.audit.report.findings_live)}
      </p>
      {report.findings.length === 0 ? (
        <p className="text-body text-muted-foreground">
          {t(($) => $.audit.report.findings_empty)}
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[36rem] border-collapse text-body">
            <thead>
              <tr className="border-b border-border text-caption text-muted-foreground">
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.report.col_item)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.report.col_department)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.report.col_due)}</th>
                <th className="py-2 text-left font-normal">{t(($) => $.audit.report.col_status)}</th>
              </tr>
            </thead>
            <tbody>
              {report.findings.map((finding) => (
                <tr key={finding.issue_id} className="border-b border-border/60">
                  <td className="py-2 pr-4">{finding.title}</td>
                  <td className="py-2 pr-4">{finding.department}</td>
                  <td className="py-2 pr-4 tabular-nums">{finding.due_date ?? ""}</td>
                  <td className="py-2">{finding.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="text-caption text-muted-foreground tabular-nums">
        {t(($) => $.audit.report.workpapers)
          .replace("{total}", String(report.workpaper_count))
          .replace("{filed}", String(report.filed_workpaper_count))}
      </p>
    </section>
  );
}

/**
 * Export copies the rendered document to the clipboard rather than downloading
 * it: what people do with an audit report is paste it into the document their
 * organization actually sends, and a file in the downloads folder is one more
 * step between here and there.
 */
function ExportButtons({ reportId }: { reportId: string }) {
  const { t } = useT("issues");
  const [busy, setBusy] = useState(false);

  const copy = async (format: "markdown" | "html") => {
    setBusy(true);
    try {
      const body = await api.exportAuditReport(reportId, format);
      await navigator.clipboard.writeText(body);
      toast.success(t(($) => $.audit.report.export_copied));
    } catch {
      toast.error(t(($) => $.audit.report.export_failed));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => void copy("markdown")}>
        {t(($) => $.audit.report.export)}
      </Button>
      <Button size="sm" variant="ghost" disabled={busy} onClick={() => void copy("html")}>
        {t(($) => $.audit.report.export_html)}
      </Button>
    </>
  );
}

/**
 * Closing the engagement's file.
 *
 * Every precondition is the server's to enforce and this only says what it
 * said: the button is offered, and a refusal explains which precondition is not
 * met. Predicting them here would be a second copy of the rule, and the one
 * that matters most — whether an archive destination is configured at all — is
 * deployment state the client cannot see.
 */
function ArchiveSection({
  wsId,
  projectId,
  archivedAt,
}: {
  wsId: string;
  projectId: string;
  archivedAt: string | null;
}) {
  const { t } = useT("issues");
  const archive = useArchiveEngagement(wsId);

  return (
    <section className="flex flex-col gap-2 border-t border-border pt-6">
      <h2 className="text-body font-semibold">{t(($) => $.audit.report.archive_title)}</h2>
      {archivedAt ? (
        <p className="text-body text-muted-foreground">
          {t(($) => $.audit.report.archived).replace("{at}", archivedAt.slice(0, 10))}
        </p>
      ) : (
        <>
          <p className="text-caption text-muted-foreground">
            {t(($) => $.audit.report.archive_hint)}
          </p>
          <Button
            size="sm"
            variant="secondary"
            className="w-fit"
            disabled={archive.isPending}
            onClick={() =>
              archive.mutate(projectId, {
                onSuccess: (result) => {
                  toast.success(
                    t(($) => $.audit.report.archive_done)
                      .replace("{workpapers}", String(result.workpaper_count))
                      .replace("{trail}", String(result.trail_count)),
                  );
                },
                onError: (err: unknown) => toast.error(reportRefusal(t, err)),
              })
            }
          >
            {t(($) => $.audit.report.archive)}
          </Button>
        </>
      )}
    </section>
  );
}

function statusLabel(t: ReturnType<typeof useT<"issues">>["t"], status: string): string {
  switch (status) {
    case "drafting": return t(($) => $.audit.report.status_drafting);
    case "reviewing": return t(($) => $.audit.report.status_reviewing);
    case "issued": return t(($) => $.audit.report.status_issued);
    default: return status;
  }
}

/**
 * A refusal in the reader's language, falling back to the server's own sentence
 * for a code this client does not know yet.
 */
function reportRefusal(t: ReturnType<typeof useT<"issues">>["t"], err: unknown): string {
  switch (refusalCode(err)) {
    case "rank_required": return t(($) => $.audit.report.refusal_rank_required);
    case "report_issued": return t(($) => $.audit.report.refusal_report_issued);
    case "report_required": return t(($) => $.audit.report.refusal_report_required);
    case "workpapers_unfinished": return t(($) => $.audit.report.refusal_workpapers_unfinished);
    case "archive_not_configured": return t(($) => $.audit.report.refusal_archive_not_configured);
    case "engagement_archived": return t(($) => $.audit.refusal.engagement_archived);
    case "reason_required": return t(($) => $.audit.refusal.reason_required);
    case "agent_not_permitted": return t(($) => $.audit.refusal.agent_not_permitted);
    default: return refusalFallback(err) ?? t(($) => $.audit.report.save_failed);
  }
}
