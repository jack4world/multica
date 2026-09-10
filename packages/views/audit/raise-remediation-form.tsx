import { useState } from "react";
import { useRaiseRemediation } from "@multica/core/audit";
import { useQuery } from "@tanstack/react-query";
import { projectListOptions } from "@multica/core/projects";
import type { AuditDepartment } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { toast } from "sonner";
import { useT } from "../i18n";
import { refusalFallback } from "./refusal-copy";

/**
 * Raising a 整改事项.
 *
 * Three fields are required and the form says so before the server does: the
 * engagement that found the problem (it is where the verifier's rank is read
 * from — an item with no source has nobody qualified to close it), the
 * responsible department, and the deadline. An item missing any of the three is
 * the item nobody fixes, which is the failure the ledger exists to prevent.
 */
export function RaiseRemediationForm({
  wsId,
  departments,
}: {
  wsId: string;
  departments: AuditDepartment[];
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [projectId, setProjectId] = useState("");
  const [departmentId, setDepartmentId] = useState("");
  const [dueDate, setDueDate] = useState("");
  const raise = useRaiseRemediation(wsId);
  const { data: projects = [] } = useQuery({ ...projectListOptions(wsId), enabled: Boolean(wsId) && open });

  const projectItems = projects.map((p) => ({ value: p.id, label: p.title }));
  const departmentItems = departments.map((d) => ({ value: d.id, label: d.name }));
  const ready =
    title.trim().length > 0 && projectId !== "" && departmentId !== "" && dueDate !== "";

  const reset = () => {
    setTitle("");
    setProjectId("");
    setDepartmentId("");
    setDueDate("");
  };

  const submit = () => {
    if (!ready) return;
    raise.mutate(
      {
        projectId,
        data: { title: title.trim(), department_id: departmentId, due_date: dueDate },
      },
      {
        onSuccess: () => {
          setOpen(false);
          reset();
        },
        onError: (err: unknown) => {
          toast.error(refusalFallback(err) ?? t(($) => $.audit.ledger.raise_failed));
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm">{t(($) => $.audit.ledger.raise_open)}</Button>} />
      <DialogContent className="flex max-w-md flex-col gap-4">
        <DialogHeader>
          <DialogTitle>{t(($) => $.audit.ledger.raise_title)}</DialogTitle>
        </DialogHeader>

        {departments.length === 0 ? (
          // Said before the attempt, not after the refusal: there is nothing to
          // fill in until the auditee has a department list.
          <p className="text-body text-muted-foreground">
            {t(($) => $.audit.ledger.raise_needs_department)}
          </p>
        ) : (
          <div className="flex flex-col gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.ledger.raise_item_title)}
              </span>
              <Input
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder={t(($) => $.audit.ledger.raise_item_title_placeholder)}
              />
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.ledger.raise_project)}
              </span>
              <Select
                items={projectItems}
                value={projectId}
                onValueChange={(value) => setProjectId(typeof value === "string" ? value : "")}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {projectItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.ledger.raise_needs_project)}
              </span>
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.ledger.raise_department)}
              </span>
              <Select
                items={departmentItems}
                value={departmentId}
                onValueChange={(value) => setDepartmentId(typeof value === "string" ? value : "")}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {departmentItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.ledger.raise_due)}
              </span>
              <Input
                type="date"
                value={dueDate}
                onChange={(e) => setDueDate(e.target.value)}
              />
            </label>
          </div>
        )}

        <DialogFooter>
          <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>
            {t(($) => $.audit.ledger.raise_cancel)}
          </Button>
          <Button size="sm" onClick={submit} disabled={!ready || raise.isPending}>
            {t(($) => $.audit.ledger.raise_submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
