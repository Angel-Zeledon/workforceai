"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
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

/** E2 "Oficinas": General plus the offices built on the departments; members, responsible, open or private, memory. */
export function OfficesPanel({ people, onChanged }: { people: PersonRef[]; onChanged?: () => void }) {
  const { t } = useT();
  const [data, setData] = useState<OfficesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dept, setDept] = useState("");
  const [vis, setVis] = useState<OfficeVisibility>("open");
  const [busy, setBusy] = useState(false);
  const [memoryOf, setMemoryOf] = useState<string | null>(null);

  const load = useCallback(() => {
    officesApi.list().then((d) => { setData(d); setError(null); }).catch(() => setError(t("offices.error.load")));
  }, [t]);
  useEffect(load, [load]);

  const run = async (fn: () => Promise<unknown>, err = "offices.error.save") => {
    setBusy(true); setError(null);
    try { await fn(); load(); onChanged?.(); } catch (e) {
      const msg = String(e);
      setError(t(msg.includes("409") ? "offices.error.hasAgents" : msg.includes("403") ? "offices.error.forbidden" : err));
    } finally { setBusy(false); }
  };

  if (error && !data) return <div role="alert" className="text-sm font-semibold text-red-600">{error} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>;
  if (!data) return <div className="text-sm text-mute">{t("org.loading")}</div>;
  const convertible = convertibleDepartments(data);
  const offices = sortOffices(data.offices);

  return (
    <div className="grid gap-4" data-testid="offices-panel">
      <p className="max-w-3xl text-[13px] text-mute">{data.offices.length > 1 ? t("offices.intro") : t("offices.introGeneral")}</p>
      {error && <div role="alert" data-testid="offices-error" className="text-[12px] font-semibold text-red-600">{error}</div>}
      {data.me.admin && convertible.length > 0 && (
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
      <ul className="grid gap-3 md:grid-cols-2">
        {offices.map((o) => (
          <OfficeCard key={o.id} o={o} people={people} admin={data.me.admin} meId={data.me.user_id} busy={busy} run={run} onMemory={() => setMemoryOf(o.id)} />
        ))}
      </ul>
      <section className="rounded-[10px] border border-line bg-panel p-4" data-testid="my-memory">
        <h3 className="flex items-center gap-2 text-[14px] font-semibold"><Icon name="user" size={14} />{t("offices.myMemory")}</h3>
        <p className="mb-2 text-[12px] text-mute">{t("offices.myMemory.hint")}</p>
        <TeamMemory scope="person" refId="" />
      </section>
      {memoryOf && (() => {
        const o = data.offices.find((x) => x.id === memoryOf);
        return o ? <TeamMemory scope="office" refId={o.id} title={t("offices.memoryOf", { name: o.name })} onClose={() => setMemoryOf(null)} /> : null;
      })()}
    </div>
  );
}

function OfficeCard({ o, people, admin, meId, busy, run, onMemory }: {
  o: OfficeView; people: PersonRef[]; admin: boolean; meId: string; busy: boolean;
  run: (fn: () => Promise<unknown>, err?: string) => Promise<void>; onMemory: () => void;
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
