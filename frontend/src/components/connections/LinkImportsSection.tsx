"use client";
import { useCallback, useEffect, useState } from "react";
import { useT, trOr, fmtDateTime, fmtNumber } from "@/lib/i18n";
import { customersApi, type Customer } from "@/lib/customersApi";
import {
  LINK_SCHEDULES, linkImportsApi, linkProblem, useLinkImportsLive,
  type LinkImport, type LinkImportInput, type LinkRun, type LinkSchedule,
} from "@/lib/linkImportsApi";
import { Btn, Card, Empty } from "../ui";
import { ErrorLine, Field, inputCls, Modal, useViewer } from "./shared";

/** "Import from a link" (docs/architecture/link-imports.md): a generic pull import next to the API sources. Paste a
 *  URL that returns data; it lands in the drive as a versioned file, once or on a schedule. Self-contained: its own
 *  API client (with a mock), its own i18n block (linkImports.*). The header value is write-only. */

const STATUS_COLOR: Record<string, string> = { ok: "#2f7d55", unchanged: "#5b6678", error: "#b4443c", skipped: "#a86208", "": "#5b6678" };

function codeText(code: string, httpStatus?: number): string {
  if (code === "http_status" && httpStatus) return trOr("linkImports.code.http_status", code) + ` (HTTP ${httpStatus})`;
  return trOr(`linkImports.code.${code}`, trOr("linkImports.code.generic", code));
}

function errMessage(e: unknown): string {
  const m = e instanceof Error ? e.message : String(e);
  if (/^[a-z_]+$/.test(m)) return codeText(m);
  if (/-> 403$/.test(m)) return trOr("linkImports.adminOnly", m);
  if (/-> 400$/.test(m)) return trOr("linkImports.err.rejected", m);
  return trOr("linkImports.code.generic", m);
}

function StatusChip({ status }: { status: string }) {
  const { t } = useT();
  const c = STATUS_COLOR[status] ?? "#5b6678";
  return (
    <span data-testid="link-import-status" data-status={status || "never"} className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}1a`, color: c }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: c }} />
      {t(`linkImports.status.${status || "never"}`)}
    </span>
  );
}

function runSummary(r: LinkRun, t: (k: string, v?: Record<string, string | number>) => string): string {
  switch (r.status) {
    case "ok": return t("linkImports.result.ok", { version: r.version, path: r.path });
    case "unchanged": return t("linkImports.result.unchanged");
    case "skipped": return t("linkImports.result.skipped", { reason: codeText(r.code) });
    default: return t("linkImports.result.error", { reason: codeText(r.code, r.http_status) });
  }
}

interface FormProps { initial: LinkImport | null; customers: Customer[]; onClose: () => void; onSaved: (li: LinkImport, created: boolean) => void }

function LinkImportForm({ initial, customers, onClose, onSaved }: FormProps) {
  const { t } = useT();
  const edit = !!initial;
  const [name, setName] = useState(initial?.name ?? "");
  const [url, setUrl] = useState(initial?.url ?? "");
  const [schedule, setSchedule] = useState<LinkSchedule>(initial?.schedule ?? "once");
  const [customer, setCustomer] = useState(initial?.customer_id ?? "");
  const [folder, setFolder] = useState("");
  const [stem, setStem] = useState(initial?.file_stem ?? "");
  const [headerName, setHeaderName] = useState(initial?.header_name ?? "");
  const [clearHeader, setClearHeader] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const urlChanged = !edit || url !== initial?.url;
  const problem = urlChanged ? linkProblem(url) : "";

  const submit = async (form: HTMLFormElement) => {
    setErr(null);
    if (!name.trim()) { setErr(t("linkImports.err.name")); return; }
    if (problem) { setErr(t(`linkImports.err.${problem}`)); return; }
    // The header value is read from the uncontrolled input at submit time and never kept in state.
    const valueInput = form.elements.namedItem("header_value") as HTMLInputElement | null;
    const value = valueInput?.value ?? "";
    const body: LinkImportInput = { name: name.trim(), schedule, file_stem: stem.trim() };
    if (urlChanged) body.url = url.trim();
    if (!edit) {
      if (customer) body.customer_id = customer;
      if (folder.trim()) body.folder = folder.trim();
    }
    if (clearHeader) body.header = { name: "", clear: true };
    else if (headerName.trim() && (value || headerName.trim() !== (initial?.header_name ?? ""))) body.header = { name: headerName.trim(), value };
    else if (!edit && value) { setErr(t("linkImports.err.headerName")); return; }
    setBusy(true);
    try {
      const li = edit ? await linkImportsApi.update(initial!.id, body) : await linkImportsApi.create(body);
      if (valueInput) valueInput.value = "";
      onSaved(li, !edit);
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal onClose={onClose} testId="link-import-form" wide>
      <form onSubmit={(e) => { e.preventDefault(); void submit(e.currentTarget); }} className="space-y-3" autoComplete="off">
        <h2 className="font-display text-lg font-semibold">{t(edit ? "linkImports.form.titleEdit" : "linkImports.form.titleNew")}</h2>
        <Field label={t("linkImports.form.name")}>
          <input data-testid="link-import-name" className={inputCls} maxLength={80} value={name} placeholder={t("linkImports.form.namePlaceholder")} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={t("linkImports.form.url")} hint={t("linkImports.form.urlHint")}>
          <input data-testid="link-import-url" className={`${inputCls} font-mono`} type="url" inputMode="url" autoCapitalize="none" spellCheck={false}
            placeholder="https://docs.example.com/…/pub?output=csv" value={url} onChange={(e) => setUrl(e.target.value)} />
          {url && problem && <span data-testid="link-import-url-err" className="mt-1 block text-[11px] font-medium text-err">{t(`linkImports.err.${problem}`)}</span>}
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("linkImports.form.schedule")} hint={schedule === "once" ? t("linkImports.form.scheduleOnceHint") : t("linkImports.form.scheduleHint")}>
            <select data-testid="link-import-schedule" className={inputCls} value={schedule} onChange={(e) => setSchedule(e.target.value as LinkSchedule)}>
              {LINK_SCHEDULES.map((s) => <option key={s} value={s}>{t(`linkImports.schedule.${s}`)}</option>)}
            </select>
          </Field>
          <Field label={t("linkImports.form.fileStem")} hint={t("linkImports.form.fileStemHint")}>
            <input data-testid="link-import-stem" className={inputCls} maxLength={80} value={stem} placeholder={t("linkImports.form.fileStemPlaceholder")} onChange={(e) => setStem(e.target.value)} />
          </Field>
        </div>
        {!edit && (
          <div className="grid gap-3 sm:grid-cols-2">
            {customers.length > 0 && (
              <Field label={t("linkImports.form.customer")} hint={t("linkImports.form.customerHint")}>
                <select data-testid="link-import-customer" className={inputCls} value={customer} onChange={(e) => setCustomer(e.target.value)}>
                  <option value="">{t("linkImports.form.customerNone")}</option>
                  {customers.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
              </Field>
            )}
            <Field label={t("linkImports.form.folder")} hint={t(customer ? "linkImports.form.folderCustomerHint" : "linkImports.form.folderHint")}>
              <input data-testid="link-import-folder" className={inputCls} spellCheck={false} value={folder}
                placeholder={customer ? "Imports/Links/…" : "/Shared/Imports/Links/…"} onChange={(e) => setFolder(e.target.value)} />
            </Field>
          </div>
        )}
        <fieldset className="space-y-2 rounded-xl border border-line bg-panel2 p-3">
          <legend className="px-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("linkImports.form.header")}</legend>
          <p className="text-[11px] text-mute">{t("linkImports.form.headerHint")}</p>
          <div className="grid gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
            <Field label={t("linkImports.form.headerName")}>
              <input data-testid="link-import-header-name" className={`${inputCls} font-mono`} maxLength={64} spellCheck={false} autoCapitalize="none" disabled={clearHeader}
                placeholder="Authorization" value={headerName} onChange={(e) => setHeaderName(e.target.value)} />
            </Field>
            <Field label={t("linkImports.form.headerValue")} hint={initial?.header_set ? t("linkImports.form.headerValueKeep", { hint: initial.header_hint || "····" }) : undefined}>
              {/* Uncontrolled and write-only: the value never enters React state nor comes back from the server. */}
              <input data-testid="link-import-header-value" name="header_value" className={`${inputCls} font-mono`} type="password" autoComplete="new-password"
                spellCheck={false} disabled={clearHeader} placeholder={initial?.header_set ? "••••••••" : "Bearer …"} />
            </Field>
          </div>
          {initial?.header_set && (
            <label className="flex items-center gap-2 text-xs">
              <input data-testid="link-import-header-clear" type="checkbox" checked={clearHeader} onChange={(e) => setClearHeader(e.target.checked)} />
              {t("linkImports.form.headerClear")}
            </label>
          )}
        </fieldset>
        <p className="text-[11px] text-mute">{t("linkImports.form.dataNote")}</p>
        <ErrorLine msg={err} />
        <div className="flex justify-end gap-2">
          <Btn onClick={onClose}>{t("linkImports.form.cancel")}</Btn>
          <Btn data-testid="link-import-save" kind="primary" type="submit" disabled={busy}>{t(edit ? "linkImports.form.save" : "linkImports.form.create")}</Btn>
        </div>
      </form>
    </Modal>
  );
}

function RunsList({ id, tick }: { id: string; tick: number }) {
  const { t } = useT();
  const [runs, setRuns] = useState<LinkRun[] | null>(null);
  useEffect(() => {
    let live = true;
    linkImportsApi.runs(id, 20).then((r) => { if (live) setRuns(r); }).catch(() => { if (live) setRuns([]); });
    return () => { live = false; };
  }, [id, tick]);
  if (!runs) return null;
  if (runs.length === 0) return <p className="text-[11px] text-mute">{t("linkImports.noRuns")}</p>;
  return (
    <ul data-testid="link-import-runs" className="divide-y divide-line rounded-lg border border-line text-[11px]">
      {runs.map((r) => (
        <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-2.5 py-1.5" data-testid="link-import-run" data-status={r.status}>
          <StatusChip status={r.status} />
          <span className="text-mute">{fmtDateTime(r.started_at)}</span>
          <span className="text-mute">{t(`linkImports.trigger.${r.trigger}`)}</span>
          {r.size > 0 && <span className="text-mute">{t("linkImports.kb", { kb: fmtNumber(Math.max(1, Math.round(r.size / 1024))) })}</span>}
          {r.version > 0 && <span className="text-mute">{t("linkImports.versionShort", { version: r.version })}</span>}
          {r.code && <span className="text-ink">{codeText(r.code, r.http_status)}</span>}
        </li>
      ))}
    </ul>
  );
}

function LinkImportRow({ li, isAdmin, tick, onEdit, onChanged, onDeleted }: {
  li: LinkImport; isAdmin: boolean; tick: number; onEdit: () => void; onChanged: (li: LinkImport) => void; onDeleted: () => void;
}) {
  const { t } = useT();
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const [msg, setMsg] = useState<{ text: string; bad: boolean } | null>(null);
  const [localTick, setLocalTick] = useState(0);

  const run = async () => {
    setBusy(true); setMsg(null);
    try {
      const res = await linkImportsApi.run(li.id);
      setMsg({ text: runSummary(res.run, t), bad: res.run.status === "error" || res.run.status === "skipped" });
      if (res.import) onChanged(res.import);
      setLocalTick((n) => n + 1);
    } catch (e) {
      setMsg({ text: errMessage(e), bad: true });
    } finally {
      setBusy(false);
    }
  };
  const toggle = async () => {
    setBusy(true);
    try { onChanged(await linkImportsApi.update(li.id, { enabled: !li.enabled })); } catch (e) { setMsg({ text: errMessage(e), bad: true }); } finally { setBusy(false); }
  };
  const remove = async () => {
    if (!window.confirm(t("linkImports.confirmDelete", { name: li.name }))) return;
    setBusy(true);
    try { await linkImportsApi.remove(li.id); onDeleted(); } catch (e) { setMsg({ text: errMessage(e), bad: true }); setBusy(false); }
  };

  return (
    <li data-testid={`link-import-${li.id}`} className="rounded-xl border border-line bg-panel p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{li.name}</div>
          <div className="truncate font-mono text-[11px] text-mute" title={li.url}>{li.host}</div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="rounded-full bg-accent-soft px-2 py-[1px] text-[10px] font-semibold text-accent">{t(`linkImports.schedule.${li.schedule}`)}</span>
          {!li.enabled && <span className="rounded-full bg-amber-500/15 px-2 py-[1px] text-[10px] font-semibold text-amber-700">{t("linkImports.paused")}</span>}
          <StatusChip status={li.last_status} />
        </div>
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-mute">
        <span>{li.last_run_at ? t("linkImports.lastRun", { time: fmtDateTime(li.last_run_at) }) : t("linkImports.neverRun")}</span>
        {li.enabled && li.next_run_at && li.schedule !== "once" && <span>{t("linkImports.nextRun", { time: fmtDateTime(li.next_run_at) })}</span>}
        {li.header_set && <span data-testid="link-import-header-hint">{t("linkImports.headerSet", { name: li.header_name, hint: li.header_hint || "····" })}</span>}
        <span className="min-w-0 truncate">{t("linkImports.destination")}: <span className="font-mono">{li.last_path || li.folder_path || li.folder}</span></span>
      </div>
      {li.last_status === "error" && li.last_code && <p className="mt-1 text-[11px] font-medium text-err">{codeText(li.last_code)}</p>}
      {li.last_status === "skipped" && li.last_code && <p className="mt-1 text-[11px] font-medium text-amber-700">{codeText(li.last_code)}</p>}
      {msg && <p data-testid="link-import-msg" role="status" className={`mt-2 text-xs font-medium ${msg.bad ? "text-err" : "text-ok"}`}>{msg.text}</p>}
      <div className="mt-2 flex flex-wrap gap-1.5">
        {isAdmin && <Btn data-testid="link-import-run" kind="primary" disabled={busy} onClick={() => void run()}>{busy ? t("linkImports.running") : t("linkImports.run")}</Btn>}
        <Btn data-testid="link-import-history" onClick={() => setOpen((o) => !o)}>{open ? t("linkImports.hideHistory") : t("linkImports.history")}</Btn>
        {isAdmin && <Btn data-testid="link-import-edit" disabled={busy} onClick={onEdit}>{t("linkImports.edit")}</Btn>}
        {isAdmin && li.schedule !== "once" && <Btn data-testid="link-import-toggle" disabled={busy} onClick={() => void toggle()}>{li.enabled ? t("linkImports.pause") : t("linkImports.resume")}</Btn>}
        {isAdmin && <Btn data-testid="link-import-delete" kind="danger" disabled={busy} onClick={() => void remove()}>{t("linkImports.delete")}</Btn>}
      </div>
      {open && <div className="mt-2"><RunsList id={li.id} tick={tick + localTick} /></div>}
    </li>
  );
}

/** Mounted in the connections screen, below the API sources. */
export function LinkImportsSection() {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const tick = useLinkImportsLive((s) => s.tick);
  const [items, setItems] = useState<LinkImport[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [form, setForm] = useState<{ edit: LinkImport | null } | null>(null);
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    try { setItems(await linkImportsApi.list()); setErr(null); } catch (e) { setItems([]); setErr(errMessage(e)); }
  }, []);
  useEffect(() => { void load(); }, [load, tick]);
  useEffect(() => {
    if (!form || form.edit) return;
    customersApi.list().then((cs) => setCustomers(Array.isArray(cs) ? cs.filter((c) => !c.archived) : [])).catch(() => setCustomers([]));
  }, [form]);

  const upsert = (li: LinkImport) => setItems((xs) => {
    const list = xs ?? [];
    return list.some((x) => x.id === li.id) ? list.map((x) => (x.id === li.id ? li : x)) : [li, ...list];
  });

  const saved = async (li: LinkImport, created: boolean) => {
    setForm(null);
    upsert(li);
    setNotice(null);
    if (created && li.schedule === "once") {
      try {
        const res = await linkImportsApi.run(li.id); // "once": import right away
        if (res.import) upsert(res.import);
        setNotice(runSummary(res.run, t));
      } catch (e) { setNotice(errMessage(e)); }
    } else if (created) {
      setNotice(t("linkImports.firstRunSoon"));
    }
  };

  return (
    <Card title={t("linkImports.title")} className="mt-4"
      right={<Btn data-testid="link-import-add" kind="primary" disabled={!isAdmin} onClick={() => setForm({ edit: null })}>{t("linkImports.add")}</Btn>}>
      <div data-testid="link-imports">
        <p className="mb-3 text-xs text-mute">{t("linkImports.subtitle")}</p>
        {!isAdmin && <p className="mb-2 text-[11px] text-mute">{t("linkImports.adminOnly")}</p>}
        {notice && <p data-testid="link-import-notice" role="status" className="mb-2 text-xs font-medium text-ink">{notice}</p>}
        <ErrorLine msg={err} />
        {items && items.length === 0 && !err && <Empty>{t("linkImports.empty")}</Empty>}
        {items && items.length > 0 && (
          <ul className="space-y-2">
            {items.map((li) => (
              <LinkImportRow key={li.id} li={li} isAdmin={isAdmin} tick={tick} onEdit={() => setForm({ edit: li })} onChanged={upsert}
                onDeleted={() => setItems((xs) => (xs ?? []).filter((x) => x.id !== li.id))} />
            ))}
          </ul>
        )}
      </div>
      {form && <LinkImportForm initial={form.edit} customers={customers} onClose={() => setForm(null)} onSaved={(li, c) => void saved(li, c)} />}
    </Card>
  );
}
