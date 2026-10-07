"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useStore, type Link } from "@/lib/store";
import { stateMeta } from "@/lib/meta";
import { useAgentView, usePreferences } from "@/lib/preferences";
import { labelEntries } from "./labels";
import { LINK_KIND } from "./kinds";

const BUBBLE_TTL = 7000;
const KIND = LINK_KIND;

function useRegister(key: string, kind: "agent" | "bubble", anchor: string) {
  return useCallback((el: HTMLElement | null) => {
    if (el) labelEntries.set(key, { el, kind, anchor });
    else labelEntries.delete(key);
  }, [key, kind, anchor]);
}

/** Etiqueta de agente: chip con nombre + estado; al pasar el cursor / seleccionar se expande con detalle. */
function AgentTag({ id }: { id: string }) {
  const agent = useStore((s) => s.agents[id]);
  const selected = useStore((s) => s.selectedAgentId === id);
  const select = useStore((s) => s.select);
  const [hover, setHover] = useState(false);
  const view = useAgentView(id);
  const showNames = usePreferences((s) => s.prefs.showNames);
  const labelSize = usePreferences((s) => s.prefs.labelSize);
  const ref = useRegister(`agent:${id}`, "agent", id);
  if (!agent) return null;
  const meta = { color: view.color };
  const scale = labelSize === "s" ? 0.85 : labelSize === "l" ? 1.2 : 1;
  const sm = stateMeta(agent.state);
  const exp = selected || hover;
  const busy = ["working", "reviewing", "thinking", "awaiting_approval"].includes(agent.state) && agent.progress > 0;
  const attention = ["awaiting_approval", "error", "blocked", "completed"].includes(agent.state);
  const fw = (52 + (showNames ? view.label.length * 7.4 : 0) + (agent.state === "idle" ? 0 : sm.label.length * 5.6 + 10)) * scale;
  return (
    <div
      ref={ref}
      data-testid={`agent-${id}`}
      data-state={agent.state}
      data-exp={exp ? "1" : "0"}
      data-fw={Math.round(fw)}
      data-tier="full"
      className="group pointer-events-none absolute left-0 top-0 origin-bottom select-none transition-opacity duration-300"
      style={{ opacity: 0 }}
    >
      <div
        onClick={(e) => { e.stopPropagation(); select(id); }}
        onMouseEnter={() => setHover(true)}
        onMouseLeave={() => setHover(false)}
        className={`pointer-events-auto cursor-pointer border border-line-strong bg-panel/95 font-sans shadow-soft backdrop-blur-sm transition-[transform,box-shadow] duration-150 hover:-translate-y-px group-data-[tier=hidden]:pointer-events-none ${exp ? "rounded-xl p-2.5" : "rounded-lg px-2 py-1 group-data-[tier=mini]:p-0.5"} ${attention && !exp ? "ac-bounce" : ""}`}
        style={{ zoom: scale, boxShadow: selected ? `0 0 0 2px ${meta.color}, 0 8px 20px -8px rgba(15,23,42,.3)` : undefined }}
      >
        {exp ? (
          <div className="ac-pop w-[196px]">
            <div className="flex items-center gap-2">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[12px] font-semibold text-white" style={{ background: meta.color }}>{view.name[0]}</span>
              <div className="min-w-0 leading-tight">
                <div className="truncate text-[13px] font-semibold tracking-tight text-ink">{view.name}</div>
                <div className="truncate text-[10.5px] font-medium text-mute">{view.title}</div>
              </div>
            </div>
            <div className="mt-1.5 inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${sm.color}1f`, color: sm.color }}>{sm.label}</div>
            <div className="mt-1 line-clamp-2 min-h-[14px] text-[11px] font-normal leading-[14px] text-ink2">{agent.activity || "Sin actividad"}</div>
            {busy && (
              <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-mute/15">
                <div className="h-full rounded-full transition-all duration-500" style={{ width: `${Math.max(4, agent.progress)}%`, background: sm.color }} />
              </div>
            )}
          </div>
        ) : (
          <div className="flex items-center gap-1.5">
            <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-[5px] text-[11px] font-semibold text-white group-data-[tier=mini]:h-6 group-data-[tier=mini]:w-6" style={{ background: meta.color }}>{view.name[0]}</span>
            {showNames && <span className="text-[12.5px] font-semibold leading-none tracking-tight text-ink group-data-[tier=mini]:hidden">{view.label}</span>}
            {agent.state !== "idle" && (
              <span className="rounded-full px-1.5 py-[2px] text-[9px] font-semibold uppercase leading-none tracking-wide group-data-[tier=mini]:hidden" style={{ background: `${sm.color}1f`, color: sm.color }}>{sm.label}</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** Burbuja de diálogo, anclada al emisor del mensaje real. */
function Bubble({ link }: { link: Link }) {
  const k = KIND[link.kind] || KIND.chat;
  const anchor = ["user", "all", "system"].includes(link.from) ? "user" : link.from;
  const ref = useRegister(`bubble:${link.id}`, "bubble", anchor);
  const text = link.text.length > 86 ? link.text.slice(0, 84) + "…" : link.text;
  const lines = Math.min(3, Math.ceil(text.length / 26));
  return (
    <div
      ref={ref}
      data-ts={link.ts}
      data-h={18 + 14 + lines * 15 + 10}
      data-tier="bubble"
      className="pointer-events-none absolute left-0 top-0 origin-bottom transition-opacity duration-300"
      style={{ opacity: 0 }}
    >
      <div className="ac-pop relative w-[184px] rounded-xl border border-line-strong bg-white px-3 py-1.5 font-sans shadow-soft" style={{ boxShadow: `inset 3px 0 0 ${k.color}, 0 8px 20px -8px rgba(15,23,42,.25)` }}>
        <div className="text-[10px] font-semibold uppercase tracking-wider" style={{ color: k.color }}>{k.label}</div>
        <div className="line-clamp-3 text-[11.5px] font-medium leading-[15px] text-ink">{text}</div>
        <span className="absolute -bottom-[9px] left-1/2 h-4 w-4 -translate-x-1/2 rotate-45 rounded-[2px] border-b border-r border-line-strong bg-white" />
      </div>
    </div>
  );
}

export function LabelLayer() {
  const order = useStore((s) => s.agentOrder);
  const links = useStore((s) => s.links);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const i = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(i);
  }, []);
  // un solo globo por emisor (el más reciente), máximo 3 a la vez
  const bubbles = useMemo(() => {
    const seen = new Set<string>();
    const out: Link[] = [];
    for (let i = links.length - 1; i >= 0 && out.length < 3; i--) {
      const l = links[i];
      if (now - l.ts > BUBBLE_TTL || seen.has(l.from)) continue;
      seen.add(l.from); out.push(l);
    }
    return out;
  }, [links, now]);
  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      {order.map((id) => <AgentTag key={id} id={id} />)}
      {bubbles.map((l) => <Bubble key={l.id} link={l} />)}
    </div>
  );
}
