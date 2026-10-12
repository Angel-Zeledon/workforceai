"use client";
import { createContext, useContext, useRef, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import {
  COMPLEXITIES, DEFAULT_ACTION, RISKS, TASK_KINDS, decodeDep, encodeDep, isValidParamKey, listMax, setKind, splitList,
  type BParam, type BTask, type BWorkflow, type BuilderState,
} from "@/lib/projects/templateLogic";
import type { TemplateParamType } from "@/lib/projects/types";
import { useStore } from "@/lib/store";
import { inputCls, labelCls } from "./templateUi";

// ---- insert placeholders at the cursor of the last focused text field --------------------------------------------
interface FocusTarget { el: HTMLInputElement | HTMLTextAreaElement; set: (v: string) => void }
export const FocusCtx = createContext<{ register: (f: FocusTarget) => void } | null>(null);

export function useInserter() {
  const last = useRef<FocusTarget | null>(null);
  const register = (f: FocusTarget) => { last.current = f; };
  const insert = (text: string) => {
    const f = last.current;
    if (!f || !f.el.isConnected) return false;
    const a = f.el.selectionStart ?? f.el.value.length, b = f.el.selectionEnd ?? a;
    f.set(f.el.value.slice(0, a) + text + f.el.value.slice(b));
    const pos = a + text.length;
    setTimeout(() => { try { f.el.focus(); f.el.setSelectionRange(pos, pos); } catch { /* element gone */ } }, 0);
    return true;
  };
  return { register, insert };
}

export function TextField({ value, onChange, testId, multiline, rows = 2, placeholder, className = "", maxLength, invalid }: {
  value: string; onChange: (v: string) => void; testId?: string; multiline?: boolean; rows?: number; placeholder?: string; className?: string; maxLength?: number; invalid?: boolean;
}) {
  const ctx = useContext(FocusCtx);
  const cls = `${inputCls} ${invalid ? "border-red-400" : ""} ${className}`;
  const focus = (e: { currentTarget: HTMLInputElement | HTMLTextAreaElement }) => ctx?.register({ el: e.currentTarget, set: onChange });
  return multiline
    ? <textarea data-testid={testId} value={value} rows={rows} placeholder={placeholder} maxLength={maxLength} onFocus={focus} onChange={(e) => onChange(e.target.value)} className={`${cls} resize-y`} />
    : <input data-testid={testId} value={value} placeholder={placeholder} maxLength={maxLength} onFocus={focus} onChange={(e) => onChange(e.target.value)} className={cls} />;
}

export function IconBtn({ children, onClick, disabled, testId, title, danger }: { children: ReactNode; onClick: () => void; disabled?: boolean; testId?: string; title: string; danger?: boolean }) {
  return (
    <button type="button" data-testid={testId} title={title} aria-label={title} disabled={disabled} onClick={onClick}
      className={`h-7 min-w-7 shrink-0 rounded-md border px-1.5 text-xs font-semibold transition disabled:cursor-not-allowed disabled:opacity-30 ${danger ? "border-err/40 text-err hover:bg-err/10" : "border-line-strong bg-panel text-mute hover:text-ink"}`}>
      {children}
    </button>
  );
}

/** up / down / remove of a tree element */
export function RowTools({ id, index, count, onMove, onRemove }: { id: string; index: number; count: number; onMove: (dir: -1 | 1) => void; onRemove: () => void }) {
  const { t } = useT();
  return (
    <div className="flex shrink-0 items-center gap-1">
      <IconBtn testId={`${id}-up`} title={t("tpl.bld.moveUp")} disabled={index === 0} onClick={() => onMove(-1)}>↑</IconBtn>
      <IconBtn testId={`${id}-down`} title={t("tpl.bld.moveDown")} disabled={index === count - 1} onClick={() => onMove(1)}>↓</IconBtn>
      <IconBtn testId={`${id}-remove`} title={t("tpl.bld.remove")} danger onClick={onRemove}>✕</IconBtn>
    </div>
  );
}

// ---- parameters ---------------------------------------------------------------------------------------------------
export function ParamRow({ p, i, state, onPatch, onRemove }: { p: BParam; i: number; state: BuilderState; onPatch: (patch: Partial<BParam>) => void; onRemove: () => void }) {
  const { t } = useT();
  const dup = !!p.key && state.params.some((q, x) => x !== i && q.key === p.key);
  const badKey = !!p.key && (!isValidParamKey(p.key) || dup);
  const items = splitList(p.default).length;
  return (
    <div data-testid={`tb-param-${i}`} className="rounded-xl border border-line bg-panel2 p-3">
      <div className="grid gap-2 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_120px_auto]">
        <label className={labelCls}>{t("tpl.bld.paramLabel")}
          <TextField testId={`tb-param-${i}-label`} value={p.label} onChange={(label) => onPatch({ label })} placeholder={t("tpl.bld.paramLabelPh")} className="mt-1" maxLength={120} />
        </label>
        <label className={labelCls}>{t("tpl.bld.paramKey")}
          <input data-testid={`tb-param-${i}-key`} value={p.key} onChange={(e) => onPatch({ key: e.target.value.trim() })} aria-invalid={badKey} className={`${inputCls} mt-1 font-mono ${badKey ? "border-red-400" : ""}`} />
        </label>
        <label className={labelCls}>{t("tpl.bld.paramType")}
          <select data-testid={`tb-param-${i}-type`} value={p.type} onChange={(e) => onPatch({ type: e.target.value as TemplateParamType })} className={`${inputCls} mt-1`}>
            {(["text", "number", "list"] as const).map((x) => <option key={x} value={x}>{t(`tpl.bld.type.${x}`)}</option>)}
          </select>
        </label>
        <div className="flex items-end"><IconBtn testId={`tb-param-${i}-remove`} title={t("tpl.bld.remove")} danger onClick={onRemove}>✕</IconBtn></div>
      </div>
      {badKey && <div className="mt-1 text-[10px] text-red-700">{t(dup ? "tpl.issue.duplicate_key" : "tpl.bld.paramKeyBad")}</div>}
      <div className="mt-2 grid gap-2 md:grid-cols-2">
        <label className={`${labelCls} ${p.type === "list" ? "md:col-span-2" : ""}`}>{t("tpl.bld.paramDefault")}
          {p.type === "list"
            ? <textarea data-testid={`tb-param-${i}-default`} value={p.default} rows={2} onChange={(e) => onPatch({ default: e.target.value })} className={`${inputCls} mt-1 resize-y`} placeholder={t("tpl.bld.listDefaultPh")} />
            : <input data-testid={`tb-param-${i}-default`} type={p.type === "number" ? "number" : "text"} value={p.default} onChange={(e) => onPatch({ default: e.target.value })} className={`${inputCls} mt-1`} />}
          {p.type === "list" && <span className="mt-0.5 block text-[10px] font-normal text-mute">{t("tpl.wiz.items", { count: items })} · {t("tpl.wiz.listHint")}</span>}
        </label>
        {p.type === "list" && (
          <>
            <label className={labelCls}>{t("tpl.bld.itemName")}
              <input data-testid={`tb-param-${i}-item`} value={p.itemName} onChange={(e) => onPatch({ itemName: e.target.value.trim() })} placeholder="country" className={`${inputCls} mt-1 font-mono`} />
              <span className="mt-0.5 block text-[10px] font-normal text-mute">{t("tpl.bld.itemNameHint", { name: `{${p.itemName || "item"}}` })}</span>
            </label>
            <label className={labelCls}>{t("tpl.bld.maxItems")}
              <input data-testid={`tb-param-${i}-max`} type="number" min={1} max={50} value={p.max} onChange={(e) => onPatch({ max: e.target.value })} placeholder={String(listMax({}))} className={`${inputCls} mt-1`} />
            </label>
          </>
        )}
        {p.type === "number" && (
          <div className="grid grid-cols-2 gap-2">
            <label className={labelCls}>{t("tpl.bld.min")}<input data-testid={`tb-param-${i}-min`} type="number" value={p.min} onChange={(e) => onPatch({ min: e.target.value })} className={`${inputCls} mt-1`} /></label>
            <label className={labelCls}>{t("tpl.bld.max")}<input data-testid={`tb-param-${i}-max`} type="number" value={p.max} onChange={(e) => onPatch({ max: e.target.value })} className={`${inputCls} mt-1`} /></label>
          </div>
        )}
      </div>
    </div>
  );
}

// ---- workflow repeat ------------------------------------------------------------------------------------------------
export function RepeatSelect({ wf, state, testId, onChange }: { wf: BWorkflow; state: BuilderState; testId: string; onChange: (repeat: string[]) => void }) {
  const { t } = useT();
  const lists = state.params.filter((p) => p.type === "list" && p.key);
  if (!lists.length) return <div data-testid={testId} className="text-[10px] text-mute">{t("tpl.bld.repeatNone")}</div>;
  return (
    <label className={labelCls}>{t("tpl.bld.repeat")}
      <select multiple size={Math.min(3, lists.length + 0)} data-testid={testId} value={wf.repeat}
        onChange={(e) => {
          const sel = Array.from(e.target.selectedOptions).map((o) => o.value);
          // at most two lists (cartesian product): keep the most recent ones
          onChange(sel.length > 2 ? [...wf.repeat.filter((r) => sel.includes(r)), ...sel.filter((r) => !wf.repeat.includes(r))].slice(-2) : sel);
        }}
        className={`${inputCls} mt-1 block !w-auto min-w-[160px]`}>
        {lists.map((p) => <option key={p.key} value={p.key}>{p.label || p.key}</option>)}
      </select>
      <span className="mt-0.5 block text-[10px] font-normal text-mute">{wf.repeat.length ? t("tpl.bld.repeatHint", { count: wf.repeat.length }) : t("tpl.bld.repeatEmpty")}</span>
    </label>
  );
}

// ---- agent field (select of the organization's agents, free text as a fallback) ------------------------------------------
function AgentField({ value, onChange, testId }: { value: string; onChange: (v: string) => void; testId: string }) {
  const { t } = useT();
  const ids = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const known = ids.includes(value);
  const freeText = ids.length === 0 || (value !== "" && !known);
  if (freeText) {
    return (
      <div className="flex items-center gap-1">
        <input data-testid={testId} value={value} onChange={(e) => onChange(e.target.value)} placeholder={t("tpl.bld.agentFree")} className={`${inputCls} !w-[130px]`} />
        {ids.length > 0 && <IconBtn title={t("tpl.bld.agentBack")} onClick={() => onChange("")}>↩</IconBtn>}
      </div>
    );
  }
  return (
    <select data-testid={testId} value={value} onChange={(e) => onChange(e.target.value === "__custom__" ? " " : e.target.value)} className={`${inputCls} !w-[150px]`} aria-label={t("tpl.bld.agent")}>
      <option value="">{t("tpl.bld.agentPick")}</option>
      {ids.map((id) => <option key={id} value={id}>{agents[id]?.name ?? id}</option>)}
      <option value="__custom__">{t("tpl.bld.agentOther")}</option>
    </select>
  );
}

// ---- task ------------------------------------------------------------------------------------------------------------------
export interface TaskRef { key: string; title: string; repeat: string[]; where: string }

export function TaskEditor({ task, pos, count, open, onToggle, state, wf, index, onPatch, onMove, onRemove }: {
  task: BTask; pos: [number, number, number]; count: number; open: boolean; onToggle: () => void; state: BuilderState; wf: BWorkflow;
  index: TaskRef[]; onPatch: (patch: Partial<BTask>) => void; onMove: (dir: -1 | 1) => void; onRemove: () => void;
}) {
  const { t } = useT();
  const id = `tb-task-${pos[0]}-${pos[1]}-${pos[2]}`;
  const byKey = new Map(index.map((x) => [x.key, x]));
  const human = task.kind !== "task";
  const approvable = task.kind === "gate" || task.kind === "task";
  const itemOf = (list: string) => state.params.find((p) => p.key === list)?.itemName || list;

  // selectable dependencies: every other task; "previous item" variants for the lists this block repeats over
  const taken = new Set(task.deps);
  const options: { group: string; items: { value: string; label: string }[] }[] = [];
  const groups = new Map<string, { value: string; label: string }[]>();
  for (const x of index) {
    if (x.key === task.key) continue;
    const arr = groups.get(x.where) ?? [];
    if (!taken.has(x.key)) arr.push({ value: x.key, label: x.title || x.key });
    for (const l of wf.repeat) if (x.repeat.includes(l) && !taken.has(encodeDep(x.key, l))) arr.push({ value: encodeDep(x.key, l), label: `${x.title || x.key} · ${t("tpl.bld.prevItem", { item: itemOf(l) })}` });
    groups.set(x.where, arr);
  }
  groups.forEach((items, group) => { if (items.length) options.push({ group, items }); });

  const summary = task.deps.length ? t("tpl.bld.depsCount", { count: task.deps.length }) : "";
  return (
    <div data-testid={id} className="rounded-lg border border-line bg-panel p-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" data-testid={`${id}-toggle`} aria-expanded={open} title={open ? t("tpl.bld.collapse") : t("tpl.bld.expand")} onClick={onToggle}
          className="h-7 w-6 shrink-0 text-xs text-mute hover:text-ink">{open ? "▾" : "▸"}</button>
        <TextField testId={`${id}-title`} value={task.title} onChange={(title) => onPatch({ title })} placeholder={t("tpl.bld.taskTitlePh")} className="min-w-[200px] flex-1" maxLength={300} />
        <select data-testid={`${id}-kind`} value={task.kind} aria-label={t("tpl.bld.kind")} onChange={(e) => onPatch(setKind(task, e.target.value as BTask["kind"]))} className={`${inputCls} !w-[104px]`}>
          {TASK_KINDS.map((k) => <option key={k} value={k}>{t(`tpl.bld.kind.${k}`)}</option>)}
        </select>
        {!human && <AgentField value={task.agent} onChange={(agent) => onPatch({ agent })} testId={`${id}-agent`} />}
        {!human && (
          <select data-testid={`${id}-complexity`} value={task.complexity} aria-label={t("tpl.bld.complexity")} title={t("tpl.bld.complexity")} onChange={(e) => onPatch({ complexity: e.target.value as BTask["complexity"] })} className={`${inputCls} !w-[64px]`}>
            {COMPLEXITIES.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        )}
        <RowTools id={id} index={pos[2]} count={count} onMove={onMove} onRemove={onRemove} />
      </div>
      {!open && (task.deps.length > 0 || task.approval.on) && (
        <div className="ml-7 mt-1 flex gap-2 text-[10px] text-mute">{summary && <span>{summary}</span>}{task.approval.on && <span>{t("tpl.bld.approvalShort")}</span>}</div>
      )}
      {open && (
        <div className="ml-7 mt-2 space-y-2.5">
          <label className={labelCls}>{t("tpl.bld.taskDescription")}
            <TextField multiline rows={2} testId={`${id}-description`} value={task.description} onChange={(description) => onPatch({ description })} placeholder={t("tpl.bld.taskDescriptionPh")} className="mt-1" maxLength={4000} />
          </label>
          <div>
            <div className={labelCls}>{t("tpl.bld.deps")}</div>
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              {task.deps.map((d, n) => {
                const x = decodeDep(d), ref = byKey.get(x.key);
                return (
                  <span key={d} data-testid={`${id}-dep-${n}`} className="inline-flex items-center gap-1 rounded-full border border-line-strong bg-panel2 px-2 py-0.5 text-[11px] text-ink">
                    {ref ? (ref.title || ref.key) : x.key}{x.prev && <em className="not-italic text-accent"> · {t("tpl.bld.prevItem", { item: itemOf(x.prev) })}</em>}
                    <button type="button" aria-label={t("tpl.bld.remove")} onClick={() => onPatch({ deps: task.deps.filter((y) => y !== d) })} className="text-mute hover:text-err">×</button>
                  </span>
                );
              })}
              <select data-testid={`${id}-deps`} value="" onChange={(e) => { if (e.target.value) onPatch({ deps: [...task.deps, e.target.value] }); }} aria-label={t("tpl.bld.depsAdd")} className={`${inputCls} !w-[230px]`}>
                <option value="">{t("tpl.bld.depsAdd")}</option>
                {options.map((g) => <optgroup key={g.group} label={g.group}>{g.items.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</optgroup>)}
              </select>
            </div>
            <p className="mt-1 text-[10px] text-mute">{t("tpl.bld.depsHint")}</p>
          </div>
          <label className={labelCls}>{t("tpl.bld.acceptance")}
            <TextField multiline rows={2} testId={`${id}-acceptance`} value={task.acceptance} onChange={(acceptance) => onPatch({ acceptance })} placeholder={t("tpl.bld.acceptancePh")} className="mt-1" />
          </label>
          {approvable && (
            <div className="flex flex-wrap items-center gap-2">
              <label className="flex items-center gap-1.5 text-xs text-ink">
                <input data-testid={`${id}-approval`} type="checkbox" checked={task.approval.on} onChange={(e) => onPatch({ approval: { ...task.approval, on: e.target.checked } })} />
                {t("tpl.bld.approval")}
              </label>
              {task.approval.on && (
                <>
                  <input data-testid={`${id}-approval-action`} value={task.approval.action} aria-label={t("tpl.bld.approvalAction")} placeholder={DEFAULT_ACTION} onChange={(e) => onPatch({ approval: { ...task.approval, action: e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, "_") } })} className={`${inputCls} !w-[160px] font-mono`} />
                  <select data-testid={`${id}-approval-risk`} value={task.approval.risk} aria-label={t("tpl.bld.risk")} onChange={(e) => onPatch({ approval: { ...task.approval, risk: e.target.value as BTask["approval"]["risk"] } })} className={`${inputCls} !w-[110px]`}>
                    {RISKS.map((r) => <option key={r} value={r}>{t(`tpl.bld.risk.${r}`)}</option>)}
                  </select>
                </>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
