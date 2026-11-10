"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { folderAccess, type AccessEntry, type AccessLevel } from "@/lib/folderAccess";
import { LEVELS, displayPath, levelKey, sortEntries, validFolder } from "@/lib/folderAccessLogic";
import { useDriveNav } from "@/lib/drive";
import { rootLabel } from "@/lib/officeFoldersLogic";
import { Btn, Card, Empty } from "../ui";
import { Icon } from "../icons";
import { DrivePicker } from "../chat/attachments";

const ROOTS = ["Agents", "Projects", "Clients", "Shared", "Offices"] as const;

/** "Carpetas que puede ver": the folders an agent sees, each with Ninguno / Leer / Editar (inherited downwards).
 * Admins and the head of the agent's team change them; everybody else sees them read-only. */
export function FolderAccessSection({ id }: { id: string }) {
  const { t } = useT();
  const show = useDriveNav((s) => s.show);
  const [entries, setEntries] = useState<AccessEntry[] | null>(null);
  const [canManage, setCanManage] = useState(false);
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const roots = Object.fromEntries(ROOTS.map((r) => [r, rootLabel(r, t)]));

  const load = useCallback(async () => {
    try {
      const r = await folderAccess.agent(id);
      setEntries(sortEntries(r.entries)); setCanManage(r.can_manage); setError("");
    } catch { setEntries([]); setError(t("access.loadError")); }
  }, [id, t]);
  useEffect(() => { setEntries(null); load(); }, [load]);

  const change = async (path: string, level: AccessLevel | "default") => {
    setBusy(path); setError("");
    try { const r = await folderAccess.set(id, path, level); setEntries(sortEntries(r.entries)); }
    catch { setError(t("access.saveError")); }
    setBusy("");
  };
  const add = async (path: string) => {
    setPicking(false);
    if (!validFolder(path, id)) { setError(t("access.invalidFolder")); return; }
    await change(path, "read");
  };

  return (
    <Card title={t("access.agent.title")} right={canManage ? <Btn data-testid="access-add" className="inline-flex items-center gap-1" onClick={() => setPicking(true)}><Icon name="plus" size={12} />{t("access.add")}</Btn> : undefined}>
      <div data-testid="folder-access" className="space-y-2 p-3">
        <p className="text-[11.5px] leading-snug text-mute">{t("access.agent.help")}</p>
        {error && <p role="alert" className="rounded-lg bg-red-500/10 px-2 py-1.5 text-[11.5px] text-red-700">{error}</p>}
        {entries === null ? <Empty>{t("drive.loading")}</Empty> : !entries.length ? <Empty>{t("access.empty")}</Empty> : (
          <ul className="space-y-1.5">
            {entries.map((e) => (
              <li key={e.path} data-testid={`access-row-${e.path}`} className="space-y-1.5 rounded-[10px] border border-line px-2.5 py-2">
                <div className="flex items-start gap-2">
                <button type="button" onClick={() => show(e.path)} className="flex min-w-0 flex-1 items-center gap-2 text-left text-[12.5px] text-ink hover:text-accent" title={t("desk.openInDrive")}>
                  <Icon name="folder" size={14} className="shrink-0 text-accent" />
                  <span className="min-w-0 break-all">{displayPath(e.path, roots)}</span>
                </button>
                <span className="shrink-0 pt-0.5 text-[10.5px] text-mute">{e.fixed ? t("access.source.fixed") : t(`access.source.${e.source}`)}</span>
                </div>
                <div className="flex flex-wrap items-center gap-2 pl-[22px]">
                {canManage && !e.fixed ? (
                  <div role="radiogroup" aria-label={t("access.levelFor", { path: e.path })} className="flex overflow-hidden rounded-lg border border-line">
                    {LEVELS.map((l) => (
                      <button key={l} type="button" role="radio" aria-checked={e.level === l} disabled={busy === e.path}
                        data-testid={`access-${e.path}-${l}`} onClick={() => { if (e.level !== l) change(e.path, l); }}
                        className={`px-2 py-1 text-[11px] font-medium transition ${e.level === l ? "bg-accent text-white" : "bg-panel text-ink2 hover:bg-panel2"}`}>
                        {t(levelKey(l))}
                      </button>
                    ))}
                  </div>
                ) : (
                  <span className={`rounded-md px-2 py-0.5 text-[11px] font-medium ${e.level === "none" ? "bg-mute/10 text-mute" : "bg-accent/10 text-accent"}`}>{t(levelKey(e.level))}</span>
                )}
                {canManage && e.source === "manual" && (
                  <button type="button" data-testid={`access-reset-${e.path}`} disabled={busy === e.path} onClick={() => change(e.path, "default")}
                    className="text-[11px] text-mute underline-offset-2 hover:text-ink hover:underline">{t("access.reset")}</button>
                )}
                </div>
              </li>
            ))}
          </ul>
        )}
        {!canManage && entries !== null && <p className="text-[11px] text-mute">{t("access.readOnly")}</p>}
      </div>
      {picking && <DrivePicker mode="folder" start="/" title={t("access.pick.title")} confirmLabel={t("access.pick.confirm")} onClose={() => setPicking(false)} onFolder={add} />}
    </Card>
  );
}
