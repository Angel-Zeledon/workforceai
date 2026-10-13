"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "@/lib/i18n";
import { drive, DriveError, type DriveFile, type Listing } from "@/lib/drive";
import type { FileRef } from "@/lib/types";
import { Btn } from "../ui";
import { Icon } from "../icons";

/**
 * Attachments of the chat composer, the way people expect them (Claude, ChatGPT): files upload as soon as they are
 * picked, dropped or pasted, show as chips with a thumbnail and their state, can be removed, and travel with the next
 * message. On top of that they live in the drive's folders: each batch lands in a folder you can change, a whole folder
 * can be uploaded, and files that are already in the drive can be attached without uploading them again.
 */
export const MAX_FILES = 10;
export const MAX_BYTES = 25 << 20; // the drive's per-file limit (DRIVE_MAX_FILE_BYTES)

export interface Att {
  id: string;
  name: string;
  size: number;
  mime: string;
  /** Object URL of an image, to show its thumbnail. */
  preview?: string;
  status: "uploading" | "done" | "error";
  ref?: FileRef;
  error?: string;
  from: "upload" | "drive";
}

const today = () => new Date().toISOString().slice(0, 10);
const defaultDest = () => `/Shared/Chat/${today()}`;
const destKey = (conv: string) => `aiw.attach.dest.${conv}`;
const safe = (name: string) => name.replace(/[\\:*?"<>|]/g, "_");
const uid = () => Math.random().toString(36).slice(2, 9);
export const isImage = (mime: string, name: string) => /^image\//.test(mime) || /\.(png|jpe?g|gif|webp|svg)$/i.test(name);

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1 << 20) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / (1 << 20)).toFixed(1)} MB`;
}

/** Everything the composer needs: the items, adding from the computer or from the drive, the destination folder. */
export function useAttachments(conv: string) {
  const [items, setItems] = useState<Att[]>([]);
  const [dest, setDestState] = useState(defaultDest);
  const urls = useRef(new Set<string>());

  useEffect(() => {
    try { const d = localStorage.getItem(destKey(conv)); if (d) setDestState(d); } catch { /* private mode */ }
  }, [conv]);
  useEffect(() => () => { urls.current.forEach((u) => URL.revokeObjectURL(u)); urls.current.clear(); }, []);
  const setDest = (d: string) => { setDestState(d); try { localStorage.setItem(destKey(conv), d); } catch { /* private mode */ } };

  const patch = useCallback((id: string, p: Partial<Att>) => setItems((cur) => cur.map((a) => (a.id === id ? { ...a, ...p } : a))), []);

  const send = useCallback(async (id: string, file: File, rel: string, dir: string) => {
    const path = `${dir.replace(/\/$/, "")}/${rel}`;
    const put = async (p: string) => drive.upload(p, file);
    try {
      let r;
      try { r = await put(path); }
      catch (x) {
        // the same name already exists in that folder: keep both (a copy with the time in its name)
        if (x instanceof DriveError && (x.status === 409 || x.code === "version_conflict")) {
          const dot = rel.lastIndexOf(".");
          const stamp = new Date().toTimeString().slice(0, 8).replace(/:/g, "");
          r = await put(`${dir.replace(/\/$/, "")}/${dot > 0 ? `${rel.slice(0, dot)} (${stamp})${rel.slice(dot)}` : `${rel} (${stamp})`}`);
        } else throw x;
      }
      patch(id, { status: "done", ref: { file_id: r.file.id, path: r.file.path, name: r.file.name, mime: r.file.mime, size: r.file.size, version: r.file.current_version, action: "attach" } });
    } catch (x) {
      patch(id, { status: "error", error: x instanceof DriveError ? x.code : "upload" });
    }
  }, [patch]);

  /** Adds files from the computer (picker, drop or paste); `relative` keeps a folder's inner paths. Returns how many were left out. */
  const addFiles = useCallback((files: File[], relative = false): number => {
    let skipped = 0;
    setItems((cur) => {
      const room = MAX_FILES - cur.length;
      const take = files.slice(0, Math.max(0, room));
      skipped = files.length - take.length;
      const added: Att[] = take.map((f) => {
        const rel = relative && (f as File & { webkitRelativePath?: string }).webkitRelativePath ? (f as File & { webkitRelativePath: string }).webkitRelativePath.split("/").map(safe).join("/") : safe(f.name);
        const preview = isImage(f.type, f.name) ? URL.createObjectURL(f) : undefined;
        if (preview) urls.current.add(preview);
        const tooBig = f.size > MAX_BYTES;
        const a: Att = { id: uid(), name: relative ? rel : f.name, size: f.size, mime: f.type, preview, status: tooBig ? "error" : "uploading", error: tooBig ? "file_too_large" : undefined, from: "upload" };
        if (!tooBig) queueMicrotask(() => void send(a.id, f, rel, dest));
        return a;
      });
      return [...cur, ...added];
    });
    return skipped;
  }, [dest, send]);

  const addDrive = useCallback((files: DriveFile[]) => {
    setItems((cur) => {
      const have = new Set(cur.map((a) => a.ref?.file_id).filter(Boolean));
      const fresh = files.filter((f) => !have.has(f.id)).slice(0, Math.max(0, MAX_FILES - cur.length));
      return [...cur, ...fresh.map((f): Att => ({
        id: uid(), name: f.name, size: f.size, mime: f.mime, status: "done", from: "drive",
        ref: { file_id: f.id, path: f.path, name: f.name, mime: f.mime, size: f.size, version: f.current_version, action: "attach" },
      }))];
    });
  }, []);

  const remove = useCallback((id: string) => {
    setItems((cur) => {
      const a = cur.find((x) => x.id === id);
      if (a?.preview) { URL.revokeObjectURL(a.preview); urls.current.delete(a.preview); }
      // a file uploaded just for this message and then removed goes to the drive's trash, not left behind
      if (a?.from === "upload" && a.ref?.file_id) void drive.remove(a.ref.file_id).catch(() => {});
      return cur.filter((x) => x.id !== id);
    });
  }, []);
  const clear = useCallback(() => {
    urls.current.forEach((u) => URL.revokeObjectURL(u)); urls.current.clear();
    setItems([]);
  }, []);

  return {
    items, dest, setDest, addFiles, addDrive, remove, clear,
    uploading: items.some((a) => a.status === "uploading"),
    failed: items.some((a) => a.status === "error"),
    refs: items.filter((a) => a.status === "done" && a.ref).map((a) => a.ref as FileRef),
    full: items.length >= MAX_FILES,
  };
}

// ------------------------------------------------------------------ the tray of chips

function Thumb({ a }: { a: Att }) {
  if (a.preview) {
    // eslint-disable-next-line @next/next/no-img-element
    return <img src={a.preview} alt="" className="h-9 w-9 shrink-0 rounded-md object-cover" />;
  }
  const ext = (a.name.split(".").pop() || "").slice(0, 4).toUpperCase();
  return <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-accent-soft text-[9.5px] font-bold tracking-tight text-accent">{ext || <Icon name="doc" size={14} />}</span>;
}

export function AttachmentTray({ items, onRemove, onRetry }: { items: Att[]; onRemove: (id: string) => void; onRetry?: (id: string) => void }) {
  const { t } = useT();
  if (!items.length) return null;
  return (
    <ul data-testid="chat-attachments" className="flex flex-wrap gap-2">
      {items.map((a) => (
        <li key={a.id} data-status={a.status} title={a.error ? t(`att.err.${a.error}`) : a.name}
          className={`group flex max-w-[230px] items-center gap-2 rounded-xl border bg-panel py-1 pl-1 pr-1.5 text-[11px] shadow-soft ${a.status === "error" ? "border-err/50" : "border-line"}`}>
          <Thumb a={a} />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium text-ink">{a.name}</span>
            <span className={`block truncate text-[10.5px] ${a.status === "error" ? "text-err" : "text-mute"}`}>
              {a.status === "uploading" ? t("att.uploading") : a.status === "error" ? t(`att.err.${a.error ?? "upload"}`) : a.from === "drive" ? `${t("att.fromDrive")} · ${fmtSize(a.size)}` : fmtSize(a.size)}
            </span>
          </span>
          {a.status === "uploading" && <span aria-hidden className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-accent/30 border-t-accent" />}
          <button type="button" aria-label={t("chat.attach.remove", { name: a.name })} onClick={() => onRemove(a.id)}
            className="shrink-0 rounded-md p-1 text-mute transition hover:bg-mute/10 hover:text-ink"><Icon name="x" size={11} /></button>
          {a.status === "error" && onRetry && <span className="sr-only">{a.error}</span>}
        </li>
      ))}
    </ul>
  );
}

// ------------------------------------------------------------------ the drive picker

/** Browse the drive's folders: pick files to attach, or pick the folder new attachments are saved in. */
export function DrivePicker({ mode, start = "/Shared", onClose, onFiles, onFolder }: {
  mode: "files" | "folder"; start?: string; onClose: () => void; onFiles?: (f: DriveFile[]) => void; onFolder?: (path: string) => void;
}) {
  const { t } = useT();
  const [path, setPath] = useState(start);
  const [listing, setListing] = useState<Listing | null>(null);
  const [err, setErr] = useState(false);
  const [sel, setSel] = useState<Map<string, DriveFile>>(new Map());
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback((p: string) => {
    setErr(false); setListing(null);
    drive.list(p).then(setListing).catch(() => setErr(true));
  }, []);
  useEffect(() => load(path), [path, load]);

  const crumbs = useMemo(() => {
    const parts = path.split("/").filter(Boolean);
    return [{ name: t("att.pick.root"), path: "/" }, ...parts.map((n, i) => ({ name: n, path: "/" + parts.slice(0, i + 1).join("/") }))];
  }, [path, t]);

  const toggle = (f: DriveFile) => setSel((m) => { const n = new Map(m); if (n.has(f.id)) n.delete(f.id); else n.set(f.id, f); return n; });
  const createFolder = async () => {
    const n = safe(newName.trim());
    if (!n || path === "/") return;
    setBusy(true);
    try { await drive.mkdir(`${path}/${n}`); setNewName(""); load(path); } catch { setErr(true); }
    setBusy(false);
  };

  if (typeof document === "undefined") return null;
  // rendered on <body>: inside the composer a blurred / transformed ancestor would pin it to the composer's box
  return createPortal(
    <div role="dialog" aria-modal="true" aria-label={mode === "files" ? t("att.pick.titleFiles") : t("att.pick.titleFolder")} data-testid="drive-picker"
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/40 p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className="flex max-h-[80vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-line bg-panel shadow-pop">
        <header className="flex items-center justify-between border-b border-line px-4 py-3">
          <h2 className="text-[14px] font-semibold text-ink">{mode === "files" ? t("att.pick.titleFiles") : t("att.pick.titleFolder")}</h2>
          <button type="button" onClick={onClose} aria-label={t("att.close")} className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={16} /></button>
        </header>
        <nav aria-label={t("att.pick.path")} className="flex flex-wrap items-center gap-1 border-b border-line bg-panel2/50 px-4 py-2 text-[12px]">
          {crumbs.map((c, i) => (
            <span key={c.path} className="flex items-center gap-1">
              {i > 0 && <span className="text-mute">/</span>}
              <button type="button" onClick={() => setPath(c.path)} className={`rounded px-1 py-0.5 hover:bg-panel2 ${i === crumbs.length - 1 ? "font-semibold text-ink" : "text-accent"}`}>{c.name}</button>
            </span>
          ))}
        </nav>
        <div className="min-h-[220px] flex-1 overflow-y-auto px-2 py-2">
          {err && <p role="alert" className="px-3 py-6 text-center text-[12px] text-err">{t("att.pick.error")}</p>}
          {!err && !listing && <p className="px-3 py-6 text-center text-[12px] text-mute">{t("att.pick.loading")}</p>}
          {listing && (
            <ul className="space-y-0.5">
              {listing.folders.map((f) => (
                <li key={f.id}>
                  <button type="button" data-testid={`pick-folder-${f.name}`} onClick={() => setPath(f.path)} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] font-medium text-ink hover:bg-panel2">
                    <Icon name="folder" size={15} className="text-accent" /><span className="truncate">{f.name}</span><Icon name="chevron" size={13} className="ml-auto text-mute" />
                  </button>
                </li>
              ))}
              {mode === "files" && listing.files.map((f) => (
                <li key={f.id}>
                  <label className="flex cursor-pointer items-center gap-2 rounded-lg px-3 py-2 text-[13px] text-ink2 hover:bg-panel2">
                    <input type="checkbox" checked={sel.has(f.id)} onChange={() => toggle(f)} className="h-4 w-4 accent-accent" data-testid={`pick-file-${f.name}`} />
                    <Icon name="doc" size={14} className="text-mute" /><span className="min-w-0 flex-1 truncate">{f.name}</span><span className="shrink-0 text-[11px] text-mute">{fmtSize(f.size)}</span>
                  </label>
                </li>
              ))}
              {!listing.folders.length && (mode === "folder" || !listing.files.length) && <li className="px-3 py-6 text-center text-[12px] text-mute">{t("att.pick.empty")}</li>}
            </ul>
          )}
        </div>
        {mode === "folder" && (
          <div className="flex items-center gap-2 border-t border-line px-4 py-2">
            <input value={newName} onChange={(e) => setNewName(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); void createFolder(); } }} placeholder={t("att.pick.newFolder")} aria-label={t("att.pick.newFolder")} disabled={path === "/"}
              className="min-w-0 flex-1 rounded-md border border-line bg-panel2 px-2.5 py-1.5 text-[12px] text-ink outline-none focus:border-accent disabled:opacity-50" />
            <Btn onClick={() => void createFolder()} disabled={busy || !newName.trim() || path === "/"}>{t("att.pick.create")}</Btn>
          </div>
        )}
        <footer className="flex items-center justify-between gap-2 border-t border-line bg-panel2/60 px-4 py-3">
          <span className="truncate text-[11.5px] text-mute">{mode === "files" ? t("att.pick.selected", { n: sel.size }) : path}</span>
          <div className="flex gap-2">
            <Btn onClick={onClose}>{t("att.close")}</Btn>
            {mode === "files"
              ? <Btn kind="primary" disabled={!sel.size} onClick={() => { onFiles?.([...sel.values()]); onClose(); }} data-testid="pick-attach">{t("att.pick.attach")}</Btn>
              : <Btn kind="primary" disabled={path === "/"} onClick={() => { onFolder?.(path); onClose(); }} data-testid="pick-here">{t("att.pick.here")}</Btn>}
          </div>
        </footer>
      </div>
    </div>,
    document.body,
  );
}

// ------------------------------------------------------------------ the "+" menu

export function AttachMenu({ dest, full, onUpload, onFolderUpload, onDrive, onPickDest, disabled }: {
  dest: string; full: boolean; disabled?: boolean; onUpload: () => void; onFolderUpload: () => void; onDrive: () => void; onPickDest: () => void;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", close); document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", esc); };
  }, [open]);
  const item = (label: string, hint: string | undefined, fn: () => void, testid: string, icon: "plus" | "folder" | "doc" | "layers") => (
    <button type="button" role="menuitem" data-testid={testid} disabled={full && icon !== "folder" && testid !== "attach-dest"} onClick={() => { setOpen(false); fn(); }}
      className="flex w-full items-start gap-2.5 rounded-lg px-2.5 py-2 text-left transition hover:bg-panel2 disabled:opacity-40">
      <Icon name={icon} size={15} className="mt-0.5 shrink-0 text-accent" />
      <span className="min-w-0"><span className="block text-[12.5px] font-medium text-ink">{label}</span>{hint && <span className="block truncate text-[11px] text-mute">{hint}</span>}</span>
    </button>
  );
  return (
    <div ref={ref} className="relative">
      <button type="button" data-testid="chat-attach" title={t("chat.attach.title")} aria-label={t("chat.attach.title")} aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen((v) => !v)}
        className="rounded-lg border border-line bg-panel2 p-2 text-mute hover:border-accent hover:text-ink disabled:opacity-40"><Icon name="plus" size={14} /></button>
      {open && (
        <div role="menu" className="absolute bottom-full left-0 z-50 mb-2 w-[270px] rounded-xl border border-line bg-panel p-1.5 shadow-pop">
          {item(t("att.menu.upload"), t("att.menu.uploadHint", { n: MAX_FILES, mb: MAX_BYTES >> 20 }), onUpload, "attach-upload", "plus")}
          {item(t("att.menu.folder"), t("att.menu.folderHint"), onFolderUpload, "attach-folder", "folder")}
          {item(t("att.menu.drive"), t("att.menu.driveHint"), onDrive, "attach-drive", "layers")}
          <div className="my-1 border-t border-line" />
          {item(t("att.menu.dest"), dest, onPickDest, "attach-dest", "folder")}
        </div>
      )}
    </div>
  );
}

/** Overlay shown while files are dragged over the composer. */
export function DropOverlay({ dest }: { dest: string }) {
  const { t } = useT();
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 z-40 flex items-center justify-center rounded-xl border-2 border-dashed border-accent bg-accent-soft/90 px-4 text-center text-[12.5px] font-semibold text-accent">
      {t("att.drop", { path: dest })}
    </div>
  );
}
