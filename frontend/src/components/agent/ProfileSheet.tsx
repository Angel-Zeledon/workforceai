"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { agentV2Api, type AgentProfile, type ProfileVersion } from "@/lib/agentV2Api";
import {
  CREATIVITY_LEVELS, DEFAULT_LIMITS, addSkill, diffRows, errorStatus, isDirty, removeSkill, validateProfile,
  type Creativity, type ProfileDraft, type ProfileIssue,
} from "@/lib/agentV2Logic";
import { ToneSelect } from "../onboarding/ToneSelect";
import type { ToneCode } from "@/lib/orgConfigApi";
import { Btn, Card, timeAgo } from "../ui";

const label = "mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute";

function draftOf(p: AgentProfile): ProfileDraft {
  return { instructions: p.instructions, creativity: p.creativity, skills: p.skills, tone: p.tone };
}

/** "Work profile" section: instructions, creativity, tone, skills, plus the version history with restore. */
export function ProfileSheet({ id }: { id: string }) {
  const { t } = useT();
  const [profile, setProfile] = useState<AgentProfile | null>(null);
  const [draft, setDraft] = useState<ProfileDraft | null>(null);
  const [versions, setVersions] = useState<ProfileVersion[]>([]);
  const [skillInput, setSkillInput] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  const apply = (p: AgentProfile) => { setProfile(p); setDraft(draftOf(p)); };
  const load = useCallback(async () => {
    try {
      const p = await agentV2Api.profile(id);
      apply(p); setErr("");
      setVersions(await agentV2Api.profileVersions(id).catch(() => []));
    } catch (e) { setErr(errorStatus(e) === 403 ? t("profile.forbidden") : t("profile.loadError")); }
  }, [id, t]);
  useEffect(() => { setProfile(null); setDraft(null); setVersions([]); setSaved(false); load(); }, [load]);

  if (!profile || !draft) {
    return <Card title={t("profile.title")}><div data-testid="agent-profile-section" className="text-xs text-mute">{err || t("profile.loading")}</div></Card>;
  }
  const limits = profile.limits ?? DEFAULT_LIMITS;
  const issues = validateProfile(draft, limits);
  const dirty = isDirty(draftOf(profile), draft);
  const set = (patch: Partial<ProfileDraft>) => { setDraft({ ...draft, ...patch }); setSaved(false); };
  const issueText = (i: ProfileIssue) => t(`profile.issue.${i.code}`, { max: "max" in i ? i.max : 0, skill: "skill" in i ? i.skill : "" });
  const fail = (e: unknown) => setErr(errorStatus(e) === 403 ? t("profile.forbidden") : errorStatus(e) === 400 ? t("profile.rejected") : t("profile.saveError"));

  const save = async () => {
    setBusy(true); setErr("");
    try {
      apply(await agentV2Api.putProfile(id, { instructions: draft.instructions, creativity: draft.creativity, skills: draft.skills, tone: draft.tone }));
      setSaved(true); setVersions(await agentV2Api.profileVersions(id).catch(() => versions));
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const restore = async (v: number) => {
    if (typeof window !== "undefined" && !window.confirm(t("profile.restoreConfirm", { v }))) return;
    setBusy(true); setErr("");
    try { apply(await agentV2Api.restoreProfile(id, v)); setSaved(true); setVersions(await agentV2Api.profileVersions(id).catch(() => versions)); }
    catch (e) { fail(e); } finally { setBusy(false); }
  };
  const addTag = () => { set({ skills: addSkill(draft.skills, skillInput, limits) }); setSkillInput(""); };
  const len = [...draft.instructions].length;

  return (
    <div className="space-y-3" data-testid="agent-profile-section">
      <Card title={t("profile.title")} right={<span className="font-mono text-[10px] text-mute">{profile.version ? t("profile.version", { v: profile.version }) : t("profile.neverEdited")}</span>}>
        <div className="space-y-3">
          <label className="block">
            <span className={label}>{t("profile.instructions")}</span>
            <textarea data-testid="agent-profile-instructions" rows={6} value={draft.instructions} onChange={(e) => set({ instructions: e.target.value })}
              placeholder={t("profile.instructionsHint")} className="w-full rounded-xl border border-line bg-panel2 px-3 py-2 text-xs text-ink outline-none focus:border-accent" />
            <div className="mt-1 flex justify-between text-[10px] text-mute">
              <span data-testid="agent-profile-counter" className={len > limits.instructions_max ? "font-semibold text-err" : ""}>{len} / {limits.instructions_max}</span>
            </div>
          </label>
          <div data-testid="agent-profile-warning" role="note" className="rounded-md border border-warn/40 bg-warn/10 px-2.5 py-1.5 text-[11px] text-ink2">{t("profile.secretsWarning")}</div>
          <div className="grid grid-cols-2 gap-2">
            <label className="block">
              <span className={label}>{t("profile.creativity")}</span>
              <select data-testid="agent-profile-creativity" value={draft.creativity} onChange={(e) => set({ creativity: e.target.value as Creativity })}
                className="w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-xs font-semibold outline-none focus:border-accent">
                {CREATIVITY_LEVELS.map((c) => <option key={c} value={c}>{t(`profile.creativity.${c}`)}</option>)}
              </select>
            </label>
            <div>
              <span className={label}>{t("profile.tone")}</span>
              <ToneSelect testid="agent-profile-tone" allowInherit value={draft.tone as ToneCode | ""} onChange={(v) => set({ tone: v })} label={t("profile.tone")} />
            </div>
          </div>
          <div data-testid="agent-profile-skills">
            <span className={label}>{t("profile.skills")}</span>
            {profile.role_skills.length > 0 && (
              <div className="mb-1 flex flex-wrap gap-1" aria-label={t("profile.roleSkills")}>
                {profile.role_skills.map((s) => <span key={s} title={t("profile.roleSkills")} className="rounded border border-line px-1.5 py-0.5 text-[10px] text-mute">{s}</span>)}
              </div>
            )}
            <div className="flex flex-wrap gap-1">
              {draft.skills.map((s) => (
                <span key={s} data-testid={`agent-profile-skill-${s}`} className="inline-flex items-center gap-1 rounded bg-accent/10 px-1.5 py-0.5 text-[10px] text-accent">
                  {s}<button type="button" aria-label={t("profile.removeSkill", { skill: s })} onClick={() => set({ skills: removeSkill(draft.skills, s) })}>×</button>
                </span>
              ))}
            </div>
            <div className="mt-1.5 flex gap-1.5">
              <input data-testid="agent-profile-skill-input" value={skillInput} maxLength={limits.skill_max_len} placeholder={t("profile.addSkill")}
                onChange={(e) => setSkillInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addTag(); } }}
                className="min-w-0 flex-1 rounded-xl border border-line bg-panel2 px-3 py-1.5 text-xs outline-none focus:border-accent" />
              <Btn data-testid="agent-profile-skill-add" onClick={addTag} disabled={!skillInput.trim()}>{t("profile.add")}</Btn>
            </div>
          </div>
          {issues.length > 0 && <ul data-testid="agent-profile-issues" className="space-y-0.5 text-[11px] text-err">{issues.map((i, n) => <li key={n}>{issueText(i)}</li>)}</ul>}
          {err && <div role="alert" data-testid="agent-profile-error" className="text-[11px] text-err">{err}</div>}
          <div className="flex items-center gap-2">
            <Btn data-testid="agent-profile-save" kind="primary" onClick={save} disabled={busy || !dirty || issues.length > 0}>{t("profile.save")}</Btn>
            {saved && !dirty && <span className="text-[11px] text-ok">{t("profile.saved")}</span>}
          </div>
        </div>
      </Card>
      <Card title={t("profile.history")}>
        <div data-testid="agent-profile-history" className="space-y-2">
          {versions.length === 0 && <div className="text-xs text-mute">{t("profile.noHistory")}</div>}
          {versions.map((v) => (
            <div key={v.version} className="rounded-md border border-line bg-panel2 p-2 text-[11px]">
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-ink">v{v.version}{v.restored_from != null && <span className="ml-1 text-mute">({t("profile.restoredFrom", { v: v.restored_from })})</span>}</span>
                <span className="text-mute">{v.author} · {timeAgo(v.created_at)}</span>
              </div>
              <ul className="mt-1 space-y-0.5 text-ink2">
                {diffRows(v.diff).map((r) => (
                  <li key={r.field}>
                    <span className="text-mute">{t(`profile.field.${r.field}`)}: </span>
                    {r.kind === "list"
                      ? <>{r.added?.map((s) => <span key={`+${s}`} className="mr-1 text-ok">+{s}</span>)}{r.removed?.map((s) => <span key={`-${s}`} className="mr-1 text-err">-{s}</span>)}</>
                      : <><span className="text-err line-through">{r.from || "∅"}</span> → <span className="text-ok">{r.to || "∅"}</span></>}
                  </li>
                ))}
              </ul>
              <div className="mt-1.5"><Btn data-testid={`agent-profile-restore-${v.version}`} onClick={() => restore(v.version)} disabled={busy}>{t("profile.restore")}</Btn></div>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
