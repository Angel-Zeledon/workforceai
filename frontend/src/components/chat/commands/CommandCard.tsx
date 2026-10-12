"use client";
import { createPortal } from "react-dom";
import { Fragment, useMemo, useRef, useEffect, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { go } from "@/lib/navBridge";
import { sortRows } from "@/lib/commands/logic";
import type { ChatMessage } from "@/lib/types";
import { Icon } from "../../icons";

/** Markdown-safe: only **bold**, `code` and line breaks, built as React nodes (never innerHTML). */
export function SafeText({ text }: { text: string }) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g);
  return (
    <>
      {parts.map((p, i) => {
        if (p.startsWith("**") && p.endsWith("**") && p.length > 4) return <strong key={i} className="font-semibold">{p.slice(2, -2)}</strong>;
        if (p.startsWith("`") && p.endsWith("`") && p.length > 2) return <code key={i} className="rounded bg-mute/10 px-1 font-mono text-[11.5px]">{p.slice(1, -1)}</code>;
        return <Fragment key={i}>{p}</Fragment>;
      })}
    </>
  );
}

const cell = (v: unknown): string => (v === null || v === undefined ? "" : typeof v === "object" ? JSON.stringify(v) : String(v));
const safeHref = (u?: string) => !!u && /^(\/(?!\/)|https?:\/\/|data:text\/|blob:)/i.test(u);

function Table({ columns, rows }: { columns: { key?: string; label: string }[]; rows: unknown[][] }) {
  const { t } = useT();
  const [sort, setSort] = useState<{ col: number; dir: 1 | -1 } | null>(null);
  const shown = useMemo(() => (sort ? sortRows(rows, sort.col, sort.dir) : rows), [rows, sort]);
  return (
    <div className="overflow-x-auto rounded-lg border border-line">
      <table data-testid="cmd-table" className="w-full border-collapse text-left text-[11.5px]">
        <thead>
          <tr className="bg-panel2">
            {columns.map((c, i) => (
              <th key={i} scope="col" aria-sort={sort?.col === i ? (sort.dir === 1 ? "ascending" : "descending") : "none"} className="whitespace-nowrap px-2 py-1 font-semibold text-ink2">
                <button type="button" title={t("cmds.card.sort", { col: c.label })} onClick={() => setSort((s) => (s?.col === i ? { col: i, dir: (s.dir * -1) as 1 | -1 } : { col: i, dir: 1 }))} className="inline-flex items-center gap-1 hover:text-ink">
                  {c.label}
                  {sort?.col === i && <Icon name="chevron" size={10} className={sort.dir === 1 ? "-rotate-90" : "rotate-90"} />}
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {shown.map((r, i) => (
            <tr key={i} className="border-t border-line">
              {columns.map((_, j) => <td key={j} className="px-2 py-1 text-ink">{cell(r[j])}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** System card of a command result: message, table, navigate, download, card (key/values) or request. */
export function CommandCard({ m }: { m: ChatMessage }) {
  const { t } = useT();
  const card = m.card!;
  const columns = (card.columns ?? []).map((c) => (typeof c === "string" ? { label: c } : { key: c.key, label: c.label }));
  const rows: unknown[][] = (card.rows ?? []).map((r) => (Array.isArray(r) ? r : columns.map((c) => (r as Record<string, unknown>)?.[c.key ?? c.label])));
  const kv = card.kind === "card" && card.data && typeof card.data === "object" && !Array.isArray(card.data) ? Object.entries(card.data as Record<string, unknown>) : [];
  let action: ReactNode = null;
  if ((card.kind === "download") && safeHref(card.url)) {
    action = (
      <a data-testid="cmd-download" href={card.url} download={card.filename} target={card.url!.startsWith("data:") ? undefined : "_blank"} rel="noreferrer"
        className="mt-1.5 inline-flex items-center gap-1.5 rounded-md border border-accent bg-accent px-2.5 py-1 text-[11.5px] font-medium text-white hover:bg-accent-hover">
        <Icon name="arrow" size={12} className="rotate-90" />{t("cmds.card.download")}{card.filename ? ` · ${card.filename}` : ""}
      </a>
    );
  } else if (card.kind === "request" || card.kind === "navigate") {
    const href = card.href || (card.request_id ? "/chats" : "");
    if (href) {
      action = (
        <button type="button" data-testid="cmd-link" onClick={() => go(href)} className="mt-1.5 inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2.5 py-1 text-[11.5px] font-medium text-accent-hover hover:border-accent">
          <Icon name="arrow" size={12} />{card.kind === "request" ? t("cmds.card.openRequest") : t("cmds.card.open")}
        </button>
      );
    }
  }
  return (
    <div data-testid="cmd-card" data-kind={card.kind} data-command={card.command} data-error={card.error ? "1" : undefined} role="group" aria-label={card.title || t("cmds.card.system")} className="mx-auto w-full max-w-[94%]">
      <div className={`rounded-xl border px-3 py-2 text-[12.5px] leading-[1.45] ${card.error ? "border-err/40 bg-err/5 text-err" : "border-line bg-panel2 text-ink"}`}>
        <div className="mb-0.5 flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide text-mute">
          <Icon name={card.error ? "alert" : "chevron"} size={11} className={card.error ? "" : "-rotate-90"} />
          {card.command ? `/${card.command}` : t("cmds.card.system")}
        </div>
        {card.title && <div className="font-semibold">{card.title}</div>}
        {card.text && <div className="whitespace-pre-line break-words"><SafeText text={card.text} /></div>}
        {card.kind === "table" && columns.length > 0 && <div className="mt-1.5"><Table columns={columns} rows={rows} /></div>}
        {kv.length > 0 && (
          <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[12px]">
            {kv.map(([k, v]) => (<Fragment key={k}><dt className="text-mute">{k}</dt><dd className="text-ink">{cell(v)}</dd></Fragment>))}
          </dl>
        )}
        {action}
      </div>
    </div>
  );
}

/** Confirmation dialog for destructive commands. Focus starts on "Cancel"; Esc cancels; focus returns to the caller. */
export function ConfirmDialog({ message, onConfirm, onCancel }: { message: string; onConfirm: () => void; onCancel: () => void }) {
  const { t } = useT();
  const reduce = usePreferences((x) => x.prefs.reduceMotion);
  const cancel = useRef<HTMLButtonElement>(null);
  const ok = useRef<HTMLButtonElement>(null);
  useEffect(() => { cancel.current?.focus(); }, []);
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") { e.stopPropagation(); onCancel(); }
    if (e.key === "Tab") {
      const order = [cancel.current, ok.current].filter(Boolean) as HTMLElement[];
      const i = order.indexOf(document.activeElement as HTMLElement);
      e.preventDefault();
      order[(i + (e.shiftKey ? -1 : 1) + order.length) % order.length]?.focus();
    }
  };
  return createPortal(
    <div data-reduce={reduce ? "1" : "0"} className="fixed inset-0 z-50 flex items-center justify-center bg-black/35 p-4" onKeyDown={onKey} onMouseDown={(e) => e.target === e.currentTarget && onCancel()}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="cmd-confirm-msg" data-testid="cmd-confirm" className="ac-pop w-full max-w-sm rounded-xl border border-line-strong bg-panel p-4 shadow-float">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("cmds.confirm.title")}</h3>
        <p id="cmd-confirm-msg" className="mt-1 text-[13.5px] font-medium text-ink">{message}</p>
        <div className="mt-4 flex justify-end gap-2">
          <button ref={cancel} type="button" data-testid="cmd-confirm-cancel" onClick={onCancel} className="rounded-lg border border-line-strong bg-panel px-3 py-1.5 text-[12.5px] font-medium text-ink hover:bg-panel2">{t("cmds.confirm.cancel")}</button>
          <button ref={ok} type="button" data-testid="cmd-confirm-ok" onClick={onConfirm} className="rounded-lg border border-err bg-err px-3 py-1.5 text-[12.5px] font-medium text-white hover:opacity-90">{t("cmds.confirm.ok")}</button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
