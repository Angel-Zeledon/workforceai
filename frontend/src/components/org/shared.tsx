"use client";
import type { ReactNode } from "react";
import { useT } from "@/lib/i18n";
import type { Chart, ChartDept, ChartMember } from "@/lib/orgChartApi";
import { NO_REF, isNoRef, refKey, type Ref } from "@/lib/orgChartLogic";
import { Icon } from "../icons";

export const inputCls = "w-full rounded-lg border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-accent";
export const btnPrimary = "rounded-md bg-accent px-4 py-2 text-xs font-semibold text-white disabled:opacity-50";
export const btnGhost = "rounded-md border border-line px-4 py-2 text-xs font-semibold text-mute hover:text-ink disabled:opacity-40";
export const btnDanger = "rounded-md border border-red-400/60 px-4 py-2 text-xs font-semibold text-red-600 hover:bg-red-500/10 disabled:opacity-40";

export interface MemberAt extends ChartMember { dept_id: string; dept_name: string }

/** Every department of the chart as a flat list, parents first. */
export function allDepts(chart: Chart | null): ChartDept[] {
  const out: ChartDept[] = [];
  const walk = (ds: ChartDept[]) => ds.forEach((d) => { out.push(d); walk(d.children); });
  walk(chart?.roots ?? []);
  return out;
}

/** Every person and agent of the chart with the department it sits in ("" = unassigned). */
export function allMembers(chart: Chart | null): MemberAt[] {
  const out: MemberAt[] = [];
  for (const d of allDepts(chart)) for (const m of d.members) out.push({ ...m, dept_id: d.id, dept_name: d.name });
  for (const m of chart?.unassigned ?? []) out.push({ ...m, dept_id: "", dept_name: "" });
  return out;
}

export const refOf = (m: { kind: "person" | "agent"; id: string }): Ref => ({ kind: m.kind, id: m.id });

/** Value <-> Ref for <select> options. */
export const refValue = (r?: Ref | null) => (isNoRef(r) ? "" : refKey(r as Ref));
export const parseRefValue = (v: string): Ref => {
  if (!v) return NO_REF;
  const i = v.indexOf(":");
  return { kind: v.slice(0, i) as Ref["kind"], id: v.slice(i + 1) };
};

export function RefSelect({ value, onChange, options, noneLabel, testid, label }: {
  value: Ref; onChange: (r: Ref) => void; options: { ref: Ref; label: string }[]; noneLabel: string; testid: string; label: string;
}) {
  const known = options.some((o) => refValue(o.ref) === refValue(value));
  return (
    <label className="flex flex-col gap-1 text-[12px] font-semibold">
      {label}
      <select data-testid={testid} value={refValue(value)} onChange={(e) => onChange(parseRefValue(e.target.value))} className={inputCls}>
        <option value="">{noneLabel}</option>
        {!known && !isNoRef(value) && <option value={refValue(value)}>{value.id}</option>}
        {options.map((o) => <option key={refValue(o.ref)} value={refValue(o.ref)}>{o.label}</option>)}
      </select>
    </label>
  );
}

/** People and agents of the chart as picker options ("Ana (person)", "Marta (agent)"). */
export function memberOptions(chart: Chart | null, t: (k: string) => string, exclude?: Ref): { ref: Ref; label: string }[] {
  return allMembers(chart)
    .filter((m) => !exclude || refKey(refOf(m)) !== refKey(exclude))
    .map((m) => ({ ref: refOf(m), label: `${m.name} · ${m.kind === "agent" ? t("org.kind.agent") : t("org.kind.person")}` }));
}

export function KindIcon({ kind }: { kind: "person" | "agent" }) {
  const { t } = useT();
  return (
    <span title={t(`org.kind.${kind}`)} className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full ${kind === "agent" ? "bg-accent-soft text-accent" : "bg-panel2 text-ink2"}`}>
      <Icon name={kind === "agent" ? "pulse" : "user"} size={13} />
    </span>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 text-[12px] font-semibold">
      <span>{label}</span>
      {children}
      {hint && <span className="text-[11px] font-normal text-mute">{hint}</span>}
    </div>
  );
}

export const COMMON_ZONES = [
  "UTC", "America/Mexico_City", "America/Bogota", "America/Lima", "America/Santiago", "America/Argentina/Buenos_Aires", "America/Sao_Paulo",
  "America/New_York", "America/Chicago", "America/Los_Angeles", "Europe/Madrid", "Europe/Lisbon", "Europe/London", "Europe/Paris", "Asia/Tokyo",
];
