"use client";
import { useCallback, useEffect, useState } from "react";
import { trOr, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { toast } from "@/lib/toast";
import { customersApi, type CustomerDetail, type CustomerInput, type HistoryCandidates, type HistoryKind } from "@/lib/customersApi";
import { useChatCustomer } from "@/lib/chatCustomer";
import { customerColor, defaultCloneName, normalizeStatus, STATUS_COLOR } from "@/lib/customersLogic";
import { Btn, Card, Dot, Empty, Loading, timeAgo, useAgentColor, useAgentName } from "../ui";
import { CustomerForm } from "./CustomerForm";

const field = "w-full rounded-md border border-line bg-panel2 px-2.5 py-1.5 text-[16px] text-ink md:text-[13px]";

export function StatusPill({ status }: { status: string }) {
  const { t } = useT();
  const st = normalizeStatus(status);
  return (
    <span data-testid={`customers-status-${st}`} className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium"
      style={{ borderColor: `${STATUS_COLOR[st]}66`, color: STATUS_COLOR[st], background: `${STATUS_COLOR[st]}14` }}>
      <span aria-hidden className="inline-block h-2 w-2 rounded-full" style={{ background: STATUS_COLOR[st] }} />
      {t(`customers.status.${st}`)}
    </span>
  );
}

/** One customer's scoped view: its clones, projects, recent requests and a request box scoped to it. */
export function CustomerView({ id, onBack, onChanged }: { id: string; onBack: () => void; onChanged: () => void }) {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const name = useAgentName();
  const color = useAgentColor();
  const [d, setD] = useState<CustomerDetail | null>(null);
  const [err, setErr] = useState(false);
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [agentId, setAgentId] = useState("");
  const [cloneName, setCloneName] = useState("");

  const [earlier, setEarlier] = useState<HistoryCandidates | null>(null);
  const [earlierOpen, setEarlierOpen] = useState(false);
  const [earlierErr, setEarlierErr] = useState(false);
  const setChatCustomer = useChatCustomer((s) => s.set);

  const load = useCallback(() => {
    setErr(false);
    customersApi.get(id).then(setD).catch(() => setErr(true));
  }, [id]);
  useEffect(load, [load]);
  // B12: while this view is open the chat works for this customer (its requests are scoped to it).
  const cust = d?.customer;
  useEffect(() => {
    if (!cust || cust.archived) return;
    setChatCustomer({ id: cust.id, name: cust.name, color: customerColor(cust.color, cust.id) });
    return () => { if (useChatCustomer.getState().customer?.id === cust.id) setChatCustomer(null); };
  }, [cust, setChatCustomer]);
  const loadEarlier = useCallback(() => {
    setEarlierErr(false);
    customersApi.historyCandidates().then(setEarlier).catch(() => setEarlierErr(true));
  }, []);

  if (err) return <Empty action={<Btn onClick={onBack}>{t("customers.back")}</Btn>}>{t("customers.error.load")}</Empty>;
  if (!d) return <Loading rows={4} />;
  const c = d.customer;
  const assigned = new Set(d.assignments.map((a) => a.agent_id));
  const free = order.filter((aid) => agents[aid] && !assigned.has(aid));
  const picked = agents[agentId];

  const save = async (v: CustomerInput) => {
    await customersApi.update(c.id, v);
    setEditing(false); load(); onChanged();
    toast(t("customers.toast.saved"));
  };
  const archive = async () => {
    if (!window.confirm(t("customers.archive.confirm", { name: c.name }))) return;
    try { await customersApi.archive(c.id); toast(t("customers.toast.archived")); onChanged(); onBack(); }
    catch { toast(t("customers.error.save"), "err"); }
  };
  const send = async () => {
    const v = text.trim();
    if (!v) return;
    setSending(true);
    try { await customersApi.request(c.id, v); setText(""); toast(t("customers.toast.requestSent")); load(); onChanged(); }
    catch { toast(t("customers.error.request"), "err"); }
    finally { setSending(false); }
  };
  const addClone = async () => {
    if (!picked) return;
    try { await customersApi.addAssignment(c.id, picked.id, cloneName.trim() || undefined); setAgentId(""); setCloneName(""); load(); onChanged(); toast(t("customers.toast.cloneAdded")); }
    catch (e) { toast(e instanceof Error && e.message ? e.message : t("customers.error.save"), "err"); }
  };
  const removeClone = async (aid: string) => {
    try { await customersApi.removeAssignment(c.id, aid); load(); onChanged(); }
    catch { toast(t("customers.error.save"), "err"); }
  };
  // Earlier work (before customers existed): assigned or removed explicitly, audited by the backend.
  const moveHistory = async (kind: HistoryKind, refId: string, to: string) => {
    if (!to && !window.confirm(t("customers.history.unassignConfirm", { name: c.name }))) return;
    try {
      const r = await customersApi.assignHistory(kind, refId, to);
      toast(to ? t("customers.history.assigned", { count: r.memories }) : t("customers.history.unassigned"));
      load(); onChanged();
      if (earlierOpen) loadEarlier();
    } catch (e) { toast(e instanceof Error && e.message ? e.message : t("customers.error.save"), "err"); }
  };
  const finishedProject = (st: string) => ["done", "failed", "cancelled", "draft"].includes(st);
  const finishedRequest = (st: string) => st === "done" || st === "failed";
  const smallBtn = "shrink-0 rounded px-2 py-1 text-[11px] text-accent hover:bg-accent-soft";

  return (
    <div data-testid="customers-view" className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <Btn data-testid="customers-back" onClick={onBack}>← {t("customers.back")}</Btn>
          <div className="min-w-0">
            <h2 className="flex flex-wrap items-center gap-2 font-display text-lg font-semibold text-ink">
              <span aria-hidden className="inline-block h-3.5 w-3.5 rounded-sm" style={{ background: customerColor(c.color, c.id) }} />
              <span className="break-words">{c.name}</span>
              <StatusPill status={d.status} />
            </h2>
            <div className="mt-0.5 text-[11.5px] text-mute">
              {c.tax_id && <span className="mr-3">{t("customers.taxId")}: {c.tax_id}</span>}
              {d.folder_path && <span className="break-all">{t("customers.folder")}: <span className="font-mono">{d.folder_path}</span></span>}
            </div>
          </div>
        </div>
        <div className="flex gap-2">
          <Btn data-testid="customers-edit" onClick={() => setEditing((v) => !v)}>{t("customers.edit")}</Btn>
          <Btn data-testid="customers-archive" kind="danger" onClick={() => void archive()}>{t("customers.archive")}</Btn>
        </div>
      </div>
      {c.notes && !editing && <p className="max-w-3xl whitespace-pre-line text-[12.5px] text-ink2">{c.notes}</p>}
      {editing && <Card title={t("customers.edit")}><CustomerForm initial={c} onSave={save} onCancel={() => setEditing(false)} /></Card>}
      <p className="max-w-3xl rounded-md border border-line bg-panel2 px-3 py-2 text-[11.5px] text-ink2">{t("customers.isolation.note")}</p>

      <div className="grid gap-4 xl:grid-cols-2 [&>*]:min-w-0">
        <Card title={t("customers.clones.title", { count: d.assignments.length })}>
          {d.assignments.length ? (
            <ul className="space-y-1.5">
              {d.assignments.map((a) => (
                <li key={a.id} data-testid={`customers-clone-${a.agent_id}`} className="flex items-center justify-between gap-2 rounded-md border border-line bg-panel2 px-2.5 py-2">
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5 truncate text-[12.5px] font-semibold text-ink"><Dot color={color(a.agent_id)} />{a.display_name}</div>
                    <div className="truncate text-[10.5px] text-mute">{t("customers.clones.base", { name: name(a.agent_id) })}</div>
                  </div>
                  <button className="shrink-0 rounded px-2 py-1 text-[11px] text-err hover:bg-err/10" aria-label={t("customers.clones.removeAria", { name: a.display_name })}
                    onClick={() => void removeClone(a.id)}>{t("customers.clones.remove")}</button>
                </li>
              ))}
            </ul>
          ) : <Empty>{t("customers.clones.empty")}</Empty>}
          <div className="mt-3 grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
            <label className="sr-only" htmlFor="customers-clone-agent">{t("customers.clones.agent")}</label>
            <select id="customers-clone-agent" data-testid="customers-clone-agent" className={field} value={agentId} onChange={(e) => setAgentId(e.target.value)}>
              <option value="">{t("customers.clones.pick")}</option>
              {free.map((aid) => <option key={aid} value={aid}>{name(aid)} · {agents[aid].title}</option>)}
            </select>
            <label className="sr-only" htmlFor="customers-clone-name">{t("customers.clones.displayName")}</label>
            <input id="customers-clone-name" data-testid="customers-clone-name" className={field} value={cloneName} maxLength={120} onChange={(e) => setCloneName(e.target.value)}
              placeholder={picked ? defaultCloneName(picked.title, c.name) : t("customers.clones.displayName")} />
            <Btn data-testid="customers-clone-add" kind="primary" disabled={!picked} onClick={() => void addClone()}>{t("customers.clones.add")}</Btn>
          </div>
        </Card>

        <Card title={t("customers.request.title")}>
          <form onSubmit={(e) => { e.preventDefault(); void send(); }} className="space-y-2">
            <label className="sr-only" htmlFor="customers-request-text">{t("customers.request.title")}</label>
            <textarea id="customers-request-text" data-testid="customers-request-text" className={`${field} min-h-[72px]`} value={text} maxLength={4000}
              onChange={(e) => setText(e.target.value)} placeholder={t("customers.request.placeholder", { name: c.name })} />
            <div className="flex items-center justify-between gap-2">
              <span className="text-[10.5px] text-mute">{t("customers.request.help")}</span>
              <Btn data-testid="customers-request-send" kind="primary" type="submit" disabled={sending || !text.trim()}>{t("customers.request.send")}</Btn>
            </div>
          </form>
        </Card>

        <Card title={t("customers.projects.title", { count: d.projects.length })}>
          {d.projects.length ? (
            <ul className="space-y-1.5">
              {d.projects.map((p) => (
                <li key={p.id} className="rounded-md border border-line bg-panel2 px-2.5 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-[12.5px] font-semibold text-ink">{p.name}</span>
                    <span className="shrink-0 text-[10.5px] text-mute">{trOr(`pv.status.${p.status}`, p.status)}</span>
                  </div>
                  <div className="mt-1 text-[10.5px] text-mute">
                    {t("customers.projects.progress", { done: p.tasks_done, total: p.tasks_total })}
                    {p.awaiting > 0 && <span className="ml-2 text-warn">{t("customers.projects.awaiting", { count: p.awaiting })}</span>}
                    {p.failed > 0 && <span className="ml-2 text-err">{t("customers.projects.failed", { count: p.failed })}</span>}
                    {finishedProject(p.status) && (
                      <button type="button" data-testid={`customers-unassign-project-${p.id}`} className={`${smallBtn} ml-2`}
                        onClick={() => void moveHistory("project", p.id, "")}>{t("customers.history.unassign")}</button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          ) : <Empty>{t("customers.projects.empty")}</Empty>}
        </Card>

        <Card title={t("customers.requests.title", { count: d.requests.length })}>
          {d.requests.length ? (
            <ul className="space-y-1.5">
              {d.requests.map((r) => (
                <li key={r.id} className="rounded-md border border-line bg-panel2 px-2.5 py-2">
                  <div className="line-clamp-2 text-[12.5px] text-ink">{r.text}</div>
                  <div className="mt-1 flex justify-between text-[10.5px] text-mute">
                    <span className="font-semibold uppercase tracking-wide">{trOr(`dash.reqStatus.${r.status}`, r.status)}</span>
                    <span className="flex items-center gap-2">
                      {finishedRequest(r.status) && (
                        <button type="button" data-testid={`customers-unassign-request-${r.id}`} className={smallBtn}
                          onClick={() => void moveHistory("request", r.id, "")}>{t("customers.history.unassign")}</button>
                      )}
                      {timeAgo(r.created_at)}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          ) : <Empty>{t("customers.requests.empty")}</Empty>}
        </Card>

        <Card title={t("customers.history.title")}>
          <p className="text-[11.5px] text-ink2">{t("customers.history.help", { name: c.name })}</p>
          {!earlierOpen ? (
            <Btn data-testid="customers-history-open" className="mt-2" onClick={() => { setEarlierOpen(true); loadEarlier(); }}>{t("customers.history.show")}</Btn>
          ) : earlierErr ? <Empty>{t("customers.history.error")}</Empty>
            : !earlier ? <Loading rows={2} />
            : !earlier.projects.length && !earlier.requests.length ? <Empty>{t("customers.history.empty")}</Empty>
            : (
              <ul data-testid="customers-history-list" className="mt-2 space-y-1.5">
                {earlier.projects.map((p) => (
                  <li key={p.id} className="flex items-center justify-between gap-2 rounded-md border border-line bg-panel2 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="truncate text-[12.5px] font-semibold text-ink">{p.name}</div>
                      <div className="text-[10.5px] text-mute">{t("customers.history.project")} · {trOr(`pv.status.${p.status}`, p.status)}</div>
                    </div>
                    <Btn data-testid={`customers-assign-project-${p.id}`} onClick={() => void moveHistory("project", p.id, c.id)}>{t("customers.history.assign")}</Btn>
                  </li>
                ))}
                {earlier.requests.map((r) => (
                  <li key={r.id} className="flex items-center justify-between gap-2 rounded-md border border-line bg-panel2 px-2.5 py-2">
                    <div className="min-w-0">
                      <div className="line-clamp-2 text-[12.5px] text-ink">{r.text}</div>
                      <div className="text-[10.5px] text-mute">{t("customers.history.request")} · {timeAgo(r.created_at)}</div>
                    </div>
                    <Btn data-testid={`customers-assign-request-${r.id}`} onClick={() => void moveHistory("request", r.id, c.id)}>{t("customers.history.assign")}</Btn>
                  </li>
                ))}
              </ul>
            )}
        </Card>
      </div>
    </div>
  );
}
