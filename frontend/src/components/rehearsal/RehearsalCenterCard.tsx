"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { useStore } from "@/lib/store";
import { rehearsalApi, useRehearsal, type AgentRehearsalSummary, type RehearsalAction } from "@/lib/rehearsal";
import { approvalRate } from "@/lib/rehearsalLogic";
import { Btn, Card, Empty, useAgentName } from "../ui";
import { RehearsalActionRow } from "./RehearsalReport";
import { RehearsalToggle } from "./RehearsalToggle";

/**
 * B9 "Ensayo general" in the security center: run a request as a rehearsal, see which agents are in rehearsal mode
 * with the evidence their rehearsals produced (would approve / would reject), and judge the latest "would have done"
 * entries grouped by request. Nothing listed here was executed.
 */
export function RehearsalCenterCard() {
  const { t } = useT();
  const name = useAgentName();
  const agentOrder = useStore((s) => s.agentOrder);
  const tick = useRehearsal((s) => s.tick);
  const canCreate = useCan("requests:create");
  const [agents, setAgents] = useState<AgentRehearsalSummary[]>([]);
  const [actions, setActions] = useState<RehearsalAction[]>([]);
  const [text, setText] = useState("");
  const [sent, setSent] = useState("");
  const [err, setErr] = useState(false);
  const load = useCallback(async () => {
    try {
      const [ags, acts] = await Promise.all([rehearsalApi.agents(), rehearsalApi.actions({ limit: 40 })]);
      setAgents(ags); setActions(acts); setErr(false);
    } catch { setErr(true); }
  }, []);
  useEffect(() => { void load(); }, [load, tick]);
  const byRequest = useMemo(() => {
    const m = new Map<string, RehearsalAction[]>();
    actions.forEach((a) => m.set(a.request_id, [...(m.get(a.request_id) ?? []), a]));
    return Array.from(m.entries());
  }, [actions]);
  const evidence = Object.fromEntries(agents.map((a) => [a.agent_id, a]));
  const submit = async () => {
    const v = text.trim();
    if (!v) return;
    setErr(false);
    try { const r = await rehearsalApi.submit(v); setSent(r.request_id); setText(""); } catch { setErr(true); }
  };
  const update = (a: RehearsalAction) => setActions((l) => l.map((x) => (x.id === a.id ? a : x)));
  return (
    <div data-testid="ctl-rehearsal">
      <Card title={t("rehearsal.center.title")}>
        <p className="mb-2 text-xs text-mute">{t("rehearsal.center.help")}</p>
        <div className="flex flex-wrap gap-2">
          <input data-testid="rehearsal-request-input" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("rehearsal.center.placeholder")}
            onKeyDown={(e) => { if (e.key === "Enter") void submit(); }}
            className="min-w-0 flex-1 rounded-md border border-line bg-panel px-2.5 py-1.5 text-[16px] sm:text-xs" />
          <Btn data-testid="rehearsal-request-submit" kind="primary" disabled={!canCreate || !text.trim()} onClick={submit}>{t("rehearsal.center.run")}</Btn>
        </div>
        {sent && <p data-testid="rehearsal-request-sent" className="mt-1 text-[11px] text-ok">{t("rehearsal.center.sent")}</p>}

        <h4 className="mb-1 mt-4 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("rehearsal.center.agents")}</h4>
        <ul className="grid gap-1.5 sm:grid-cols-2">
          {agentOrder.map((id) => {
            const ev = evidence[id];
            const rate = ev ? approvalRate(ev) : null;
            return (
              <li key={id} data-testid={`rehearsal-agent-${id}`} className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-panel2 px-3 py-1.5 text-xs">
                <div className="min-w-0">
                  <b>{name(id)}</b>
                  <div className="text-[10.5px] text-mute">
                    {ev && ev.actions > 0 ? t("rehearsal.center.evidence", { approve: ev.would_approve, reject: ev.would_reject, undecided: ev.undecided }) : t("rehearsal.center.noEvidence")}
                    {rate !== null && <> · {t("rehearsal.center.rate", { pct: Math.round(rate * 100) })}</>}
                  </div>
                </div>
                <RehearsalToggle id={id} />
              </li>
            );
          })}
        </ul>

        <h4 className="mb-1 mt-4 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("rehearsal.center.recent")}</h4>
        {err && <p role="alert" className="text-[11px] text-err">{t("rehearsal.error")}</p>}
        {byRequest.length === 0 ? <Empty>{t("rehearsal.center.empty")}</Empty> : (
          <div className="space-y-3">
            {byRequest.map(([req, list]) => (
              <div key={req} data-testid="rehearsal-request-group">
                <div className="mb-1 font-mono text-[10.5px] text-mute">{t("rehearsal.center.request", { id: req.slice(0, 8) })}</div>
                <ul className="space-y-2">{list.map((a) => <RehearsalActionRow key={a.id} a={a} onChange={update} />)}</ul>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
