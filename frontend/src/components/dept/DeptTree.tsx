"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, type DriveFolder } from "@/lib/drive";
import { pathKey, pendingUnder } from "@/lib/deptLogic";
import { Icon } from "../icons";

/**
 * The department's folders (left of the department's Drive). Each level is listed with `drive.list` the first time it is
 * expanded; the current folder is highlighted in soft blue. A folder that holds something pending shows how many.
 * `refresh` changes when files changed: the open levels are listed again.
 */
export function DeptTree({ root, rootName, current, pending, onOpen, refresh }: {
  root: string; rootName: string; current: string; pending: Map<string, unknown>; onOpen: (path: string) => void; refresh?: string;
}) {
  const { t } = useT();
  const [children, setChildren] = useState<Record<string, DriveFolder[]>>({});
  // open levels: key -> real path (the key is lowercase; the drive is asked with the real path)
  const [open, setOpen] = useState<Record<string, string>>(() => ({ [pathKey(root)]: root }));

  const load = useCallback(async (path: string) => {
    const l = await drive.list(path).catch(() => null);
    setChildren((c) => ({ ...c, [pathKey(path)]: l?.folders ?? [] }));
  }, []);

  // the root and every ancestor of the current folder are open (and listed)
  useEffect(() => {
    const segs = current.slice(root.length).split("/").filter(Boolean);
    const chain = [root];
    for (const s of segs) chain.push(chain[chain.length - 1] + "/" + s);
    setOpen((o) => { const n = { ...o }; chain.forEach((p) => { n[pathKey(p)] = p; }); return n; });
    chain.forEach((p) => { void load(p); });
  }, [root, current, load]);
  useEffect(() => {
    if (refresh) Object.values(open).forEach((p) => { void load(p); });
  }, [refresh]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggle = (path: string) => {
    const k = pathKey(path);
    setOpen((o) => { const n = { ...o }; if (n[k]) delete n[k]; else n[k] = path; return n; });
    if (!children[k]) void load(path);
  };

  const node = (path: string, name: string, depth: number) => {
    const k = pathKey(path);
    const isOpen = !!open[k];
    const here = k === pathKey(current);
    const kids = children[k];
    const n = pendingUnder(pending, path);
    return (
      <li key={k}>
        <div className={`flex min-h-10 items-center gap-0.5 rounded-md pr-1.5 text-[13px] md:min-h-8 ${here ? "bg-accent-soft font-semibold text-accent" : "text-ink hover:bg-panel2"}`}
          style={{ paddingLeft: 2 + depth * 14 }}>
          <button type="button" onClick={() => toggle(path)} aria-label={t("dept.expand", { name })} title={t("dept.expand", { name })} aria-expanded={isOpen}
            className={`flex h-7 w-6 shrink-0 items-center justify-center rounded text-mute hover:text-ink ${kids && !kids.length ? "invisible" : ""}`}>
            <Icon name="chevron" size={13} className={`transition-transform ${isOpen ? "rotate-90" : ""}`} />
          </button>
          <button type="button" data-testid={`dept-tree-${name}`} aria-current={here ? "true" : undefined}
            onClick={() => { onOpen(path); if (!isOpen) toggle(path); }}
            className="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left">
            <Icon name="folder" size={14} className={`shrink-0 ${here ? "text-accent" : "text-mute"}`} />
            <span className="min-w-0 flex-1 truncate" title={name}>{name}</span>
            {n > 0 && <span title={t("dept.reviewUnder", { n })} className="shrink-0 rounded bg-warn/10 px-1.5 text-[10.5px] font-semibold text-warn">{n}</span>}
          </button>
        </div>
        {isOpen && kids && kids.length > 0 && <ul>{kids.map((f) => node(f.path, f.name, depth + 1))}</ul>}
      </li>
    );
  };

  return (
    <nav aria-label={t("dept.folders")} data-testid="dept-tree">
      <ul className="space-y-px">{node(root, rootName, 0)}</ul>
    </nav>
  );
}
