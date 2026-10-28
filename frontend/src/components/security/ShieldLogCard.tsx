"use client";
import { useEffect, useRef } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useShield } from "@/lib/shield";
import { sourceLabel } from "@/lib/shieldLogic";
import { openThoughts } from "@/lib/agentFocus";
import { Btn, Card, Empty, useAgentName } from "../ui";

/**
 * B10 security log: every time content an agent read tried to give it orders (audit action
 * security.injection_suspected), newest first, read from the tamper-evident audit trail. Needs audit:read.
 */
export function ShieldLogCard() {
  const { t } = useT();
  const log = useShield((s) => s.log);
  const state = useShield((s) => s.logState);
  const name = useAgentName();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => { void useShield.getState().loadLog(); }, []);
  useEffect(() => { // deep link from the shield notice (/settings?s=security#ctl-shield-log)
    if (typeof window !== "undefined" && window.location.hash === "#ctl-shield-log") ref.current?.scrollIntoView({ block: "start" });
  }, []);
  return (
    <div id="ctl-shield-log" data-testid="ctl-shield-log" ref={ref}>
      <Card title={t("shield.log.title")} right={<Btn data-testid="shield-log-refresh" onClick={() => void useShield.getState().loadLog()}>{t("shield.log.refresh")}</Btn>}>
        <p className="mb-2 text-xs text-mute">{t("shield.log.help")}</p>
        {state === "restricted" ? <Empty><span data-testid="shield-log-restricted">{t("shield.log.restricted")}</span></Empty>
          : state === "error" && log.length === 0 ? <Empty>{t("shield.log.error")}</Empty>
          : log.length === 0 ? <Empty>{state === "loading" ? t("profile.loading") : t("shield.log.empty")}</Empty> : (
          <ul className="divide-y divide-line/60 text-xs">
            {log.map((a) => (
              <li key={a.id} data-testid="shield-log-row" data-agent={a.agent_id} className="flex flex-wrap items-center justify-between gap-2 py-2">
                <div className="min-w-0">
                  <b>{name(a.agent_id)}</b> <span className="text-mute">· {a.ts ? fmtDateTime(a.ts) : ""}</span>
                  <div className="text-ink2">{t("shield.log.row", { source: sourceLabel(a) })}</div>
                </div>
                {a.task_id && <button type="button" data-testid="shield-log-thoughts" className="text-[11.5px] font-semibold text-accent hover:underline"
                  onClick={() => openThoughts(a.agent_id, a.task_id)}>{t("shield.alert.openThoughts")}</button>}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
