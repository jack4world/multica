import { useCallback } from "react";
import { DRAFT_NEW_SESSION, useChatStore } from "@multica/core/chat";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { useNavigation } from "../navigation";

/**
 * Put a question in front of an agent from wherever the auditor is standing.
 *
 * There is one chat composer per workspace (DRAFT_NEW_SESSION); this fills it
 * and opens it. The floating window is preferred because it keeps the page the
 * question is about on screen — an auditor asking "what does 二级复核 mean here"
 * wants to keep looking at the workpaper, not lose it. When the floating
 * window is turned off in Settings, the Chat page is the only place the
 * composer exists, so go there instead; the draft is the same slot either way.
 *
 * An empty question opens the composer without touching whatever was already
 * typed in it.
 */
export function useAskAgent(): (question: string) => void {
  const setOpen = useChatStore((s) => s.setOpen);
  const setActiveSession = useChatStore((s) => s.setActiveSession);
  const setInputDraft = useChatStore((s) => s.setInputDraft);
  const floatingEnabled = useChatStore((s) => s.floatingChatEnabled);
  const { push } = useNavigation();
  const slug = useWorkspaceSlug() ?? "";

  return useCallback(
    (question: string) => {
      // A fresh conversation, not a reply into whichever one was open last.
      setActiveSession(null);
      if (question.trim() !== "") setInputDraft(DRAFT_NEW_SESSION, question);
      if (floatingEnabled) {
        setOpen(true);
      } else {
        push(paths.workspace(slug).chat());
      }
    },
    [setActiveSession, setInputDraft, floatingEnabled, setOpen, push, slug],
  );
}
