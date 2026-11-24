"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { agentPositionApi, positionError, type AgentPosition } from "@/lib/positions";
import { accessSourceKey, levelLabelKey, viaKey } from "@/lib/positionsLogic";
import { skillsApi, type AgentSkill } from "@/lib/skills";
import { folderAccess, type AccessEntry } from "@/lib/folderAccess";
import { displayPath, sortEntries } from "@/lib/folderAccessLogic";
import { rootLabel } from "@/lib/officeFoldersLogic";
import { Card, Empty } from "../ui";
import { Icon } from "../icons";
import { baseOffice, officeLabel } from "@/lib/agentOfficesLogic";

const ROOTS = ["Agents", "Projects", "Clients", "Shared", "Offices"] as const;

/** "Su puesto" in the agent's profile: the position and whom it reports to (changeable by whoever manages the
 * department), the agent's skills with where each one comes from, and the folders it can look at with the origin of
 * each rule. Skills and folders load on their own: a failure of one never hides the other. */
export type PositionPart = "position" | "skills" | "folders";
export function PositionSection({ id, parts = ["position", "skills", "folders"] }: { id: string; parts?: PositionPart[] }) {
  const { t } = useT();
  const [pos, setPos] = useState<AgentPosition | null>(null);
  const [skills, setSkills] = useState<AgentSkill[] | null>(null);
  const [access, setAccess] = useState<AccessEntry[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const roots = Object.fromEntries(ROOTS.map((r) => [r, rootLabel(r, t)]));

  const loadPosition = useCallback(async () => {
    try { setPos(await agentPositionApi.get(id)); setError(""); } catch { setPos(null); setError(t("pos.agent.loadError")); }
  }, [id, t]);
  const loadRest = useCallback(async () => {
    skillsApi.agentSkills(id).then(setSkills).catch(() => setSkills([]));
    folderAccess.agent(id).then((r) => setAccess(sortEntries(r.entries))).catch(() => setAccess([]));
  }, [id]);
  useEffect(() => { setPos(null); setSkills(null); setAccess(null); void loadPosition(); void loadRest(); }, [loadPosition, loadRest]);

  const change = async (positionId: string) => {
    setBusy(true); setError("");
    try {
      setPos(positionId ? await agentPositionApi.set(id, positionId) : await agentPositionApi.clear(id));
      await loadRest();
    } catch (e) { setError(positionError(e, t)); }
    setBusy(false);
  };

  const current = pos?.position ?? null;
  const boss = current?.reports_to ? pos?.positions.find((p) => p.id === current.reports_to)?.name : "";

  return (
    <Card title={parts.length === 1 && parts[0] === "folders" ? t("pos.agent.folders") : t("pos.agent.title")}>
      <div data-testid="position-section" className="space-y-5">
        {error && <p role="alert" className="rounded-lg bg-red-500/10 px-2.5 py-2 text-[12px] text-red-700">{error}</p>}

        {parts.includes("position") && <div>
          {pos === null ? (!error && <p className="text-[12px] text-mute">{t("pos.loading")}</p>) : (
            <>
              <p data-testid="position-name" className="text-[14px] font-semibold text-ink">{current ? current.name : t("pos.agent.none")}</p>
              <p data-testid="position-office" className="text-[12px] text-mute">{t("pos.agent.office", { name: officeLabel(baseOffice(pos), t("offices.general")) })}</p>
              {boss && <p className="text-[12px] text-mute">{t("pos.agent.reportsTo", { name: boss })}</p>}
              {!pos.office_id ? <p className="mt-1 text-[12px] text-mute">{t("pos.agent.noDept")}</p> : (
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  {pos.can_manage && (
                    <select data-testid="position-select" aria-label={t("pos.agent.change")} disabled={busy} value={current?.id ?? ""} onChange={(e) => void change(e.target.value)}
                      className="min-h-9 min-w-0 max-w-full rounded-lg border border-line bg-panel px-2 text-[12.5px] text-ink outline-none focus:border-accent">
                      <option value="">{t("pos.agent.none")}</option>
                      {pos.positions.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                    </select>
                  )}
                  <Link href={`/d/${encodeURIComponent(pos.office_id)}/puestos`} className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:underline">
                    {t("pos.agent.manage")}<Icon name="arrow" size={11} />
                  </Link>
                </div>
              )}
            </>
          )}
        </div>}

        {parts.includes("skills") && <section aria-labelledby={`pos-skills-${id}`}>
          <h4 id={`pos-skills-${id}`} className="mb-1.5 text-[12px] font-medium text-ink">{t("pos.agent.skills")}</h4>
          {skills === null ? <p className="text-[12px] text-mute">{t("pos.loading")}</p> : !skills.length ? <Empty>{t("pos.agent.skills.none")}</Empty> : (
            <ul data-testid="position-skills" className="space-y-1">
              {skills.map((s) => (
                <li key={s.id} data-via={s.via} className="flex items-center gap-2 rounded-lg bg-panel2 px-2.5 py-1.5">
                  <span className="min-w-0 flex-1 break-words font-mono text-[12px] font-semibold text-ink">{s.name}</span>
                  <span title={s.via === "position" && s.position_name ? t("pos.agent.skills.fromPosition", { name: s.position_name }) : undefined}
                    className={`shrink-0 rounded border px-1.5 py-0.5 text-[10.5px] ${s.via === "position" ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute"}`}>{t(viaKey(s.via))}</span>
                </li>
              ))}
            </ul>
          )}
        </section>}

        {parts.includes("folders") && <section aria-labelledby={`pos-folders-${id}`}>
          <h4 id={`pos-folders-${id}`} className="text-[12px] font-medium text-ink">{t("pos.agent.folders")}</h4>
          <p className="mb-1.5 text-[11.5px] text-mute">{t("pos.agent.folders.help")}</p>
          {access === null ? <p className="text-[12px] text-mute">{t("pos.loading")}</p> : !access.length ? <Empty>{t("pos.agent.folders.none")}</Empty> : (
            <ul data-testid="position-folders" className="space-y-1">
              {access.map((e) => (
                <li key={e.path} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg border border-line px-2.5 py-1.5">
                  <Icon name="folder" size={13} className="shrink-0 text-accent" />
                  <span className="min-w-0 flex-1 break-all text-[12.5px] text-ink">{displayPath(e.path, roots)}</span>
                  <span className={`shrink-0 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium ${e.level === "none" ? "bg-mute/10 text-mute" : "bg-accent-soft text-accent"}`}>{t(levelLabelKey(e.level))}</span>
                  <span className="shrink-0 text-[10.5px] text-mute">{t(accessSourceKey(e.source, e.fixed))}</span>
                </li>
              ))}
            </ul>
          )}
        </section>}
      </div>
    </Card>
  );
}
