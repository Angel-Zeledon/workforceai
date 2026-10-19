"use client";
import { useEffect, useState, type ReactNode } from "react";
import { intlLocale, trOr, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { resolveAgent, usePreferences } from "@/lib/preferences";
import { MeetingsError, type Attendee, type Meeting, type Provider } from "./api";
import { dayKey, type Phase } from "./logic";

export const PHASE_COLOR: Record<Phase, string> = {
  live: "#2f7d55", soon: "#a86208", scheduled: "#2b6cb0", overdue: "#b4443c", ended: "#5b6678", cancelled: "#8a8f9a",
};

export function PhaseBadge({ phase, testId = "meeting-phase" }: { phase: Phase; testId?: string }) {
  const { t } = useT();
  const c = PHASE_COLOR[phase];
  return (
    <span data-testid={testId} data-phase={phase} className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}1a`, color: c }}>
      <span className={`h-1.5 w-1.5 rounded-full ${phase === "live" ? "animate-pulse" : ""}`} style={{ background: c }} />
      {t(`meetings.phase.${phase}`)}
    </span>
  );
}

export const PROVIDERS: Provider[] = ["jitsi", "meet", "zoom", "teams", "custom", "none"];

export function ProviderChip({ provider }: { provider: Provider }) {
  const { t } = useT();
  if (provider === "none") return null;
  return <span className="whitespace-nowrap rounded-md border border-line bg-panel2 px-1.5 py-[1px] text-[10px] font-semibold text-ink2">{t(`meetings.provider.${provider}`)}</span>;
}

/** Translated name of a room (default rooms are translated; custom rooms show their own name). */
export function useRoomName(rooms?: { id: string; name?: string }[]) {
  const { t } = useT();
  return (id: string) => {
    if (!id) return t("meetings.room.none");
    const r = rooms?.find((x) => x.id === id);
    return r?.name || trOr(`meetings.room.${id}`, id);
  };
}

/** Localized text of an error thrown by the API layer. */
export function errText(e: unknown, t: (k: string) => string): string {
  const code = e instanceof MeetingsError ? e.code : "generic";
  return trOr(`meetings.error.${code}`, t("meetings.error.generic"));
}

const fmt = (o: Intl.DateTimeFormatOptions) => (d: Date) => new Intl.DateTimeFormat(intlLocale(), o).format(d);
export const fmtTimeShort = (iso: string) => fmt({ hour: "2-digit", minute: "2-digit" })(new Date(iso));
export const fmtDayLong = (key: string) => fmt({ weekday: "long", day: "numeric", month: "long" })(new Date(`${key}T12:00:00`));
export const fmtDayShort = (iso: string) => fmt({ weekday: "short", day: "numeric", month: "short" })(new Date(iso));
export const fmtDateTimeShort = (iso: string) => fmt({ weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })(new Date(iso));

export function fmtRange(start: string, end: string): string {
  const sameDay = dayKey(start) === dayKey(end);
  return sameDay ? `${fmtDayShort(start)}, ${fmtTimeShort(start)} – ${fmtTimeShort(end)}` : `${fmtDateTimeShort(start)} – ${fmtDateTimeShort(end)}`;
}

/** Re-renders every `ms` (phases like "starts soon" change with the clock). */
export function useNow(ms = 30000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => { const i = setInterval(() => setNow(new Date()), ms); return () => clearInterval(i); }, [ms]);
  return now;
}

export function useAgentList() {
  const agents = useStore((s) => s.agents);
  const prefs = usePreferences((s) => s.prefs.agents);
  return Object.values(agents).map((a) => {
    const r = resolveAgent(a, prefs[a.id]);
    return { id: a.id, name: r.name, color: r.color, title: a.title };
  });
}

export function AttendeeChip({ a, color }: { a: Attendee; color?: string }) {
  const { t } = useT();
  const c = a.kind === "agent" ? color ?? "#2b6cb0" : "#5b6678";
  return (
    <span data-testid="attendee-chip" data-kind={a.kind} title={a.email || a.id || a.name}
      className="inline-flex max-w-full items-center gap-1 truncate rounded-full border px-2 py-[2px] text-[11px]" style={{ borderColor: `${c}55`, background: `${c}14`, color: c }}>
      <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ background: c }} />
      <span className="truncate">{a.name}</span>
      {a.kind === "agent" && <span className="shrink-0 text-[9px] font-semibold uppercase opacity-70">{t("meetings.attendee.ai")}</span>}
      {a.kind === "external" && <span className="shrink-0 text-[9px] font-semibold uppercase opacity-70">{t("meetings.attendee.guest")}</span>}
    </span>
  );
}

export function Attendees({ list, max = 99 }: { list: Attendee[]; max?: number }) {
  const agents = useAgentList();
  const color = (id?: string) => agents.find((x) => x.id === id)?.color;
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {list.slice(0, max).map((a, i) => <AttendeeChip key={`${a.kind}:${a.id ?? a.email ?? a.name}:${i}`} a={a} color={a.kind === "agent" ? color(a.id) : undefined} />)}
      {list.length > max && <span className="text-[11px] text-mute">+{list.length - max}</span>}
    </span>
  );
}

export const inputCls = "w-full rounded-lg border border-line bg-panel2 px-3 py-1.5 text-xs outline-none focus:border-accent disabled:opacity-50";

export function ErrorBar({ msg, testId = "meetings-error" }: { msg: string; testId?: string }) {
  if (!msg) return null;
  return <div data-testid={testId} role="alert" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{msg}</div>;
}

export function Section({ title, children, right }: { title: string; children: ReactNode; right?: ReactNode }) {
  return (
    <section className="rounded-xl border border-line bg-panel p-4">
      <div className="mb-2.5 flex items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{title}</h3>
        {right}
      </div>
      {children}
    </section>
  );
}

/** A meeting that is still open (its link can be used). */
export const isOpen = (m: Pick<Meeting, "status">) => m.status === "scheduled" || m.status === "live";

/** Copies text; returns whether it worked (clipboard needs a secure context). */
export async function copyText(text: string): Promise<boolean> {
  try { await navigator.clipboard.writeText(text); return true; } catch { return false; }
}
