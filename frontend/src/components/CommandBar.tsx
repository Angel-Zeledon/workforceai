"use client";
import { useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { Btn } from "./ui";

// Canned demo requests: the label is localized, the prompt sent to the backend is always the catalog "prompt" entry.
const SUGGESTIONS = ["cmd.suggestion1", "cmd.suggestion2", "cmd.suggestion3"];

export function CommandBar() {
  const { t } = useT();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const active = useStore((s) => Object.values(s.requests).find((r) => r.status === "planning" || r.status === "running" || r.status === "awaiting_approval"));

  const send = async (raw: string) => {
    const v = raw.trim();
    if (!v || busy) return;
    setBusy(true); setErr("");
    try { await api.postRequest(v); setText(""); } catch { setErr(t("cmd.sendError")); }
    setBusy(false);
  };
  return (
    <div className="pointer-events-auto w-full max-w-[760px]">
      {!text && (
        <div className="mb-2 flex flex-wrap justify-center gap-1.5">
          {SUGGESTIONS.map((s) => (
            <button key={s} onClick={() => send(t(`${s}.prompt`))} disabled={busy}
              className="rounded-md border border-line bg-panel/85 px-3 py-1 text-[11px] text-ink2 backdrop-blur transition hover:border-accent hover:text-ink disabled:opacity-50">
              {t(`${s}.label`)}
            </button>
          ))}
        </div>
      )}
      <form
        onSubmit={(e) => { e.preventDefault(); send(text); }}
        className="flex items-center gap-2 rounded-xl border border-line-strong bg-panel/95 p-2 shadow-float backdrop-blur"
      >
        <span className="pl-2 text-accent">›</span>
        <input
          data-testid="command-input"
          value={text} onChange={(e) => setText(e.target.value)} placeholder={t("cmd.placeholder")}
          className="min-w-0 flex-1 bg-transparent px-1 py-2 text-sm text-ink outline-none placeholder:text-mute"
        />
        {active && <span className="hidden whitespace-nowrap text-[11px] text-mute sm:block">{t("cmd.inProgress", { status: t(`cmd.status.${active.status}`) })}</span>}
        <Btn data-testid="command-submit" type="submit" kind="primary" disabled={busy || !text.trim()}>{busy ? t("cmd.sending") : t("cmd.send")}</Btn>
      </form>
      {err && <div className="mt-1 text-center text-[11px] text-red-700">{err}</div>}
    </div>
  );
}
