"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { permissionsApi, statusOf, type AgentPermissions, type CategoryView } from "@/lib/permissionsApi";
import { exceptionLabel, exceptionsApply, validAskIf, type AskIf, type Mode, type PlainLevel } from "@/lib/permissionsLogic";
import { Btn, Card, Loading } from "../ui";

/** "Permissions" of one agent (docs/architecture/06-permisos-autonomia.md section 11): the level in plain words, the
 *  exceptions per kind of action and the safety floor. The server decides who may change what; the UI only mirrors it. */
export function PermissionsSection({ id }: { id: string }) {
  const { t } = useT();
  const tick = useStore((s) => s.trustTick[id] || 0); // agent.autonomy_changed reloads it
  const [v, setV] = useState<AgentPermissions | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [lift, setLift] = useState<string | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(false);

  const load = useCallback(async () => {
    try { setV(await permissionsApi.agent(id)); setErr(""); } catch { setErr("perm.error.load"); }
  }, [id]);
  useEffect(() => { void load(); }, [load, tick]);

  const run = async (f: () => Promise<AgentPermissions>) => {
    setBusy(true); setErr("");
    try { setV(await f()); } catch (e) {
      const st = statusOf(e);
      setErr(st === 403 ? "perm.error.forbidden" : st === 409 ? "perm.error.conflict" : "perm.error.action");
    } finally { setBusy(false); }
  };

  if (!v) return <Card title={t("perm.title")}>{err ? <p className="text-xs text-err">{t(err)}</p> : <Loading rows={2} />}</Card>;
  const levels: PlainLevel[] = showAdvanced || v.level === "suggest" ? ["ask_all", "important", "alone", "suggest"] : ["ask_all", "important", "alone"];
  const can = v.can_edit && !busy;
  const active = v.factory && exceptionsApply(v.level);

  return (
    <Card title={t("perm.title")}>
      <div data-testid="agent-permissions" className="space-y-4">
        <p className="text-xs text-ink2">{t("perm.intro")}</p>
        {!v.factory && <p data-testid="perm-legacy" className="rounded-lg border border-line bg-panel2 px-3 py-2 text-[11.5px] leading-snug text-ink2">{t("perm.legacy")}</p>}
        <fieldset disabled={!can} className="space-y-1.5" aria-label={t("perm.title")}>
          {levels.map((l) => {
            const o = v.options.find((x) => x.level === l);
            const on = v.factory && v.level === l;
            const disabled = !o?.allowed;
            return (
              <label key={l} data-testid={`perm-level-${l}`}
                className={`flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2 transition ${on ? "border-accent bg-accent-soft" : "border-line hover:border-line-strong"} ${disabled ? "cursor-not-allowed opacity-55" : ""}`}>
                <input type="radio" name={`perm-level-${id}`} className="mt-0.5 accent-[var(--accent,#2b6cb0)]" checked={on} disabled={disabled}
                  onChange={() => run(() => permissionsApi.level(id, l))} />
                <span className="min-w-0">
                  <span className="block text-[12.5px] font-semibold text-ink">{t(`perm.level.${l}`)}</span>
                  <span className="block text-[11.5px] leading-snug text-mute">{t(`perm.level.${l}.desc`)}</span>
                  {disabled && o?.reason && <span className="mt-0.5 block text-[11px] text-warn">{t(`perm.reason.${["role_ceiling", "kill_switch_active", "read_only_mode", "agent_paused"].includes(o.reason) ? o.reason : "other"}`)}</span>}
                </span>
              </label>
            );
          })}
        </fieldset>
        {!showAdvanced && v.level !== "suggest" && (
          <button type="button" className="text-[11px] font-medium text-accent hover:underline" onClick={() => setShowAdvanced(true)}>{t("perm.advanced")}</button>
        )}

        <div>
          <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("perm.exceptions.title")}</div>
          {!v.factory ? <p className="text-[11.5px] text-mute">{t("perm.exceptions.legacy")}</p>
            : !active ? <p className="text-[11.5px] text-mute">{t("perm.exceptions.inactive")}</p> : null}
          <ul className="mt-1.5 space-y-2">
            {v.categories.map((c) => (
              <CategoryRow key={c.category} agentId={id} c={c} disabled={!can || !active} canEdit={can} canLift={v.can_lift_floor && v.factory && !busy}
                onSet={(m, cond) => run(() => m === "" ? permissionsApi.removeException(id, c.category) : permissionsApi.setException(id, c.category, m, cond))}
                onLift={() => setLift(c.category)} onRestore={() => run(() => permissionsApi.restoreFloor(id, c.category))} />
            ))}
          </ul>
        </div>
        {!v.can_edit && <p className="text-[11px] text-mute">{t("perm.readonly")}</p>}
        {err && <p role="alert" className="text-[11.5px] text-err">{t(err)}</p>}
        {v.updated_at && <p className="text-[10.5px] text-mute">{t("perm.updated", { who: v.updated_by ?? "", when: new Date(v.updated_at).toLocaleString() })}</p>}
      </div>
      {lift && <LiftFloorDialog agent={v.name} cat={lift} onCancel={() => setLift(null)}
        onConfirm={() => { const c = lift; setLift(null); void run(() => permissionsApi.liftFloor(id, c)); }} />}
    </Card>
  );
}

function CategoryRow({ agentId, c, disabled, canEdit, canLift, onSet, onLift, onRestore }: {
  agentId: string; c: CategoryView; disabled: boolean; canEdit: boolean; canLift: boolean;
  onSet: (m: Mode | "", cond?: AskIf) => void; onLift: () => void; onRestore: () => void;
}) {
  const { t } = useT();
  const mode: Mode | "" = c.exception?.mode ?? "";
  const [draft, setDraft] = useState<AskIf>(c.exception?.if ?? {});
  const [editing, setEditing] = useState(false);
  useEffect(() => { setDraft(c.exception?.if ?? {}); }, [c.exception]);
  const floored = c.floor && !c.lifted;
  return (
    <li data-testid={`perm-cat-${c.category}`} className="rounded-lg border border-line px-3 py-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="min-w-0">
          <span className="block text-[12.5px] font-medium text-ink">{t(`perm.cat.${c.category}`)}</span>
          <span className="block text-[11px] text-mute">{t(`perm.now.${c.effective}`)}
            {c.exception?.mode === "ask_if" && <> · {exceptionLabel("ask_if", c.exception.if).map((x) => t(x.key, x.vars)).join(", ")}</>}</span>
        </span>
        {floored ? (
          <span className="rounded-full bg-warn/10 px-2 py-0.5 text-[10.5px] font-semibold text-warn" title={t("perm.floor.explain")}>{t("perm.floor.badge")}</span>
        ) : (
          <select aria-label={t(`perm.cat.${c.category}`)} data-testid={`perm-mode-${c.category}`} disabled={disabled} value={editing ? "ask_if" : mode}
            onChange={(e) => {
              const m = e.target.value as Mode | "";
              if (m === "ask_if") { setEditing(true); return; }
              setEditing(false); onSet(m);
            }}
            className="rounded-md border border-line-strong bg-panel px-2 py-1 text-[11.5px] text-ink disabled:opacity-50">
            <option value="">{t("perm.mode.default")}</option>
            <option value="ask">{t("perm.mode.ask")}</option>
            <option value="never">{t("perm.mode.never")}</option>
            <option value="ask_if">{t("perm.mode.ask_if")}</option>
          </select>
        )}
      </div>
      {floored && <p className="mt-1 text-[11px] text-mute">{t("perm.floor.explain")}{canLift && <> <button type="button" data-testid={`perm-lift-${c.category}`} className="font-medium text-accent hover:underline" onClick={onLift}>{t("perm.floor.lift")}</button></>}</p>}
      {c.lifted && (
        <p className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-err">
          <span className="rounded-full bg-err/10 px-2 py-0.5 font-semibold">{t("perm.floor.liftedBadge", { by: c.lifted.by })}</span>
          <button type="button" className="font-medium text-accent hover:underline disabled:opacity-50" disabled={!canEdit} onClick={onRestore}>{t("perm.floor.restore")}</button>
        </p>
      )}
      {editing && !floored && (
        <div className="mt-2 grid gap-2 rounded-md bg-panel2 p-2 text-[11.5px] text-ink2">
          <label className="flex items-center justify-between gap-2">{t("perm.if.perDayLabel")}
            <input type="number" min={1} max={10000} step={1} value={draft.per_day_over ?? ""} aria-label={t("perm.if.perDayLabel")}
              onChange={(e) => setDraft({ ...draft, per_day_over: e.target.value ? Number(e.target.value) : undefined })}
              className="w-24 rounded border border-line-strong bg-panel px-2 py-0.5 text-right" /></label>
          <label className="flex items-center justify-between gap-2">{t("perm.if.amountLabel")}
            <input type="number" min={0} step="any" value={draft.amount_over ?? ""} aria-label={t("perm.if.amountLabel")}
              onChange={(e) => setDraft({ ...draft, amount_over: e.target.value ? Number(e.target.value) : undefined })}
              className="w-24 rounded border border-line-strong bg-panel px-2 py-0.5 text-right" /></label>
          <label className="flex items-center gap-2"><input type="checkbox" checked={!!draft.new_recipient} onChange={(e) => setDraft({ ...draft, new_recipient: e.target.checked })} />{t("perm.if.newLabel")}</label>
          {!validAskIf(draft) && <span className="text-[11px] text-warn">{t("perm.if.invalid")}</span>}
          <div className="flex justify-end gap-2">
            <Btn onClick={() => setEditing(false)}>{t("perm.cancel")}</Btn>
            <Btn kind="primary" data-testid={`perm-save-if-${c.category}-${agentId}`} disabled={!validAskIf(draft)} onClick={() => { setEditing(false); onSet("ask_if", draft); }}>{t("perm.if.save")}</Btn>
          </div>
        </div>
      )}
      {!editing && mode === "ask_if" && !floored && !disabled && (
        <button type="button" className="mt-1 text-[11px] font-medium text-accent hover:underline" onClick={() => setEditing(true)}>{t("perm.mode.ask_if")}</button>
      )}
    </li>
  );
}

function LiftFloorDialog({ agent, cat, onCancel, onConfirm }: { agent: string; cat: string; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useT();
  const [ok, setOk] = useState(false);
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === "Escape") onCancel(); };
    document.addEventListener("keydown", k);
    return () => document.removeEventListener("keydown", k);
  }, [onCancel]);
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-0 sm:items-center sm:p-4" role="presentation" onMouseDown={onCancel}>
      <div role="dialog" aria-modal="true" aria-label={t("perm.floor.confirmTitle")} data-testid="perm-lift-dialog" onMouseDown={(e) => e.stopPropagation()}
        className="ac-pop max-h-[92dvh] w-full overflow-y-auto rounded-t-2xl border border-line bg-panel px-4 pb-[max(1rem,env(safe-area-inset-bottom))] pt-4 shadow-float sm:w-[min(420px,92vw)] sm:rounded-xl sm:p-4">
        <h3 className="text-[14px] font-semibold text-ink">{t("perm.floor.confirmTitle")}</h3>
        <p className="mt-2 text-[12.5px] leading-snug text-ink2">{t("perm.floor.confirmBody", { agent, cat: t(`perm.cat.${cat}`) })}</p>
        <label className="mt-3 flex items-center gap-2 text-[12.5px] text-ink"><input type="checkbox" data-testid="perm-understand" checked={ok} onChange={(e) => setOk(e.target.checked)} />{t("perm.floor.understand")}</label>
        <div className="mt-4 flex justify-end gap-2">
          <Btn onClick={onCancel}>{t("perm.cancel")}</Btn>
          <Btn kind="danger" data-testid="perm-lift-confirm" disabled={!ok} onClick={onConfirm}>{t("perm.floor.confirm")}</Btn>
        </div>
      </div>
    </div>
  );
}
