"use client";
import type { ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { AUDIENCES, PROFESSIONS, type Audience, type Profession } from "@/lib/teamsLogic";
import { inputCls } from "../connections/shared";

/**
 * E1 sign-up questions (docs/architecture/equipos-oficinas.md §6): "who is it for?" and "what do you do?", plus the
 * company and team names they need. When the address can enter its company (verified domain), it offers that instead
 * of creating another organization.
 */
export function SignupTeamFields({ audience, setAudience, profession, setProfession, org, setOrg, team, setTeam, joinOrg, join, setJoin }: {
  audience: Audience | ""; setAudience: (a: Audience) => void;
  profession: Profession | ""; setProfession: (p: Profession) => void;
  org: string; setOrg: (v: string) => void; team: string; setTeam: (v: string) => void;
  joinOrg: string; join: boolean; setJoin: (v: boolean) => void;
}) {
  const { t } = useT();
  return (
    <>
      {joinOrg && (
        <div data-testid="signup-join" className="rounded-[10px] border border-accent/30 bg-accent-soft px-3 py-2.5 text-[12.5px] leading-snug text-ink2">
          <p>{t("e1.signup.join.body", { org: joinOrg })}</p>
          <label className="mt-2 flex items-center gap-2 font-medium text-ink">
            <input type="checkbox" data-testid="signup-join-toggle" checked={join} onChange={(e) => setJoin(e.target.checked)} />
            {t("e1.signup.join.toggle", { org: joinOrg })}
          </label>
        </div>
      )}
      {!join && (
        <fieldset>
          <legend className="mb-1 block text-xs font-medium text-ink2">{t("e1.signup.audience")}</legend>
          <div role="radiogroup" aria-label={t("e1.signup.audience")} className="grid gap-1.5 sm:grid-cols-3">
            {AUDIENCES.map((a) => (
              <button key={a} type="button" role="radio" aria-checked={audience === a} data-testid={`signup-audience-${a}`} onClick={() => setAudience(a)}
                className={`rounded-[10px] border px-3 py-2 text-left transition ${audience === a ? "border-accent bg-accent-soft" : "border-line bg-panel hover:border-accent/50"}`}>
                <span className="block text-[13px] font-semibold text-ink">{t(`e1.audience.${a}.t`)}</span>
                <span className="block text-[11.5px] leading-snug text-mute">{t(`e1.audience.${a}.d`)}</span>
              </button>
            ))}
          </div>
        </fieldset>
      )}
      {!join && (audience === "team" || audience === "company") && (
        <Field label={t("e1.signup.org")}>
          <input data-testid="auth-org" autoComplete="organization" required className={inputCls} value={org} onChange={(e) => setOrg(e.target.value)} />
        </Field>
      )}
      {!join && audience === "team" && (
        <Field label={t("e1.signup.team")}>
          <input data-testid="signup-team" maxLength={80} placeholder={profession ? t(`e1.team.${profession}`) : ""} className={inputCls} value={team} onChange={(e) => setTeam(e.target.value)} />
        </Field>
      )}
      <Field label={t("e1.signup.profession")}>
        <select data-testid="signup-profession" required className={inputCls} value={profession} onChange={(e) => setProfession(e.target.value as Profession)}>
          <option value="" disabled>{t("e1.signup.professionPick")}</option>
          {PROFESSIONS.map((p) => <option key={p} value={p}>{t(`e1.profession.${p}`)}</option>)}
        </select>
      </Field>
      {profession && <p data-testid="signup-agents-hint" className="-mt-1 text-[11px] text-mute">{t(`e1.agentsHint.${profession}`)}</p>}
    </>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block text-xs font-medium text-ink2">
      <span className="mb-1 block">{label}</span>
      {children}
    </label>
  );
}
