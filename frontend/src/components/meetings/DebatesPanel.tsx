"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { api } from "@/lib/api";
import { useCan } from "@/lib/session";
import { useStore } from "@/lib/store";
import type { Debate } from "@/lib/types";
import { Btn, Loading } from "../ui";
import { EmptyState } from "../nav/ui2";
import { ErrorBar, Section, fmtDateTimeShort, inputCls, useAgentList } from "./shared";
import { debateErrorCode, parseOptions } from "./debateLogic";

/**
 * Team debates (B5, docs/architecture/team-debates.md): start one and read the transcripts. Every turn is model
 * output, shown as untrusted; a debate is advisory and never approves anything (the decision still goes through
 * the approvals tray).
 */
export function DebatesPanel() {
  const { t } = useT();
  const canWrite = useCan("requests:create");
  const debates = useStore((s) => s.debates);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    let alive = true;
    const load = () => void useStore.getState().loadDebates().finally(() => { if (alive) setLoaded(true); });
    load();
    const i = setInterval(load, 30000);
    return () => { alive = false; clearInterval(i); };
  }, []);
  const list = useMemo(() => Object.values(debates).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)), [debates]);
  return (
    <div className="space-y-3" data-testid="debates-panel">
      <p className="text-xs text-mute">{t("debates.intro")}</p>
      {canWrite && <StartDebate />}
      {!loaded && !list.length ? <Loading rows={3} /> : list.length === 0 ? (
        <EmptyState icon="users" testid="debates-empty" title={t("debates.empty.title")} body={t("debates.empty.body")} />
      ) : list.map((d) => <DebateCard key={d.id} d={d} />)}
    </div>
  );
}

function StartDebate() {
  const { t, locale } = useT();
  const agents = useAgentList().filter((a) => a.id !== "assistant");
  const [topic, setTopic] = useState("");
  const [options, setOptions] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const opts = parseOptions(options);
  const badPick = picked.length === 1 || picked.length > 4;
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  const submit = async () => {
    setBusy(true); setErr("");
    try {
      const d = await api.startDebate({ topic: topic.trim(), ...(opts.length ? { options: opts } : {}), ...(picked.length ? { agent_ids: picked } : {}), locale });
      if (d?.id) useStore.setState((s) => ({ debates: { ...s.debates, [d.id]: d } }));
      setTopic(""); setOptions(""); setPicked([]);
    } catch (e) {
      setErr(t(`debates.error.${debateErrorCode(e)}`));
    } finally { setBusy(false); }
  };
  return (
    <Section title={t("debates.new.title")}>
      <div className="space-y-2.5">
        <input data-testid="debate-topic" className={inputCls} value={topic} maxLength={300} placeholder={t("debates.new.topic")} aria-label={t("debates.new.topic")} onChange={(e) => setTopic(e.target.value)} />
        <textarea data-testid="debate-options" className={`${inputCls} min-h-[64px]`} value={options} placeholder={t("debates.new.options")} aria-label={t("debates.new.options")} onChange={(e) => setOptions(e.target.value)} />
        <div>
          <div className="mb-1 text-[11px] text-mute">{t("debates.new.participants")}</div>
          <div className="flex flex-wrap gap-1.5">
            {agents.map((a) => (
              <button key={a.id} type="button" aria-pressed={picked.includes(a.id)} data-testid={`debate-agent-${a.id}`} onClick={() => toggle(a.id)}
                className={`rounded-full border px-2.5 py-1 text-[11px] ${picked.includes(a.id) ? "border-accent bg-accent-soft text-accent" : "border-line-strong text-ink2 hover:bg-panel2"}`}>{a.name}</button>
            ))}
          </div>
          {badPick && <div className="mt-1 text-[11px] text-err">{t("debates.new.pickHint")}</div>}
        </div>
        <ErrorBar msg={err} testId="debate-error" />
        <div className="flex items-center justify-between gap-2">
          <span className="text-[11px] text-mute">{t("debates.new.caps")}</span>
          <Btn kind="primary" data-testid="debate-start" disabled={busy || !topic.trim() || badPick || opts.length > 4} onClick={() => void submit()}>
            {busy ? t("debates.new.running") : t("debates.new.start")}
          </Btn>
        </div>
      </div>
    </Section>
  );
}

function DebateCard({ d }: { d: Debate }) {
  const { t } = useT();
  const agents = useAgentList();
  const nameOf = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
  const colorOf = (id: string) => agents.find((a) => a.id === id)?.color ?? "#94a3b8";
  const [open, setOpen] = useState(false);
  const status = d.status === "done" ? "done" : d.status === "failed" ? "failed" : "running";
  return (
    <article data-testid={`debate-${d.id}`} data-status={status} className="rounded-xl border border-line bg-panel p-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="break-words text-sm font-semibold text-ink">{d.topic}</h3>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-mute">
            <span>{t(`debates.trigger.${d.trigger}`)}</span>
            {d.created_at && <span>· {fmtDateTimeShort(d.created_at)}</span>}
            {d.status === "done" && <span>· {t("debates.turns", { count: d.turns.length, max: d.max_turns })}</span>}
            {d.cost_usd > 0 && <span>· ${d.cost_usd.toFixed(3)}</span>}
          </div>
        </div>
        <span className={`rounded-full px-2 py-0.5 text-[10.5px] font-semibold ${status === "done" ? "bg-ok/15 text-ok" : status === "failed" ? "bg-err/10 text-err" : "bg-accent-soft text-accent"}`}>
          {t(`debates.status.${status}`)}
        </span>
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {d.participants.map((p) => (
          <span key={p} className="inline-flex items-center gap-1 rounded-full border border-line px-2 py-0.5 text-[11px] text-ink2">
            <span className="h-2 w-2 rounded-full" style={{ background: colorOf(p) }} />{nameOf(p)}
          </span>
        ))}
      </div>
      {d.status === "failed" && <p className="mt-2 text-xs text-err">{t(`debates.fail.${d.fail_code || "unavailable"}`)}</p>}
      {d.status === "done" && (
        <>
          <div data-testid="debate-synthesis" className="mt-3 rounded-lg border border-line bg-panel2 p-3 text-xs">
            <div className="text-[10.5px] font-semibold uppercase tracking-wider text-mute">{t("debates.synthesis")}</div>
            <div className="mt-1 font-semibold text-ink">{d.synthesis.choice}</div>
            {d.synthesis.rationale && <p className="mt-0.5 text-ink2">{d.synthesis.rationale}</p>}
            {d.synthesis.dissent.length > 0 && (
              <div className="mt-2">
                <div className="text-[10.5px] font-semibold uppercase tracking-wider text-mute">{t("debates.dissent")}</div>
                <ul className="mt-0.5 list-disc pl-4 text-ink2">{d.synthesis.dissent.map((x, i) => <li key={i}>{x}</li>)}</ul>
              </div>
            )}
          </div>
          {d.options.length > 0 && (
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px]">
              <span className="text-mute">{t("debates.options")}</span>
              {d.options.map((o) => <span key={o} className={`rounded-md border px-1.5 py-0.5 ${o === d.synthesis.choice ? "border-accent text-accent" : "border-line text-ink2"}`}>{o}</span>)}
            </div>
          )}
          <p data-testid="debate-advisory" className="mt-2 text-[11px] text-mute">{t("debates.advisory")}</p>
          <button type="button" aria-expanded={open} data-testid="debate-toggle" onClick={() => setOpen((v) => !v)} className="mt-2 text-[11.5px] font-medium text-accent hover:underline">
            {open ? t("debates.hide") : t("debates.show")}
          </button>
          {open && (
            <div className="mt-2 space-y-2" data-testid="debate-transcript">
              <div role="note" className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-800 dark:text-amber-300">{t("debates.untrusted")}</div>
              <ol className="space-y-1.5">
                {d.turns.map((x) => (
                  <li key={x.seq} className="rounded-lg border border-line px-2.5 py-1.5 text-xs" style={{ boxShadow: `inset 3px 0 0 ${colorOf(x.agent_id)}` }}>
                    <div className="text-[10.5px] text-mute"><span className="font-semibold text-ink2">{nameOf(x.agent_id)}</span> · {t(`debates.kind.${x.kind}`)}</div>
                    {/* model output: rendered as plain text, never as markup */}
                    <p className="whitespace-pre-wrap break-words text-ink">{x.text}</p>
                  </li>
                ))}
              </ol>
            </div>
          )}
        </>
      )}
    </article>
  );
}
