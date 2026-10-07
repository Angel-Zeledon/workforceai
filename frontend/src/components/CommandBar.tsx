"use client";
import { useEffect, useRef, useState } from "react";
import { useStore, typingIn } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { ChatInput, ChatThread, TurnChip, sendChat } from "./chat/ChatThread";
import { Icon } from "./icons";
import { useAgentName } from "./ui";

// Canned demo messages: label localized, prompt always from the catalog.
const SUGGESTIONS = ["cmd.suggestionHello", "cmd.suggestionFinance", "cmd.suggestion1", "cmd.suggestion2", "cmd.suggestion3"];
const OFFICE = "office";

/** Office channel: a chat with the whole office. Whoever is relevant answers; tasks come with who/why. */
export function CommandBar() {
  const { t } = useT();
  const name = useAgentName();
  const [open, setOpen] = useState(true);
  const [seen, setSeen] = useState(0);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const toggle = useRef<HTMLButtonElement>(null);
  const msgs = useStore((s) => s.chat[OFFICE]);
  const typing = useStore((s) => s.typing);
  const lastTurn = useStore((s) => (s.lastTurnId ? s.routes[s.lastTurnId] : undefined));
  const active = useStore((s) => Object.values(s.requests).find((r) => r.status === "planning" || r.status === "running" || r.status === "awaiting_approval"));
  const total = msgs?.length ?? 0;
  const who = typingIn(typing, OFFICE);
  const [, tick] = useState(0);
  useEffect(() => { const i = setInterval(() => tick((x) => x + 1), 2000); return () => clearInterval(i); }, []);
  useEffect(() => { if (open) setSeen(total); }, [open, total]);
  const hasThread = total > 0 || who.length > 0;
  const last = msgs?.[total - 1];
  const unread = Math.max(0, total - seen);

  const quick = async (text: string) => {
    if (busy) return;
    setBusy(true); setErr(""); setOpen(true);
    try { await sendChat(OFFICE, text); } catch { setErr(t("chat.sendError")); }
    setBusy(false);
  };

  return (
    <div data-testid="office-chat" data-open={open && hasThread ? "true" : "false"} className="pointer-events-auto w-full max-w-[760px]">
      {hasThread && (
        <section aria-label={t("chat.office.title")} className="mb-2 overflow-hidden rounded-xl border border-line-strong bg-panel/95 shadow-float backdrop-blur">
          <header className="flex items-center gap-2 px-3 py-2">
            <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("chat.office.title")}</h3>
            {lastTurn && <TurnChip intent={lastTurn.intent} withHint />}
            {!open && who.length > 0 && <span className="text-[11px] text-mute" aria-hidden>{t(who.length === 1 ? "chat.typing.one" : "chat.typing.many", { name: name(who[0]).split(" ")[0] })}</span>}
            {!open && who.length === 0 && last && <span className="min-w-0 flex-1 truncate text-[11.5px] text-ink2">{name(last.from).split(" ")[0]}: {last.text.split("\n")[0]}</span>}
            <span className="flex-1" />
            {!open && unread > 0 && <span data-testid="chat-unread" className="rounded-full bg-accent px-1.5 py-[1px] text-[10px] font-semibold text-white">{t("chat.office.new", { count: unread })}</span>}
            <button
              ref={toggle} type="button" data-testid="chat-collapse" aria-expanded={open} onClick={() => setOpen((o) => !o)}
              title={open ? t("chat.office.collapse") : t("chat.office.expand")} aria-label={open ? t("chat.office.collapse") : t("chat.office.expand")}
              className="rounded-md p-1 text-mute hover:bg-mute/10 hover:text-ink"
            >
              <Icon name="down" size={15} className={`transition-transform ${open ? "" : "rotate-180"}`} />
            </button>
          </header>
          {open && <ChatThread conv={OFFICE} showRoute emptyText={t("chat.empty.office")} className="h-[min(300px,36vh)] border-t border-line" />}
        </section>
      )}
      {(!hasThread || !open) && !busy && (
        <div className="mb-2 flex flex-wrap justify-center gap-1.5">
          {SUGGESTIONS.map((s) => (
            <button key={s} onClick={() => quick(t(`${s}.prompt`))} disabled={busy}
              className="rounded-md border border-line bg-panel/90 px-3 py-1 text-[11px] text-ink2 backdrop-blur transition hover:border-accent hover:text-ink disabled:opacity-50">
              {t(`${s}.label`)}
            </button>
          ))}
        </div>
      )}
      <div className="rounded-xl border border-line-strong bg-panel/95 p-2 shadow-float backdrop-blur">
        <div className="flex items-center gap-2">
          <div className="min-w-0 flex-1">
            <ChatInput conv={OFFICE} testId="command-input" submitTestId="command-submit" placeholder={t("cmd.placeholder")} onSent={() => setOpen(true)} />
          </div>
          {active && <span className="hidden whitespace-nowrap text-[11px] text-mute sm:block">{t("cmd.inProgress", { status: t(`cmd.status.${active.status}`) })}</span>}
        </div>
      </div>
      {err && <div role="alert" className="mt-1 text-center text-[11px] text-err">{err}</div>}
    </div>
  );
}
