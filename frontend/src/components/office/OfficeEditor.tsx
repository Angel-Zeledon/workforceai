"use client";
// The office editor panel: the shell (title, tabs, undo, save) and the entry button. Each tab lives in ./editor/.
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { effective, styleOf, useOfficeLayout } from "@/lib/officeLayout";
import { STYLES_WITHOUT_EDITING } from "@/lib/officeLayoutModel";
import { Btn } from "../ui";
import { Icon } from "../icons";
import { ErrorLine } from "../connections/shared";
import dynamic from "next/dynamic";
import { StyleTab } from "./editor/StyleTab";
import { SpaceTab } from "./editor/SpaceTab";
import { DesksTab } from "./editor/DesksTab";
import { FurnitureTab } from "./editor/FurnitureTab";
import { FOCUS } from "./editor/parts";

export { LightControls, AutoLightToggle, LightButton } from "./editor/LightPanel";

const ThumbStudio = dynamic(() => import("../shop/ThumbStudio").then((m) => m.ThumbStudio), { ssr: false });

/** Entry point shown in the office HUD: only people who can spend coins can redecorate. */
export function EditOfficeButton() {
  const { t } = useT();
  const editing = useOfficeLayout((s) => s.editing);
  const can = useCan("shop:buy");
  const editable = useOfficeLayout((s) => s.officeEditable); // E2: another office is arranged by its responsible
  if (editing || !can || !editable) return null;
  return (
    <button type="button" data-testid="office-edit" onClick={() => useOfficeLayout.getState().startEdit()}
      className="pointer-events-auto flex min-h-10 items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs font-semibold text-ink shadow-soft backdrop-blur transition hover:bg-panel2">
      <Icon name="edit" size={15} />{t("oe.edit")}
    </button>
  );
}

const TABS = ["style", "space", "desks", "furniture"] as const;

export function OfficeEditor() {
  const { t } = useT();
  const editing = useOfficeLayout((s) => s.editing);
  const tab = useOfficeLayout((s) => s.tab);
  const saving = useOfficeLayout((s) => s.saving);
  const error = useOfficeLayout((s) => s.error);
  const canUndo = useOfficeLayout((s) => s.past.length > 0);
  const canRedo = useOfficeLayout((s) => s.future.length > 0);
  const grid = useOfficeLayout((s) => s.grid);
  const placing = useOfficeLayout((s) => s.placing);
  const dragging = useOfficeLayout((s) => s.dragging);
  const sel = useOfficeLayout((s) => s.selection);
  const dirty = useOfficeLayout((s) => !!s.draft && JSON.stringify(s.draft) !== JSON.stringify(s.layout));
  const noMove = useOfficeLayout((s) => (s.tab === "desks" || s.tab === "furniture") && STYLES_WITHOUT_EDITING.includes(styleOf(effective(s))));
  if (!editing) return null;
  const st = useOfficeLayout.getState;
  const hint = placing ? t("oe.hint.placing") : sel ? t("oe.hint.drag") : t("oe.hint.idle");
  // arrows between the tabs, like any tab list
  const onTabKey = (e: React.KeyboardEvent) => {
    const i = TABS.indexOf(tab as (typeof TABS)[number]);
    const to = e.key === "ArrowRight" ? (i + 1) % TABS.length : e.key === "ArrowLeft" ? (i - 1 + TABS.length) % TABS.length : -1;
    if (to < 0) return;
    e.preventDefault();
    st().setTab(TABS[to]);
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-testid="oe-tab-${TABS[to]}"]`)?.focus());
  };
  return (
    <>
      <ThumbStudio />
      <div data-testid="office-editor" className="pointer-events-auto absolute inset-x-0 bottom-0 z-40 flex max-h-[56dvh] flex-col overflow-hidden rounded-t-2xl border-b-0 pb-[env(safe-area-inset-bottom)] md:inset-x-auto md:bottom-4 md:left-4 md:top-4 md:max-h-none md:w-[380px] md:rounded-2xl md:border-b md:pb-0 border border-line bg-panel shadow-pop">
        <header className="border-b border-line px-4 pb-0 pt-3">
          <div className="flex items-center justify-between">
            <h2 className="font-display text-[15px] font-semibold text-ink">{t("oe.title")}</h2>
            <button type="button" onClick={() => st().cancel()} aria-label={t("oe.cancel")} className={`-m-1.5 rounded-md p-2.5 text-mute hover:bg-panel2 hover:text-ink ${FOCUS}`}><Icon name="x" size={16} /></button>
          </div>
          <div role="tablist" onKeyDown={onTabKey} className="mt-2 flex gap-1">
            {TABS.map((k) => (
              <button key={k} type="button" role="tab" aria-selected={tab === k} tabIndex={tab === k ? 0 : -1} data-testid={`oe-tab-${k}`} onClick={() => st().setTab(k)}
                className={`-mb-px min-h-10 border-b-2 px-3 py-2 text-[12.5px] font-medium transition ${FOCUS} ${tab === k ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>{t(`oe.tab.${k}`)}</button>
            ))}
          </div>
        </header>
        <div role="tabpanel" className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
          {noMove && <p data-testid="oe-view-note" className="mb-3 rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[12px] text-ink2">{t("oe.style.noEdit")}</p>}
          {tab === "style" && <StyleTab />}
          {tab === "space" && <SpaceTab />}
          {tab === "desks" && <DesksTab />}
          {tab === "furniture" && <FurnitureTab />}
        </div>
        <footer className="space-y-2 border-t border-line bg-panel2/60 px-4 py-3">
          {error && (
            <div className="space-y-1.5">
              <ErrorLine msg={t(`oe.err.${error}`)} />
              {error === "conflict" && <Btn onClick={() => { st().cancel(); void st().load(); }}>{t("oe.reload")}</Btn>}
            </div>
          )}
          <div className="flex items-center gap-1.5">
            <button type="button" disabled={!canUndo} onClick={() => st().undo()} aria-label={t("oe.undo")} title={`${t("oe.undo")} (Ctrl+Z)`} data-testid="oe-undo" className={`min-h-10 min-w-10 rounded-md border border-line-strong bg-panel p-1.5 text-ink2 transition hover:bg-panel2 disabled:opacity-40 ${FOCUS}`}><Icon name="back" size={15} /></button>
            <button type="button" disabled={!canRedo} onClick={() => st().redo()} aria-label={t("oe.redo")} title={`${t("oe.redo")} (Ctrl+Y)`} data-testid="oe-redo" className={`min-h-10 min-w-10 rounded-md border border-line-strong bg-panel p-1.5 text-ink2 transition hover:bg-panel2 disabled:opacity-40 ${FOCUS}`}><Icon name="back" size={15} className="-scale-x-100" /></button>
            <button type="button" aria-pressed={grid} onClick={() => st().toggleGrid()} aria-label={t("oe.grid")} title={t("oe.grid")} className={`flex min-h-10 min-w-10 items-center justify-center rounded-md border p-1.5 transition ${FOCUS} ${grid ? "border-accent/40 bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink2 hover:bg-panel2"}`}><Icon name="grid" size={15} /></button>
            <span className="flex-1" />
            <Btn className="min-h-10" onClick={() => st().cancel()} data-testid="oe-cancel">{t("oe.cancel")}</Btn>
            <Btn className="min-h-10" kind="primary" disabled={saving} onClick={() => void st().save()} data-testid="oe-save">{saving ? t("oe.saving") : dirty ? t("oe.save") : t("oe.done")}</Btn>
          </div>
        </footer>
      </div>
      <div className="pointer-events-none absolute left-[412px] top-4 z-30 hidden rounded-full border border-line bg-panel/95 px-3.5 py-1.5 text-[12px] font-medium text-ink2 shadow-soft backdrop-blur md:block">
        {dragging ? t("oe.hint.moving") : hint}
      </div>
    </>
  );
}
