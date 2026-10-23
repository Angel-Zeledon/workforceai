"use client";
import { useT } from "@/lib/i18n";
import { useShield } from "@/lib/shield";
import { sourceLabel } from "@/lib/shieldLogic";
import { openThoughts } from "@/lib/agentFocus";
import { go } from "@/lib/navBridge";
import { Icon } from "../icons";
import { useAgentName } from "../ui";

/**
 * B10: the notice of a "shield moment". Says who read what, that the content was treated as data and NOT obeyed (and
 * that any send or payment of that task now waits for a person), and links the task's thought audit and the security
 * log. Ids only: the injected text is never shown here.
 */
export function ShieldAlerts() {
  const { t } = useT();
  const notices = useShield((s) => s.notices);
  const dismiss = useShield((s) => s.dismiss);
  const name = useAgentName();
  if (!notices.length) return null;
  return (
    <div aria-live="assertive" role="status" data-testid="shield-alerts" className="pointer-events-none fixed bottom-20 right-3 z-[121] flex w-[min(380px,calc(100vw-24px))] flex-col gap-2 pb-[env(safe-area-inset-bottom)]">
      {notices.map((a) => (
        <div key={a.id} data-testid="shield-alert" data-agent={a.agent_id} data-task={a.task_id}
          className="ac-toast pointer-events-auto rounded-lg border border-accent/40 bg-panel p-3 text-[12.5px] text-ink shadow-float">
          <div className="flex items-start gap-2.5">
            <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-accent-soft text-accent"><Icon name="shield" size={16} /></span>
            <div className="min-w-0 flex-1">
              <b className="block text-[13px]">{t("shield.alert.title")}</b>
              <p className="mt-0.5 leading-snug text-ink2">{t("shield.alert.body", { agent: name(a.agent_id), source: sourceLabel(a) })}</p>
              <p className="mt-1 text-[11.5px] leading-snug text-mute">{t("shield.alert.safe")}</p>
              <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11.5px] font-semibold">
                <button type="button" data-testid="shield-alert-thoughts" className="text-accent hover:underline"
                  onClick={() => { openThoughts(a.agent_id, a.task_id); dismiss(a.id); }}>{t("shield.alert.openThoughts")}</button>
                <button type="button" data-testid="shield-alert-log" className="text-accent hover:underline"
                  onClick={() => { go("/settings?s=security#ctl-shield-log"); dismiss(a.id); }}>{t("shield.alert.openLog")}</button>
              </div>
            </div>
            <button type="button" aria-label={t("common.close")} onClick={() => dismiss(a.id)} className="-m-1 p-1.5 text-mute hover:text-ink"><Icon name="x" size={14} /></button>
          </div>
        </div>
      ))}
    </div>
  );
}
