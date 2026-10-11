"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useStore } from "@/lib/store";
import { useCost } from "@/lib/cost";
import { useProjects } from "@/lib/projects/store";
import { usePreferences } from "@/lib/preferences";
import { USER_POS } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { ApprovalCard } from "../ApprovalsTray";
import { readable, useAgentColor, useAgentName } from "../ui";
import { feetPos } from "./registry";
import { useRegister } from "./useRegister";
import { getSignals } from "./officeSignals";
import {
  BUBBLE_GAP_MS, BUBBLE_SHOW_MS, approvalsSpot, phaseFor, pickBubble, stepBubbles, type Bubble, type BubbleState,
} from "./officeState";

// ---------------------------------------------------------------------------------------------
// Approvals desk card: the agent waits at the CEO desk, this is its approval with the SAME actions as the tray.
// ---------------------------------------------------------------------------------------------

/** Agents that have actually reached the approvals desk (checked against their live world position). */
function useArrived(): string[] {
  const [arrived, setArrived] = useState<string[]>([]);
  useEffect(() => {
    const tick = () => {
      const sig = getSignals();
      const next: string[] = [];
      sig.apprSlot.forEach((slot, id) => {
        const f = feetPos.get(id);
        if (!f) return;
        const [sx, sz] = approvalsSpot(USER_POS, slot);
        if (Math.hypot(f.x - sx, f.z - sz) < 0.6) next.push(id);
      });
      setArrived((cur) => (cur.length === next.length && cur.every((v, i) => v === next[i]) ? cur : next));
    };
    tick();
    const i = setInterval(tick, 400);
    return () => clearInterval(i);
  }, []);
  return arrived;
}

export function ApprovalDeskCard() {
  const { t } = useT();
  const name = useAgentName();
  const approvals = useStore((s) => s.approvals);
  const arrived = useArrived();
  const ref = useRegister("popup:approvals", "popup", "approvals");
  const sig = getSignals();
  // oldest pending approval of an agent standing at the desk first
  const queue = arrived.map((id) => sig.apprOf.get(id)).filter((a): a is NonNullable<typeof a> => !!a && approvals[a.id]?.status === "pending");
  const first = queue[0];
  if (!first) return null;
  const a = approvals[first.id] ?? first;
  return (
    <div ref={ref} data-h={190} data-w={316} data-tier="popup" className="pointer-events-none absolute left-0 top-0 origin-bottom" style={{ opacity: 0 }}>
      <div data-testid="office-approval-desk" data-approval-id={a.id} className="ac-pop pointer-events-auto relative w-[304px] rounded-xl border border-amber-500/50 bg-white p-2 font-sans shadow-float">
        <div className="mb-1 px-1 text-[10px] font-semibold uppercase tracking-wider text-amber-700">{t("office.approvalDesk.title")} · {t("office.approvalDesk.waiting", { name: name(a.agent_id).split(" ")[0] })}</div>
        <ApprovalCard a={a} compact scope="office" />
        {queue.length > 1 && <div data-testid="office-approval-more" className="mt-1 px-1 text-[10.5px] font-medium text-mute">{t("office.approvalDesk.more", { count: queue.length - 1 })}</div>}
        <span className="absolute -bottom-[9px] left-1/2 h-4 w-4 -translate-x-1/2 rotate-45 rounded-[2px] border-b border-r border-amber-500/50 bg-white" />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Meeting room tag: shown while a plan is being drafted or reviewed
// ---------------------------------------------------------------------------------------------

export function MeetingTag() {
  const { t } = useT();
  const sig = useSignalsTick();
  const ref = useRegister("popup:meeting", "popup", "meeting");
  if (!sig.meetingSlot.size) return null;
  return (
    <div ref={ref} data-h={26} data-w={190} data-tier="popup" className="pointer-events-none absolute left-0 top-0 origin-bottom" style={{ opacity: 0 }}>
      <div data-testid="office-meeting-tag" className="ac-pop rounded-full border border-line-strong bg-white px-3 py-1 font-sans text-[10.5px] font-semibold tracking-tight text-ink shadow-soft">
        <span className="mr-1.5 inline-block h-1.5 w-1.5 rounded-full bg-[#5a7fd0]" />{t("office.planning.title")}
      </div>
    </div>
  );
}

/** Re-renders when the derived office signals change (store sources are compared by identity inside getSignals). */
function useSignalsTick() {
  const [, force] = useState(0);
  const last = useRef(getSignals());
  useEffect(() => {
    const unsubs = [
      useStore.subscribe(() => { const n = getSignals(); if (n !== last.current) { last.current = n; force((v) => v + 1); } }),
      useCost.subscribe(() => { const n = getSignals(); if (n !== last.current) { last.current = n; force((v) => v + 1); } }),
      useProjects.subscribe(() => { const n = getSignals(); if (n !== last.current) { last.current = n; force((v) => v + 1); } }),
    ];
    return () => unsubs.forEach((u) => u());
  }, []);
  return last.current;
}

// ---------------------------------------------------------------------------------------------
// Speech bubbles with REAL content: plan phase titles (meeting room), consult / chat line, task title and step
// ---------------------------------------------------------------------------------------------

interface Visible { id: string; text: string; kind: Bubble["kind"]; shownAt: number }

function SpeechBubble({ v }: { v: Visible }) {
  const { t } = useT();
  const agentColor = useAgentColor();
  const ref = useRegister(`bubble:speech:${v.id}`, "bubble", v.id);
  const color = readable(agentColor(v.id));
  const lines = Math.min(2, Math.ceil(v.text.length / 26));
  return (
    <div ref={ref} data-speech="1" data-ts={v.shownAt} data-ttl={BUBBLE_SHOW_MS} data-h={16 + 14 + lines * 15 + 10} data-tier="bubble"
      className="pointer-events-none absolute left-0 top-0 origin-bottom transition-opacity duration-300" style={{ opacity: 0 }}>
      <div data-testid={`office-bubble-${v.id}`} data-kind={v.kind} className="ac-pop relative w-[170px] rounded-xl border border-line-strong bg-white px-2.5 py-1 font-sans shadow-soft"
        style={{ boxShadow: `inset 3px 0 0 ${color}, 0 6px 16px -8px rgba(15,23,42,.25)` }}>
        <div className="text-[9px] font-semibold uppercase tracking-wider" style={{ color }}>{t(`office.bubble.${v.kind}`)}</div>
        <div className="line-clamp-2 text-[11px] font-medium leading-[15px] text-ink">{v.text}</div>
        <span className="absolute -bottom-[8px] left-1/2 h-3.5 w-3.5 -translate-x-1/2 rotate-45 rounded-[2px] border-b border-r border-line-strong bg-white" />
      </div>
    </div>
  );
}

export function SpeechLayer() {
  const show = usePreferences((s) => s.prefs.showBubbles);
  const [vis, setVis] = useState<Visible[]>([]);
  const state = useRef<BubbleState>(new Map());
  useEffect(() => {
    if (!show) { setVis([]); state.current.clear(); return; }
    const tick = () => {
      const S = useStore.getState();
      const sig = getSignals();
      const now = Date.now();
      const cycle = Math.floor(now / (BUBBLE_SHOW_MS + BUBBLE_GAP_MS));
      // agents already showing a message bubble (LabelLayer, link kinds) do not get a second one
      const talking = new Set<string>();
      for (const l of S.links) if (now - l.ts < 7000) talking.add(l.from);
      const cands: { id: string; bubble: Bubble | null }[] = [];
      const meeting: typeof cands = [], rest: typeof cands = [];
      for (const id of S.agentOrder) {
        const ag = S.agents[id];
        if (!ag || talking.has(id)) continue;
        let line: { text: string; kind: string; ageMs: number } | null = null;
        for (let i = S.links.length - 1; i >= 0; i--) {
          const l = S.links[i];
          if (l.from === id && (l.kind === "chat" || l.kind === "consult")) { line = { text: l.text, kind: l.kind, ageMs: now - l.ts }; break; }
        }
        const slot = sig.meetingSlot.get(id);
        const phaseTitle = slot === undefined ? null
          : sig.phases.length ? phaseFor(sig.phases, slot, cycle)
          : slot === 0 && sig.planningText ? sig.planningText : null; // the request text is the only real content until phases arrive
        const paused = sig.cafeSlot.has(id);
        const bubble = pickBubble({
          state: paused ? "working" : ag.state, activity: ag.activity || "", phaseTitle, line,
          taskTitle: paused ? "" : (ag.current_task_id ? S.tasks[ag.current_task_id]?.title ?? "" : ""),
        });
        (slot !== undefined ? meeting : rest).push({ id, bubble: bubble && paused ? { ...bubble, urgent: true } : bubble });
      }
      cands.push(...meeting, ...rest);
      const ids = stepBubbles(state.current, cands, now);
      setVis((cur) => {
        const next: Visible[] = ids.map((id) => { const s = state.current.get(id)!; return { id, text: s.text, kind: s.kind, shownAt: s.shownAt }; });
        return cur.length === next.length && cur.every((c, i) => c.id === next[i].id && c.text === next[i].text && c.shownAt === next[i].shownAt) ? cur : next;
      });
    };
    tick();
    const i = setInterval(tick, 1000);
    return () => clearInterval(i);
  }, [show]);
  const list = useMemo(() => vis, [vis]);
  return <>{list.map((v) => <SpeechBubble key={`${v.id}:${v.shownAt}`} v={v} />)}</>;
}
