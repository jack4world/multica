import { useMemo, useState } from "react";
import {
  useAuditDepartments,
  useAuditMode,
  useReassignRemediation,
  useRemediationLedger,
} from "@multica/core/audit";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueStatuses } from "@multica/core/issue-statuses";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import type { RemediationItem } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { AppLink } from "../navigation/app-link";
import { useT } from "../i18n";
import { AskAgentMenu } from "./ask-agent-menu";
import { DepartmentManager } from "./department-manager";
import { RaiseRemediationForm } from "./raise-remediation-form";

/**
 * The 整改台账.
 *
 * The three questions this page exists to answer, in the order an internal
 * audit function asks them: what is late, whose it is, and what closed the ones
 * that are closed. Everything else on the page — filters, the department list,
 * raising an item — is in service of those.
 *
 * Lateness comes from the SERVER, on every row. A browser's clock, its timezone
 * and a stale cache are three ways to disagree with the ledger about who owes
 * what, and the ledger is the thing people are held to.
 */
export function RemediationLedgerPage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const slug = useWorkspaceSlug() ?? "";
  const { data: auditMode } = useAuditMode(wsId);
  const enabled = auditMode?.enabled === true;

  const [overdueOnly, setOverdueOnly] = useState(false);
  const [departmentId, setDepartmentId] = useState("");

  const filters = useMemo(
    () => ({
      ...(overdueOnly ? { overdue: true } : {}),
      ...(departmentId ? { department_id: departmentId } : {}),
    }),
    [overdueOnly, departmentId],
  );

  const { data: departments = [] } = useAuditDepartments(wsId, enabled);
  const { data: items = [], isPending } = useRemediationLedger(wsId, filters, enabled);

  if (!enabled) return null;

  const departmentItems = [
    { value: "", label: t(($) => $.audit.ledger.filter_department_all) },
    ...departments.map((d) => ({ value: d.id, label: d.name })),
  ];
  const filtering = overdueOnly || departmentId !== "";

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-title font-semibold">{t(($) => $.audit.ledger.title)}</h1>
          <p className="text-body text-muted-foreground">{t(($) => $.audit.ledger.subtitle)}</p>
        </div>
        <AskAgentMenu
          prompts={[
            { id: "chase", question: t(($) => $.audit.ask.ledger.chase) },
            { id: "verify", question: t(($) => $.audit.ask.ledger.verify) },
          ]}
        />
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={overdueOnly ? "secondary" : "ghost"}
          onClick={() => setOverdueOnly(false)}
          data-active={!overdueOnly}
          className="data-[active=true]:font-semibold"
        >
          {t(($) => $.audit.ledger.filter_all)}
        </Button>
        <Button
          size="sm"
          variant={overdueOnly ? "default" : "ghost"}
          onClick={() => setOverdueOnly(true)}
        >
          {t(($) => $.audit.ledger.filter_overdue)}
        </Button>
        <Select
          items={departmentItems}
          value={departmentId}
          onValueChange={(value) => setDepartmentId(typeof value === "string" ? value : "")}
        >
          <SelectTrigger size="sm" aria-label={t(($) => $.audit.ledger.filter_department)}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {departmentItems.map((item) => (
              <SelectItem key={item.value || "all"} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="ml-auto">
          <RaiseRemediationForm wsId={wsId} departments={departments} />
        </div>
      </div>

      {isPending ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.ledger.loading)}</p>
      ) : items.length === 0 ? (
        // "Nothing here" and "nothing matches your filter" are different facts,
        // and a department head who filtered to their own department needs to
        // know which one they are looking at.
        <p className="text-body text-muted-foreground">
          {filtering ? t(($) => $.audit.ledger.empty_filtered) : t(($) => $.audit.ledger.empty)}
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[48rem] border-collapse text-body">
            <thead>
              <tr className="border-b border-border text-caption text-muted-foreground">
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.ledger.col_item)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.ledger.col_department)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.ledger.col_due)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.ledger.col_status)}</th>
                <th className="py-2 text-left font-normal">{t(($) => $.audit.ledger.col_source)}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <LedgerRow key={item.issue_id} item={item} slug={slug} wsId={wsId} departments={departments} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <DepartmentManager wsId={wsId} departments={departments} />
    </div>
  );
}

function LedgerRow({
  item,
  slug,
  wsId,
  departments,
}: {
  item: RemediationItem;
  slug: string;
  wsId: string;
  departments: { id: string; name: string }[];
}) {
  const { t } = useT("issues");
  // The catalog's own name, not the stored key. `remediating` is a machine
  // handle; 整改中 is what the auditee named the status, and it is editable —
  // rendering the key would show an English identifier to a reader whose whole
  // vocabulary the vertical exists to respect.
  const statuses = useIssueStatuses(wsId);
  return (
    <tr className="border-b border-border/60 align-top">
      <td className="max-w-[22rem] py-3 pr-4">
        <AppLink
          href={paths.workspace(slug).issueDetail(item.issue_id)}
          className="font-medium hover:underline"
        >
          {item.title}
        </AppLink>
        {item.verification_note ? (
          <p className="mt-1 line-clamp-2 text-caption text-muted-foreground">
            {item.verification_note}
          </p>
        ) : null}
      </td>
      <td className="py-3 pr-4">
        <ReassignDepartment wsId={wsId} item={item} departments={departments} />
      </td>
      <td className="py-3 pr-4 tabular-nums">
        {item.due_date ? (
          <span className="flex flex-col gap-1">
            <time dateTime={item.due_date}>{item.due_date}</time>
            {item.overdue === true ? (
              // The one thing this page is scanned for. Carried on a badge as
              // well as in colour, so it survives being printed and read by
              // someone who cannot tell the two reds apart.
              <Badge variant="destructive" className="w-fit">
                {t(($) => $.audit.ledger.days_late).replace("{days}", String(item.days_late ?? 0))}
              </Badge>
            ) : null}
          </span>
        ) : (
          <span className="text-muted-foreground">{t(($) => $.audit.ledger.no_due_date)}</span>
        )}
      </td>
      <td className="py-3 pr-4">
        <span>{statuses.labelOf(item.status)}</span>
      </td>
      <td className="py-3 text-muted-foreground">{item.source_project_title}</td>
    </tr>
  );
}

/**
 * Correcting a mis-routed item in place. It keeps its history, its deadline and
 * its place in the ledger; recreating it would lose all three.
 */
function ReassignDepartment({
  wsId,
  item,
  departments,
}: {
  wsId: string;
  item: RemediationItem;
  departments: { id: string; name: string }[];
}) {
  const { t } = useT("issues");
  const items = departments.map((d) => ({ value: d.id, label: d.name }));
  const { mutate, isPending } = useReassignRemediation(wsId);

  if (departments.length === 0) return <span>{item.department_name}</span>;

  return (
    <Select
      items={items}
      value={item.department_id}
      disabled={isPending}
      onValueChange={(value) => {
        if (typeof value !== "string" || value === item.department_id) return;
        mutate({ issueId: item.issue_id, departmentId: value });
      }}
    >
      <SelectTrigger size="sm" aria-label={t(($) => $.audit.ledger.reassign)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
