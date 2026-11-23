"use client";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { ChatInput } from "../../chat/ChatThread";
import { convOf } from "./workspaceLogic";

/**
 * The box where work is asked for: always visible, with a visible "Para:" (the whole office or one agent), attachments,
 * "/" commands, the goal (when there are goals) and Enviar. All of that is `ChatInput`; this only adds who it goes to.
 */
export function Composer({ target, onTarget, autoFocus, hero = false }: { target: string | null; onTarget: (id: string | null) => void; autoFocus: boolean; hero?: boolean }) {
  const { t } = useT();
  const order = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const first = target ? (agents[target]?.name ?? "").split(" ")[0] : "";
  return (
    <div data-testid="work-composer" data-tour="dashboard.compose" className={`w-full ${hero ? "" : "border-t border-line bg-bg px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-2.5 md:px-5"}`}>
      <div className={`mx-auto w-full ${hero ? "max-w-2xl" : "max-w-3xl"}`}>
        <div className="mb-1.5 flex items-center gap-2 text-[13px]">
          <label htmlFor="work-to" className="font-medium text-ink2">{t("work.to")}</label>
          <div className="relative">
            <select id="work-to" data-testid="work-to" value={target ?? ""} onChange={(e) => onTarget(e.target.value || null)}
              className="h-10 max-w-[60vw] appearance-none rounded-lg border border-line bg-panel py-0 pl-3 pr-8 text-[13px] font-semibold text-ink outline-none transition focus:border-accent sm:h-9">
              <option value="">{t("work.all")}</option>
              {order.map((id) => agents[id] ? <option key={id} value={id}>{agents[id].name}</option> : null)}
            </select>
            <svg aria-hidden width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-mute"><path d="M6 9l6 6 6-6" /></svg>
          </div>
        </div>
        <ChatInput key="work-input" conv={convOf(target)} large autoFocus={autoFocus} testId="work-input" submitTestId="work-submit"
          placeholder={target ? t("work.placeholder.agent", { name: first }) : t("work.placeholder.all")} />
        <p className="mt-1 hidden text-[11.5px] text-mute sm:block">{t("work.hint")}</p>
      </div>
    </div>
  );
}
