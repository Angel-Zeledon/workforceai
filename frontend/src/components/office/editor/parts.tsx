"use client";
// Small pieces shared by the tabs of the office editor. The panel keeps ONE look (white, the brand blue, IBM Plex Sans,
// ~10 px corners) whatever the office style is: only the pictures inside the little windows follow the style.
import { useState } from "react";
import { isHex } from "@/lib/officeLayoutModel";

/** Keyboard focus the same everywhere in the panel: a 2 px brand-blue ring that stays clear of the border. */
export const FOCUS = "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

export function Section({ title, children, aside }: { title: string; children: React.ReactNode; aside?: React.ReactNode }) {
  return (
    <section className="space-y-2.5">
      <div className="flex items-center justify-between gap-2"><h3 className="text-[12.5px] font-semibold text-ink">{title}</h3>{aside}</div>
      {children}
    </section>
  );
}

export function Swatches({ colors, value, onPick, label }: { colors: string[]; value: string; onPick: (c: string) => void; label: (c: string) => string }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {colors.map((c) => (
        <button key={c} type="button" aria-label={label(c)} aria-pressed={value.toLowerCase() === c.toLowerCase()} onClick={() => onPick(c)}
          className={`h-7 w-7 [@media(pointer:coarse)]:h-10 [@media(pointer:coarse)]:w-10 rounded-full border transition hover:scale-110 ${FOCUS} ${value.toLowerCase() === c.toLowerCase() ? "border-accent ring-2 ring-accent/40" : "border-line-strong"}`} style={{ background: c }} />
      ))}
    </div>
  );
}

export function ColorField({ value, onPick, label }: { value: string; onPick: (c: string) => void; label: string }) {
  const [text, setText] = useState("");
  return (
    <div className="mt-2 flex items-center gap-2 text-[12px] text-mute">
      <label className="relative flex h-7 w-7 [@media(pointer:coarse)]:h-10 [@media(pointer:coarse)]:w-10 cursor-pointer items-center justify-center overflow-hidden rounded-full border border-line-strong focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent" style={{ background: value }} title={label}>
        <input type="color" value={isHex(value) && value.length === 7 ? value : "#ffffff"} onChange={(e) => onPick(e.target.value)} className="absolute inset-0 h-full w-full cursor-pointer opacity-0" aria-label={label} />
      </label>
      <input value={text} onChange={(e) => { setText(e.target.value); if (isHex(e.target.value)) onPick(e.target.value); }} placeholder={value}
        className={`w-24 [@media(pointer:coarse)]:min-h-10 rounded-md border border-line bg-panel2 px-2 py-1 font-mono text-[11.5px] text-ink ${FOCUS}`} aria-label={label} />
      <span>{label}</span>
    </div>
  );
}

export function Range({ label, value, min, max, step, onChange, left, right, testid }: { label: string; value: number; min: number; max: number; step: number; onChange: (v: number) => void; left?: string; right?: string; testid?: string }) {
  return (
    <label className="block text-[12px] text-mute">
      <span className="mb-1 flex justify-between"><span>{label}</span><span className="font-mono text-ink2">{value.toFixed(2)}</span></span>
      <input data-testid={testid} type="range" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} className="w-full accent-accent" />
      {(left || right) && <span className="flex justify-between text-[11px]"><span>{left}</span><span>{right}</span></span>}
    </label>
  );
}

/**
 * One little window: a picture on top, a name under it. `selected` is the single state that reads at a glance (brand-blue
 * border and ring); `children` is the picture (an ItemThumb, a swatch...). Used for furniture, desks, chairs, views, rooms.
 */
export function Tile({ selected, onClick, name, sub, children, testid, pressed = "pressed", title, className = "" }: {
  selected: boolean; onClick: () => void; name: string; sub?: string; children: React.ReactNode; testid?: string; pressed?: "pressed" | "current"; title?: string; className?: string;
}) {
  return (
    <button type="button" data-testid={testid} title={title ?? name} aria-pressed={pressed === "pressed" ? selected : undefined} aria-current={pressed === "current" && selected ? "true" : undefined} onClick={onClick}
      className={`group flex min-w-0 flex-col overflow-hidden rounded-[10px] border bg-panel text-left transition ${FOCUS} ${selected ? "border-accent ring-2 ring-accent/30" : "border-line hover:border-line-strong hover:shadow-soft"} ${className}`}>
      <span className={`flex items-center justify-center bg-panel2/70 py-1.5 transition-colors ${selected ? "bg-accent-soft/50" : ""}`}>{children}</span>
      <span className="block px-2 py-1.5">
        <span className="line-clamp-2 min-h-[2.1rem] break-words text-[12px] font-medium leading-[1.05rem] text-ink">{name}</span>
        {sub && <span className="mt-0.5 block truncate text-[11px] leading-tight text-mute">{sub}</span>}
      </span>
    </button>
  );
}

/** Row of a state that has nothing yet: a quiet picture and one clear action (never a dead end). */
export function EmptyArt({ title, body, action, onAction, art }: { title: string; body?: string; action: string; onAction: () => void; art: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-[10px] border border-dashed border-line-strong px-4 py-5 text-center">
      <div className="h-20 w-28 opacity-90">{art}</div>
      <p className="text-[12.5px] font-semibold text-ink">{title}</p>
      {body && <p className="text-[12px] leading-snug text-mute">{body}</p>}
      <button type="button" onClick={onAction} className={`mt-1 min-h-10 rounded-lg bg-accent px-4 text-[12.5px] font-semibold text-white transition hover:brightness-110 ${FOCUS}`}>{action}</button>
    </div>
  );
}
