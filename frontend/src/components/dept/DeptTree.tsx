"use client";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { drive, type DriveFolder, type DriveShare } from "@/lib/drive";
import { pathKey, pendingUnder } from "@/lib/deptLogic";
import { Icon } from "../icons";

/**
 * The folders of one place of the Files screen (a department, Compartido con todos, a project...). Each level is listed
 * with `drive.list` the first time it is expanded; the current folder is highlighted in soft blue. A folder that holds
 * something pending shows how many. `refresh` changes when files changed: the open levels are listed again.
 * `label` names a folder in plain words, `hide` leaves the system's folders out and `leaf` marks a folder whose
 * subfolders are not browsed one by one (the chat's attachments, grouped by date in the list).
 */
export function DeptTree({ root, rootName, rootIcon, current, pending, onOpen, refresh, shared, sharedCurrent, ownerName, onOpenShared, label, hide, leaf }: {
  root: string; rootName: string; rootIcon?: ReactNode; current: string; pending: Map<string, unknown>; onOpen: (path: string) => void; refresh?: string;
  /** "Compartido conmigo": what other departments share with this one (D4); `sharedCurrent` is the one being browsed (the own folders are then not highlighted). Left out: no such block. */
  shared?: DriveShare[]; sharedCurrent?: string | null; ownerName?: (officeId: string) => string; onOpenShared?: (s: DriveShare) => void;
  label?: (f: DriveFolder) => string; hide?: (path: string) => boolean; leaf?: (path: string) => boolean;
}) {
  const { t } = useT();
  const [children, setChildren] = useState<Record<string, DriveFolder[]>>({});
  // open levels: key -> real path (the key is lowercase; the drive is asked with the real path)
  const [open, setOpen] = useState<Record<string, string>>(() => ({ [pathKey(root)]: root }));

  const load = useCallback(async (path: string) => {
    const l = await drive.list(path).catch(() => null);
    setChildren((c) => ({ ...c, [pathKey(path)]: (l?.folders ?? []).filter((f) => !hide?.(f.path)) }));
  }, [hide]);

  // the root and every ancestor of the current folder are open (and listed)
  useEffect(() => {
    const segs = pathKey(current).startsWith(pathKey(root)) ? current.slice(root.length).split("/").filter(Boolean) : [];
    const chain = [root];
    for (const s of segs) chain.push(chain[chain.length - 1] + "/" + s);
    const listed = chain.filter((p, i) => i === 0 || !leaf?.(chain[i - 1]));
    setOpen((o) => { const n = { ...o }; listed.forEach((p) => { n[pathKey(p)] = p; }); return n; });
    listed.forEach((p) => { if (!leaf?.(p)) void load(p); });
  }, [root, current, load, leaf]);
  useEffect(() => {
    if (refresh) Object.values(open).forEach((p) => { if (!leaf?.(p)) void load(p); });
  }, [refresh]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggle = (path: string) => {
    const k = pathKey(path);
    setOpen((o) => { const n = { ...o }; if (n[k]) delete n[k]; else n[k] = path; return n; });
    if (!children[k] && !leaf?.(path)) void load(path);
  };

  const node = (path: string, name: string, depth: number, icon?: ReactNode) => {
    const k = pathKey(path);
    const isLeaf = !!leaf?.(path);
    const isOpen = !!open[k] && !isLeaf;
    const here = !sharedCurrent && (k === pathKey(current) || (isLeaf && pathKey(current).startsWith(k + "/")));
    const kids = isLeaf ? [] : children[k];
    const n = pendingUnder(pending, path);
    return (
      <li key={k}>
        <div className={`flex min-h-10 items-center gap-0.5 rounded-md pr-1.5 text-[13px] md:min-h-8 ${here ? "bg-accent-soft font-semibold text-accent" : "text-ink hover:bg-panel2"}`}
          style={{ paddingLeft: 2 + depth * 14 }}>
          <button type="button" onClick={() => toggle(path)} aria-label={t("dept.expand", { name })} title={t("dept.expand", { name })} aria-expanded={isOpen}
            tabIndex={kids && !kids.length ? -1 : 0}
            className={`flex h-7 w-6 shrink-0 items-center justify-center rounded text-mute hover:text-ink ${kids && !kids.length ? "invisible" : ""}`}>
            <Icon name="chevron" size={13} className={`transition-transform ${isOpen ? "rotate-90" : ""}`} />
          </button>
          <button type="button" data-testid={`dept-tree-${name}`} aria-current={here ? "true" : undefined}
            onClick={() => { onOpen(path); if (!isOpen) toggle(path); }}
            className="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left">
            {icon ?? <Icon name="folder" size={14} className={`shrink-0 ${here ? "text-accent" : "text-mute"}`} />}
            <span className="min-w-0 flex-1 truncate" title={name}>{name}</span>
            {n > 0 && <span title={t("dept.reviewUnder", { n })} className="shrink-0 rounded bg-warn/10 px-1.5 text-[10.5px] font-semibold text-warn">{n}</span>}
          </button>
        </div>
        {isOpen && kids && kids.length > 0 && <ul>{kids.map((f) => node(f.path, label ? label(f) : f.name, depth + 1))}</ul>}
      </li>
    );
  };

  return (
    <nav aria-label={t("dept.folders")} data-testid="dept-tree">
      <ul className="space-y-px">{node(root, rootName, 0, rootIcon)}</ul>
      {shared && (
        <div data-testid="dept-tree-shared" className="mt-2 border-t border-line pt-2">
          <div className="px-2 pb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("dept.shared.title")}</div>
          {shared.length === 0 ? <p className="px-2 py-1 text-[12px] text-mute">{t("dept.shared.empty")}</p> : (
            <ul className="space-y-px">
              {shared.map((s) => {
                const here = !!sharedCurrent && pathKey(s.path) === pathKey(sharedCurrent);
                return (
                  <li key={s.id}>
                    <button type="button" data-testid={`dept-shared-${s.name}`} aria-current={here ? "true" : undefined} onClick={() => onOpenShared?.(s)}
                      className={`flex min-h-10 w-full items-center gap-1.5 rounded-md px-2 text-left text-[13px] md:min-h-8 ${here ? "bg-accent-soft font-semibold text-accent" : "text-ink hover:bg-panel2"}`}>
                      <Icon name={s.kind === "folder" ? "folder" : "doc"} size={14} className={`shrink-0 ${here ? "text-accent" : "text-mute"}`} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate" title={s.name}>{s.name}</span>
                        <span className="block truncate text-[11px] font-normal text-mute">{t("dept.shared.from", { office: ownerName?.(s.owner_office_id) ?? "" })}</span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}
    </nav>
  );
}
