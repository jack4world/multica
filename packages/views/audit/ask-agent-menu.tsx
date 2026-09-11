"use client";

import { Sparkles } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../i18n";
import { useAskAgent } from "./ask-agent";

export interface AskAgentPrompt {
  id: string;
  /** The question, written the way the auditor would ask it. */
  question: string;
}

/**
 * The "问 AI" button every audit page carries in its header.
 *
 * It offers a few questions the page is the natural place to ask — not a
 * generic "chat with an agent", which tells a first-time auditor nothing about
 * what the agent is for. Picking one drops it into the composer; the last item
 * opens an empty composer for anything else.
 */
export function AskAgentMenu({ prompts }: { prompts: AskAgentPrompt[] }) {
  const { t } = useT("issues");
  const ask = useAskAgent();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button variant="outline" size="sm">
            <Sparkles data-icon="inline-start" />
            {t(($) => $.audit.ask.button)}
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="min-w-64">
        {prompts.map((prompt) => (
          <DropdownMenuItem key={prompt.id} onClick={() => ask(prompt.question)}>
            {prompt.question}
          </DropdownMenuItem>
        ))}
        {prompts.length > 0 ? <DropdownMenuSeparator /> : null}
        <DropdownMenuItem onClick={() => ask("")}>
          {t(($) => $.audit.ask.free_form)}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
