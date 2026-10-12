"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { skillsApi, useSkillsNav, type AgentSkill, type Skill } from "@/lib/skills";
import { useCan } from "@/lib/session";
import { Btn, Card } from "../ui";
import { StatusBadge } from "./shared";

/** The skills of an agent (Work profile): direct assignments and the defaults of its role. Assigning one
 * changes what the agent is told exists; it never changes what the agent can do. */
export function AgentSkillsCard({ id }: { id: string }) {
  const { t } = useT();
  const canAssign = useCan("agents:write");
  const [mine, setMine] = useState<AgentSkill[] | null>(null);
  const [all, setAll] = useState<Skill[]>([]);
  const [pick, setPick] = useState("");
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      setMine(await skillsApi.agentSkills(id)); setErr("");
      setAll(await skillsApi.list().catch(() => []));
    } catch { setErr(t("skills.error.load")); }
  }, [id, t]);
  useEffect(() => { setMine(null); load(); }, [load]);

  const free = all.filter((s) => !(mine ?? []).some((m) => m.id === s.id));
  const run = async (fn: () => Promise<AgentSkill[]>) => { setErr(""); try { setMine(await fn()); } catch { setErr(t("skills.error.generic")); } };

  return (
    <Card title={t("skills.agentCard.title")} right={<button type="button" data-testid="agent-skills-open-library" className="text-[11px] font-medium text-accent" onClick={() => useSkillsNav.getState().show(null)}>{t("skills.agentCard.library")}</button>}>
      <div data-testid="agent-skills-card">
        <p className="mb-2 text-[11px] text-mute">{t("skills.agentCard.help")}</p>
        {mine === null ? <div className="text-xs text-mute">{err || t("skills.loading")}</div> : mine.length === 0 ? <div className="text-xs text-mute">{t("skills.agentCard.none")}</div> : (
          <ul className="space-y-1.5">
            {mine.map((s) => (
              <li key={s.id} data-testid={`agent-skill-${s.name}`} data-via={s.via} data-servable={s.servable} className="rounded-md bg-panel2 px-2.5 py-1.5">
                <div className="flex items-center gap-2">
                  <button type="button" className="min-w-0 flex-1 truncate text-left font-mono text-[12px] font-semibold text-ink hover:text-accent" onClick={() => useSkillsNav.getState().show(s.id)}>{s.name}</button>
                  <StatusBadge status={s.status} />
                  <span className="shrink-0 rounded border border-line px-1.5 py-0.5 text-[9.5px] text-mute">{t(`skills.via.${s.via}`)}</span>
                  {canAssign && s.via === "agent" && <button type="button" data-testid={`agent-skill-remove-${s.name}`} aria-label={t("skills.agents.remove")} className="text-[11px] text-mute hover:text-err" onClick={() => run(() => skillsApi.unassign(id, s.id))}>✕</button>}
                </div>
                {!s.servable && <div className="mt-0.5 text-[10.5px] text-warn">{t("skills.agentCard.notServed")}</div>}
              </li>
            ))}
          </ul>
        )}
        {canAssign && (
          <div className="mt-3 flex gap-2">
            <select data-testid="agent-skill-select" aria-label={t("skills.agentCard.pick")} value={pick} onChange={(e) => setPick(e.target.value)} className="min-w-0 flex-1 rounded-lg border border-line bg-panel2 px-2 py-1.5 text-xs">
              <option value="">{t("skills.agentCard.pick")}</option>
              {free.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
            <Btn kind="primary" data-testid="agent-skill-assign" disabled={!pick} onClick={() => run(async () => { const r = await skillsApi.assign(id, pick); setPick(""); return r; })}>{t("skills.agents.assign")}</Btn>
          </div>
        )}
        {err && mine !== null && <div className="mt-2 text-xs text-err">{err}</div>}
      </div>
    </Card>
  );
}
