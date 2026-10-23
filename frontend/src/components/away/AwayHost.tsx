"use client";
import { useCallback, useEffect, useState } from "react";
import { create } from "zustand";
import { awayApi, type AwayItem, type AwaySection, type AwaySummary } from "@/lib/awayApi";
import { alertKey, awayDuration, headlineParts, itemAction, MIN_HOURS_CHOICES, parseMinHours } from "@/lib/awayLogic";
import { useAwayReplay } from "@/lib/awayReplay";
import { fmtDateTime, fmtUsd, intlLocale, useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { useSession } from "@/lib/session";
import { useStore } from "@/lib/store";
import { Icon } from "../icons";
import { welcomeSeen } from "../onboarding/WelcomeOverlay";
import { ApprovalCard } from "../ApprovalsTray";
import { ReportView } from "../ReportView";
import { Btn, RiskBadge, useAgentName } from "../ui";

/**
 * "While you were away" (B3, docs/architecture/away-summary.md): a dismissible sheet over the office with what
 * happened during the absence, the approvals to decide first, and an optional timelapse of the office.
 * The summary is computed by the server from recorded data (no AI); this file only renders it.
 */

const PREF_KEY = "aiw.away.minHours";
const ENV_DEFAULT = parseMinHours(process.env.NEXT_PUBLIC_AWAY_MIN_HOURS, 8);
const HEARTBEAT_MS = 5 * 60 * 1000;

/** "Wed 6:34 PM": an absence spans a night, so the weekday matters and the seconds do not. */
function shortWhen(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(intlLocale(), { weekday: "short", hour: "2-digit", minute: "2-digit" }).format(d);
}

function readPref(): number {
  try { return parseMinHours(localStorage.getItem(PREF_KEY), ENV_DEFAULT); } catch { return ENV_DEFAULT; }
}

interface AwayState {
  summary: AwaySummary | null;
  open: boolean;
  loading: boolean;
  minHours: number;
  load: (auto: boolean) => Promise<void>;
  openNow: () => void;
  close: () => void;
  setMinHours: (h: number) => void;
}

export const useAway = create<AwayState>((set, get) => ({
  summary: null, open: false, loading: false, minHours: ENV_DEFAULT,
  load: async (auto) => {
    const minHours = readPref();
    set({ loading: true, minHours });
    try {
      // "never" (0) still asks with the server default so the summary can be opened by hand.
      const s = await awayApi.summary(minHours === 0 ? undefined : minHours);
      set({ summary: s, loading: false });
      if (auto && s.show && minHours !== 0) set({ open: true });
    } catch {
      set({ loading: false });
    }
  },
  openNow: () => {
    set({ open: true });
    if (!get().summary) void get().load(false);
  },
  close: () => {
    set({ open: false });
    const s = get().summary;
    if (s) set({ summary: { ...s, show: false, dismissed: true } });
    awayApi.dismiss().catch(() => { /* best effort: it opens again next time at worst */ });
  },
  setMinHours: (h) => {
    try { localStorage.setItem(PREF_KEY, String(h)); } catch { /* private mode */ }
    set({ minHours: h });
  },
}));

/** Tells the server the person is looking (on load, every few minutes while visible, and when the tab comes back). */
export function useAwayHeartbeat() {
  useEffect(() => {
    const beat = () => { if (document.visibilityState === "visible") awayApi.heartbeat().catch(() => {}); };
    beat();
    const id = setInterval(beat, HEARTBEAT_MS);
    document.addEventListener("visibilitychange", beat);
    return () => { clearInterval(id); document.removeEventListener("visibilitychange", beat); };
  }, []);
}

/** Mounted in the office: loads the summary on arrival and when the tab comes back, opens it on its own when due. */
export function AwayHost() {
  const load = useAway((s) => s.load);
  const open = useAway((s) => s.open);
  const uid = useSession((s) => s.user?.id ?? "anon");
  useEffect(() => {
    // Never on top of the first-run welcome: until it was seen the summary waits behind its HUD button.
    const auto = () => welcomeSeen(uid);
    void load(auto());
    const onVis = () => { if (document.visibilityState === "visible") void load(auto()); };
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, [load, uid]);
  useEffect(() => () => useAwayReplay.getState().stop(), []);
  return (
    <>
      {open && <AwaySheet />}
      <ReplayBar />
    </>
  );
}

/** HUD button: opens the summary of the last absence at any time. */
export function AwayButton() {
  const { t } = useT();
  const openNow = useAway((s) => s.openNow);
  const s = useAway((x) => x.summary);
  const waiting = s?.counts.approvals_waiting ?? 0;
  if (!s || s.first_visit || !s.since) return null;
  return (
    <button type="button" data-testid="away-open" onClick={openNow} title={t("away.open")}
      className="pointer-events-auto relative flex min-h-10 items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs font-semibold text-ink shadow-soft backdrop-blur transition hover:bg-panel2">
      <Icon name="moon" size={15} /><span className="max-sm:hidden">{t("away.open")}</span>
      {waiting > 0 && <span className="rounded-full bg-warn px-1.5 text-[10px] font-semibold text-white">{waiting}</span>}
    </button>
  );
}

function useDuration(minutes: number) {
  const { t } = useT();
  const d = awayDuration(minutes);
  return t(`away.duration.${d.unit}`, { count: d.n });
}

function AwaySheet() {
  const { t } = useT();
  const s = useAway((x) => x.summary);
  const loading = useAway((x) => x.loading);
  const close = useAway((x) => x.close);
  const minHours = useAway((x) => x.minHours);
  const setMinHours = useAway((x) => x.setMinHours);
  const startReplay = useAwayReplay((x) => x.start);
  const duration = useDuration(s?.away_minutes ?? 0);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") useAway.setState({ open: false }); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const hide = () => useAway.setState({ open: false }); // close without dismissing (navigating somewhere)
  const parts = s ? headlineParts(s.counts) : [];
  return (
    <div className="pointer-events-auto fixed inset-0 z-[60] flex items-end justify-center bg-black/25 sm:items-center sm:p-6" onClick={hide}>
      <section role="dialog" aria-modal="true" aria-labelledby="away-title" data-testid="away-sheet" onClick={(e) => e.stopPropagation()}
        className="flex max-h-[88dvh] w-full max-w-[560px] flex-col overflow-hidden rounded-t-2xl border border-line bg-panel shadow-pop sm:rounded-2xl">
        <header className="flex items-start gap-3 border-b border-line px-4 py-3.5 sm:px-5">
          <span className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-accent-soft text-accent"><Icon name="moon" size={18} /></span>
          <div className="min-w-0 flex-1">
            <h2 id="away-title" className="text-[15px] font-semibold text-ink">{t("away.title")}</h2>
            {s?.since && (
              <p className="mt-0.5 text-[12px] text-mute">
                {t("away.subtitle", { duration, since: fmtDateTime(s.since) })}
                {s.clipped && <> · {t("away.clipped")}</>}
              </p>
            )}
          </div>
          <button type="button" data-testid="away-close" onClick={close} aria-label={t("away.dismiss")}
            className="-m-1.5 rounded-md p-2.5 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={15} /></button>
        </header>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:px-5">
          {!s && loading && <p className="text-[13px] text-mute">{t("away.loading")}</p>}
          {s && !s.since && <p data-testid="away-empty" className="text-[13px] text-ink2">{t(s.first_visit ? "away.firstVisit" : "away.noAbsence")}</p>}
          {s?.since && (
            <>
              <p data-testid="away-headline" className="text-[14px] leading-relaxed text-ink">
                {s.quiet || !parts.length ? t("away.quiet") : parts.map((p) => t(`away.part.${p.key}`, { count: p.count })).join(" · ")}
                {s.spend.usd > 0 && <span className="text-ink2"> · {t("away.spend", { usd: fmtUsd(s.spend.usd), count: s.spend.calls })}</span>}
              </p>
              <ActNow s={s} onNavigate={hide} />
              {s.sections.map((sec) => <SectionBlock key={sec.key} sec={sec} onNavigate={hide} />)}
              <Agents s={s} />
              {s.timelapse.available && (
                <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-panel2 px-3 py-2.5">
                  <div className="min-w-0 text-[12px] text-ink2">
                    <div className="font-semibold text-ink">{t("away.timelapse.title")}</div>
                    {t("away.timelapse.body", { count: s.timelapse.total })}
                  </div>
                  <Btn kind="ghost" data-testid="away-timelapse" onClick={() => { hide(); startReplay(s.timelapse.frames); }}>
                    <Icon name="play" size={13} className="mr-1 inline-block align-[-2px]" />{t("away.timelapse.play")}
                  </Btn>
                </div>
              )}
            </>
          )}
        </div>

        <footer className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-3 sm:px-5">
          <label className="flex items-center gap-2 text-[11.5px] text-mute">
            {t("away.pref.label")}
            <select data-testid="away-min-hours" value={minHours} onChange={(e) => setMinHours(Number(e.target.value))}
              className="rounded-md border border-line bg-panel px-1.5 py-1 text-[11.5px] text-ink">
              {MIN_HOURS_CHOICES.map((h) => <option key={h} value={h}>{h === 0 ? t("away.pref.never") : t("away.pref.hours", { count: h })}</option>)}
            </select>
          </label>
          <div className="flex items-center gap-3">
            <span className="hidden text-[10.5px] text-mute sm:inline" title={t("away.method.hint")}>{t("away.method")}</span>
            <Btn kind="primary" data-testid="away-dismiss" onClick={close}>{t("away.dismiss")}</Btn>
          </div>
        </footer>
      </section>
    </div>
  );
}

function ActNow({ s, onNavigate }: { s: AwaySummary; onNavigate: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const approvals = useStore((x) => x.approvals);
  const [openId, setOpenId] = useState<string | null>(null);
  const { counts } = s;
  if (!s.act_now.length && !counts.approvals_waiting_other) return null;
  return (
    <div data-testid="away-act-now">
      <h3 className="mb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("away.actNow.title")}</h3>
      <ul className="space-y-1.5">
        {s.act_now.map((it) => {
          const live = approvals[it.ref_id];
          const decided = live && live.status !== "pending";
          return (
            <li key={it.ref_id} className="rounded-lg border border-amber-500/40 bg-amber-50/60 px-3 py-2 dark:bg-amber-500/10">
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] font-semibold text-ink">{it.title}</div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-[11px] text-mute">
                    {it.agent_id && <span>{name(it.agent_id)}</span>}
                    <span>· {t("away.waitingSince", { time: shortWhen(it.at) })}</span>
                    {it.new && <span className="rounded bg-accent-soft px-1 font-semibold text-accent">{t("away.new")}</span>}
                    {(it.required_approvals ?? 1) >= 2 && <span className="font-semibold">{t("away.double")}</span>}
                  </div>
                </div>
                {it.risk && <RiskBadge risk={it.risk} />}
                {decided ? <span className="text-[11px] font-semibold text-emerald-700">{t("away.handled")}</span> : (
                  <Btn kind="ghost" data-testid={`away-approval-${it.ref_id}`} onClick={() => {
                    if (live) setOpenId(openId === it.ref_id ? null : it.ref_id);
                    else { onNavigate(); go(`/approvals/${encodeURIComponent(it.ref_id)}`); }
                  }}>{openId === it.ref_id ? t("common.hide") : t("away.review")}</Btn>
                )}
              </div>
              {openId === it.ref_id && live && <div className="mt-2"><ApprovalCard a={live} scope="away" /></div>}
            </li>
          );
        })}
      </ul>
      {counts.approvals_waiting > s.act_now.length && (
        <button type="button" onClick={() => { onNavigate(); go("/dashboard"); }} className="mt-1.5 text-[11.5px] font-semibold text-accent">
          {t("away.more", { count: counts.approvals_waiting - s.act_now.length })}
        </button>
      )}
      {counts.approvals_waiting_other > 0 && <p className="mt-1.5 text-[11px] text-mute">{t("away.waitingOther", { count: counts.approvals_waiting_other })}</p>}
    </div>
  );
}

function itemText(it: AwayItem, t: (k: string, v?: Record<string, string | number>) => string, agent: (id: string) => string): { main: string; sub?: string } {
  switch (it.kind) {
    case "alert":
      return { main: t(alertKey(it.code), { detail: it.detail || "", agent: it.agent_id ? agent(it.agent_id) : "" }), sub: it.code };
    case "request_completed":
      return { main: it.title || t("away.untitled") };
    case "request_failed":
      return { main: it.title || t("away.untitled"), sub: t("away.kind.request_failed") };
    case "task_failed":
      return { main: it.title || t("away.untitled"), sub: [t("away.kind.task_failed"), it.detail].filter(Boolean).join(": ") };
    case "task_blocked":
      return { main: it.title || t("away.untitled"), sub: t("away.kind.task_blocked") };
    case "initiative":
      return { main: it.title };
    case "decision":
      return { main: it.title || t("away.untitled"), sub: t(`away.decision.${it.code || "other"}`) };
  }
  return { main: it.title };
}

const SEV_DOT: Record<string, string> = { high: "bg-red-500", medium: "bg-amber-500", low: "bg-sky-500", info: "bg-emerald-500" };

function SectionBlock({ sec, onNavigate }: { sec: AwaySection; onNavigate: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const select = useStore((x) => x.select);
  const reports = useStore((x) => x.reports);
  const [report, setReport] = useState<string | null>(null);
  const act = useCallback((it: AwayItem) => {
    switch (itemAction(it)) {
      case "report":
        if (reports[it.ref_id]) setReport(report === it.ref_id ? null : it.ref_id);
        else { onNavigate(); go("/dashboard"); }
        return;
      case "agent": onNavigate(); select(it.agent_id || it.ref_id); return;
      case "approval": // a decided approval: its history lives in the dashboard
      case "dashboard": onNavigate(); go("/dashboard"); return;
    }
  }, [reports, report, onNavigate, select]);
  return (
    <div data-testid={`away-section-${sec.key}`}>
      <h3 className="mb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t(`away.section.${sec.key}`, { count: sec.total })}</h3>
      <ul className="space-y-1">
        {sec.items.map((it) => {
          const txt = itemText(it, t, name);
          const action = itemAction(it);
          return (
            <li key={`${it.kind}-${it.ref_id}-${it.at}`} className="rounded-lg border border-line bg-panel2/60 px-3 py-2">
              <div className="flex items-start gap-2">
                <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${SEV_DOT[it.severity] || SEV_DOT.info}`} aria-hidden />
                <div className="min-w-0 flex-1">
                  <div className="break-words text-[12.5px] font-medium text-ink [overflow-wrap:anywhere]">{txt.main}</div>
                  <div className="mt-0.5 text-[11px] text-mute">
                    {shortWhen(it.at)}
                    {it.agent_id && it.kind !== "alert" && <> · {name(it.agent_id)}</>}
                    {txt.sub && it.kind !== "alert" && <> · <span className="break-words [overflow-wrap:anywhere]">{txt.sub}</span></>}
                    {!!it.repeats && <> · {t("away.repeats", { count: it.repeats + 1 })}</>}
                  </div>
                </div>
                {action !== "none" && (
                  <button type="button" onClick={() => act(it)} className="shrink-0 rounded-md px-2 py-1 text-[11.5px] font-semibold text-accent hover:bg-accent/10">
                    {t(`away.action.${action}`)}
                  </button>
                )}
              </div>
              {report === it.ref_id && reports[it.ref_id] && <div className="mt-2 rounded-md border border-line bg-panel p-3"><ReportView r={reports[it.ref_id]} /></div>}
            </li>
          );
        })}
      </ul>
      {sec.total > sec.items.length && <p className="mt-1 text-[11px] text-mute">{t("away.andMore", { count: sec.total - sec.items.length })}</p>}
    </div>
  );
}

function Agents({ s }: { s: AwaySummary }) {
  const { t } = useT();
  if (!s.agents.length) return null;
  return (
    <div data-testid="away-agents">
      <h3 className="mb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("away.agents.title")}</h3>
      <ul className="grid gap-1 sm:grid-cols-2">
        {s.agents.map((a) => (
          <li key={a.agent_id} className="flex items-center justify-between gap-2 rounded-md border border-line px-2.5 py-1.5 text-[12px]">
            <span className="truncate font-medium text-ink">{a.name || a.agent_id}</span>
            <span className="shrink-0 text-[11px] text-mute">
              {t("away.agents.tasks", { count: a.tasks_completed })}{a.tasks_failed > 0 && <> · <span className="text-red-700">{t("away.agents.failed", { count: a.tasks_failed })}</span></>}{a.usd > 0 && <> · {fmtUsd(a.usd)}</>}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Pill over the office while the timelapse plays. */
function ReplayBar() {
  const { t } = useT();
  const playing = useAwayReplay((x) => x.playing);
  const at = useAwayReplay((x) => x.at);
  const index = useAwayReplay((x) => x.index);
  const total = useAwayReplay((x) => x.total);
  const stop = useAwayReplay((x) => x.stop);
  if (!playing) return null;
  return (
    <div className="pointer-events-none absolute inset-x-0 top-[max(4.5rem,env(safe-area-inset-top))] z-40 flex justify-center px-4">
      <div data-testid="away-replay" role="status" className="pointer-events-auto flex items-center gap-3 rounded-full border border-line bg-panel/95 px-4 py-2 text-[12px] shadow-pop backdrop-blur">
        <Icon name="clock" size={14} className="text-accent" />
        <span className="font-semibold text-ink">{t("away.timelapse.playing")}</span>
        <span className="tabular-nums text-ink2">{at ? shortWhen(at) : ""}</span>
        <span className="tabular-nums text-mute">{index}/{total}</span>
        <button type="button" data-testid="away-replay-stop" onClick={stop} className="rounded-md px-2 py-0.5 font-semibold text-accent hover:bg-accent/10">{t("away.timelapse.stop")}</button>
      </div>
    </div>
  );
}
