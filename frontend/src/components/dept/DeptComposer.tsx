"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { useAgentName } from "../ui";
import { Icon } from "../icons";

/** A row's "Pedir a un agente…": the text to start with and the file to attach (`n` changes on every click). */
export interface AskSeed { text: string; path: string; name: string; n: number }

/**
 * The composer at the bottom of the department's Drive: pick one of the office's agents (the first by default), write
 * what you need and "Pedir" sends it to that agent's chat with the current folder as the work folder
 * (POST /messages `folder`). A row's "Pedir a un agente…" fills the text and attaches that file.
 */
export function DeptComposer({ agents, initialAgent, folder, seed }: {
  agents: string[]; initialAgent?: string; folder: string; seed?: AskSeed | null;
}) {
  const { t } = useT();
  const name = useAgentName();
  const [agent, setAgent] = useState(initialAgent ?? agents[0] ?? "");
  const [text, setText] = useState("");
  const [file, setFile] = useState<{ path: string; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => { if (!agents.includes(agent)) setAgent(initialAgent ?? agents[0] ?? ""); }, [agents, initialAgent, agent]);
  useEffect(() => {
    if (!seed) return;
    setText(seed.text); setFile({ path: seed.path, name: seed.name }); setSent(null); setErr("");
    requestAnimationFrame(() => { const el = input.current; if (el) { el.focus(); el.setSelectionRange(el.value.length, el.value.length); } });
  }, [seed]);

  const send = async () => {
    const v = text.trim();
    if (!v || !agent || busy) return;
    setBusy(true); setErr(""); setSent(null);
    const conv = `agent:${agent}`;
    const attachments = file ? [file.path] : [];
    // the person's message shows in the agent's chat right away, as from the chat itself
    useStore.getState().addLocalChat({
      id: `local:${Date.now().toString(36)}`, conversation: conv, turn_id: null, from: "user", to: agent, kind: "chat", text: v, reply_to: null,
      ts: new Date().toISOString(), ...(file ? { files: [{ path: file.path, name: file.name, action: "attach" }] } : {}),
    });
    try {
      await api.postMessage(conv, v, attachments, undefined, folder);
      setText(""); setFile(null); setSent(agent);
    } catch { setErr(t("chat.sendError")); }
    setBusy(false);
  };

  if (!agents.length) {
    return <div data-testid="dept-composer" className="border-t border-line bg-panel px-3 py-3 text-[12.5px] text-mute sm:px-5">{t("dept.ask.noAgents")}</div>;
  }
  return (
    <form data-testid="dept-composer" onSubmit={(e) => { e.preventDefault(); void send(); }}
      className="shrink-0 border-t border-line bg-panel px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-2.5 sm:px-5">
      {(sent || err || file) && (
        <div className="mb-2 flex flex-wrap items-center gap-2 text-[12px]">
          {file && (
            <span data-testid="dept-ask-file" className="inline-flex max-w-full items-center gap-1 rounded-md border border-line bg-panel2 py-0.5 pl-2 pr-0.5 text-ink">
              <Icon name="doc" size={13} className="shrink-0 text-mute" />
              <span className="min-w-0 truncate">{file.name}</span>
              <button type="button" aria-label={t("dept.ask.removeFile")} title={t("dept.ask.removeFile")} onClick={() => setFile(null)}
                className="flex h-6 w-6 items-center justify-center rounded text-mute hover:bg-line/60 hover:text-ink"><Icon name="x" size={12} /></button>
            </span>
          )}
          {sent && (
            <Link href={`/chats/${encodeURIComponent(sent)}`} prefetch={false} data-testid="dept-sent"
              className="inline-flex items-center gap-1 font-semibold text-accent hover:text-accent-hover hover:underline">
              <Icon name="check" size={13} />{t("dept.sent", { agent: name(sent) })}<Icon name="chevron" size={12} />
            </Link>
          )}
          {err && <span role="alert" className="text-err">{err}</span>}
        </div>
      )}
      {/* phone: the agent on its own line above, so the field keeps the whole width */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 sm:flex-nowrap">
        <label className="flex min-w-0 items-center gap-1.5 max-sm:w-full sm:contents" htmlFor="dept-ask-agent">
          <span className="text-[12px] text-mute sm:sr-only">{t("dept.ask.agent")}:</span>
          <select id="dept-ask-agent" data-testid="dept-ask-agent" value={agent} onChange={(e) => setAgent(e.target.value)}
            className="min-h-9 min-w-0 shrink-0 truncate rounded-lg border border-line bg-panel px-2 text-[13px] text-ink outline-none focus:border-accent max-sm:max-w-[70%] max-sm:py-1 sm:max-w-[200px]">
            {agents.map((id) => <option key={id} value={id}>{name(id)}</option>)}
          </select>
        </label>
        <input ref={input} data-testid="dept-ask-input" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("dept.ask.placeholder")}
          aria-label={t("dept.ask.placeholder")}
          className="min-h-10 min-w-0 flex-1 rounded-lg border border-line bg-panel2 px-3 text-base text-ink outline-none focus:border-accent sm:min-h-9 sm:text-[13px]" />
        <button type="submit" data-testid="dept-ask-send" disabled={!text.trim() || busy}
          className="min-h-10 shrink-0 rounded-lg bg-accent px-4 text-[13px] font-semibold text-white transition hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-40 sm:min-h-9">
          {t("dept.ask.send")}
        </button>
      </div>
    </form>
  );
}
