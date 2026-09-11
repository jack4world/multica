"use client";

import { useEffect } from "react";
import { useAuditMode } from "@multica/core/audit";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../navigation";

/**
 * Where a bare `/{slug}` goes.
 *
 * An auditee workspace opens on the 审计台, because the person opening it is
 * an auditor who wants to know what is waiting on them — not a task list they
 * would have to learn to read. Every other workspace opens on Issues, as it
 * always has. The decision is made here, on the client, because it depends on
 * a workspace fact the server-side redirect cannot see.
 *
 * `replace`, not `push`: the bare path is a hop, and a Back that lands on it
 * would only hop forward again.
 */
export function WorkspaceLanding() {
  const wsId = useWorkspaceId();
  const { data: auditMode, isPending } = useAuditMode(wsId);
  const { replace } = useNavigation();
  const p = useWorkspacePaths();

  useEffect(() => {
    if (isPending) return;
    replace(auditMode?.enabled === true ? p.audit() : p.issues());
  }, [isPending, auditMode?.enabled, replace, p]);

  return null;
}
