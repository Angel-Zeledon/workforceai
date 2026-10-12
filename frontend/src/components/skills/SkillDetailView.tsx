"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { skillsApi, SkillError, useSkillsNav, type SkillDetail, type SkillVersion } from "@/lib/skills";
import type { SkillDraft } from "@/lib/skillsLogic";
import { useStore } from "@/lib/store";
import { useCan } from "@/lib/session";
import { Btn, Card, Empty, timeAgo, useAgentName } from "../ui";
import { Icon } from "../icons";
import { SkillEditor } from "./SkillEditor";
import { SkillReview } from "./SkillReview";
import { SourceChip, StatusBadge } from "./shared";

type Tab = "content" | "review" | "versions" | "agents";

/** One skill: editor, review, versions and the agents that have it. "new" opens an empty editor. */
export function SkillDetailView({ id, prefill, tab, onBack, onChanged }: { id: string | "new"; prefill: SkillDraft | null; tab: Tab; onBack: () => void; onChanged: () => void }) {
  const { t } = useT();
  const select = useSkillsNav((s) => s.select);
  const canWrite = useCan("skills:write");
  const canReview = useCan("skills:review");
  const canAssign = useCan("agents:write");
  const [detail, setDetail] = useState<SkillDetail | null>(null);
  const [versions, setVersions] = useState<SkillVersion[]>([]);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (id === "new") return;
    try {
      const d = await skillsApi.get(id);
      setDetail(d); setErr("");
      setVersions(await skillsApi.versions(id).catch(() => []));
    } catch { setErr(t("skills.error.load")); }
  }, [id, t]);
  useEffect(() => { setDetail(null); setMsg(""); load(); }, [load]);

  const afterChange = async () => { await load(); onChanged(); };
  const fail = (e: unknown) => setErr(e instanceof SkillError && t(`skills.error.${e.code}`) !== `skills.error.${e.code}` ? t(`skills.error.${e.code}`) : t("skills.error.generic"));

  if (id === "new") {
    return (
      <div className="mx-auto w-full max-w-4xl space-y-4 p-5" data-testid="skill-detail">
        <BackBar onBack={onBack} title={t("skills.new")} />
        <SkillEditor key={prefill?.name ?? "blank"} prefill={prefill} canWrite={canWrite} onSaved={(d) => { onChanged(); select(d.id, "content"); }} />
      </div>
    );
  }
  if (!detail) return <div className="p-5"><Empty>{err || t("skills.loading")}</Empty></div>;

  const pending = detail.current_version > detail.active_version && detail.active_version > 0;
  const tabs: Tab[] = ["content", "review", "versions", "agents"];
  const download = async (format: "zip" | "md") => {
    setBusy(true);
    try { await skillsApi.download(detail, format); setMsg(t("skills.exported")); } catch (e) { fail(e); } finally { setBusy(false); }
  };

  return (
    <div className="mx-auto w-full max-w-4xl space-y-4 p-5" data-testid="skill-detail" data-skill={detail.name}>
      <BackBar onBack={onBack} title={<span className="flex flex-wrap items-center gap-2"><span className="font-mono">{detail.name}</span><StatusBadge status={detail.status} testId="skill-detail-status" /><SourceChip source={detail.source} builtin={detail.builtin} />
        <span className="font-sans text-[11px] font-normal text-mute">v{detail.current_version}{detail.active_version > 0 && detail.active_version !== detail.current_version ? ` · ${t("skills.activeVersion", { v: detail.active_version })}` : ""}</span></span>}
        right={<div className="flex gap-2">
          <Btn data-testid="skill-export-zip" disabled={busy} onClick={() => download("zip")}>{t("skills.export.zip")}</Btn>
          <Btn data-testid="skill-export-md" disabled={busy} onClick={() => download("md")}>{t("skills.export.md")}</Btn>
        </div>} />
      {msg && <div data-testid="skill-notice" className="rounded-lg bg-ok/10 px-3 py-2 text-xs text-ok">{msg}</div>}
      {err && <div data-testid="skill-error" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{err}</div>}
      {pending && <div data-testid="skill-pending-banner" className="rounded-lg bg-warn/10 px-3 py-2 text-xs text-warn">{t("skills.pendingBanner", { active: detail.active_version, current: detail.current_version })}</div>}
      {detail.status === "in_review" && !pending && <div className="rounded-lg bg-warn/10 px-3 py-2 text-xs text-warn">{t("skills.inReviewBanner")}</div>}
      {detail.status === "draft" && canWrite && (
        <div className="flex items-center gap-3 rounded-lg border border-line bg-panel px-3 py-2 text-xs text-ink2">
          <span className="flex-1">{t("skills.draftBanner")}</span>
          <Btn data-testid="skill-submit" disabled={busy} onClick={async () => { setBusy(true); try { await skillsApi.submit(detail.id); await afterChange(); } catch (e) { fail(e); } finally { setBusy(false); } }}>{t("skills.submit")}</Btn>
        </div>
      )}

      <div role="tablist" className="flex gap-1 border-b border-line">
        {tabs.map((tb) => (
          <button key={tb} role="tab" aria-selected={tab === tb} data-testid={`skill-tab-${tb}`} onClick={() => select(detail.id, tb)}
            className={`-mb-px border-b-2 px-3 py-2 text-xs font-medium ${tab === tb ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>
            {t(`skills.tab.${tb}`)}{tb === "review" && (detail.status === "in_review" || pending) ? <span className="ml-1.5 inline-block h-1.5 w-1.5 rounded-full bg-warn align-middle" /> : null}
          </button>
        ))}
      </div>

      {tab === "content" && <SkillEditor key={`${detail.id}-${detail.current_version}`} detail={detail} canWrite={canWrite} onSaved={(d, changed) => { setMsg(changed ? t("skills.savedVersion", { v: d.current_version }) : t("skills.noChanges")); afterChange(); }} />}
      {tab === "review" && <SkillReview detail={detail} canReview={canReview} onChanged={afterChange} onDeleted={() => { onChanged(); onBack(); }} />}
      {tab === "versions" && <Versions versions={versions} detail={detail} canWrite={canWrite} onRestore={async (v) => { setBusy(true); try { await skillsApi.restore(detail.id, v); setMsg(t("skills.restored", { v })); await afterChange(); } catch (e) { fail(e); } finally { setBusy(false); } }} />}
      {tab === "agents" && <AgentsTab detail={detail} canAssign={canAssign} onChanged={afterChange} />}
    </div>
  );
}

function BackBar({ onBack, title, right }: { onBack: () => void; title: React.ReactNode; right?: React.ReactNode }) {
  const { t } = useT();
  return (
    <div className="flex flex-wrap items-center gap-3">
      <button type="button" data-testid="skills-back" onClick={onBack} className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-accent hover:bg-accent-soft"><Icon name="back" size={12} />{t("skills.backToLibrary")}</button>
      <h1 className="font-display text-xl font-semibold text-ink">{title}</h1>
      <div className="ml-auto">{right}</div>
    </div>
  );
}

function Versions({ versions, detail, canWrite, onRestore }: { versions: SkillVersion[]; detail: SkillDetail; canWrite: boolean; onRestore: (v: number) => void }) {
  const { t } = useT();
  return (
    <div data-testid="skill-versions" className="space-y-2">
      {versions.map((v) => (
        <Card key={v.version}>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="font-mono text-sm font-semibold text-ink">v{v.version}</span>
            {v.version === detail.current_version && <span className="rounded bg-accent-soft px-1.5 py-0.5 text-[10px] font-semibold uppercase text-accent">{t("skills.versions.latest")}</span>}
            {v.version === detail.active_version && <span className="rounded bg-ok/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-ok">{t("skills.versions.active")}</span>}
            {v.restored_from ? <span className="text-mute">{t("skills.versions.restoredFrom", { v: v.restored_from })}</span> : null}
            <span className="text-mute">{v.author} · {timeAgo(v.created_at)}</span>
            {v.warnings.some((w) => w.code === "injection" || w.code === "script") && <span className="rounded bg-warn/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-warn">{t("skills.versions.warnings")}</span>}
            {canWrite && v.version !== detail.current_version && <Btn className="ml-auto" data-testid={`skill-restore-${v.version}`} onClick={() => onRestore(v.version)}>{t("skills.versions.restore")}</Btn>}
          </div>
          <p className="mt-1 text-xs text-ink2">{v.changelog || "—"}</p>
        </Card>
      ))}
    </div>
  );
}

function AgentsTab({ detail, canAssign, onChanged }: { detail: SkillDetail; canAssign: boolean; onChanged: () => void }) {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const name = useAgentName();
  const [pick, setPick] = useState("");
  const [err, setErr] = useState("");
  const assigned = new Set(detail.assignments.map((a) => a.agent_id));
  const free = Object.values(agents).filter((a) => !assigned.has(a.id));
  const run = async (fn: () => Promise<unknown>) => { setErr(""); try { await fn(); onChanged(); } catch { setErr(t("skills.error.generic")); } };
  return (
    <Card title={t("skills.agents.title")}>
      <p className="mb-3 text-xs text-mute">{t("skills.agents.help")}</p>
      {detail.assignments.length === 0 ? <div className="text-xs text-mute">{t("skills.agents.none")}</div> : (
        <ul className="mb-3 space-y-1">
          {detail.assignments.map((a) => (
            <li key={a.agent_id} data-testid={`skill-assigned-${a.agent_id}`} className="flex items-center justify-between rounded-md bg-panel2 px-3 py-1.5 text-xs">
              <span className="font-medium text-ink">{name(a.agent_id)}</span>
              {canAssign && <button type="button" data-testid={`skill-unassign-${a.agent_id}`} className="text-mute hover:text-err" onClick={() => run(() => skillsApi.unassign(a.agent_id, detail.id))}>{t("skills.agents.remove")}</button>}
            </li>
          ))}
        </ul>
      )}
      {canAssign && (
        <div className="flex gap-2">
          <select data-testid="skill-assign-select" value={pick} onChange={(e) => setPick(e.target.value)} className="min-w-0 flex-1 rounded-lg border border-line bg-panel2 px-3 py-1.5 text-xs">
            <option value="">{t("skills.agents.pick")}</option>
            {free.map((a) => <option key={a.id} value={a.id}>{name(a.id)}</option>)}
          </select>
          <Btn kind="primary" data-testid="skill-assign" disabled={!pick} onClick={() => run(async () => { await skillsApi.assign(pick, detail.id); setPick(""); })}>{t("skills.agents.assign")}</Btn>
        </div>
      )}
      {err && <div className="mt-2 text-xs text-err">{err}</div>}
    </Card>
  );
}
