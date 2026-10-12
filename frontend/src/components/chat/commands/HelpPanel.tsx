"use client";
import { createPortal } from "react-dom";
import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { GROUP_ORDER, matchCommands, primaryAlias } from "@/lib/commands/logic";
import type { Command, Lang } from "@/lib/commands/registry";
import { Icon } from "../../icons";
import { safeIcon } from "./CommandMenu";

/** All commands, grouped, with examples and search. Picking one inserts it in the composer. */
export function HelpPanel({ commands, onPick, onClose }: { commands: Command[]; onPick: (c: Command) => void; onClose: () => void }) {
  const { t, locale } = useT();
  const lang = locale as Lang;
  const reduce = usePreferences((x) => x.prefs.reduceMotion);
  const [q, setQ] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => { input.current?.focus(); }, []);
  const found = useMemo(() => matchCommands(commands, q, lang), [commands, q, lang]);
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") { e.stopPropagation(); onClose(); }
    if (e.key === "Tab") {
      const f = Array.from((e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>("input,button:not([disabled])"));
      const i = f.indexOf(document.activeElement as HTMLElement);
      if (e.shiftKey && i <= 0) { e.preventDefault(); f[f.length - 1]?.focus(); }
      else if (!e.shiftKey && i === f.length - 1) { e.preventDefault(); f[0]?.focus(); }
    }
  };
  return createPortal(
    <div data-reduce={reduce ? "1" : "0"} className="fixed inset-0 z-50 flex items-end justify-center bg-black/35 p-0 sm:items-center sm:p-4" onKeyDown={onKey} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal="true" aria-label={t("cmds.help.title")} data-testid="cmd-help-panel" className="ac-pop flex max-h-[88vh] w-full max-w-2xl flex-col rounded-t-2xl border border-line-strong bg-panel shadow-float sm:rounded-2xl">
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          <h2 className="flex-1 font-display text-[15px] font-semibold text-ink">{t("cmds.help.title")}</h2>
          <button ref={close} type="button" aria-label={t("cmds.help.close")} data-testid="cmd-help-close" onClick={onClose} className="rounded-md p-1 text-mute hover:bg-mute/10 hover:text-ink"><Icon name="x" size={16} /></button>
        </header>
        <div className="border-b border-line px-4 py-2">
          <input ref={input} data-testid="cmd-help-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("cmds.help.search")} aria-label={t("cmds.help.search")} autoComplete="off"
            className="w-full rounded-lg border border-line bg-panel2 px-3 py-2 text-[13px] text-ink outline-none placeholder:text-mute" />
          <p className="mt-1.5 text-[11px] text-mute">{t("cmds.help.hint")}</p>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          {found.length === 0 && <p className="px-3 py-6 text-center text-xs text-mute">{t("cmds.help.empty")}</p>}
          {GROUP_ORDER.map((g) => {
            const rows = found.filter((m) => m.cmd.group === g);
            if (!rows.length) return null;
            return (
              <section key={g} aria-label={t(`cmds.group.${g}`)} className="mb-2">
                <h3 className="px-2 pb-1 pt-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t(`cmds.group.${g}`)}</h3>
                <ul>
                  {rows.map(({ cmd }) => (
                    <li key={cmd.id}>
                      <button type="button" data-testid="cmd-help-item" data-cmd={cmd.id} disabled={cmd.allowed === false} title={cmd.allowed === false ? t("cmds.disabled") : undefined} onClick={() => onPick(cmd)}
                        className="flex w-full items-start gap-2.5 rounded-lg px-2 py-1.5 text-left hover:bg-accent-soft disabled:cursor-not-allowed disabled:opacity-45">
                        <span aria-hidden className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-panel2 text-accent-hover"><Icon name={safeIcon(cmd.icon)} size={13} /></span>
                        <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-baseline gap-x-2">
                            <span className="font-mono text-[12.5px] font-semibold text-ink">/{primaryAlias(cmd, lang)}</span>
                            {[...new Set([cmd.id, ...cmd.aliases.en, ...cmd.aliases.es])].filter((a) => a !== primaryAlias(cmd, lang)).slice(0, 2).map((a) => <span key={a} className="font-mono text-[10.5px] text-mute">/{a}</span>)}
                            <span className="text-[12px] text-ink2">{cmd.title[lang]}</span>
                          </span>
                          <span className="block text-[11.5px] text-ink2">{cmd.description[lang]}</span>
                          <span className="block font-mono text-[10.5px] text-mute">{cmd.example[lang]}</span>
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      </div>
    </div>,
    document.body,
  );
}
