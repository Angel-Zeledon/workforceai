"use client";
import { useEffect, useRef, useState } from "react";
import { useStore } from "@/lib/store";
import { LOCALES, langName, useT, type Locale } from "@/lib/i18n";
import { resolveAgent, usePreferences, type LabelSize } from "@/lib/preferences";
import { AgentCustomizer } from "./AgentCustomizer";
import { Icon, type IconName } from "./icons";
import { usePageMode } from "./nav/pageMode";

function Seg<T extends string>({ value, options, onChange, testid, label, compact }: { value: T; options: { v: T; label: string; icon?: IconName }[]; onChange: (v: T) => void; testid: string; label?: string; compact?: boolean }) {
  return (
    <div className="inline-flex rounded-md border border-line bg-panel p-0.5 shadow-pop" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button key={o.v} type="button" role="radio" aria-checked={value === o.v} data-testid={`${testid}-${o.v}`} onClick={() => onChange(o.v)}
          title={o.label} aria-label={o.label} className={`whitespace-nowrap rounded-md px-3 py-1 text-[11px] font-medium transition ${value === o.v ? "bg-accent-soft text-accent" : "text-mute hover:text-ink"}`}>
          {o.icon ? <Icon name={o.icon} size={13} className={`inline-block align-[-2px] ${compact ? "sm:mr-1.5" : "mr-1.5"}`} /> : null}<span className={compact ? "hidden sm:inline" : ""}>{o.label}</span>
        </button>
      ))}
    </div>
  );
}

function Toggle({ on, onChange, label, testid }: { on: boolean; onChange: (v: boolean) => void; label: string; testid: string }) {
  return (
    <button type="button" role="switch" aria-checked={on} data-testid={testid} onClick={() => onChange(!on)} className="flex w-full items-center justify-between rounded-md px-1 py-1 text-left text-[12px] font-medium text-ink hover:bg-mute/[0.08]">
      <span>{label}</span>
      <span className={`relative h-5 w-9 shrink-0 rounded-full transition ${on ? "bg-accent" : "bg-mute/30"}`}>
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-all ${on ? "left-[18px]" : "left-0.5"}`} />
      </span>
    </button>
  );
}

/** Office settings popover (language, labels, motion, per-employee customization). Opened from the admin menu. */
export function OfficeSettingsPanel({ onClose }: { onClose: () => void }) {
  const { t, locale } = useT();
  const prefs = usePreferences((s) => s.prefs);
  const set = usePreferences((s) => s.set);
  const order = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const [emp, setEmp] = useState<string>("");
  const current = emp && agents[emp] ? emp : order[0] ?? "";
  const page = usePageMode();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); prev?.focus?.(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return (
    <div className={page ? undefined : "pointer-events-auto fixed inset-0 z-[54]"} onMouseDown={page ? undefined : (e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div ref={ref} tabIndex={-1} role="dialog" aria-label={t("settings.title")} data-testid="office-settings-panel"
        className={page ? "mx-auto w-full max-w-xl p-5 outline-none" : "ac-pop absolute right-3 top-[60px] max-sm:left-3 max-h-[calc(100dvh-76px)] w-[330px] max-w-[calc(100vw-24px)] overflow-y-auto rounded-xl border border-line bg-panel p-4 shadow-float outline-none"}>
          <div className={`mb-3 flex items-center justify-between ${page ? "hidden" : ""}`}>
            <h3 className="text-[15px] font-semibold tracking-tight text-ink">{t("settings.title")}</h3>
            <button type="button" onClick={onClose} aria-label={t("common.close")} className="flex min-h-10 min-w-10 items-center justify-center rounded-md p-1 text-mute hover:bg-mute/10 hover:text-ink sm:min-h-0 sm:min-w-0"><Icon name="x" size={15} /></button>
          </div>

          <section className="mb-4">
            <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("settings.language")}</div>
            <Seg<Locale> value={locale} testid="language-option" label={t("settings.language")} onChange={(v) => set({ locale: v })}
              options={LOCALES.map((l) => ({ v: l, label: langName(l) }))} />
          </section>

          <section className="mb-4 space-y-1">
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("settings.labels")}</div>
            <Toggle testid="setting-show-names" on={prefs.showNames} onChange={(v) => set({ showNames: v })} label={t("settings.showNames")} />
            <div className="flex items-center justify-between px-1 py-1 text-[12px] font-medium text-ink">
              <span>{t("settings.labelSize")}</span>
              <Seg<LabelSize> value={prefs.labelSize} testid="setting-label-size" onChange={(v) => set({ labelSize: v })}
                options={[{ v: "s", label: "S" }, { v: "m", label: "M" }, { v: "l", label: "L" }]} />
            </div>
            <Toggle testid="setting-show-bubbles" on={prefs.showBubbles} onChange={(v) => set({ showBubbles: v })} label={t("settings.showBubbles")} />
            <Toggle testid="setting-reduce-motion" on={prefs.reduceMotion} onChange={(v) => set({ reduceMotion: v })} label={t("settings.reduceMotion")} />
          </section>

          <section>
            <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("settings.employees")}</div>
            <div className="mb-3 flex flex-wrap gap-1.5" data-testid="settings-employee-list">
              {order.map((id) => {
                const v = resolveAgent(agents[id], prefs.agents[id]);
                return (
                  <button key={id} type="button" data-testid={`settings-employee-${id}`} onClick={() => setEmp(id)}
                    className="flex items-center gap-1.5 rounded-md border bg-panel2 px-2 py-0.5 text-[11px] font-medium text-ink transition hover:bg-mute/10"
                    style={{ borderColor: current === id ? v.color : "rgb(var(--line))" }}>
                    <span className="h-3 w-3 rounded-full" style={{ background: v.color }} />{v.label}
                  </button>
                );
              })}
            </div>
            {current && <AgentCustomizer key={current} id={current} tid="settings-" />}
          </section>
      </div>
    </div>
  );
}
