"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import type { Locale } from "@/lib/i18n-core";
import { cleanInvites, parseEmails, PROFESSIONS, suggestedTeams, teamRowsIssues, type HeadChoice, type InviteRow, type Profession, type TeamRow, type Welcome } from "@/lib/teamsLogic";
import { inviteLink, teamsApi } from "@/lib/teamsApi";
import { orgChartApi } from "@/lib/orgChartApi";
import { Modal } from "@/components/templates/Modal";
import { IndustryField } from "./IndustryField";

/**
 * The responsible's team set-up (docs/architecture/equipos-oficinas.md §6.1): for "my team", invite colleagues to the
 * team now or later; for "my company", pick the teams from a list (or an industry template, or new ones) and who leads
 * each. Every team is a department with the agents of its work. Invitations respect the plan's seats.
 */
interface Sent { email: string; link?: string; failed?: boolean; limit?: boolean }

const sel = "rounded-[10px] border border-line-strong bg-panel px-2.5 py-2 text-sm outline-none focus:border-accent";
const btn = "min-h-10 rounded-lg px-4 py-2 text-xs font-semibold transition disabled:opacity-50";

async function sendInvites(rows: InviteRow[], deptId: string, lead: boolean): Promise<Sent[]> {
  const out: Sent[] = [];
  for (const r of rows) {
    try {
      const inv = await teamsApi.invite({ email: r.email, role: "member", department_id: deptId, profession: r.profession, lead });
      out.push({ email: r.email, link: inviteLink(inv.token) });
    } catch (e) {
      out.push({ email: r.email, failed: true, limit: String((e as Error).message).includes("409") });
    }
  }
  return out;
}

export function TeamSetupWizard({ welcome, onClose, onDone }: { welcome: Welcome; onClose: () => void; onDone: () => void }) {
  const { t, locale } = useT();
  const company = welcome.audience === "company";
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [sent, setSent] = useState<Sent[] | null>(null);

  // "my team": invitations to the team created at sign-up.
  const [paste, setPaste] = useState("");
  const [invProf, setInvProf] = useState<Profession>(PROFESSIONS.includes(welcome.profession as Profession) ? (welcome.profession as Profession) : "other");

  // "my company": the team list.
  const [rows, setRows] = useState<TeamRow[]>(() => suggestedTeams((p) => t(`e1.team.${p}`)));
  const [industry, setIndustry] = useState("");
  const issues = useMemo(() => teamRowsIssues(rows), [rows]);
  const chosen = rows.filter((r) => r.on);
  const edit = (key: string, patch: Partial<TeamRow>) => setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  const addRow = () => setRows((rs) => [...rs, { key: `custom${rs.length}`, name: "", profession: "other", head: "later", email: "", on: true }]);

  const finish = async () => {
    try { await teamsApi.setupDone(); } catch { /* the wizard can be opened again from the menu */ }
    onDone();
  };

  const inviteTeam = async () => {
    if (!welcome.team) return;
    setBusy(true); setErr(null);
    setSent(await sendInvites(cleanInvites(parseEmails(paste, invProf)), welcome.team.id, false));
    setBusy(false);
  };

  const createTeams = async () => {
    setBusy(true); setErr(null);
    const out: Sent[] = [];
    try {
      if (industry) await orgChartApi.applyIndustry(industry, { locale: locale as Locale });
      for (const r of chosen) {
        const res = await teamsApi.createTeam({ name: r.name.trim(), profession: r.profession, head_me: r.head === "me" });
        if (r.head === "invite") out.push(...(await sendInvites([{ email: r.email.trim().toLowerCase(), profession: r.profession }], res.team.id, true)));
      }
      setSent(out);
    } catch { setErr(t("e1.setup.err.create")); }
    setBusy(false);
  };

  if (sent) {
    return (
      <Modal title={t("e1.setup.title")} onClose={() => void finish()} testid="team-setup">
        <p data-testid="team-setup-done" className="mb-3 text-sm text-ink2">{t(sent.length ? "e1.setup.sent" : "e1.setup.created")}</p>
        {sent.length > 0 && (
          <ul className="mb-4 grid gap-2">
            {sent.map((s) => (
              <li key={s.email} className="rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[12px]">
                <b className="block text-ink">{s.email}</b>
                {s.link ? <input readOnly className="mt-1 w-full rounded-md border border-line bg-panel px-2 py-1 font-mono text-[11px]" value={s.link} onFocus={(e) => e.currentTarget.select()} />
                  : <span className="text-err">{t(s.limit ? "e1.setup.err.seats" : "e1.setup.err.invite")}</span>}
              </li>
            ))}
          </ul>
        )}
        {sent.length > 0 && <p className="mb-4 text-[11px] text-mute">{t("members.invite.linkOnce")}</p>}
        <div className="flex justify-end"><button type="button" data-testid="team-setup-finish" onClick={() => void finish()} className={`${btn} bg-accent text-white`}>{t("e1.setup.finish")}</button></div>
      </Modal>
    );
  }

  return (
    <Modal title={t("e1.setup.title")} onClose={onClose} testid="team-setup" wide={company}>
      {!company ? (
        <div className="grid gap-3">
          <p className="text-sm text-ink2">{t("e1.setup.team.body", { team: welcome.team?.name ?? "" })}</p>
          <label className="grid gap-1 text-[12px] font-semibold">{t("e1.setup.team.emails")}
            <textarea data-testid="team-setup-emails" rows={3} className={`${sel} font-normal`} value={paste} onChange={(e) => setPaste(e.target.value)} placeholder={t("e1.setup.team.placeholder")} />
          </label>
          <label className="grid gap-1 text-[12px] font-semibold">{t("e1.setup.team.profession")}
            <select data-testid="team-setup-profession" className={sel} value={invProf} onChange={(e) => setInvProf(e.target.value as Profession)}>
              {PROFESSIONS.map((p) => <option key={p} value={p}>{t(`e1.profession.${p}`)}</option>)}
            </select>
          </label>
          <p className="text-[11px] text-mute">{t("e1.setup.team.hint")}</p>
        </div>
      ) : (
        <div className="grid gap-3">
          <p className="text-sm text-ink2">{t("e1.setup.company.body")}</p>
          <ul className="grid gap-1.5" data-testid="team-setup-rows">
            {rows.map((r) => (
              <li key={r.key} data-testid={`team-row-${r.key}`} className={`grid gap-2 rounded-[10px] border px-3 py-2 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:items-center ${r.on ? "border-accent/40 bg-accent-soft/40" : "border-line"}`}>
                <label className="flex items-center gap-2 text-[12.5px] font-semibold">
                  <input type="checkbox" checked={r.on} onChange={(e) => edit(r.key, { on: e.target.checked })} aria-label={r.name || t("e1.setup.company.new")} />
                  <span className="text-[11px] font-medium text-mute">{t(`e1.profession.${r.profession}`)}</span>
                </label>
                <input className={sel} value={r.name} disabled={!r.on} maxLength={80} placeholder={t("e1.setup.company.name")} onChange={(e) => edit(r.key, { name: e.target.value })} />
                <div className="flex flex-wrap items-center gap-2">
                  <select aria-label={t("e1.setup.company.head")} className={sel} disabled={!r.on} value={r.head} onChange={(e) => edit(r.key, { head: e.target.value as HeadChoice })}>
                    {(["me", "invite", "later"] as const).map((h) => <option key={h} value={h}>{t(`e1.setup.head.${h}`)}</option>)}
                  </select>
                  {r.on && r.head === "invite" && <input type="email" className={`${sel} min-w-0 flex-1`} placeholder={t("auth.email")} value={r.email} onChange={(e) => edit(r.key, { email: e.target.value })} />}
                  {r.key.startsWith("custom") && r.on && (
                    <select aria-label={t("e1.signup.profession")} className={sel} value={r.profession} onChange={(e) => edit(r.key, { profession: e.target.value as Profession })}>
                      {PROFESSIONS.map((p) => <option key={p} value={p}>{t(`e1.profession.${p}`)}</option>)}
                    </select>
                  )}
                </div>
                {issues[r.key] && <p className="text-[11.5px] font-semibold text-err sm:col-span-3">{t(issues[r.key])}</p>}
              </li>
            ))}
          </ul>
          <button type="button" data-testid="team-setup-add" onClick={addRow} className="w-fit text-[12px] font-semibold text-accent hover:underline">{t("e1.setup.company.add")}</button>
          <IndustryField value={industry} onChange={setIndustry} locale={locale as Locale} />
          <p className="text-[11px] text-mute">{t("e1.setup.company.hint")}</p>
        </div>
      )}
      {err && <p role="alert" className="mt-3 text-sm font-semibold text-err">{err}</p>}
      <div className="mt-5 flex flex-wrap items-center justify-between gap-2">
        <button type="button" data-testid="team-setup-later" disabled={busy} onClick={() => void finish()} className={`${btn} border border-line text-mute hover:text-ink`}>{t("e1.setup.later")}</button>
        {!company ? (
          <button type="button" data-testid="team-setup-invite" disabled={busy || cleanInvites(parseEmails(paste, invProf)).length === 0 || !welcome.team} onClick={() => void inviteTeam()}
            className={`${btn} bg-accent text-white`}>{busy ? t("e1.setup.working") : t("e1.setup.team.invite")}</button>
        ) : (
          <button type="button" data-testid="team-setup-create" disabled={busy || (chosen.length === 0 && !industry) || Object.keys(issues).length > 0} onClick={() => void createTeams()}
            className={`${btn} bg-accent text-white`}>{busy ? t("e1.setup.working") : t("e1.setup.company.create", { n: chosen.length })}</button>
        )}
      </div>
    </Modal>
  );
}
