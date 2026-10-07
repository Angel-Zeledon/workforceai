"use client";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { fmtTime } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { Btn, readable, useAgentColor, useAgentName } from "./ui";

const EMPTY: never[] = [];

export function ConversationThread({ id, canSend = true, height = "max-h-72" }: { id: string; canSend?: boolean; height?: string }) {
  const messages = useStore((s) => s.messages[id]) ?? EMPTY;
  const loadMessages = useStore((s) => s.loadMessages);
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => { loadMessages(id); }, [id]);
  useEffect(() => { box.current?.scrollTo({ top: box.current.scrollHeight }); }, [messages.length]);

  const send = async () => {
    const t = text.trim();
    if (!t) return;
    setBusy(true);
    try { await api.sendMessage(id, t); setText(""); } catch { /* surfaced by missing echo */ }
    setBusy(false);
  };
  return (
    <div>
      <div ref={box} className={`${height} space-y-2 overflow-y-auto rounded-md border border-line bg-bg/60 p-2.5`}>
        {messages.length === 0 && <div className="py-4 text-center text-xs text-mute">{t("conversation.empty")}</div>}
        {messages.map((m) => (
          <div key={m.id} className={`flex flex-col ${m.from === "user" ? "items-end" : "items-start"}`}>
            <div className="mb-0.5 flex items-center gap-1.5 text-[10px] text-mute">
              <span className="font-semibold" style={{ color: m.from === "user" ? "#2b6cb0" : readable(color(m.from)) }}>{name(m.from)}</span>
              <span>→ {name(m.to)}</span>
              <span className="rounded bg-mute/[0.08] px-1 uppercase tracking-wide">{t(`kind.${m.kind}`)}</span>
              <span className="font-mono">{fmtTime(m.ts)}</span>
            </div>
            <div className={`max-w-[92%] rounded-lg px-2.5 py-1.5 text-[12px] leading-snug ${m.from === "user" ? "bg-accent/20 text-ink2" : "bg-panel2 text-ink2"}`}>{m.text}</div>
          </div>
        ))}
      </div>
      {canSend && (
        <form onSubmit={(e) => { e.preventDefault(); send(); }} className="mt-2 flex gap-2">
          <input value={text} onChange={(e) => setText(e.target.value)} placeholder={t("conversation.placeholder")}
            className="min-w-0 flex-1 rounded-md border border-line bg-panel2 px-3 py-1.5 text-xs text-ink outline-none focus:border-accent" />
          <Btn type="submit" kind="primary" disabled={busy || !text.trim()}>{t("cmd.send")}</Btn>
        </form>
      )}
    </div>
  );
}
