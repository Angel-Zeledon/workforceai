"use client";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import type { ChartDept } from "@/lib/orgChartApi";
import { flattenDepartments } from "@/lib/orgChartLogic";
import { splitBySpace } from "@/lib/orgTabsLogic";
import { officesApi } from "@/lib/officesApi";
import { addableMembers, convertibleDepartments, officeBadge, sortOffices, type OfficeView, type OfficeVisibility, type OfficesResponse, type PersonRef } from "@/lib/officesLogic";
import { Icon } from "../icons";
import { TeamMemory } from "./TeamMemory";
import { btnDanger, btnGhost, btnPrimary, inputCls } from "./shared";

const badgeCls: Record<string, string> = {
  general: "border-line text-mute",
  open: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700",
  private: "border-amber-500/30 bg-amber-500/10 text-amber-800",
};

/**
 * The Oficinas tab of "Agentes y oficinas": one list of offices in two parts. "Con espacio propio": General plus the
 * offices with their place in the 3D view (members, responsible, open or private, memory). "Sin espacio todavía": the
 * org chart's departments that are not an office yet (they work in General); an admin can give one its own space.
 * Nothing changes in the data: a department is an office without a space of its own.
 */
export function OfficesPanel({ people, onChanged, depts = [], onEditDept, onNewDept, actions, focusDepartments = false }: {
  people: PersonRef[]; onChanged?: () => void;
  /** every department of the org chart (parents first), for the "without a space yet" part */
  depts?: ChartDept[];
  onEditDept?: (id: string) => void; onNewDept?: () => void;
  /** buttons for the top of the tab ("Empezar con un tipo de empresa") */
  actions?: ReactNode;
  /** an old `?tab=departments` link: scroll to the "without a space yet" part */
  focusDepartments?: boolean;
}) {
  const { t } = useT();
  const [data, setData] = useState<OfficesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dept, setDept] = useState("");
  const [vis, setVis] = useState<OfficeVisibility>("open");
  const [busy, setBusy] = useState(false);
  const [memoryOf, setMemoryOf] = useState<string | null>(null);
  const deptsRef = useRef<HTMLElement>(null);

  const load = useCallback(() => {
    officesApi.list().then((d) => { setData(d); setError(null); }).catch(() => setError(t("offices.error.load")));
  }, [t]);
  useEffect(load, [load]);
  const ready = !!data || !!error;
  useEffect(() => { if (focusDepartments && ready) deptsRef.current?.scrollIntoView({ block: "start" }); }, [focusDepartments, ready]);

  const run = async (fn: () => Promise<unknown>, err = "offices.error.save") => {
    setBusy(true); setError(null);
    try { await fn(); load(); onChanged?.(); } catch (e) {
      const msg = String(e);
      setError(t(msg.includes("409") ? "offices.error.hasAgents" : msg.includes("403") ? "offices.error.forbidden" : err));
    } finally { setBusy(false); }
  };

  // parents first, as in the org chart; the ones with an office go to "Con espacio propio"
  const byId = new Map(depts.map((d) => [d.id, d]));
  const ordered = flattenDepartments(depts.map((d) => ({ id: d.id, name: d.name, parent_id: d.parent_id }))).map((f) => byId.get(f.id)!);
  const { withoutSpace } = splitBySpace(ordered, data?.offices ?? []);
  const convertible = data ? convertibleDepartments(data) : [];
  const offices = data ? sortOffices(data.offices) : [];
  const editable = new Set(depts.map((d) => d.id));

  return (
    <div className="grid gap-5" data-testid="offices-panel">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="min-w-[220px] max-w-3xl flex-1 text-[13px] text-mute">{data && data.offices.length > 1 ? t("offices.intro") : t("offices.introGeneral")}</p>
        <div className="flex flex-wrap items-center gap-2">
          {actions}
          {onNewDept && <button type="button" data-testid="org-new-dept" onClick={onNewDept} className={btnGhost}>{t("org.dept.new")}</button>}
        </div>
      </div>
      {error && <div role="alert" data-testid="offices-error" className="text-[12px] font-semibold text-red-600">{error} {!data && <button type="button" onClick={load} className="underline">{t("org.retry")}</button>}</div>}

      <section data-testid="offices-with-space" className="grid gap-2">
        <SectionTitle title={t("org.offices.withSpace")} count={data ? offices.length : undefined} hint={t("org.offices.withSpaceHint")} />
        {!data && !error && <div className="text-sm text-mute">{t("org.loading")}</div>}
        {data && (
          <ul className="grid gap-3 md:grid-cols-2">
            {offices.map((o) => (
              <OfficeCard key={o.id} o={o} people={people} admin={data.me.admin} meId={data.me.user_id} busy={busy} run={run} onMemory={() => setMemoryOf(o.id)}
                onEdit={onEditDept && !o.general && editable.has(o.id) ? () => onEditDept(o.id) : undefined} />
            ))}
          </ul>
        )}
      </section>

      {/* the old "Estructura" tab: same test ids, so old links and tests still find it */}
      <section ref={deptsRef} data-testid="org-tab-departments" className="grid scroll-mt-4 gap-2">
        <SectionTitle title={t("org.offices.withoutSpace")} count={withoutSpace.length} hint={t("org.offices.withoutSpaceHint")} />
        <div data-testid="org-dept-table">
          {depts.length === 0 && <p className="text-[13px] text-mute">{t("org.offices.noneWithout")}</p>}
          {depts.length > 0 && withoutSpace.length === 0 && <p className="text-[13px] text-mute">{t("org.offices.allSpaced")}</p>}
          <ul className="grid gap-1.5">
            {withoutSpace.map((d) => {
              const parent = d.parent_id ? byId.get(d.parent_id) : undefined;
              return (
                <li key={d.id}>
                  <button type="button" data-testid={`org-dept-row-${d.id}`} onClick={() => onEditDept?.(d.id)}
                    className="flex w-full flex-wrap items-center justify-between gap-2 rounded-[10px] border border-line bg-panel px-3 py-2 text-left hover:border-accent/50">
                    <span className="min-w-0">
                      <span className="text-[13px] font-semibold">{d.name}</span>
                      {parent && <span className="ml-2 text-[11px] text-mute">{t("org.dept.inside", { name: parent.name })}</span>}
                    </span>
                    <span className="flex flex-wrap gap-x-3 text-[11px] text-mute">
                      {d.cost_center && <span>{t("org.costCenter")}: {d.cost_center}</span>}
                      <span>{t("org.headOf", { name: d.head_name || t("org.noHead") })}</span>
                      <span>{t("org.counts", { people: d.people, agents: d.agents })}</span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
        {data?.me.admin && convertible.length > 0 && (
          <div className="flex flex-wrap items-end gap-2 rounded-[10px] border border-line bg-panel p-3" data-testid="offices-convert">
            <label className="flex min-w-[200px] flex-1 flex-col gap-1 text-[12px] font-semibold">
              {t("offices.convert.label")}
              <select data-testid="offices-convert-dept" value={dept} onChange={(e) => setDept(e.target.value)} className={inputCls}>
                <option value="">{t("offices.convert.pick")}</option>
                {convertible.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-[12px] font-semibold">
              {t("offices.visibility")}
              <select data-testid="offices-convert-vis" value={vis} onChange={(e) => setVis(e.target.value as OfficeVisibility)} className={inputCls}>
                <option value="open">{t("offices.badge.open")}</option>
                <option value="private">{t("offices.badge.private")}</option>
              </select>
            </label>
            <button type="button" data-testid="offices-convert-go" disabled={!dept || busy} className={btnPrimary}
              onClick={() => void run(async () => { await officesApi.enable(dept, vis); setDept(""); })}>{t("offices.convert.go")}</button>
            <p className="w-full text-[11px] text-mute">{t("offices.convert.hint")}</p>
          </div>
        )}
      </section>

      <section className="rounded-[10px] border border-line bg-panel p-4" data-testid="my-memory">
        <h3 className="flex items-center gap-2 text-[14px] font-semibold"><Icon name="user" size={14} />{t("offices.myMemory")}</h3>
        <p className="mb-2 text-[12px] text-mute">{t("offices.myMemory.hint")}</p>
        <TeamMemory scope="person" refId="" />
      </section>
      {memoryOf && data && (() => {
        const o = data.offices.find((x) => x.id === memoryOf);
        return o ? <TeamMemory scope="office" refId={o.id} title={t("offices.memoryOf", { name: o.name })} onClose={() => setMemoryOf(null)} /> : null;
      })()}
    </div>
  );
}

function SectionTitle({ title, count, hint }: { title: string; count?: number; hint: string }) {
  return (
    <div>
      <h3 className="flex items-baseline gap-2 text-[14px] font-semibold text-ink">{title}{count !== undefined && <span className="text-[12px] font-normal text-mute">{count}</span>}</h3>
      <p className="text-[12px] text-mute">{hint}</p>
    </div>
  );
}

function OfficeCard({ o, people, admin, meId, busy, run, onMemory, onEdit }: {
  o: OfficeView; people: PersonRef[]; admin: boolean; meId: string; busy: boolean;
  run: (fn: () => Promise<unknown>, err?: string) => Promise<void>; onMemory: () => void; onEdit?: () => void;
}) {
  const { t } = useT();
  const [add, setAdd] = useState("");
  const [confirmClose, setConfirmClose] = useState(false);
  const badge = officeBadge(o);
  const leadIds = new Set(o.leads.map((l) => l.user_id));
  const manage = o.can_manage && !o.general;
  return (
    <li data-testid={`office-${o.id}`} className="flex flex-col gap-2 rounded-[10px] border border-line bg-panel p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-2 text-[14px] font-semibold">
          <Icon name={o.can_enter ? "office" : "shield"} size={14} />{o.general ? t("offices.general") : o.name}
        </span>
        <span data-testid={`office-badge-${o.id}`} className={`rounded-full border px-2 py-0.5 text-[11px] font-semibold ${badgeCls[badge]}`}>{t(`offices.badge.${badge}`)}</span>
      </div>
      <div className="flex flex-wrap gap-x-3 text-[12px] text-mute">
        <span>{t("offices.leads", { names: o.leads.map((l) => l.name || "?").join(", ") || t("org.noHead") })}</span>
        <span>{o.can_enter ? t("offices.counts", { people: o.member_count, agents: o.agents.length }) : t("offices.countsPeople", { people: o.member_count })}</span>
        {o.is_lead && !o.general && <span className="font-semibold text-accent">{t("offices.youLead")}</span>}
        {!o.is_lead && o.is_member && !o.general && <span className="font-semibold text-accent">{t("offices.youMember")}</span>}
      </div>
      {o.general && <p className="text-[12px] text-mute">{t("offices.general.hint")}</p>}
      {!o.can_enter && <p data-testid={`office-door-${o.id}`} className="text-[12px] text-mute">{t("offices.closedDoor")}</p>}
      {o.can_enter && !o.general && (
        <ul className="flex flex-wrap gap-1.5" data-testid={`office-members-${o.id}`}>
          {o.members.map((m) => (
            <li key={m.user_id} className="flex items-center gap-1 rounded-full border border-line bg-panel2 px-2 py-0.5 text-[12px]">
              <span>{m.name || m.user_id}</span>
              {leadIds.has(m.user_id) && <span className="text-[10px] font-semibold text-accent">{t("offices.lead")}</span>}
              {manage && (
                <>
                  <button type="button" disabled={busy} data-testid={`office-role-${o.id}-${m.user_id}`} className="text-[10px] font-semibold text-mute underline-offset-2 hover:text-ink hover:underline"
                    onClick={() => void run(() => officesApi.setMember(o.id, m.user_id, leadIds.has(m.user_id) ? "member" : "lead"))}>
                    {leadIds.has(m.user_id) ? t("offices.makeMember") : t("offices.makeLead")}
                  </button>
                  <button type="button" disabled={busy} aria-label={t("offices.remove", { name: m.name })} data-testid={`office-remove-${o.id}-${m.user_id}`}
                    className="text-mute hover:text-red-600" onClick={() => void run(() => officesApi.removeMember(o.id, m.user_id))}><Icon name="x" size={10} /></button>
                </>
              )}
            </li>
          ))}
          {o.members.length === 0 && <li className="text-[12px] text-mute">{t("offices.noMembers")}</li>}
        </ul>
      )}
      {manage && (
        <div className="flex flex-wrap items-center gap-2">
          <select aria-label={t("offices.addMember")} data-testid={`office-add-${o.id}`} value={add} onChange={(e) => setAdd(e.target.value)} className={`${inputCls} max-w-[200px] py-1 text-[12px]`}>
            <option value="">{t("offices.addMember")}</option>
            {addableMembers(people, o).map((p) => <option key={p.user_id} value={p.user_id}>{p.name}</option>)}
          </select>
          <button type="button" disabled={!add || busy} data-testid={`office-add-go-${o.id}`} className={btnGhost}
            onClick={() => void run(async () => { await officesApi.setMember(o.id, add, "member"); setAdd(""); })}>{t("offices.add")}</button>
          <button type="button" disabled={busy} data-testid={`office-vis-${o.id}`} className={btnGhost}
            onClick={() => void run(() => officesApi.setVisibility(o.id, o.visibility === "open" ? "private" : "open"))}>
            {o.visibility === "open" ? t("offices.makePrivate") : t("offices.makeOpen")}
          </button>
        </div>
      )}
      <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
        {o.can_enter && <button type="button" data-testid={`office-memory-${o.id}`} onClick={onMemory} className="text-[12px] font-semibold text-accent hover:underline">{t("offices.memory")}</button>}
        {onEdit && <button type="button" data-testid={`office-edit-${o.id}`} onClick={onEdit} className="text-[12px] font-semibold text-mute hover:text-ink hover:underline">{t("org.offices.edit")}</button>}
        {o.is_member && !o.is_lead && !o.general && (
          <button type="button" disabled={busy} className="text-[12px] text-mute hover:text-ink" data-testid={`office-leave-${o.id}`}
            onClick={() => void run(() => officesApi.removeMember(o.id, meId))}>{t("offices.leave")}</button>
        )}
        {admin && !o.general && !confirmClose && <button type="button" data-testid={`office-close-${o.id}`} disabled={busy} onClick={() => setConfirmClose(true)} className="ml-auto text-[12px] text-red-600 hover:underline">{t("offices.close")}</button>}
        {admin && !o.general && confirmClose && <button type="button" data-testid={`office-close-confirm-${o.id}`} disabled={busy} onClick={() => void run(() => officesApi.disable(o.id))} className={`ml-auto ${btnDanger}`}>{t("offices.closeConfirm")}</button>}
      </div>
    </li>
  );
}
