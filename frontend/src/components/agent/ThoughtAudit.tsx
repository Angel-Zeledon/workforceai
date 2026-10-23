"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtUsd, useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { agentV2Api, downloadTraceExport, type Trace, type TraceList } from "@/lib/agentV2Api";
import { errorStatus, fmtLatency, nextBefore, segmentDefaultOpen, traceTotalTokens } from "@/lib/agentV2Logic";
import { Btn, Card, Empty, timeAgo } from "../ui";

const PAGE = 50;

/** One prompt block (system or segment) that can be collapsed. */
export function CollapsibleBlock({ title, text, defaultOpen, testid, note }: { title: string; text: string; defaultOpen: boolean; testid: string; note?: string }) {
  const { t } = useT();
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div data-testid={testid} data-open={open ? "true" : "false"} className="rounded-md border border-line bg-bg/60">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center justify-between px-2 py-1 text-left text-[11px] font-medium text-ink2">
        <span>{title}</span><span className="text-mute">{open ? t("common.hide") : t("common.show")}</span>
      </button>
      {open && <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words border-t border-line px-2 py-1.5 font-mono text-[10.5px] text-ink2">{text}{note && <span className="block text-mute">{note}</span>}</pre>}
    </div>
  );
}

function summarizeData(d: Record<string, unknown> | undefined): string {
  if (!d) return "";
  const parts = Object.entries(d).filter(([, v]) => v !== null && typeof v !== "object").map(([k, v]) => `${k}=${String(v)}`);
  return parts.length ? ` (${parts.join(", ")})` : "";
}

export function TraceDetail({ trace }: { trace: Trace }) {
  const { t } = useT();
  const h = "text-[10px] font-semibold uppercase tracking-wide text-mute";
  return (
    <div data-testid={`thought-step-detail-${trace.id}`} className="mt-2 space-y-2.5 text-[11.5px]">
      <div>
        <div className={h}>{t("thoughts.rationale")}</div>
        <p className="mt-0.5 whitespace-pre-wrap text-ink2">{trace.rationale || t("thoughts.noRationale")}</p>
        {trace.output_summary && <div className="mt-1 text-mute">{trace.output_summary.summary} · {t("common.confidence")} {Math.round((trace.output_summary.confidence || 0) * 100)}% · {t("thoughts.findings", { n: trace.output_summary.findings })} · {t("thoughts.recommendations", { n: trace.output_summary.recommendations })}</div>}
        {trace.status === "error" && trace.error && <div className="mt-1 text-err">{trace.error}</div>}
      </div>
      <div>
        <div className={h}>{t("thoughts.prompt")}</div>
        {!trace.prompt_stored || !trace.prompt ? <div data-testid="thought-prompt-not-stored" className="text-mute">{t("thoughts.promptNotStored")}</div> : (
          <div className="mt-1 space-y-1">
            <CollapsibleBlock testid={`thought-prompt-system-${trace.id}`} title={t("thoughts.system")} text={trace.prompt.system} defaultOpen={false} />
            {trace.prompt.segments.map((s, i) => (
              <CollapsibleBlock key={i} testid={`thought-segment-${trace.id}-${i}`} defaultOpen={segmentDefaultOpen(s)}
                title={s.kind === "untrusted" ? `${t("thoughts.untrusted")}${s.label ? `: ${s.label}` : ""}` : s.label || t("thoughts.text")}
                text={s.text} note={s.truncated ? t("thoughts.truncated") : undefined} />
            ))}
            {trace.truncated && <div className="text-[10px] text-mute">{t("thoughts.truncated")}</div>}
          </div>
        )}
      </div>
      <div>
        <div className={h}>{t("thoughts.tools")}</div>
        {trace.tool_requests.length === 0 ? <div className="text-mute">{t("thoughts.noTools")}</div> : (
          <ul className="space-y-0.5">
            {trace.tool_requests.map((r, i) => (
              <li key={i} className="font-mono text-[10.5px] text-ink2">
                {r.tool}.{r.action} · {t("thoughts.risk")} {r.risk} · {r.decision ? t(`thoughts.decision.${r.decision}`) : "-"}
                {r.approval_status && <> · {t(`thoughts.approval.${r.approval_status}`)}</>}{r.reason && <span className="text-mute"> ({r.reason})</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div>
        <div className={h}>{t("thoughts.events")}{trace.steer_notes_applied > 0 && <span className="ml-1 normal-case">({t("thoughts.steerApplied", { n: trace.steer_notes_applied })})</span>}</div>
        {trace.events.length === 0 ? <div className="text-mute">{t("thoughts.noEvents")}</div> : (
          <ul className="space-y-0.5">
            {trace.events.map((e, i) => (
              <li key={i} className="text-ink2"><span className="font-mono text-[10px] text-mute">{timeAgo(e.ts)}</span> {t(`thoughts.event.${e.type}`)}{summarizeData(e.data)}</li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

export function ThoughtStep({ agentId, trace, focused = false }: { agentId: string; trace: Trace; focused?: boolean }) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<Trace | null>(null);
  const [err, setErr] = useState(false);
  const toggle = async () => {
    const next = !open;
    setOpen(next);
    if (next && !detail) {
      try { setDetail(await agentV2Api.trace(agentId, trace.id)); setErr(false); } catch { setErr(true); }
    }
  };
  return (
    <div data-testid={`thought-step-${trace.id}`} data-focused={focused ? "true" : undefined} className={`rounded-md border bg-panel2 p-2.5 ${focused ? "border-accent ring-1 ring-accent/40" : "border-line"}`}>
      <button type="button" aria-expanded={open} onClick={toggle} className="block w-full text-left">
        <div className="flex items-center justify-between gap-2 text-[11px]">
          <span className="font-mono text-ink">{trace.provider}/{trace.model}</span>
          <span className={trace.status === "ok" ? "text-ok" : "text-err"}>{t(`thoughts.status.${trace.status}`)}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-2 text-[10.5px] text-mute">
          <span>{timeAgo(trace.created_at)}</span><span>{t("thoughts.attempt", { n: trace.attempt })}</span>
          <span>{t("thoughts.tokens", { n: traceTotalTokens(trace) })}</span><span>{fmtUsd(trace.cost_usd)}</span><span>{fmtLatency(trace.latency_ms)}</span>
        </div>
      </button>
      {open && (detail ? <TraceDetail trace={detail} /> : <div className="mt-2 text-[11px] text-mute">{err ? t("thoughts.detailError") : t("profile.loading")}</div>)}
    </div>
  );
}

/** Per-agent thought audit timeline. Admin/owner only (audit:read); everyone else sees a "restricted" notice. */
export function ThoughtAudit({ id, focusTaskId = "" }: { id: string; focusTaskId?: string }) {
  const { t } = useT();
  const canAudit = useCan("audit:read");
  const canOrg = useCan("org:manage");
  const [list, setList] = useState<TraceList | null>(null);
  const [traces, setTraces] = useState<Trace[]>([]);
  const [restricted, setRestricted] = useState(false);
  const [err, setErr] = useState("");
  const [more, setMore] = useState("");
  const [store, setStore] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await agentV2Api.traces(id, PAGE);
      setList(r); setTraces(r.traces); setStore(r.store_prompts); setMore(nextBefore(r.traces, PAGE)); setRestricted(false); setErr("");
    } catch (e) { if (errorStatus(e) === 403) setRestricted(true); else setErr(t("thoughts.loadError")); }
  }, [id, t]);
  useEffect(() => { setList(null); setTraces([]); setRestricted(false); load(); }, [load]);

  if (restricted || !canAudit) return <div data-testid="agent-thoughts-tab"><Empty><span data-testid="thought-restricted">{t("thoughts.restricted")}</span></Empty></div>;

  const loadMore = async () => {
    try { const r = await agentV2Api.traces(id, PAGE, more); setTraces([...traces, ...r.traces]); setMore(nextBefore(r.traces, PAGE)); } catch { setErr(t("thoughts.loadError")); }
  };
  const doExport = async () => {
    setBusy(true); setErr("");
    try { await downloadTraceExport(id); } catch (e) { setErr(errorStatus(e) === 403 ? t("thoughts.restricted") : t("thoughts.exportError")); } finally { setBusy(false); }
  };
  const toggleStore = async (v: boolean) => {
    setBusy(true); setErr("");
    try { const r = await agentV2Api.putTraceSettings(v); setStore(r.store_prompts); } catch (e) { setErr(errorStatus(e) === 403 ? t("thoughts.restricted") : t("thoughts.settingError")); } finally { setBusy(false); }
  };

  return (
    <div data-testid="agent-thoughts-tab" className="space-y-3">
      <div role="note" className="rounded-md border border-line bg-panel2 px-2.5 py-1.5 text-[11px] text-ink2">{t("thoughts.note")}</div>
      <Card title={t("thoughts.privacy")} right={<Btn data-testid="thought-export" onClick={doExport} disabled={!canAudit || busy}>{t("thoughts.export")}</Btn>}>
        <label className="flex items-start gap-2 text-[11.5px] text-ink2">
          <input data-testid="thought-store-prompts-toggle" type="checkbox" checked={!!store} disabled={!canOrg || busy || store === null} onChange={(e) => toggleStore(e.target.checked)} className="mt-0.5" />
          <span>{t("thoughts.storePrompts")}</span>
        </label>
        <p className="mt-1.5 text-[10.5px] text-mute">{t("thoughts.storeTradeoff", { days: list?.retention_days ?? 0 })}</p>
      </Card>
      {err && <div role="alert" className="text-[11px] text-err">{err}</div>}
      {list && !list.store_prompts && <div className="text-[11px] text-mute">{t("thoughts.promptNotStored")}</div>}
      {list && traces.length === 0 && <Empty>{t("thoughts.empty")}</Empty>}
      {focusTaskId && <div role="note" data-testid="thought-shield-focus" className="rounded-md border border-accent/40 bg-accent-soft px-2.5 py-1.5 text-[11px] text-ink2">{t("shield.thoughts.focus")}{traces.some((tr) => tr.task_id === focusTaskId) ? "" : ` ${t("shield.thoughts.notListed")}`}</div>}
      {traces.map((tr) => <ThoughtStep key={tr.id} agentId={id} trace={tr} focused={!!focusTaskId && tr.task_id === focusTaskId} />)}
      {more && <Btn data-testid="thought-load-more" onClick={loadMore}>{t("thoughts.loadMore")}</Btn>}
    </div>
  );
}
