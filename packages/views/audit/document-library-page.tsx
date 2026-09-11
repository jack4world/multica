import { useMemo, useRef, useState } from "react";
import {
  useAuditCategories,
  useAuditDocuments,
  useAuditMode,
  useCreateAuditCategory,
  useDeleteAuditCategory,
  useWithdrawAuditDocument,
  useWithdrawnAuditDocuments,
  useFileAuditDocument,
} from "@multica/core/audit";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import type { AuditCategory, AuditDocument } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { toast } from "sonner";
import { useT } from "../i18n";
import { AskAgentMenu } from "./ask-agent-menu";
import { refusalFallback } from "./refusal-copy";

/**
 * 审计资料: the auditee's own material, filed by classification number.
 *
 * The library belongs to the WORKSPACE, not to an engagement — the same
 * client's policies, contracts and vouchers span every audit of it, and filing
 * per engagement would mean copying them forward every year and letting the
 * copies drift.
 *
 * Two panes: the tree on the left, the drawer's contents on the right. Asking
 * for a parent shows everything beneath it at any depth, because an auditor
 * looking for a voucher should not have to walk the tree to find it.
 */
export function DocumentLibraryPage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const { data: auditMode } = useAuditMode(wsId);
  const enabled = auditMode?.enabled === true;
  const { data: categories = [], isPending } = useAuditCategories(wsId, enabled);
  const [selected, setSelected] = useState("");
  // The library has to be able to say what it used to hold. Withdrawal keeps
  // the row and writes the trail; without somewhere to read it, that answer
  // lives only in the trail and the product cannot make the claim.
  const [showWithdrawn, setShowWithdrawn] = useState(false);

  // The first top-level drawer, until someone picks another. A library that
  // opens on nothing makes the reader's first act a click that teaches them
  // nothing.
  const current = selected || categories[0]?.path || "";

  if (!enabled) return null;

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-title font-semibold">{t(($) => $.audit.library.title)}</h1>
          <p className="text-body text-muted-foreground">{t(($) => $.audit.library.subtitle)}</p>
        </div>
        <AskAgentMenu
          prompts={[
            { id: "filing", question: t(($) => $.audit.ask.library.filing) },
            { id: "withdraw", question: t(($) => $.audit.ask.library.withdraw) },
          ]}
        />
      </header>

      {isPending ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.library.loading)}</p>
      ) : (
        <div className="flex flex-col gap-6 lg:flex-row">
          <nav className="w-full shrink-0 lg:w-64">
            <CategoryTree
              wsId={wsId}
              categories={categories}
              current={current}
              onSelect={setSelected}
            />
          </nav>
          <div className="min-w-0 flex-1">
            {showWithdrawn ? (
              <WithdrawnMaterial wsId={wsId} onBack={() => setShowWithdrawn(false)} />
            ) : current === "" ? (
              <p className="text-body text-muted-foreground">
                {t(($) => $.audit.library.select_drawer)}
              </p>
            ) : (
              <>
                <DrawerContents wsId={wsId} categoryPath={current} categories={categories} />
                <div className="mt-4">
                  <Button size="sm" variant="ghost" onClick={() => setShowWithdrawn(true)}>
                    {t(($) => $.audit.library.withdrawn_show)}
                  </Button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * The filing scheme, indented by depth.
 *
 * Rendered from the paths themselves rather than assembled into a nested
 * structure: a path IS the position ("03/01" is under "03"), fixed-width
 * segments sort as an auditor expects, and the server already returns them in
 * filing order.
 */
function CategoryTree({
  wsId,
  categories,
  current,
  onSelect,
}: {
  wsId: string;
  categories: AuditCategory[];
  current: string;
  onSelect: (path: string) => void;
}) {
  const { t } = useT("issues");
  const remove = useDeleteAuditCategory(wsId);

  return (
    <ul className="flex flex-col gap-0.5">
      {categories.map((category) => {
        const active = category.path === current;
        return (
          <li key={category.path} className="group flex items-center gap-1">
            <button
              type="button"
              onClick={() => onSelect(category.path)}
              data-active={active}
              // The selected drawer stays identifiable while hovered: the
              // weight carries the state, which hover does not touch.
              className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1 text-left text-body hover:bg-accent/70 data-[active=true]:bg-accent data-[active=true]:font-semibold"
              style={{ paddingLeft: `${(category.depth - 1) * 12 + 8}px` }}
            >
              <span className="shrink-0 text-caption text-muted-foreground tabular-nums">
                {category.path}
              </span>
              <span className="truncate">{category.name}</span>
            </button>
            {!category.is_standard && (
              <Button
                size="icon-sm"
                variant="ghost"
                className="opacity-0 group-hover:opacity-100"
                title={t(($) => $.audit.library.delete_category)}
                onClick={() =>
                  remove.mutate(category.path, {
                    onError: (err: unknown) =>
                      toast.error(
                        refusalFallback(err) ?? t(($) => $.audit.library.delete_category_failed),
                      ),
                  })
                }
              >
                ×
              </Button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function DrawerContents({
  wsId,
  categoryPath,
  categories,
}: {
  wsId: string;
  categoryPath: string;
  categories: AuditCategory[];
}) {
  const { t } = useT("issues");
  const { data: documents = [], isPending } = useAuditDocuments(wsId, categoryPath);
  const category = categories.find((c) => c.path === categoryPath);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-body font-semibold">
          {category ? `${category.path} ${category.name}` : categoryPath}
        </h2>
        {category?.is_standard ? (
          <Badge variant="secondary">{t(($) => $.audit.library.standard)}</Badge>
        ) : null}
        <span className="ml-auto">
          <FileDocument wsId={wsId} categoryPath={categoryPath} />
        </span>
      </div>

      {isPending ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.library.loading)}</p>
      ) : documents.length === 0 ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.library.empty_drawer)}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[36rem] border-collapse text-body">
            <thead>
              <tr className="border-b border-border text-caption text-muted-foreground">
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.library.col_title)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.library.col_size)}</th>
                <th className="py-2 pr-4 text-left font-normal">{t(($) => $.audit.library.col_filed)}</th>
                <th className="py-2" />
              </tr>
            </thead>
            <tbody>
              {documents.map((doc) => (
                <DocumentRow key={doc.id} wsId={wsId} doc={doc} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <NewSubCategory wsId={wsId} parent={categoryPath} depth={category?.depth ?? 1} />
    </div>
  );
}

function DocumentRow({ wsId, doc }: { wsId: string; doc: AuditDocument }) {
  const { t } = useT("issues");
  const withdraw = useWithdrawAuditDocument(wsId);
  // Collected before the request, not discovered as a refusal: taking evidence
  // out of the file is an act that has to explain itself.
  const [reason, setReason] = useState("");
  const [confirming, setConfirming] = useState(false);
  return (
    <tr className="border-b border-border/60">
      <td className="py-2 pr-4">
        {/* The href is a capability that expires in about a minute, minted when
            this list was fetched. Opening it late fails rather than serving the
            document, which is the trade audit material makes: a link that keeps
            working is a link that keeps working after it leaks. */}
        <a
          href={resolvePublicFileUrl(doc.download_url) ?? doc.download_url}
          target="_blank"
          rel="noreferrer"
          className="hover:underline"
        >
          {doc.title}
        </a>
        {doc.category_path !== "" ? (
          <span className="ml-2 text-caption text-muted-foreground tabular-nums">
            {doc.category_path}
          </span>
        ) : null}
      </td>
      <td className="py-2 pr-4 tabular-nums text-muted-foreground">{formatSize(doc.size_bytes)}</td>
      <td className="py-2 pr-4 tabular-nums text-muted-foreground">{doc.created_at.slice(0, 10)}</td>
      <td className="py-2 text-right">
        {confirming ? (
          <span className="flex items-center justify-end gap-2">
            <Input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t(($) => $.audit.library.withdraw_placeholder)}
              aria-label={t(($) => $.audit.library.withdraw_reason)}
              className="w-64"
            />
            <Button
              size="sm"
              variant="destructive"
              disabled={reason.trim().length === 0 || withdraw.isPending}
              onClick={() =>
                withdraw.mutate(
                  { id: doc.id, reason: reason.trim() },
                  {
                    onSuccess: () => setConfirming(false),
                    onError: (err: unknown) =>
                      toast.error(refusalFallback(err) ?? t(($) => $.audit.library.withdraw_failed)),
                  },
                )
              }
            >
              {t(($) => $.audit.library.withdraw_confirm)}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
              {t(($) => $.audit.library.withdraw_cancel)}
            </Button>
          </span>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setConfirming(true)}>
            {t(($) => $.audit.library.withdraw)}
          </Button>
        )}
      </td>
    </tr>
  );
}

/**
 * Filing material.
 *
 * The bytes go through the platform's own upload path and this records where
 * they are filed — two steps rather than a second storage path for audit
 * material, which would be a second thing to secure.
 */
function FileDocument({ wsId, categoryPath }: { wsId: string; categoryPath: string }) {
  const { t } = useT("issues");
  const [title, setTitle] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const file = useFileAuditDocument(wsId);

  return (
    <span className="flex items-center gap-2">
      <Input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder={t(($) => $.audit.library.file_title)}
        aria-label={t(($) => $.audit.library.file_title)}
        className="w-56"
      />
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        onChange={(e) => {
          const chosen = e.target.files?.[0];
          if (!chosen) return;
          file.mutate(
            { file: chosen, categoryPath, title },
            {
              onSuccess: () => setTitle(""),
              onError: (err: unknown) =>
                toast.error(refusalFallback(err) ?? t(($) => $.audit.library.file_failed)),
            },
          );
          // Cleared so choosing the same file twice fires again — a re-upload
          // after a failure is the case that matters.
          e.target.value = "";
        }}
      />
      <Button size="sm" disabled={file.isPending} onClick={() => inputRef.current?.click()}>
        {file.isPending ? t(($) => $.audit.library.filing) : t(($) => $.audit.library.file_here)}
      </Button>
    </span>
  );
}

/**
 * A drawer this client needs, under the one being looked at.
 *
 * The scheme is two levels deep, so the form is offered only where a child can
 * legally go. The server refuses a deeper path too; not offering it is what
 * keeps the refusal from being the way people learn the rule.
 */
function NewSubCategory({ wsId, parent, depth }: { wsId: string; parent: string; depth: number }) {
  const { t } = useT("issues");
  const [segment, setSegment] = useState("");
  const [name, setName] = useState("");
  const create = useCreateAuditCategory(wsId);
  const path = useMemo(() => `${parent}/${segment}`, [parent, segment]);

  if (depth >= 2) return null;

  const ready = /^[0-9]{2}$/.test(segment) && name.trim().length > 0;

  return (
    <div className="flex flex-wrap items-end gap-2 border-t border-border pt-4">
      <label className="flex flex-col gap-1">
        <span className="text-caption text-muted-foreground">
          {t(($) => $.audit.library.new_category_path)}
        </span>
        <Input
          value={segment}
          onChange={(e) => setSegment(e.target.value)}
          aria-label={t(($) => $.audit.library.new_category_path)}
          className="w-40"
          inputMode="numeric"
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-caption text-muted-foreground">
          {t(($) => $.audit.library.new_category_name)}
        </span>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          aria-label={t(($) => $.audit.library.new_category_name)}
          className="w-56"
        />
      </label>
      <Button
        size="sm"
        disabled={!ready || create.isPending}
        onClick={() =>
          create.mutate(
            { path, name: name.trim() },
            {
              onSuccess: () => {
                setSegment("");
                setName("");
              },
              onError: (err: unknown) =>
                toast.error(refusalFallback(err) ?? t(($) => $.audit.library.new_category_failed)),
            },
          )
        }
      >
        {t(($) => $.audit.library.new_category_add)}
      </Button>
    </div>
  );
}

/** Human sizes. A voucher scan is measured in KB and a policy PDF in MB. */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * What the library used to hold.
 *
 * No download link on any row: the material is out of the file. What this view
 * answers is that it WAS here and why it went — which is the whole point of
 * withdrawing rather than deleting.
 */
function WithdrawnMaterial({ wsId, onBack }: { wsId: string; onBack: () => void }) {
  const { t } = useT("issues");
  const { data: documents = [], isPending } = useWithdrawnAuditDocuments(wsId);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <h2 className="text-body font-semibold">{t(($) => $.audit.library.withdrawn_title)}</h2>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={onBack}>
          {t(($) => $.audit.library.withdrawn_hide)}
        </Button>
      </div>
      {isPending ? (
        <p className="text-body text-muted-foreground">{t(($) => $.audit.library.loading)}</p>
      ) : documents.length === 0 ? (
        <p className="text-body text-muted-foreground">
          {t(($) => $.audit.library.withdrawn_empty)}
        </p>
      ) : (
        <ul className="flex flex-col divide-y divide-border/60">
          {documents.map((doc: AuditDocument) => (
            <li key={doc.id} className="flex flex-col gap-1 py-3">
              <span className="flex items-center gap-2">
                <span className="font-medium line-through decoration-muted-foreground">
                  {doc.title}
                </span>
                <span className="text-caption text-muted-foreground tabular-nums">
                  {doc.category_path}
                </span>
              </span>
              <span className="text-caption text-muted-foreground">
                {t(($) => $.audit.library.withdrawn_at)} {doc.withdrawn_at?.slice(0, 10) ?? ""} ·{" "}
                {t(($) => $.audit.library.withdrawn_reason_label)}: {doc.withdrawal_reason ?? ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
