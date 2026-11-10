"use client";
import { useRef, useState } from "react";
import { useT, trOr, tr } from "@/lib/i18n";
import { connApi, dbApi, errCode } from "@/lib/connections/api";
import {
  DB_LIMITS, dbToDraft, draftToDatabase, hasDbErrors, isDbErrorCode, SSL_MODES, validateDb,
  type DbDraft, type DbErrCode, type DbErrors,
} from "@/lib/connections/dbSource";
import { useConnections } from "@/lib/connections/store";
import type { Connection, DatabaseConfig, DbResult, DbSchema, DbSslMode } from "@/lib/connections/types";
import { Btn } from "../ui";
import { EditSecretField, ErrorLine, errText, Field, inputCls, takeSecret } from "./shared";

/** Forms of the "database" provider ("Database (read only)"). The password and the connection string are NOT here:
 *  they stay in the uncontrolled secret inputs of the wizard / drawer and are sent once. */

type SetDraft = (fn: (d: DbDraft) => DbDraft) => void;

/** Text of a database error code (sql_not_read_only, db_unreachable, ...), else the generic connection error. */
export function dbErrText(e: unknown): string {
  const code = errCode(e);
  return isDbErrorCode(code) ? tr(`conn.db.err.${code}`) : errText(e);
}

function FieldErr({ code, testId }: { code?: DbErrCode; testId?: string }) {
  const { t } = useT();
  if (!code) return null;
  return <span data-testid={testId} data-code={code} className="mt-1 block text-[11px] font-medium text-err">{t(`conn.db.ferr.${code}`)}</span>;
}

/** Step 2 of the wizard (and the drawer editor): engine, where the database is, and what agents may see. */
export function DbFields({ draft, setDraft, errors, allowUrl = true }: { draft: DbDraft; setDraft: SetDraft; errors: DbErrors | null; allowUrl?: boolean }) {
  const { t } = useT();
  const set = (patch: Partial<DbDraft>) => setDraft((d) => ({ ...d, ...patch }));
  return (
    <div className="space-y-3">
      <Field label={t("conn.db.engine")}>
        <select data-testid="conn-db-engine" className={inputCls} value={draft.engine} onChange={(e) => set({ engine: e.target.value as DbDraft["engine"] })}>
          <option value="postgres">PostgreSQL</option>
          <option value="mysql" disabled>{t("conn.db.engineMysqlSoon")}</option>
        </select>
        <span className="mt-1 block text-[11px] text-mute">{t("conn.db.engineHint")}</span>
        <FieldErr code={errors?.engine} />
      </Field>
      {allowUrl && (
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.db.inputMode")}</div>
          <div className="grid gap-2 sm:grid-cols-2">
            {(["fields", "url"] as const).map((m) => (
              <button key={m} type="button" data-testid={`conn-db-mode-${m}`} data-checked={draft.mode === m} onClick={() => set({ mode: m })}
                className={`rounded-md border p-2.5 text-left text-xs transition ${draft.mode === m ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
                <b className="block text-[13px]">{t(`conn.db.mode.${m}`)}</b>
                <span className="text-mute">{t(`conn.db.mode.${m}.desc`)}</span>
              </button>
            ))}
          </div>
        </div>
      )}
      {draft.mode === "fields" ? (
        <>
          <div className="grid gap-2 sm:grid-cols-[minmax(0,3fr)_minmax(0,1fr)]">
            <Field label={t("conn.db.host")}>
              <input data-testid="conn-db-host" className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false}
                placeholder="db.example.com" value={draft.host} onChange={(e) => set({ host: e.target.value })} />
              <FieldErr code={errors?.host} testId="conn-db-err-host" />
            </Field>
            <Field label={t("conn.db.port")}>
              <input data-testid="conn-db-port" className={`${inputCls} font-mono`} inputMode="numeric" autoComplete="off" value={draft.port} onChange={(e) => set({ port: e.target.value })} />
              <FieldErr code={errors?.port} testId="conn-db-err-port" />
            </Field>
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            <Field label={t("conn.db.database")}>
              <input data-testid="conn-db-name" className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false}
                placeholder="postgres" value={draft.database} onChange={(e) => set({ database: e.target.value })} />
            </Field>
            <Field label={t("conn.db.user")}>
              <input data-testid="conn-db-user" className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false}
                value={draft.user} onChange={(e) => set({ user: e.target.value })} />
              <FieldErr code={errors?.user} testId="conn-db-err-user" />
            </Field>
          </div>
          <Field label={t("conn.db.ssl")} hint={t(`conn.db.ssl.${draft.sslmode}.hint`)}>
            <select data-testid="conn-db-ssl" className={inputCls} value={draft.sslmode} onChange={(e) => set({ sslmode: e.target.value as DbSslMode })}>
              {SSL_MODES.map((m) => <option key={m} value={m}>{t(`conn.db.ssl.${m}`)}</option>)}
            </select>
          </Field>
        </>
      ) : (
        <p data-testid="conn-db-url-note" className="rounded-md border border-line bg-panel2 px-3 py-2 text-[11px] text-mute">{t("conn.db.urlNote")}</p>
      )}
      <Field label={t("conn.db.description")} hint={`${t("conn.db.descriptionHint")} ${[...draft.description].length}/${DB_LIMITS.description}`}>
        <textarea data-testid="conn-db-description" className={`${inputCls} min-h-[56px] resize-y`} rows={2} maxLength={DB_LIMITS.description}
          placeholder={t("conn.db.descriptionPlaceholder")} value={draft.description} onChange={(e) => set({ description: e.target.value })} />
        <FieldErr code={errors?.description} />
      </Field>
      <Field label={t("conn.db.schemas")} hint={t("conn.db.schemasHint")}>
        <input data-testid="conn-db-schemas" className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false}
          placeholder="public" value={draft.schemas} onChange={(e) => set({ schemas: e.target.value })} />
        <FieldErr code={errors?.schemas} testId="conn-db-err-schemas" />
      </Field>
    </div>
  );
}

/** What every database connection guarantees (shown in the wizard and the drawer). */
export function DbReadOnlyNote() {
  const { t } = useT();
  return <p data-testid="conn-db-readonly-note" className="rounded-md border border-line bg-panel2 px-3 py-2 text-[11px] text-mute">{t("conn.db.readOnlyNote")}</p>;
}

function Summary({ c }: { c: Connection }) {
  const { t } = useT();
  const d = c.database;
  if (!d) return <p className="text-mute">{t("conn.db.none")}</p>;
  return (
    <div data-testid="conn-db-summary" className="space-y-1">
      <p className="break-all font-mono text-[12px]">{d.user}@{d.host}:{d.port}/{d.database}</p>
      <p className="text-mute">PostgreSQL · {t(`conn.db.ssl.${d.sslmode}`)}</p>
      <p className="text-mute">{d.schemas?.length ? t("conn.db.schemasList", { list: d.schemas.join(", ") }) : t("conn.db.schemasAll")}</p>
      {d.description && <p className="whitespace-pre-wrap">{d.description}</p>}
    </div>
  );
}

function Tables({ schema }: { schema: DbSchema }) {
  const { t } = useT();
  const [open, setOpen] = useState<string | null>(null);
  if (schema.tables.length === 0) return <p data-testid="conn-db-tables-empty" className="text-mute">{t("conn.db.tablesEmpty")}</p>;
  return (
    <div data-testid="conn-db-tables" className="space-y-1">
      <p className="text-[11px] text-mute">{t("conn.db.tablesCount", { n: schema.tables.length })}{schema.truncated ? ` · ${t("conn.db.tablesTruncated")}` : ""}</p>
      <ul className="max-h-64 space-y-1 overflow-y-auto">
        {schema.tables.map((tb) => {
          const key = `${tb.schema}.${tb.name}`;
          return (
            <li key={key} data-testid={`conn-db-table-${key}`} className="rounded-md border border-line bg-panel2">
              <button type="button" className="flex w-full items-baseline justify-between gap-2 px-2 py-1.5 text-left" onClick={() => setOpen(open === key ? null : key)}>
                <span className="min-w-0 truncate font-mono text-[12px]"><span className="text-mute">{tb.schema}.</span>{tb.name}</span>
                <span className="shrink-0 text-[10px] text-mute">{t(`conn.db.kind.${tb.kind}`)}{tb.rows_estimate >= 0 ? ` · ~${tb.rows_estimate}` : ""}</span>
              </button>
              {open === key && (
                <div className="border-t border-line px-2 py-1.5">
                  {tb.comment && <p className="mb-1 text-mute">{tb.comment}</p>}
                  <ul className="space-y-0.5">
                    {tb.columns.map((col) => (
                      <li key={col.name} className="flex flex-wrap gap-x-2 font-mono text-[11px]">
                        <span>{col.name}</span><span className="text-mute">{col.type}{col.not_null ? " not null" : ""}</span>
                        {col.comment && <span className="font-sans text-mute">— {col.comment}</span>}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function ResultTable({ res }: { res: DbResult }) {
  const { t } = useT();
  if (res.error) {
    return (
      <div data-testid="conn-db-result-error" data-sqlstate={res.error.sqlstate} className="rounded-md border border-err/30 bg-err/5 px-3 py-2 text-xs text-err">
        <b>{t("conn.db.queryFailed", { code: res.error.sqlstate })}</b>
        <p className="mt-0.5 break-words font-mono text-[11px]">{res.error.message}</p>
      </div>
    );
  }
  return (
    <div data-testid="conn-db-result" data-rows={res.row_count} className="space-y-1">
      <p className="text-[11px] text-mute">
        {t("conn.db.resultMeta", { n: res.row_count, ms: res.elapsed_ms })}
        {res.truncated && <> · <b data-testid="conn-db-result-truncated" className="text-amber-700">{t(`conn.db.truncated.${res.truncated_by ?? "rows"}`, { n: DB_LIMITS.maxRows })}</b></>}
      </p>
      <div className="wf-scroll-x max-h-72 overflow-auto rounded-md border border-line">
        <table className="min-w-full border-collapse text-[11px]">
          <thead className="sticky top-0 bg-panel2">
            <tr>{res.columns.map((c, i) => <th key={i} className="border-b border-line px-2 py-1 text-left font-semibold" title={c.type}>{c.name}</th>)}</tr>
          </thead>
          <tbody>
            {res.rows.map((r, i) => (
              <tr key={i} className="odd:bg-panel even:bg-panel2/50">
                {r.map((v, j) => <td key={j} className="max-w-[220px] truncate border-b border-line/60 px-2 py-0.5 font-mono" title={v ?? "NULL"}>{v === null ? <span className="text-mute">NULL</span> : v}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/** Drawer section of a database connection: configuration, the tables it can read and a test query by hand. */
export function DatabaseSection({ c, canEdit }: { c: Connection; canEdit: boolean }) {
  const { t } = useT();
  const [draft, setDraftState] = useState<DbDraft | null>(null);
  const [showErr, setShowErr] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<"" | "save" | "tables" | "query">("");
  const [schema, setSchema] = useState<DbSchema | null>(null);
  const [sql, setSql] = useState("");
  const [res, setRes] = useState<DbResult | null>(null);
  const setDraft: SetDraft = (fn) => setDraftState((d) => (d ? fn(d) : d));
  const errors = showErr && draft ? validateDb(draft) : null;
  const active = c.status === "active";
  const secretRef = useRef<HTMLInputElement>(null);
  const storesSecret = c.mode === "live" && c.kind === "api_key";

  const save = async () => {
    if (!draft) return;
    if (hasDbErrors(validateDb(draft))) { setShowErr(true); setErr(t("conn.db.fixErrors")); return; }
    setBusy("save"); setErr(null);
    try {
      await connApi.patch(c.id, { database: draftToDatabase(draft) as DatabaseConfig, secret: takeSecret(secretRef) });
      await useConnections.getState().loadConnections();
      setDraftState(null); setShowErr(false); setSchema(null);
    } catch (x) { setErr(dbErrText(x)); }
    finally { setBusy(""); }
  };
  const loadTables = async () => {
    setBusy("tables"); setErr(null);
    try { setSchema(await dbApi.schema(c.id)); } catch (x) { setErr(dbErrText(x)); }
    finally { setBusy(""); }
  };
  const run = async () => {
    setBusy("query"); setErr(null); setRes(null);
    try { setRes(await dbApi.query(c.id, sql)); } catch (x) { setErr(dbErrText(x)); }
    finally { setBusy(""); }
  };

  return (
    <section data-testid="conn-db-section" className="space-y-3">
      <div>
        <div className="mb-1 flex items-center justify-between gap-2">
          <h3 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.db.section")}</h3>
          {!draft && canEdit && <Btn data-testid="conn-db-edit" onClick={() => { setDraftState(dbToDraft(c.database)); setShowErr(false); setErr(null); }}>{t("conn.src.edit")}</Btn>}
        </div>
        {!draft ? <Summary c={c} /> : (
          <div data-testid="conn-db-editor" className="space-y-3">
            <DbFields draft={draft} setDraft={setDraft} errors={errors} allowUrl={false} />
            {storesSecret ? <EditSecretField inputRef={secretRef} /> : <p className="text-[11px] text-mute">{t("conn.db.passwordStays")}</p>}
            <div className="flex justify-end gap-2">
              <Btn data-testid="conn-db-cancel" disabled={!!busy} onClick={() => { setDraftState(null); setShowErr(false); setErr(null); }}>{t("conn.src.cancel")}</Btn>
              <Btn data-testid="conn-db-save" kind="primary" disabled={!!busy} onClick={save}>{busy === "save" ? t("conn.working") : t("conn.src.save")}</Btn>
            </div>
          </div>
        )}
      </div>
      <DbReadOnlyNote />
      {c.mode === "simulated" && <p className="text-[11px] text-mute">{t("conn.db.simulatedNote")}</p>}
      <div>
        <div className="mb-1 flex items-center justify-between gap-2">
          <h3 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.db.tables")}</h3>
          <Btn data-testid="conn-db-load-tables" disabled={!canEdit || !active || !!busy} onClick={loadTables}>{busy === "tables" ? t("conn.working") : schema ? t("conn.db.reloadTables") : t("conn.db.loadTables")}</Btn>
        </div>
        {schema ? <Tables schema={schema} /> : <p className="text-[11px] text-mute">{t("conn.db.tablesHelp")}</p>}
      </div>
      <div>
        <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.db.testQuery")}</h3>
        <p className="mb-1 text-[11px] text-mute">{t("conn.db.testQueryHelp")}</p>
        <textarea data-testid="conn-db-sql" className={`${inputCls} min-h-[72px] resize-y font-mono text-[12px]`} rows={3} spellCheck={false} maxLength={20000}
          placeholder="SELECT * FROM public.customers LIMIT 20" value={sql} onChange={(e) => setSql(e.target.value)} />
        <div className="mt-1.5 flex justify-end">
          <Btn data-testid="conn-db-run" kind="primary" disabled={!canEdit || !active || !sql.trim() || !!busy} onClick={run}>{busy === "query" ? t("conn.working") : t("conn.db.run")}</Btn>
        </div>
        {res && <div className="mt-2"><ResultTable res={res} /></div>}
      </div>
      <ErrorLine msg={err} />
    </section>
  );
}

/** Text of a failed connection test of a database (the code says why: host, TLS, password...). */
export function dbTestText(code: string | undefined): string {
  return code && isDbErrorCode(code) ? tr(`conn.db.err.${code}`) : trOr("conn.test.failed", "");
}
