"use client";
import { useEffect, useMemo } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { useAgentView } from "@/lib/preferences";
import { ChatThread } from "../../chat/ChatThread";
import { Loading } from "../../ui";
import { BlockBoundary } from "./BlockBoundary";
import { Composer } from "./Composer";
import { RunBlock } from "./RunBlock";
import { HelperProposal, useProposals } from "../../helpers/HelperProposal";
import { convOf, exampleRoles, inProgress, OFFICE_CONV } from "./workspaceLogic";

const KNOWN_ROLES = new Set(["sales", "analyst", "accounting", "hr", "legal", "operations", "assistant"]);
const NO_MSGS: never[] = [];

/** Header of the conversation: who it is with and, for an agent, what they are doing. */
function ThreadHeader({ target }: { target: string | null }) {
  const { t } = useT();
  const v = useAgentView(target ?? "");
  const a = useStore((s) => (target ? s.agents[target] : undefined));
  const select = useStore((s) => s.select);
  return (
    <div className="flex min-h-12 items-center gap-2 border-b border-line px-4 py-2 md:px-5">
      <h2 className="min-w-0 flex-1 truncate text-[14px] font-semibold text-ink">
        {target ? v.name : t("work.thread.all")}
        <span className="ml-2 text-[12.5px] font-normal text-ink2">{target ? (a?.activity || v.title) : t("work.thread.allSub")}</span>
      </h2>
      {target && (
        <button type="button" data-testid="thread-open-panel" onClick={() => select(target)}
          className="min-h-10 shrink-0 rounded-md border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-ink2 transition hover:border-accent hover:text-accent">{t("work.thread.openPanel")}</button>
      )}
    </div>
  );
}

/** Nothing asked yet: examples under the box; picking one fills it (it is never sent on its own). */
function Examples({ onPick }: { onPick: (text: string) => void }) {
  const { t } = useT();
  const order = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const ex = useMemo(() => exampleRoles(order, agents, KNOWN_ROLES, 4), [order, agents]);
  const lines = ex.length ? ex.map((e) => ({ key: e.role, text: t(`work.example.${e.role}`) })) : [{ key: "generic", text: t("work.example.generic") }];
  const first = (id: string) => (agents[id]?.name ?? "").split(" ")[0];
  return (
    <div className="mx-auto w-full max-w-2xl px-3 md:px-5">
      <ul className="divide-y divide-line" aria-label={t("work.empty.examples")}>
        {lines.map((l, i) => (
          <li key={l.key}>
            <button type="button" data-testid={`work-example-${l.key}`} onClick={() => onPick(l.text)}
              className="flex min-h-11 w-full items-center gap-2.5 px-2 py-2 text-left text-[13.5px] text-ink2 transition hover:bg-panel2 hover:text-accent">
              <span className="min-w-0 flex-1">{l.text}</span>
              {ex[i] && <span className="shrink-0 text-[12px] text-mute">{first(ex[i].agentId)}</span>}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** The conversation with the whole office or with one agent, the live block of what is in motion, and the box to write. */
export function WorkThread({ target, onTarget, autoFocus }: { target: string | null; onTarget: (id: string | null) => void; autoFocus: boolean }) {
  const { t } = useT();
  const loaded = useStore((s) => s.loaded);
  const connected = useStore((s) => s.connected);
  const requests = useStore((s) => s.requests);
  const tasks = useStore((s) => s.tasks);
  const office = useStore((s) => s.chat[OFFICE_CONV]) ?? NO_MSGS;
  const loadChat = useStore((s) => s.loadChat);
  const setDraft = useStore((s) => s.setDraft);
  const conv = convOf(target);
  const proposals = useProposals();
  useEffect(() => { loadChat(OFFICE_CONV); }, [loadChat]);
  const running = useMemo(() => inProgress(requests, Object.values(tasks), { agent: target }).slice(0, 2), [requests, tasks, target]);
  const empty = loaded && target === null && office.length === 0 && Object.keys(requests).length === 0;
  const teamSize = useStore((s) => s.agentOrder.length);
  const first = useStore((s) => (target ? (s.agents[target]?.name ?? "").split(" ")[0] : ""));

  return (
    <section aria-label={t("work.thread.label")} data-testid="work-thread" data-empty={empty ? "1" : "0"} className={`flex h-full min-h-0 min-w-0 flex-col bg-panel ${empty ? "gap-5 overflow-y-auto pb-6 pt-[clamp(24px,14vh,150px)]" : ""}`}>
      {!loaded ? <div className="flex-1 p-5"><Loading rows={4} /></div>
        : empty ? (
          <div className="px-4 text-center">
            <h2 className="text-[22px] font-semibold leading-tight tracking-tight text-ink sm:text-[26px]">{t("work.empty.title")}</h2>
            <p className="mx-auto mt-2 max-w-md text-[14px] leading-relaxed text-ink2">{t("work.empty.body", { n: teamSize })}</p>
          </div>
        )
        : (
          <>
            <ThreadHeader target={target} />
            {!connected && <div role="status" data-testid="work-offline" className="border-b border-line bg-panel2 px-4 py-1.5 text-[12px] text-ink2">{t("work.offline")}</div>}
            <BlockBoundary name="thread">
              <ChatThread key={conv} conv={conv} showRoute className="flex-1"
                emptyText={target ? t("work.thread.emptyAgent", { name: first }) : t("work.thread.emptyAll")} />
            </BlockBoundary>
          </>
        )}
      {loaded && proposals.length > 0 && (
        <div data-testid="work-proposals" className="max-h-[46%] shrink-0 space-y-2 overflow-y-auto border-t border-line px-3 py-2 md:px-5">
          {proposals.map((g) => <div key={g.id} className="mx-auto w-full max-w-3xl"><BlockBoundary name={`helpers-${g.id}`}><HelperProposal group={g} /></BlockBoundary></div>)}
        </div>
      )}
      {loaded && !empty && running.length > 0 && (
        <div data-testid="work-running" role="status" aria-live="polite" className="max-h-[38%] shrink-0 space-y-2 overflow-y-auto border-t border-line px-3 py-2 md:px-5">
          {running.map((r) => <div key={r.id} className="mx-auto w-full max-w-3xl"><BlockBoundary name={`run-${r.id}`}><RunBlock row={r} collapsible defaultOpen={false} /></BlockBoundary></div>)}
        </div>
      )}
      <Composer target={target} onTarget={onTarget} autoFocus={autoFocus} hero={empty} />
      {empty && <BlockBoundary name="examples"><Examples onPick={(text) => setDraft(conv, text)} /></BlockBoundary>}
    </section>
  );
}
