"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, type DriveFolder } from "@/lib/drive";
import {
  covers, levelOf, levelLabelKey, maxLevelFor, relativePath, setEntry,
  type Bounds, type Level, type LibraryEntry, type Package,
} from "@/lib/positionsLogic";
import { Icon } from "../icons";

const same = (a: string, b: string) => a.replace(/\/+$/, "").toLowerCase() === b.replace(/\/+$/, "").toLowerCase();
const base = (p: string) => p.split("/").filter(Boolean).pop() ?? p;

/** Folder tree of the department's library: only the roots the department may use (its own folder, the shared one
 * and what is shared into it). Per folder: Leer / Editar, or exclude a subfolder of one already included. Never
 * offers more than the superior has (`superior`) and never Editar on what is shared into the department. */
export function FolderTreePicker({ value, onChange, bounds, officeName, superior, readOnly = false }: {
  value: LibraryEntry[]; onChange: (next: LibraryEntry[]) => void; bounds: Bounds; officeName: string; superior: Package | null; readOnly?: boolean;
}) {
  const { t } = useT();
  const roots = [
    { path: bounds.office_folder, label: t("pos.lib.rootOffice", { name: officeName }) },
    { path: bounds.shared, label: t("pos.lib.rootShared") },
    ...bounds.shared_in.map((p) => ({ path: p, label: t("pos.lib.rootSharedIn", { name: base(p) }) })),
  ];
  const labelOf = (path: string) => {
    const r = roots.find((x) => covers(x.path, path));
    if (!r) return path;
    const rel = relativePath(path, bounds);
    return rel ? `${r.label} / ${rel.split("/").join(" / ")}` : r.label;
  };

  const chosen = [...value].sort((a, b) => a.path.localeCompare(b.path));
  return (
    <div data-testid="folder-tree-picker" className="space-y-3">
      <div>
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("pos.lib.chosen")}</div>
        {!chosen.length ? <p className="text-[12px] text-mute">{t("pos.lib.empty")}</p> : (
          <ul className="space-y-1">
            {chosen.map((e) => (
              <li key={e.path} className="flex items-center gap-2 rounded-lg bg-panel2 px-2.5 py-1.5 text-[12px]">
                <Icon name="folder" size={13} className="shrink-0 text-accent" />
                <span className="min-w-0 flex-1 break-words text-ink">{labelOf(e.path)}</span>
                <span className={`shrink-0 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium ${e.level === "none" ? "bg-mute/10 text-mute" : "bg-accent-soft text-accent"}`}>
                  {e.level === "none" ? t("pos.lib.excluded") : t(levelLabelKey(e.level))}
                </span>
                {!readOnly && <button type="button" aria-label={`${t("pos.lib.remove")} ${labelOf(e.path)}`} onClick={() => onChange(setEntry(value, e.path, null))} className="shrink-0 text-[11px] text-mute hover:text-ink">{t("pos.lib.remove")}</button>}
              </li>
            ))}
          </ul>
        )}
      </div>
      <ul className="space-y-1 rounded-[10px] border border-line p-1.5">
        {roots.map((r) => (
          <Node key={r.path} path={r.path} name={r.label} depth={0} value={value} onChange={onChange} bounds={bounds} superior={superior} readOnly={readOnly} startOpen={same(r.path, bounds.office_folder)} />
        ))}
      </ul>
      {bounds.shared_in.length > 0 && <p className="text-[11px] text-mute">{t("pos.lib.sharedReadOnly")}</p>}
    </div>
  );
}

function Node({ path, name, depth, value, onChange, bounds, superior, readOnly, startOpen = false }: {
  path: string; name: string; depth: number; value: LibraryEntry[]; onChange: (n: LibraryEntry[]) => void; bounds: Bounds; superior: Package | null; readOnly: boolean; startOpen?: boolean;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [kids, setKids] = useState<DriveFolder[] | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");

  const load = async () => {
    setState("loading");
    try { const l = await drive.list(path); setKids([...(l.folders ?? [])].sort((a, b) => a.name.localeCompare(b.name))); setState("idle"); }
    catch { setState("error"); }
  };
  const toggle = () => { const next = !open; setOpen(next); if (next && kids === null && state !== "loading") void load(); };
  useEffect(() => { if (startOpen) { setOpen(true); void load(); } }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const own = value.find((e) => same(e.path, path));
  const above = value.filter((e) => !same(e.path, path));
  const inherited: Level = above.length ? levelOf(above, path, bounds) : "none";
  const max = maxLevelFor(superior, path, bounds);
  const pick = (level: Level | null) => onChange(setEntry(value, path, level));
  const seg = (l: "read" | "edit") => {
    const blocked = max === "none" || (l === "edit" && max !== "edit");
    const on = own?.level === l;
    return (
      <button key={l} type="button" role="radio" aria-checked={on} disabled={blocked && !on}
        title={blocked ? (max === "none" ? t("pos.lib.superiorNo") : readOnlyShared(path, bounds) ? t("pos.lib.sharedReadOnly") : t("pos.lib.superiorLimit")) : undefined}
        onClick={() => pick(on ? null : l)}
        className={`px-2.5 py-1 text-[11.5px] font-medium transition disabled:cursor-not-allowed disabled:opacity-40 ${on ? "bg-accent text-white" : "bg-panel text-ink2 hover:bg-panel2"}`}>
        {t(levelLabelKey(l))}
      </button>
    );
  };

  return (
    <li>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg px-1.5 py-1 hover:bg-panel2/60" style={{ paddingLeft: 6 + depth * 14 }}>
        <button type="button" onClick={toggle} aria-expanded={open} aria-label={t(open ? "pos.lib.close" : "pos.lib.open", { name })}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left text-[12.5px] text-ink">
          <Icon name="chevron" size={12} className={`shrink-0 text-mute transition ${open ? "rotate-90" : ""}`} />
          <Icon name="folder" size={14} className="shrink-0 text-accent" />
          <span className="min-w-0 break-words">{name}</span>
        </button>
        {readOnly ? (
          own && <span className="text-[11px] text-mute">{own.level === "none" ? t("pos.lib.excluded") : t(levelLabelKey(own.level))}</span>
        ) : own && own.level === "none" ? (
          <>
            <span className="rounded-md bg-mute/10 px-1.5 py-0.5 text-[11px] text-mute">{t("pos.lib.excluded")}</span>
            <button type="button" onClick={() => pick(null)} className="text-[11px] text-accent hover:underline">{t("pos.lib.unexclude")}</button>
          </>
        ) : !own && inherited !== "none" ? (
          <>
            <span className="text-[11px] text-mute">{t("pos.lib.inherits", { level: t(levelLabelKey(inherited)) })}</span>
            <button type="button" onClick={() => pick("none")} className="text-[11px] text-accent hover:underline">{t("pos.lib.exclude")}</button>
          </>
        ) : (
          <div role="radiogroup" aria-label={t("pos.lib.levelFor", { name })} className="flex overflow-hidden rounded-lg border border-line">
            {seg("read")}{seg("edit")}
          </div>
        )}
      </div>
      {open && (
        <ul className="space-y-0.5">
          {state === "loading" && <li className="py-1 text-[11.5px] text-mute" style={{ paddingLeft: 26 + depth * 14 }}>{t("pos.lib.loading")}</li>}
          {state === "error" && (
            <li className="py-1 text-[11.5px] text-err" style={{ paddingLeft: 26 + depth * 14 }}>
              {t("pos.lib.loadError")} <button type="button" onClick={() => void load()} className="text-accent underline">{t("pos.retry")}</button>
            </li>
          )}
          {state === "idle" && kids?.length === 0 && <li className="py-1 text-[11.5px] text-mute" style={{ paddingLeft: 26 + depth * 14 }}>{t("pos.lib.noSub")}</li>}
          {kids?.map((k) => <Node key={k.path} path={k.path} name={k.name} depth={depth + 1} value={value} onChange={onChange} bounds={bounds} superior={superior} readOnly={readOnly} />)}
        </ul>
      )}
    </li>
  );
}

const readOnlyShared = (path: string, b: Bounds) => b.shared_in.some((r) => covers(r, path));
