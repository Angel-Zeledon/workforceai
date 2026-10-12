"use client";
import { useT } from "@/lib/i18n";

/** Placeholder for the chat history screen (/chats and /chats/[agentId]); built by another workstream. */
export function ChatsView({ agentId }: { agentId?: string }) {
  const { t } = useT();
  return (
    <div data-testid="chats-view" data-agent={agentId ?? ""} className="flex h-full items-center justify-center p-8 text-center">
      <div className="max-w-sm">
        <h2 className="text-lg font-semibold text-ink">{t("chats.stub.title")}</h2>
        <p className="mt-2 text-sm text-mute">{t("chats.stub.body")}</p>
      </div>
    </div>
  );
}
