"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { agentList, isProfession, PROFESSIONS, type Profession, type Welcome, type WelcomePlan } from "@/lib/teamsLogic";
import { teamsApi } from "@/lib/teamsApi";
import { Icon, type IconName } from "../icons";

/**
 * The E1 part of the welcome (docs/architecture/equipos-oficinas.md §6.4): what this person's case is (personal office,
 * responsible of the company, responsible of a team, member), their agents and team, and three first steps for that case.
 * A person who came in without a team (verified domain, single sign-on) picks it here.
 */
const ICONS: IconName[] = ["chat", "users", "shield"];

export function TeamWelcomeBody({ w, plan, onPicked }: { w: Welcome; plan: WelcomePlan; onPicked: (w: Welcome) => void }) {
  const { t } = useT();
  const vars = { org: w.org_name, team: w.team?.name ?? "", head: w.team?.head_name ?? "", by: w.invited_by };
  return (
    <div data-testid="team-welcome" data-variant={plan.variant} className="relative mt-4 grid gap-2 text-left">
      <p className="text-center text-[13.5px] leading-relaxed text-ink2">{t(`e1.welcome.${plan.variant}.sub`, vars)}</p>
      {(w.team || w.invited_by) && (
        <div className="rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[12.5px] text-ink2">
          {w.team && <p data-testid="team-welcome-team"><b className="text-ink">{t("e1.welcome.team", { team: w.team.name })}</b>
            {w.team.head_is_me ? ` · ${t("e1.welcome.youLead")}` : w.team.head_name ? ` · ${t("e1.welcome.head", { head: w.team.head_name })}` : ""}</p>}
          {w.invited_by && <p>{t("e1.welcome.invitedBy", { by: w.invited_by })}</p>}
          {w.teammates.length > 0 && <p>{t("e1.welcome.teammates", { names: w.teammates.map((m) => m.name).join(", ") })}</p>}
        </div>
      )}
      {w.agents.length > 0 && <p data-testid="team-welcome-agents" className="text-[12.5px] text-ink2"><b className="text-ink">{t("e1.welcome.agents")}</b> {agentList(w.agents)}</p>}
      {plan.primary === "pickTeam" ? <PickTeam w={w} onPicked={onPicked} /> : (
        <ol className="grid gap-2">
          {[1, 2, 3].map((n, i) => (
            <li key={n} className="flex items-start gap-3 rounded-[10px] border border-line bg-panel2 px-3 py-2.5">
              <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-accent-soft text-accent"><Icon name={ICONS[i]} size={15} /></span>
              <span className="min-w-0 text-[12.5px] leading-snug text-ink2"><b className="block text-[13px] text-ink">{t(`e1.welcome.${plan.variant}.step${n}.t`, vars)}</b>{t(`e1.welcome.${plan.variant}.step${n}.d`, vars)}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

/** Team (and profession, if it is missing) of a person who came in without them. */
function PickTeam({ w, onPicked }: { w: Welcome; onPicked: (w: Welcome) => void }) {
  const { t } = useT();
  const [dept, setDept] = useState(w.teams[0]?.id ?? "");
  const [prof, setProf] = useState<Profession | "">(isProfession(w.profession) ? w.profession : "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  const save = async () => {
    setBusy(true); setErr(false);
    try { onPicked(await teamsApi.pickTeam({ ...(dept ? { department_id: dept } : {}), ...(prof ? { profession: prof } : {}) })); }
    catch { setErr(true); } finally { setBusy(false); }
  };
  const sel = "w-full rounded-[10px] border border-line-strong bg-panel px-3 py-2 text-sm outline-none focus:border-accent";
  return (
    <div data-testid="team-pick" className="grid gap-2 rounded-[10px] border border-line bg-panel2 p-3">
      {w.teams.length > 0 && (
        <label className="grid gap-1 text-[12px] font-semibold text-ink">{t("e1.pick.team")}
          <select data-testid="team-pick-team" className={sel} value={dept} onChange={(e) => setDept(e.target.value)}>
            {w.teams.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            <option value="">{t("e1.pick.noTeam")}</option>
          </select>
        </label>
      )}
      <label className="grid gap-1 text-[12px] font-semibold text-ink">{t("e1.signup.profession")}
        <select data-testid="team-pick-profession" className={sel} value={prof} onChange={(e) => setProf(e.target.value as Profession)}>
          <option value="" disabled>{t("e1.signup.professionPick")}</option>
          {PROFESSIONS.map((p) => <option key={p} value={p}>{t(`e1.profession.${p}`)}</option>)}
        </select>
      </label>
      <p className="text-[11px] text-mute">{t("e1.pick.hint")}</p>
      {err && <p role="alert" className="text-[12px] font-semibold text-err">{t("e1.pick.err")}</p>}
      <button type="button" data-testid="team-pick-save" disabled={busy || (!dept && !prof)} onClick={() => void save()}
        className="min-h-10 rounded-lg bg-accent px-4 py-2 text-sm font-semibold text-white transition hover:bg-accent-hover disabled:opacity-50">{t("e1.pick.save")}</button>
    </div>
  );
}
