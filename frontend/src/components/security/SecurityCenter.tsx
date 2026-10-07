"use client";
import { useState, type ReactNode } from "react";
import { MOCK } from "@/lib/config";
import { fmtDateTime, fmtUsd, useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import { useStore } from "@/lib/store";
import { Btn, Card, Empty } from "../ui";
import { ErrorLine, errText, inputCls, useViewer } from "../connections/shared";
import { EmailDraftCard } from "./EmailDraftCard";
import { PlanReviewCard } from "./PlanReviewCard";

function Section({ id, title, children, right }: { id: string; title: string; children: ReactNode; right?: ReactNode }) {
  return <div data-testid={id}><Card title={title} right={right}>{children}</Card></div>;
}

function GlobalState() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  const { isOwner, isAdmin } = useViewer();
  const [reason, setReason] = useState("");
  const [resume, setResume] = useState<"all" | "none">("all");
  const [err, setErr] = useState<string | null>(null);
  if (!controls) return null;
  const level = controls.kill_switch_level;
  const ro = controls.mode === "read_only";
  const run = async (fn: () => Promise<unknown>) => { setErr(null); try { await fn(); await useConnections.getState().loadControls(); await useConnections.getState().loadConnections(); } catch (e) { setErr(errText(e)); } };
  return (
    <Section id="ctl-global" title={t("ctl.global.title")}>
      <div className="space-y-3 text-xs">
        <p data-testid="ctl-global-state" data-level={level} data-mode={controls.mode} className="font-semibold">
          {level !== "none" ? t(`ctl.level.${level}`) : ro ? t("ctl.state.readonly") : t("ctl.state.normal")}
          {controls.set_by && level !== "none" && <span className="ml-2 font-normal text-mute">{t("ctl.setBy", { name: (controls.humans ?? []).find((h) => h.id === controls.set_by)?.name ?? controls.set_by, when: fmtDateTime(controls.set_at) })}</span>}
        </p>

        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-panel2 px-3 py-2">
          <div><b>{t("ctl.readonly.title")}</b><p className="text-mute">{t("ctl.readonly.help")}</p></div>
          <Btn data-testid="ctl-readonly-toggle" data-on={ro} disabled={ro ? !isOwner : !isAdmin} onClick={() => run(() => ctlApi.setReadOnly(!ro))}>{ro ? t("ctl.readonly.disable") : t("ctl.readonly.enable")}</Btn>
        </div>
        {ro && !isOwner && <p className="text-[11px] text-mute">{t("ctl.ownerOnlyRelease")}</p>}

        {level !== "none" && (
          <div className="space-y-2 rounded-xl border border-red-300 bg-red-50 p-3">
            <p className="font-semibold text-red-800">{t("ctl.release.help")}</p>
            <input data-testid="ctl-release-reason" className={inputCls} placeholder={t("ctl.release.reason")} value={reason} onChange={(e) => setReason(e.target.value)} disabled={!isOwner} />
            {level === "lockdown" && (
              <label className="flex items-center gap-2 font-semibold"><input type="checkbox" data-testid="ctl-release-resume-all" checked={resume === "all"} onChange={(e) => setResume(e.target.checked ? "all" : "none")} />{t("ctl.release.resumeAll")}</label>
            )}
            <Btn data-testid="ctl-release" kind="primary" disabled={!isOwner || !reason.trim()} onClick={() => run(async () => { await ctlApi.release(reason, resume); setReason(""); })}>{t("ctl.release.button")}</Btn>
            {!isOwner && <p className="text-[11px] font-semibold text-red-700">{t("ctl.ownerOnlyRelease")}</p>}
          </div>
        )}
        <ErrorLine msg={err} />
      </div>
    </Section>
  );
}

function ToolSwitches() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  const providers = useConnections((s) => s.providers);
  const { isOwner, isAdmin } = useViewer();
  const [err, setErr] = useState<string | null>(null);
  const tools = Array.from(new Map(providers.filter((p) => p.available).flatMap((p) => p.capabilities.map((c) => [c.id, c] as const))).values());
  const off = controls?.disabled_tools ?? [];
  const toggle = async (tool: string, disabled: boolean) => {
    setErr(null);
    try { await ctlApi.setToolDisabled(tool, disabled); await useConnections.getState().loadControls(); } catch (e) { setErr(errText(e)); }
  };
  return (
    <Section id="ctl-tools" title={t("ctl.tools.title")}>
      <p className="mb-2 text-xs text-mute">{t("ctl.tools.help")}</p>
      <div className="grid gap-1.5 sm:grid-cols-2">
        {tools.map((c) => {
          const disabled = off.includes(c.id);
          return (
            <div key={c.id} className="flex items-center justify-between gap-2 rounded-xl border border-line bg-panel2 px-3 py-1.5 text-xs">
              <span><b>{t(`conn.capShort.${c.id}`)}</b> <span className="font-mono text-[10px] text-mute">{c.id}</span></span>
              <button type="button" data-testid={`ctl-tool-kill-${c.id}`} data-disabled={disabled} disabled={disabled ? !isOwner : !isAdmin} onClick={() => toggle(c.id, !disabled)}
                className={`rounded-md border px-3 py-0.5 text-[11px] font-semibold transition disabled:opacity-40 ${disabled ? "border-red-400 bg-red-500/15 text-red-700" : "border-line bg-panel text-ink hover:border-red-300"}`}>
                {disabled ? t("ctl.tools.off") : t("ctl.tools.stop")}
              </button>
            </div>
          );
        })}
      </div>
      <div className="mt-2"><ErrorLine msg={err} /></div>
    </Section>
  );
}

function AgentPauses() {
  const { t } = useT();
  const agentOrder = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const ac = useConnections((s) => s.agentControls);
  const { isAdmin } = useViewer();
  const [err, setErr] = useState<string | null>(null);
  const act = async (id: string, action: "pause" | "resume") => {
    setErr(null);
    try { await ctlApi.agentControl(id, action, action === "pause" ? "manual" : undefined); await useConnections.getState().loadControls(); } catch (e) { setErr(errText(e)); }
  };
  return (
    <Section id="ctl-agents" title={t("ctl.agents.title")}>
      <p className="mb-2 text-xs text-mute">{t("ctl.agents.help")}</p>
      <ul className="grid gap-1.5 sm:grid-cols-2">
        {agentOrder.map((id) => {
          const paused = ac[id]?.control === "paused";
          return (
            <li key={id} data-testid={`ctl-agent-row-${id}`} data-control={paused ? "paused" : "active"} className="flex items-center justify-between gap-2 rounded-xl border border-line bg-panel2 px-3 py-1.5 text-xs">
              <span><b>{agents[id]?.name ?? id}</b>{paused && <span className="ml-2 rounded-full bg-amber-500/20 px-2 text-[10px] font-semibold text-amber-700">{t("ctl.agent.paused")}</span>}</span>
              {paused
                ? <Btn data-testid={`ctl-agent-resume-${id}`} kind="ok" disabled={!isAdmin} onClick={() => act(id, "resume")}>{t("ctl.agent.resume")}</Btn>
                : <Btn data-testid={`ctl-agent-pause-${id}`} disabled={!isAdmin} onClick={() => act(id, "pause")}>{t("ctl.agent.pause")}</Btn>}
            </li>
          );
        })}
      </ul>
      <div className="mt-2"><ErrorLine msg={err} /></div>
    </Section>
  );
}

function SimulationCard() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  if (!MOCK || !controls) return null;
  const reload = async () => { await useConnections.getState().load(); };
  return (
    <Section id="ctl-sim" title={t("ctl.sim.title")}>
      <p className="mb-2 text-[11px] text-mute">{t("ctl.sim.help")}</p>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="font-semibold">{t("ctl.sim.viewer")}:</span>
        {(controls.humans ?? []).map((h) => (
          <Btn key={h.id} data-testid={`ctl-sim-viewer-${h.id}`} kind={controls.viewer.id === h.id ? "primary" : "ghost"} onClick={async () => { await ctlApi.devViewer(h.id); await reload(); }}>{h.name} · {t(`ctl.role.${h.role}`)}</Btn>
        ))}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
        <span className="font-semibold">{t("ctl.sim.admins")}:</span>
        {([1, 2] as const).map((n) => <Btn key={n} data-testid={`ctl-sim-admins-${n}`} kind={controls.admin_count === n ? "primary" : "ghost"} onClick={async () => { await ctlApi.devAdmins(n); await reload(); }}>{n}</Btn>)}
      </div>
      <div className="mt-2"><Btn data-testid="ctl-plan-simulate" onClick={async () => { await ctlApi.simulatePlan(); await useConnections.getState().loadPlans(); }}>{t("ctl.sim.plan")}</Btn></div>
    </Section>
  );
}

/** Security panel (spec 14.6): global state, kill switch release, read-only, tool switches, agent pause, plan review, outbox, spend. */
export function SecurityCenter() {
  const { t } = useT();
  const plans = useConnections((s) => s.plans);
  const drafts = useConnections((s) => s.drafts);
  const spend = useConnections((s) => s.spend);
  const agents = useStore((s) => s.agents);
  const openPlans = plans.filter((p) => p.status === "plan_ready");
  const closedPlans = plans.filter((p) => p.status !== "plan_ready").slice(0, 3);
  const activeDrafts = drafts.filter((d) => ["draft", "pending_approval", "held", "blocked"].includes(d.status));
  const doneDrafts = drafts.filter((d) => ["sent", "cancelled", "rejected"].includes(d.status)).slice(0, 4);

  return (
    <div className="mx-auto h-full max-w-6xl overflow-y-auto px-5 py-4 pb-10">
      <h1 className="font-display text-xl font-semibold">{t("ctl.title")}</h1>
      <p className="mb-4 text-xs text-mute">{t("ctl.subtitle")}</p>
      <div className="space-y-4">
        <GlobalState />
        <div className="grid gap-4 lg:grid-cols-2"><ToolSwitches /><AgentPauses /></div>
        <Section id="ctl-plan-review" title={t("ctl.plan.title")}>
          <p className="mb-2 text-xs text-mute">{t("ctl.plan.help")}</p>
          <div className="space-y-3">
            {openPlans.map((p) => <PlanReviewCard key={p.id} plan={p} />)}
            {openPlans.length === 0 && <Empty>{t("ctl.plan.empty")}</Empty>}
            {closedPlans.map((p) => <PlanReviewCard key={p.id} plan={p} />)}
          </div>
        </Section>
        <Section id="ctl-outbox" title={t("ctl.outbox.title")}>
          <p className="mb-2 text-xs text-mute">{t("ctl.outbox.help")}</p>
          <div className="space-y-3">
            {activeDrafts.map((d) => <EmailDraftCard key={d.id} draft={d} />)}
            {activeDrafts.length === 0 && <Empty>{t("ctl.outbox.empty")}</Empty>}
            {doneDrafts.map((d) => <EmailDraftCard key={d.id} draft={d} />)}
          </div>
        </Section>
        <Section id="ctl-limits" title={t("ctl.limits.title")}>
          <table data-testid="ctl-limits-table" className="w-full border-collapse text-xs">
            <thead><tr className="border-b border-line text-left text-[11px] font-semibold uppercase tracking-wide text-mute">{["scope", "period", "used", "limit", "onHit"].map((h) => <th key={h} className="px-2 py-1.5">{t(`ctl.limits.col.${h}`)}</th>)}</tr></thead>
            <tbody>
              {spend.map((s) => (
                <tr key={s.id} className="border-b border-line/60 last:border-0">
                  <td className="px-2 py-1.5 font-semibold">{t(`ctl.scope.${s.scope_type}`)}{s.scope_id ? ` · ${agents[s.scope_id]?.name ?? s.scope_id}` : ""}</td>
                  <td className="px-2 py-1.5">{t(`ctl.period.${s.period}`)}</td>
                  <td className="px-2 py-1.5 font-mono">{fmtUsd(s.used_usd)}</td>
                  <td className="px-2 py-1.5 font-mono">{fmtUsd(s.limit_usd)}</td>
                  <td className="px-2 py-1.5">{t(`ctl.onHit.${s.on_hit}`)}</td>
                </tr>
              ))}
              {spend.length === 0 && <tr><td colSpan={5} className="px-2 py-4 text-center text-mute">{t("ctl.limits.empty")}</td></tr>}
            </tbody>
          </table>
        </Section>
        <SimulationCard />
      </div>
    </div>
  );
}
