"use client";
/**
 * Data inboxes (docs/architecture/data-inboxes.md): the generic way to bring data in. An admin creates an inbox
 * (destination folder, optional customer, allowed content, max size) and gets a URL + token, shown ONCE, that any
 * software can POST JSON, CSV or files to. Each push becomes a versioned file in the drive, authored by the inbox
 * and treated as external data (never as instructions).
 */
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { API_URL } from "@/lib/config";
import { customersApi, type Customer } from "@/lib/customersApi";
import { dataInboxesApi, InboxApiError } from "@/lib/dataInboxesApi";
import {
  DEFAULT_MAX_MB, INBOX_KINDS, curlExamples, driveHref, fmtBytes, pushUrlOf, validateInbox,
  type CreatedDataInbox, type DataInbox, type InboxKind, type InboxPush,
} from "@/lib/dataInboxLogic";
import { fmtDateTime, trOr, useT } from "@/lib/i18n";
import { Btn, Empty, Loading } from "../ui";
import { ErrorLine, Field, Modal, inputCls } from "./shared";

const origin = () => (typeof window === "undefined" ? "" : window.location.origin);
const urlOf = (ib: DataInbox) => pushUrlOf(ib, API_URL, origin());

function errMsg(e: unknown): string {
  const code = e instanceof InboxApiError ? e.code : "request_failed";
  return trOr(`dataInbox.err.${code}`, trOr("dataInbox.err.request_failed", "Error"));
}

function CopyField({ label, value, testId, secret }: { label: string; value: string; testId: string; secret?: boolean }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* clipboard blocked: the field is selectable */ }
  };
  return (
    <div>
      <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{label}</span>
      <div className="flex items-stretch gap-2">
        <input readOnly data-testid={testId} value={value} onFocus={(e) => e.currentTarget.select()}
          className={`${inputCls} font-mono text-[12px] ${secret ? "bg-amber-500/5" : ""}`} />
        <Btn data-testid={`${testId}-copy`} onClick={() => void copy()}>{done ? t("dataInbox.copied") : t("dataInbox.copy")}</Btn>
      </div>
    </div>
  );
}

function CodeBlock({ title, code, testId }: { title: string; code: string; testId: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(code); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* selectable */ }
  };
  return (
    <div className="rounded-lg border border-line bg-panel2">
      <div className="flex items-center justify-between border-b border-line px-3 py-1.5">
        <span className="text-[11px] font-semibold text-mute">{title}</span>
        <button type="button" data-testid={`${testId}-copy`} onClick={() => void copy()} className="text-[11px] font-medium text-accent hover:underline">
          {done ? t("dataInbox.copied") : t("dataInbox.copy")}
        </button>
      </div>
      <pre data-testid={testId} className="overflow-x-auto whitespace-pre px-3 py-2 font-mono text-[11px] leading-relaxed text-ink">{code}</pre>
    </div>
  );
}

function Examples({ url, token }: { url: string; token: string }) {
  const { t } = useT();
  const ex = curlExamples(url, token);
  return (
    <div className="space-y-2">
      <CodeBlock title={t("dataInbox.example.json")} code={ex.json} testId="inbox-curl-json" />
      <CodeBlock title={t("dataInbox.example.csv")} code={ex.csv} testId="inbox-curl-csv" />
      <CodeBlock title={t("dataInbox.example.file")} code={ex.file} testId="inbox-curl-file" />
      <p className="text-[11px] text-mute">{t("dataInbox.example.headers")}</p>
    </div>
  );
}

/** The only moment the token is visible: after creation or rotation. */
function TokenOnce({ created, onClose }: { created: CreatedDataInbox; onClose: () => void }) {
  const { t } = useT();
  const url = urlOf(created);
  return (
    <Modal onClose={onClose} testId="inbox-token-once" wide>
      <h2 className="mb-1 font-display text-lg font-semibold">{t("dataInbox.once.title", { name: created.name })}</h2>
      <p role="alert" className="mb-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs font-medium text-amber-800">{t("dataInbox.once.warning")}</p>
      <div className="space-y-3">
        <CopyField label={t("dataInbox.url")} value={url} testId="inbox-url" />
        <CopyField label={t("dataInbox.token")} value={created.token} testId="inbox-token" secret />
        <Examples url={url} token={created.token} />
        <p className="text-[11px] text-mute">{t("dataInbox.dataNotInstructions")}</p>
      </div>
      <div className="mt-4 flex justify-end"><Btn kind="primary" data-testid="inbox-token-done" onClick={onClose}>{t("dataInbox.once.done")}</Btn></div>
    </Modal>
  );
}

interface FormState { name: string; folder: string; customer: string; allowed: InboxKind[]; maxMb: number; rate: number }

function InboxForm({ edit, onClose, onSaved, onCreated }: {
  edit?: DataInbox; onClose: () => void; onSaved: (ib: DataInbox) => void; onCreated: (c: CreatedDataInbox) => void;
}) {
  const { t } = useT();
  const [f, setF] = useState<FormState>(() => edit
    ? { name: edit.name, folder: edit.folder, customer: edit.customer_id, allowed: edit.allowed, maxMb: Math.round((edit.max_bytes / (1 << 20)) * 100) / 100, rate: edit.rate_per_min }
    : { name: "", folder: "", customer: "", allowed: ["json", "csv"], maxMb: DEFAULT_MAX_MB, rate: 30 });
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    if (edit) return;
    customersApi.list().then((cs) => setCustomers(Array.isArray(cs) ? cs.filter((c) => !c.archived) : [])).catch(() => setCustomers([]));
  }, [edit]);
  const errs = validateInbox({ name: f.name, folder: f.folder, allowed: f.allowed, maxMb: f.maxMb });
  const toggle = (k: InboxKind) => setF((s) => ({ ...s, allowed: s.allowed.includes(k) ? s.allowed.filter((x) => x !== k) : INBOX_KINDS.filter((x) => x === k || s.allowed.includes(x)) }));
  const submit = async () => {
    if (errs.length) { setErr(t(`dataInbox.err.${errs[0]}`)); return; }
    setBusy(true); setErr(null);
    const body = { name: f.name.trim(), allowed: f.allowed, max_bytes: Math.round(f.maxMb * (1 << 20)), rate_per_min: f.rate };
    try {
      if (edit) onSaved(await dataInboxesApi.update(edit.id, body));
      else onCreated(await dataInboxesApi.create({ ...body, folder: f.folder.trim(), customer_id: f.customer }));
    } catch (e) { setErr(errMsg(e)); } finally { setBusy(false); }
  };
  const selected = customers.find((c) => c.id === f.customer);
  return (
    <Modal onClose={onClose} testId="inbox-form" wide>
      <h2 className="mb-3 font-display text-lg font-semibold">{edit ? t("dataInbox.form.editTitle") : t("dataInbox.form.newTitle")}</h2>
      <div className="space-y-3">
        <Field label={t("dataInbox.form.name")}>
          <input data-testid="inbox-name" className={inputCls} value={f.name} maxLength={80} placeholder={t("dataInbox.form.namePlaceholder")} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
        {!edit && customers.length > 0 && (
          <Field label={t("dataInbox.form.customer")} hint={t("dataInbox.form.customerHint")}>
            <select data-testid="inbox-customer" className={inputCls} value={f.customer} onChange={(e) => setF({ ...f, customer: e.target.value })}>
              <option value="">{t("dataInbox.form.noCustomer")}</option>
              {customers.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </Field>
        )}
        <Field label={t("dataInbox.form.folder")} hint={edit ? t("dataInbox.form.folderFixed") : selected ? t("dataInbox.form.folderCustomerHint", { folder: selected.folder_path || "/Clients" }) : t("dataInbox.form.folderHint")}>
          <input data-testid="inbox-folder" className={inputCls} value={edit ? (edit.folder_path || edit.folder) : f.folder} disabled={!!edit}
            placeholder={selected ? "Imports/..." : "/Shared/Imports/..."} onChange={(e) => setF({ ...f, folder: e.target.value })} />
        </Field>
        <fieldset>
          <legend className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("dataInbox.form.allowed")}</legend>
          <div className="flex flex-wrap gap-3">
            {INBOX_KINDS.map((k) => (
              <label key={k} className="inline-flex items-center gap-1.5 text-sm">
                <input type="checkbox" data-testid={`inbox-kind-${k}`} checked={f.allowed.includes(k)} onChange={() => toggle(k)} />
                {t(`dataInbox.kind.${k}`)}
              </label>
            ))}
          </div>
        </fieldset>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("dataInbox.form.maxSize")} hint={t("dataInbox.form.maxSizeHint")}>
            <input data-testid="inbox-max" type="number" min={0.01} max={25} step={0.5} className={inputCls} value={f.maxMb} onChange={(e) => setF({ ...f, maxMb: Number(e.target.value) })} />
          </Field>
          <Field label={t("dataInbox.form.rate")} hint={t("dataInbox.form.rateHint")}>
            <input data-testid="inbox-rate" type="number" min={1} max={600} className={inputCls} value={f.rate} onChange={(e) => setF({ ...f, rate: Math.max(1, Math.min(600, Number(e.target.value) || 1)) })} />
          </Field>
        </div>
        <ErrorLine msg={err} />
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <Btn onClick={onClose}>{t("dataInbox.cancel")}</Btn>
        <Btn kind="primary" data-testid="inbox-save" disabled={busy} onClick={() => void submit()}>{edit ? t("dataInbox.save") : t("dataInbox.create")}</Btn>
      </div>
    </Modal>
  );
}

function StatusPill({ status }: { status: DataInbox["status"] }) {
  const { t } = useT();
  const c = status === "active" ? "#2f7d55" : "#5b6678";
  return (
    <span data-testid="inbox-status" data-status={status} className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}1a`, color: c }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: c }} />{t(`dataInbox.status.${status}`)}
    </span>
  );
}

function InboxDetail({ ib, isAdmin, onClose, onChanged, onToken }: {
  ib: DataInbox; isAdmin: boolean; onClose: () => void; onChanged: (ib: DataInbox) => void; onToken: (c: CreatedDataInbox) => void;
}) {
  const { t } = useT();
  const [pushes, setPushes] = useState<InboxPush[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<"rotate" | "revoke" | null>(null);
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    dataInboxesApi.pushes(ib.id).then((p) => { if (live) setPushes(p); }).catch((e) => { if (live) { setPushes([]); setErr(errMsg(e)); } });
    return () => { live = false; };
  }, [ib.id, ib.push_count]);
  const act = async (what: "rotate" | "revoke") => {
    setBusy(true); setErr(null);
    try {
      if (what === "rotate") { const c = await dataInboxesApi.rotate(ib.id); onChanged(c); onToken(c); }
      else onChanged(await dataInboxesApi.revoke(ib.id));
      setConfirm(null);
    } catch (e) { setErr(errMsg(e)); } finally { setBusy(false); }
  };
  const url = urlOf(ib);
  const active = ib.status === "active";
  return (
    <Modal onClose={onClose} testId="inbox-detail" wide>
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate font-display text-lg font-semibold">{ib.name}</h2>
          <p className="truncate text-[11px] text-mute">{ib.folder_path || ib.folder}</p>
        </div>
        <StatusPill status={ib.status} />
      </div>
      <div className="space-y-3">
        <CopyField label={t("dataInbox.url")} value={url} testId="inbox-detail-url" />
        <p className="text-[11px] text-mute">{t("dataInbox.tokenHidden", { hint: ib.token_hint })}</p>
        {active && <Examples url={url} token="<TOKEN>" />}
        <section>
          <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("dataInbox.pushes.title")}</h3>
          {pushes === null ? <Loading rows={2} /> : pushes.length === 0 ? <Empty>{t("dataInbox.pushes.empty")}</Empty> : (
            <div className="overflow-x-auto">
              <table data-testid="inbox-pushes" className="w-full text-left text-xs">
                <thead className="text-[10px] uppercase tracking-wide text-mute">
                  <tr><th className="py-1 pr-3">{t("dataInbox.pushes.when")}</th><th className="py-1 pr-3">{t("dataInbox.pushes.size")}</th><th className="py-1 pr-3">{t("dataInbox.pushes.type")}</th><th className="py-1">{t("dataInbox.pushes.file")}</th></tr>
                </thead>
                <tbody>
                  {pushes.map((p) => (
                    <tr key={p.id} className="border-t border-line align-top">
                      <td className="py-1.5 pr-3 whitespace-nowrap">{fmtDateTime(p.received_at)}</td>
                      <td className="py-1.5 pr-3 whitespace-nowrap">{fmtBytes(p.size)}</td>
                      <td className="py-1.5 pr-3 whitespace-nowrap">{[...new Set(p.files.map((f) => t(`dataInbox.kind.${f.kind}`)))].join(", ")}</td>
                      <td className="py-1.5">
                        {p.files.map((f) => (
                          <div key={`${f.file_id}-${f.version}`} className="truncate">
                            <Link href={driveHref(f.path)} data-testid="inbox-push-file" className="font-medium text-accent hover:underline">{f.name}</Link>
                            <span className="text-mute"> · v{f.version}{f.unchanged ? ` · ${t("dataInbox.pushes.unchanged")}` : ""}</span>
                          </div>
                        ))}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
        <p className="text-[11px] text-mute">{t("dataInbox.dataNotInstructions")}</p>
        <ErrorLine msg={err} />
        {confirm && (
          <div data-testid="inbox-confirm" className="rounded-md border border-err/30 bg-err/5 px-3 py-2 text-xs">
            <p className="mb-2 font-medium text-err">{t(`dataInbox.confirm.${confirm}`)}</p>
            <div className="flex gap-2">
              <Btn kind="danger" data-testid="inbox-confirm-yes" disabled={busy} onClick={() => void act(confirm)}>{t(`dataInbox.action.${confirm}`)}</Btn>
              <Btn onClick={() => setConfirm(null)}>{t("dataInbox.cancel")}</Btn>
            </div>
          </div>
        )}
      </div>
      <div className="mt-4 flex flex-wrap justify-end gap-2">
        {isAdmin && active && <Btn data-testid="inbox-edit" onClick={() => setEditing(true)}>{t("dataInbox.action.edit")}</Btn>}
        {isAdmin && active && <Btn data-testid="inbox-rotate" onClick={() => setConfirm("rotate")}>{t("dataInbox.action.rotate")}</Btn>}
        {isAdmin && active && <Btn kind="danger" data-testid="inbox-revoke" onClick={() => setConfirm("revoke")}>{t("dataInbox.action.revoke")}</Btn>}
        <Btn kind="primary" onClick={onClose}>{t("dataInbox.close")}</Btn>
      </div>
      {editing && <InboxForm edit={ib} onClose={() => setEditing(false)} onCreated={() => undefined} onSaved={(v) => { onChanged(v); setEditing(false); }} />}
    </Modal>
  );
}

function InboxCard({ ib, onOpen }: { ib: DataInbox; onOpen: () => void }) {
  const { t } = useT();
  return (
    <button type="button" data-testid={`inbox-card-${ib.id}`} data-status={ib.status} onClick={onOpen}
      className={`ac-pop w-full rounded-xl border border-line bg-panel p-4 text-left shadow-pop transition hover:border-accent ${ib.status === "revoked" ? "opacity-60" : ""}`}>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{ib.name}</div>
          <div className="truncate text-[11px] text-mute">{ib.folder_path || ib.folder}</div>
        </div>
        <StatusPill status={ib.status} />
      </div>
      <div className="mt-2 flex flex-wrap gap-1">
        {ib.allowed.map((k) => <span key={k} className="rounded-md border border-line-strong px-2 py-[1px] text-[10px] font-semibold text-mute">{t(`dataInbox.kind.${k}`)}</span>)}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-mute">
        <span>{t("dataInbox.card.max", { size: fmtBytes(ib.max_bytes) })}</span>
        <span>{t("dataInbox.card.pushes", { count: ib.push_count })}</span>
        <span>…{ib.token_hint}</span>
        <span>{ib.last_push_at ? `${t("dataInbox.card.lastPush")} ${fmtDateTime(ib.last_push_at)}` : t("dataInbox.card.never")}</span>
      </div>
    </button>
  );
}

export function DataInboxesTab({ isAdmin }: { isAdmin: boolean }) {
  const { t } = useT();
  const [items, setItems] = useState<DataInbox[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [token, setToken] = useState<CreatedDataInbox | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const load = useCallback(() => {
    dataInboxesApi.list().then((l) => { setItems(Array.isArray(l) ? l : []); setErr(null); }).catch((e) => { setItems([]); setErr(errMsg(e)); });
  }, []);
  useEffect(() => { if (isAdmin) load(); }, [isAdmin, load]);
  const upsert = (v: DataInbox) => setItems((xs) => {
    const plain: DataInbox = { ...v };
    delete (plain as Partial<CreatedDataInbox>).token; // the token never stays in the list state
    return [plain, ...(xs ?? []).filter((x) => x.id !== v.id)].sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  });
  const open = items?.find((x) => x.id === openId) ?? null;

  return (
    <div data-testid="data-inboxes" className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-2 rounded-xl border border-line bg-panel p-4">
        <div className="min-w-0 max-w-2xl">
          <h2 className="font-display text-base font-semibold">{t("dataInbox.title")}</h2>
          <p className="text-xs text-mute">{t("dataInbox.subtitle")}</p>
        </div>
        <Btn data-testid="inbox-add" kind="primary" disabled={!isAdmin} onClick={() => setCreating(true)}>{t("dataInbox.add")}</Btn>
      </div>
      {!isAdmin ? <Empty>{t("dataInbox.adminOnly")}</Empty>
        : items === null ? <Loading rows={2} />
        : (
          <>
            <ErrorLine msg={err} />
            {items.length === 0 ? <Empty>{t("dataInbox.empty")}</Empty> : (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                {items.map((ib) => <InboxCard key={ib.id} ib={ib} onOpen={() => setOpenId(ib.id)} />)}
              </div>
            )}
          </>
        )}
      {creating && <InboxForm onClose={() => setCreating(false)} onSaved={upsert} onCreated={(c) => { upsert(c); setCreating(false); setToken(c); }} />}
      {open && <InboxDetail ib={open} isAdmin={isAdmin} onClose={() => setOpenId(null)} onChanged={upsert} onToken={(c) => { setOpenId(null); setToken(c); }} />}
      {token && <TokenOnce created={token} onClose={() => setToken(null)} />}
    </div>
  );
}
