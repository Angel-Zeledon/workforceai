"use client";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useT } from "@/lib/i18n";
import { drive, type DriveFolder } from "@/lib/drive";
import {
  folderLabel, isAttachmentsFolder, moveError, moveWarning, nameError, visibleFolders, type NameCtx, type OfficeRef,
} from "@/lib/filesLogic";
import { pathKey } from "@/lib/deptLogic";
import { errorText } from "@/lib/errors";
import { Icon } from "../icons";

/** The frame of a small dialog (rendered on <body>; Esc and a click outside close it; focus goes to the first field). */
function Dialog({ title, testid, onClose, children }: { title: string; testid: string; onClose: () => void; children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLElement>("input, button[data-autofocus], button")?.focus({ preventScroll: true });
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeRef.current(); } };
    window.addEventListener("keydown", key, true);
    return () => { window.removeEventListener("keydown", key, true); if (before?.isConnected) before.focus({ preventScroll: true }); };
  }, []);
  if (typeof document === "undefined") return null;
  return createPortal(
    <div className="fixed inset-0 z-[70] flex items-end justify-center bg-black/25 sm:items-center sm:p-6" onMouseDown={onClose}>
      <div ref={box} role="dialog" aria-modal="true" aria-labelledby={`${testid}-title`} data-testid={testid} onMouseDown={(e) => e.stopPropagation()}
        className="flex max-h-[88dvh] w-full max-w-[460px] flex-col rounded-t-[10px] border border-line bg-panel p-4 pb-[max(1rem,env(safe-area-inset-bottom))] shadow-pop sm:rounded-[10px] sm:p-5">
        <h2 id={`${testid}-title`} className="break-words text-[15px] font-semibold text-ink [overflow-wrap:anywhere]">{title}</h2>
        {children}
      </div>
    </div>,
    document.body,
  );
}

const primary = "min-h-10 rounded-lg bg-accent px-4 text-[13px] font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-9";
const secondary = "min-h-10 rounded-lg border border-line-strong bg-panel px-4 text-[13px] font-medium text-ink hover:bg-panel2 sm:min-h-9";

/**
 * "Nueva carpeta" and "Cambiar nombre": one field, checked while typing with the drive's own rules (and against the
 * names already in the folder). `submit` does the work and throws on a refusal, shown in words.
 */
export function NameDialog({ title, label, cta, initial = "", siblings, original, file = false, submit, onClose }: {
  title: string; label: string; cta: string; initial?: string; siblings: string[]; original?: string; file?: boolean;
  submit: (name: string) => Promise<void>; onClose: () => void;
}) {
  const { t } = useT();
  const [name, setName] = useState(initial);
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const input = useRef<HTMLInputElement>(null);
  // a file's rename selects the name without its extension
  useEffect(() => {
    const el = input.current;
    if (!el || !initial) return;
    const dot = file ? initial.lastIndexOf(".") : -1;
    el.setSelectionRange(0, dot > 0 ? dot : initial.length);
  }, [initial, file]);
  const problem = nameError(name, siblings, { original, file });
  const unchanged = !!original && name.trim() === original;
  const show = touched || name !== initial ? problem : null;
  const go = async () => {
    setTouched(true);
    if (problem || unchanged || busy) { if (unchanged) onClose(); return; }
    setBusy(true); setErr("");
    try { await submit(name.trim()); onClose(); } catch (e) { setErr(errorText(e)); setBusy(false); }
  };
  return (
    <Dialog title={title} testid="files-name-dialog" onClose={onClose}>
      <form onSubmit={(e) => { e.preventDefault(); void go(); }} noValidate>
        <label htmlFor="files-name-input" className="mt-3 block text-[12px] font-medium text-ink2">{label}</label>
        <input ref={input} id="files-name-input" data-testid="files-name-input" value={name} maxLength={200} autoComplete="off" spellCheck={false}
          onChange={(e) => { setName(e.target.value); setErr(""); }} onBlur={() => setTouched(true)}
          aria-invalid={!!show} aria-describedby={show || err ? "files-name-err" : undefined}
          className={`mt-1 min-h-10 w-full rounded-lg border bg-panel px-3 text-base text-ink outline-none sm:min-h-9 sm:text-[13px] ${show ? "border-err focus:border-err" : "border-line focus:border-accent"}`} />
        {(show || err) && <p id="files-name-err" role="alert" data-testid="files-name-error" className="mt-1.5 text-[12px] text-err">{show ? t(show) : err}</p>}
        <div className="mt-4 flex flex-wrap justify-end gap-2">
          <button type="button" onClick={onClose} className={secondary}>{t("files.cancel")}</button>
          <button type="submit" data-testid="files-name-submit" disabled={busy || !!problem} className={primary}>{cta}</button>
        </div>
      </form>
    </Dialog>
  );
}

/** A place the move picker starts from (a department, Compartido con todos, Proyectos, Clientes). */
export interface PickRoot { path: string; label: string }

/**
 * "Mover a…": a tree of the places the person works in; the chosen folder is checked (not into itself, not where it
 * is, the person edits there, no name clash) and the move says when it changes who sees the item.
 */
export function MoveDialog({ item, roots, ctx, onMove, onClose }: {
  item: { kind: "file" | "folder"; path: string; name: string }; roots: PickRoot[]; ctx: NameCtx & { offices: OfficeRef[] };
  onMove: (dest: string) => Promise<void>; onClose: () => void;
}) {
  const { t } = useT();
  const [sel, setSel] = useState<string | null>(null);
  const [check, setCheck] = useState<{ path: string; access?: string; names: string[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (!sel) return;
    let live = true;
    setCheck(null);
    drive.list(sel).then((l) => live && setCheck({ path: sel, access: l.access ?? "edit", names: [...l.folders, ...l.files].map((x) => x.name.toLowerCase()) }))
      .catch(() => live && setCheck({ path: sel, access: "read", names: [] }));
    return () => { live = false; };
  }, [sel]);
  const ready = check && sel && check.path === sel ? check : null;
  const problem = sel ? moveError(item, sel)
    ?? (ready && ready.access !== "edit" ? "files.move.readOnly" : null)
    ?? (ready && ready.names.includes(item.name.toLowerCase()) ? "files.name.taken" : null) : null;
  const warn = sel && !problem ? moveWarning(item.path, sel, ctx) : null;
  const go = async () => {
    if (!sel || problem || !ready || busy) return;
    setBusy(true); setErr("");
    try { await onMove(sel); onClose(); } catch (e) { setErr(errorText(e)); setBusy(false); }
  };
  return (
    <Dialog title={t("files.move.title", { name: item.name })} testid="files-move-dialog" onClose={onClose}>
      <p className="mt-1 text-[12.5px] text-mute">{t("files.move.pick")}</p>
      <div className="mt-2 min-h-[200px] flex-1 overflow-y-auto rounded-lg border border-line p-1.5" data-testid="files-move-tree">
        <ul role="tree" aria-label={t("files.move.pick")} className="space-y-px">
          {roots.map((r) => <PickNode key={r.path} path={r.path} label={r.label} depth={0} sel={sel} onSel={setSel} ctx={ctx} exclude={item.kind === "folder" ? item.path : undefined} />)}
        </ul>
      </div>
      {sel && problem && <p role="alert" data-testid="files-move-error" className="mt-2 text-[12px] text-err">{t(problem)}</p>}
      {warn && <p data-testid="files-move-warning" className="mt-2 rounded-lg border border-warn/30 bg-warn/5 px-2.5 py-1.5 text-[12px] text-ink2">{t(warn.key, warn.vars)}</p>}
      {err && <p role="alert" className="mt-2 text-[12px] text-err">{err}</p>}
      <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
        <button type="button" onClick={onClose} className={secondary}>{t("files.cancel")}</button>
        <button type="button" data-testid="files-move-submit" disabled={!sel || !!problem || !ready || busy} onClick={() => void go()} className={primary}>{t("files.move.here")}</button>
      </div>
    </Dialog>
  );
}

function PickNode({ path, label, depth, sel, onSel, ctx, exclude }: {
  path: string; label: string; depth: number; sel: string | null; onSel: (p: string) => void; ctx: NameCtx; exclude?: string;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [kids, setKids] = useState<DriveFolder[] | null>(null);
  const leaf = isAttachmentsFolder(path);
  const load = useCallback(() => {
    drive.list(path).then((l) => setKids(visibleFolders(l.folders).filter((f) => !exclude || pathKey(f.path) !== pathKey(exclude)))).catch(() => setKids([]));
  }, [path, exclude]);
  const toggle = () => { if (!open && !kids) load(); setOpen(!open); };
  const here = !!sel && pathKey(sel) === pathKey(path);
  return (
    <li role="treeitem" aria-expanded={leaf ? undefined : open} aria-selected={here}>
      <div className={`flex min-h-10 items-center gap-0.5 rounded-md pr-1.5 text-[13px] sm:min-h-8 ${here ? "bg-accent-soft font-semibold text-accent" : "text-ink hover:bg-panel2"}`} style={{ paddingLeft: 2 + depth * 14 }}>
        <button type="button" onClick={toggle} aria-label={t("files.expand", { name: label })} disabled={leaf || (!!kids && !kids.length)}
          className="flex h-7 w-6 shrink-0 items-center justify-center rounded text-mute hover:text-ink disabled:invisible">
          <Icon name="chevron" size={13} className={`transition-transform ${open ? "rotate-90" : ""}`} />
        </button>
        <button type="button" data-testid={`files-pick-${label}`} onClick={() => onSel(path)} onDoubleClick={toggle} className="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left">
          <Icon name="folder" size={14} className={`shrink-0 ${here ? "text-accent" : "text-mute"}`} />
          <span className="min-w-0 flex-1 truncate" title={label}>{label}</span>
        </button>
      </div>
      {open && kids && kids.length > 0 && (
        <ul role="group">{kids.map((f) => <PickNode key={f.path} path={f.path} label={folderLabel(f.path, ctx)} depth={depth + 1} sel={sel} onSel={onSel} ctx={ctx} exclude={exclude} />)}</ul>
      )}
    </li>
  );
}
