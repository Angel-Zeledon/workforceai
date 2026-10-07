"use client";
import { useState } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import {
  ACCESSORIES, HAIR_STYLES, NAME_MAX, NICK_MAX, PASTEL_COLORS, SKIN_TONES,
  resolveAgent, useAgentView, usePreferences,
} from "@/lib/preferences";

const field = "w-full rounded-xl border border-line bg-panel2 px-3 py-1.5 text-xs font-semibold text-ink outline-none focus:border-accent";

/**
 * Employee customization form (name, nickname, title, color, skin, hair, accessory).
 * `tid` prefixes the data-testids so the form can be mounted in several places without duplicates
 * ("" in the agent panel, "settings-" in the office settings panel).
 */
export function AgentCustomizer({ id, tid = "" }: { id: string; tid?: string }) {
  const { t } = useT();
  const agent = useStore((s) => s.agents[id]);
  const ap = usePreferences((s) => s.prefs.agents[id]);
  const setAgent = usePreferences((s) => s.setAgent);
  const resetAgent = usePreferences((s) => s.resetAgent);
  const view = useAgentView(id);
  if (!agent) return null;
  const base = resolveAgent(agent, undefined);
  return (
    <div className="space-y-3" data-testid={`${tid}customizer-${id}`}>
      <label className="block">
        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.fullName")}</span>
        <input data-testid={`${tid}name-input-${id}`} className={field} maxLength={NAME_MAX} value={ap?.name ?? ""} placeholder={base.name}
          onChange={(e) => setAgent(id, { name: e.target.value })} />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="block">
          <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.nickname")}</span>
          <input data-testid={`${tid}nickname-input-${id}`} className={field} maxLength={NICK_MAX} value={ap?.nickname ?? ""} placeholder={base.label}
            onChange={(e) => setAgent(id, { nickname: e.target.value })} />
        </label>
        <label className="block">
          <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.jobTitle")}</span>
          <input data-testid={`${tid}title-input-${id}`} className={field} maxLength={40} value={ap?.title ?? ""} placeholder={base.title}
            onChange={(e) => setAgent(id, { title: e.target.value })} />
        </label>
      </div>
      <div className="text-[10px] font-semibold text-mute">{t("custom.nicknameHint", { max: NICK_MAX })}</div>

      <div>
        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.color")}</span>
        <div className="flex flex-wrap gap-1.5" data-testid={`${tid}color-picker-${id}`} role="radiogroup" aria-label={t("custom.color")}>
          {PASTEL_COLORS.map((c, i) => (
            <button key={c} type="button" role="radio" aria-checked={view.color === c} data-testid={`${tid}color-option-${id}-${i}`}
              onClick={() => setAgent(id, { color: c })} title={c}
              className="h-7 w-7 rounded-full border transition hover:scale-110"
              style={{ background: c, borderColor: view.color === c ? "#151b26" : "#ffffff", boxShadow: "0 2px 0 rgba(150,110,50,.25)" }} />
          ))}
        </div>
      </div>

      <div>
        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.skin")}</span>
        <div className="flex flex-wrap gap-1.5" data-testid={`${tid}skin-picker-${id}`}>
          {SKIN_TONES.map((c, i) => (
            <button key={c} type="button" data-testid={`${tid}skin-option-${id}-${i}`} onClick={() => setAgent(id, { skin: c })}
              className="h-6 w-6 rounded-full border transition hover:scale-110"
              style={{ background: c, borderColor: view.skin === c ? "#151b26" : "#ffffff" }} />
          ))}
        </div>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <label className="block">
          <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.hair")}</span>
          <select data-testid={`${tid}hair-select-${id}`} className={field} value={view.hairStyle} onChange={(e) => setAgent(id, { hairStyle: e.target.value as never })}>
            {HAIR_STYLES.map((h) => <option key={h.v} value={h.v}>{t(`hair.${h.v}`)}</option>)}
          </select>
        </label>
        <label className="block">
          <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute">{t("custom.accessory")}</span>
          <select data-testid={`${tid}accessory-select-${id}`} className={field} value={view.accessory} onChange={(e) => setAgent(id, { accessory: e.target.value as never })}>
            {ACCESSORIES.map((a) => <option key={a.v} value={a.v}>{t(`accessory.${a.v}`)}</option>)}
          </select>
        </label>
      </div>

      <button type="button" data-testid={`${tid}customizer-reset-${id}`} onClick={() => resetAgent(id)} disabled={!ap}
        className="rounded-md border border-line px-3 py-1 text-[11px] font-semibold text-mute transition hover:text-ink disabled:opacity-40">
        {t("custom.reset")}
      </button>
    </div>
  );
}

/** Inline editable display name (used on dashboard cards). Click the pencil, Enter/blur saves, Escape cancels. */
export function InlineName({ id, className = "" }: { id: string; className?: string }) {
  const { t } = useT();
  const view = useAgentView(id);
  const setAgent = usePreferences((s) => s.setAgent);
  return <InlineNameInner id={id} value={view.name} className={className} placeholderLabel={t("custom.editName")} onSave={(v) => setAgent(id, { name: v.trim() })} />;
}

function InlineNameInner({ id, value, className, placeholderLabel, onSave }: { id: string; value: string; className: string; placeholderLabel: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  if (editing) {
    return (
      <input autoFocus data-testid={`dash-name-input-${id}`} maxLength={NAME_MAX} value={draft} onClick={(e) => e.stopPropagation()}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => { onSave(draft); setEditing(false); }}
        onKeyDown={(e) => { if (e.key === "Enter") { onSave(draft); setEditing(false); } if (e.key === "Escape") setEditing(false); }}
        className={`min-w-0 rounded-lg border border-accent bg-panel px-1.5 py-0 text-ink outline-none ${className}`} />
    );
  }
  return (
    <span className={`inline-flex min-w-0 items-center gap-1 ${className}`}>
      <span className="truncate">{value}</span>
      <button type="button" data-testid={`dash-name-edit-${id}`} aria-label={placeholderLabel} title={placeholderLabel}
        onClick={(e) => { e.stopPropagation(); setDraft(value); setEditing(true); }}
        className="shrink-0 rounded-full px-1 text-[11px] text-mute transition hover:bg-mute/[0.14] hover:text-ink">✎</button>
    </span>
  );
}
