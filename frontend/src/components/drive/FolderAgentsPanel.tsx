"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { folderAccess, type AccessLevel, type FolderAgent } from "@/lib/folderAccess";
import { LEVELS, countByLevel, levelKey } from "@/lib/folderAccessLogic";
import { Empty } from "../ui";
import { Icon } from "../icons";
import { Avatar } from "../chat/ChatThread";

/** "Qué agentes la ven": for one folder, the level of every agent (inherited from above or set here). People who may
 * change an agent's folders (admins, the head of its team) change the level right here. */
export function FolderAgentsPanel({ path, label, onClose }: { path: string; label: string; onClose: () => void }) {
  const { t } = useT();
  const [agents, setAgents] = useState<FolderAgent[] | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try { setAgents(await folderAccess.folder(path)); setError(""); } catch { setAgents([]); setError(t("access.loadError")); }
  }, [path, t]);
  useEffect(() => { setAgents(null); load(); }, [load]);

  const change = async (a: FolderAgent, level: AccessLevel) => {
    setBusy(a.agent_id); setError("");
    try { await folderAccess.set(a.agent_id, path, level); await load(); } catch { setError(t("access.saveError")); }
    setBusy("");
  };
  const n = countByLevel(agents ?? []);

  return (
    <aside data-testid="folder-agents" aria-label={t("access.folder.title")} className="flex w-[min(380px,100vw)] shrink-0 flex-col border-l border-line bg-panel">
      <header className="flex items-start justify-between gap-2 border-b border-line px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-[14px] font-semibold text-ink">{t("access.folder.title")}</h2>
          <p className="truncate text-[11.5px] text-mute">{label}</p>
        </div>
        <button type="button" onClick={onClose} aria-label={t("common.close")} className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={16} /></button>
      </header>
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto p-3">
        <p className="text-[11.5px] leading-snug text-mute">{t("access.folder.help")}</p>
        {agents && agents.length > 0 && (
          <p data-testid="folder-agents-counts" className="text-[11.5px] text-ink2">{t("access.folder.counts", { edit: n.edit, read: n.read, none: n.none })}</p>
        )}
        {error && <p role="alert" className="rounded-lg bg-red-500/10 px-2 py-1.5 text-[11.5px] text-red-700">{error}</p>}
        {agents === null ? <Empty>{t("drive.loading")}</Empty> : !agents.length ? <Empty>{t("access.folder.empty")}</Empty> : (
          <ul className="space-y-1.5">
            {agents.map((a) => (
              <li key={a.agent_id} data-testid={`folder-agent-${a.agent_id}`} className="flex items-center gap-2 rounded-[10px] border border-line px-2.5 py-2">
                <Avatar id={a.agent_id} size={26} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12.5px] font-medium text-ink">{a.name}</div>
                  <div className="text-[10.5px] text-mute">{t(`access.source.${a.source}`)}</div>
                </div>
                {a.can_manage ? (
                  <select value={a.level} disabled={busy === a.agent_id} aria-label={t("access.levelFor", { path: a.name })} data-testid={`folder-agent-level-${a.agent_id}`}
                    onChange={(e) => change(a, e.target.value as AccessLevel)}
                    className="rounded-lg border border-line bg-panel px-2 py-1 text-[11.5px] text-ink outline-none focus:border-accent">
                    {LEVELS.map((l) => <option key={l} value={l}>{t(levelKey(l))}</option>)}
                  </select>
                ) : (
                  <span className={`rounded-md px-2 py-0.5 text-[11px] font-medium ${a.level === "none" ? "bg-mute/10 text-mute" : "bg-accent/10 text-accent"}`}>{t(levelKey(a.level))}</span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}
