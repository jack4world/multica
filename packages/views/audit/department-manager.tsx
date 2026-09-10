import { useState } from "react";
import { useCreateAuditDepartment, useDeleteAuditDepartment } from "@multica/core/audit";
import type { AuditDepartment } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { toast } from "sonner";
import { useT } from "../i18n";
import { refusalFallback } from "./refusal-copy";

/**
 * The auditee's 责任部门 list.
 *
 * A closed list rather than free text, and the hint says why: every count in
 * every follow-up report is by department, and 财务部 beside 财务处 makes those
 * counts quietly wrong. The cost of the list is this panel; the cost of not
 * having it is a number nobody can defend.
 */
export function DepartmentManager({
  wsId,
  departments,
}: {
  wsId: string;
  departments: AuditDepartment[];
}) {
  const { t } = useT("issues");
  const [name, setName] = useState("");
  const create = useCreateAuditDepartment(wsId);
  const remove = useDeleteAuditDepartment(wsId);

  const submit = () => {
    const trimmed = name.trim();
    if (trimmed.length === 0) return;
    create.mutate(trimmed, {
      onSuccess: () => setName(""),
      onError: (err: unknown) => {
        toast.error(refusalFallback(err) ?? t(($) => $.audit.ledger.department_duplicate));
      },
    });
  };

  return (
    <section className="flex flex-col gap-3 border-t border-border pt-6">
      <div className="flex flex-col gap-1">
        <h2 className="text-body font-semibold">{t(($) => $.audit.ledger.departments_title)}</h2>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.audit.ledger.departments_hint)}
        </p>
      </div>

      <ul className="flex flex-col divide-y divide-border/60">
        {departments.map((department) => (
          <li key={department.id} className="flex items-center justify-between gap-4 py-2">
            <span className="truncate">{department.name}</span>
            <span className="flex items-center gap-3">
              <span className="text-caption text-muted-foreground tabular-nums">
                {t(($) => $.audit.ledger.department_items).replace(
                  "{count}",
                  String(department.item_count),
                )}
              </span>
              <Button
                size="sm"
                variant="ghost"
                // Disabled here AND refused by the server. A department that
                // owes items cannot go: a closed item still names the
                // department that fixed it, and a report that cannot resolve
                // the name has a hole in it.
                disabled={department.item_count > 0 || remove.isPending}
                onClick={() =>
                  remove.mutate(department.id, {
                    onError: (err: unknown) => {
                      toast.error(
                        refusalFallback(err) ?? t(($) => $.audit.ledger.department_in_use),
                      );
                    },
                  })
                }
              >
                {t(($) => $.audit.ledger.department_delete)}
              </Button>
            </span>
          </li>
        ))}
      </ul>

      <div className="flex gap-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t(($) => $.audit.ledger.department_name_placeholder)}
          aria-label={t(($) => $.audit.ledger.departments_title)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
          className="max-w-xs"
        />
        <Button size="sm" onClick={submit} disabled={name.trim().length === 0 || create.isPending}>
          {t(($) => $.audit.ledger.department_add)}
        </Button>
      </div>
    </section>
  );
}
