"use client";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { useAgentView } from "@/lib/preferences";
import { ReasonChip } from "../ui";
import { ChatInput, ChatThread } from "./ChatThread";

/** 1:1 chat with a single agent (conversation "agent:<id>"): only that agent answers. */
export function AgentChat({ id }: { id: string }) {
  const { t } = useT();
  const view = useAgentView(id);
  const agent = useStore((s) => s.agents[id]);
  const task = useStore((s) => (agent?.current_task_id ? s.tasks[agent.current_task_id] : undefined));
  const conv = `agent:${id}`;
  const first = view.name.split(" ")[0];
  return (
    <div data-testid="chat-pane-agent" className="flex h-full min-h-0 flex-col">
      {task?.assigned_reason && (
        <div className="border-b border-line bg-panel2/60 px-3 py-2">
          <div className="mb-1 truncate text-[11px] font-semibold text-ink">{task.title}</div>
          <ReasonChip agentId={id} reason={task.assigned_reason} />
        </div>
      )}
      <ChatThread conv={conv} emptyText={t("chat.empty.agent", { name: first })} className="flex-1" />
      <div className="border-t border-line bg-panel p-3">
        <ChatInput conv={conv} testId="chat-input" submitTestId="chat-submit" placeholder={t("chat.placeholder.agent", { name: first })} />
      </div>
    </div>
  );
}
