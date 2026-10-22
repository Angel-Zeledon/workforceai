"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { connApi } from "@/lib/connections/api";
import {
  draftToSource, emptyEndpoint, hasErrors, normalizeEndpointName, SOURCE_LIMITS, sourceToDraft, validateSource,
  type EndpointDraft, type SourceDraft, type SourceErrCode, type SourceErrors,
} from "@/lib/connections/apiSource";
import { useConnections } from "@/lib/connections/store";
import type { ApiAuthStyle, ApiSource, Connection } from "@/lib/connections/types";
import { Btn } from "../ui";
import { ErrorLine, errText, Field, inputCls } from "./shared";

/** Forms of the custom_api provider ("Other platform (API)"): a generic read-only HTTP source. The API key is NOT here:
 *  it stays in the uncontrolled secret input of the wizard / drawer. */

function FieldErr({ code, testId }: { code?: SourceErrCode; testId?: string }) {
  const { t } = useT();
  if (!code) return null;
  return <span data-testid={testId} data-code={code} className="mt-1 block text-[11px] font-medium text-err">{t(`conn.src.err.${code}`)}</span>;
}

type SetDraft = (fn: (d: SourceDraft) => SourceDraft) => void;

/** Base URL, "what's in there?" and the optional endpoint list. */
export function SourceFields({ draft, setDraft, errors }: { draft: SourceDraft; setDraft: SetDraft; errors: SourceErrors | null }) {
  const { t } = useT();
  const setEp = (i: number, patch: Partial<EndpointDraft>) =>
    setDraft((d) => ({ ...d, endpoints: d.endpoints.map((e, j) => (j === i ? { ...e, ...patch } : e)) }));
  const full = draft.endpoints.length >= SOURCE_LIMITS.endpoints;
  return (
    <div className="space-y-3">
      <Field label={t("conn.src.baseUrl")} hint={t("conn.src.baseUrlHint")}>
        <input data-testid="conn-src-base-url" className={inputCls} type="url" inputMode="url" autoComplete="off" autoCapitalize="none" spellCheck={false}
          placeholder="https://api.example.com/v1" value={draft.base_url} onChange={(e) => setDraft((d) => ({ ...d, base_url: e.target.value }))} />
        <FieldErr code={errors?.base_url} testId="conn-src-err-base-url" />
      </Field>
      <Field label={t("conn.src.description")} hint={`${t("conn.src.descriptionHint")} ${[...draft.description].length}/${SOURCE_LIMITS.description}`}>
        <textarea data-testid="conn-src-description" className={`${inputCls} min-h-[64px] resize-y`} rows={2} maxLength={SOURCE_LIMITS.description}
          placeholder={t("conn.src.descriptionPlaceholder")} value={draft.description} onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))} />
        <FieldErr code={errors?.description} />
      </Field>
      <div>
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.src.endpoints")}</div>
        <p className="mb-2 text-[11px] text-mute">{t("conn.src.endpointsHelp")}</p>
        <div className="space-y-2">
          {draft.endpoints.map((ep, i) => {
            const re = errors?.rows[i];
            return (
              <div key={i} data-testid={`conn-src-endpoint-${i}`} className="space-y-2 rounded-xl border border-line bg-panel2 p-2.5">
                <div className="grid gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
                  <Field label={t("conn.src.endpoint.name")}>
                    <input data-testid={`conn-src-endpoint-name-${i}`} className={inputCls} autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={40}
                      placeholder="stock" value={ep.name} onChange={(e) => setEp(i, { name: normalizeEndpointName(e.target.value) })} />
                    <FieldErr code={re?.name} testId={`conn-src-err-name-${i}`} />
                  </Field>
                  <Field label={t("conn.src.endpoint.path")}>
                    <input data-testid={`conn-src-endpoint-path-${i}`} className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false}
                      placeholder="/products/{sku}/stock" value={ep.path} onChange={(e) => setEp(i, { path: e.target.value })} />
                    <FieldErr code={re?.path} testId={`conn-src-err-path-${i}`} />
                  </Field>
                </div>
                <Field label={t("conn.src.endpoint.description")}>
                  <input data-testid={`conn-src-endpoint-description-${i}`} className={inputCls} maxLength={SOURCE_LIMITS.endpointDescription}
                    placeholder={t("conn.src.endpoint.descriptionPlaceholder")} value={ep.description} onChange={(e) => setEp(i, { description: e.target.value })} />
                  <FieldErr code={re?.description} />
                </Field>
                <Field label={t("conn.src.endpoint.params")} hint={t("conn.src.endpoint.paramsHint")}>
                  <input data-testid={`conn-src-endpoint-params-${i}`} className={inputCls} autoComplete="off" autoCapitalize="none" spellCheck={false}
                    placeholder="sku, warehouse" value={ep.params} onChange={(e) => setEp(i, { params: e.target.value })} />
                </Field>
                <div className="flex justify-end">
                  <Btn data-testid={`conn-src-endpoint-remove-${i}`} kind="danger" onClick={() => setDraft((d) => ({ ...d, endpoints: d.endpoints.filter((_, j) => j !== i) }))}>{t("conn.src.removeEndpoint")}</Btn>
                </div>
              </div>
            );
          })}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Btn data-testid="conn-src-endpoint-add" disabled={full} onClick={() => setDraft((d) => ({ ...d, endpoints: [...d.endpoints, emptyEndpoint()] }))}>+ {t("conn.src.addEndpoint")}</Btn>
          {full && <span className="text-[11px] text-mute">{t("conn.src.endpointsMax")}</span>}
        </div>
        <FieldErr code={errors?.endpoints} />
      </div>
    </div>
  );
}

const AUTH_STYLES: ApiAuthStyle[] = ["bearer", "header", "query"];

/** "How is the key sent?": Bearer, a named header or a named query parameter. */
export function AuthStyleFields({ draft, setDraft, errors }: { draft: SourceDraft; setDraft: SetDraft; errors: SourceErrors | null }) {
  const { t } = useT();
  return (
    <div>
      <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.src.auth")}</div>
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup">
        {AUTH_STYLES.map((s) => (
          <button key={s} type="button" role="radio" aria-checked={draft.auth_style === s} data-testid={`conn-src-auth-${s}`} data-checked={draft.auth_style === s}
            onClick={() => setDraft((d) => ({ ...d, auth_style: s }))}
            className={`rounded-xl border p-2.5 text-left text-xs transition ${draft.auth_style === s ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
            <b className="block text-[13px]">{t(`conn.src.auth.${s}`)}</b>
            <span className="break-words font-mono text-[11px] text-mute">{t(`conn.src.auth.${s}.example`)}</span>
          </button>
        ))}
      </div>
      {draft.auth_style !== "bearer" && (
        <div className="mt-2">
          <Field label={t(draft.auth_style === "header" ? "conn.src.auth.headerName" : "conn.src.auth.queryName")}>
            <input data-testid="conn-src-auth-name" className={`${inputCls} font-mono`} autoComplete="off" autoCapitalize="none" spellCheck={false} maxLength={SOURCE_LIMITS.authName}
              placeholder={draft.auth_style === "header" ? "X-API-Key" : "api_key"} value={draft.auth_name} onChange={(e) => setDraft((d) => ({ ...d, auth_name: e.target.value }))} />
            <FieldErr code={errors?.auth_name} testId="conn-src-err-auth-name" />
          </Field>
        </div>
      )}
    </div>
  );
}

/** Read view of a source (drawer). */
export function SourceSummary({ source }: { source?: ApiSource }) {
  const { t } = useT();
  if (!source) return <p className="text-mute">{t("conn.src.none")}</p>;
  const eps = source.endpoints ?? [];
  const style = source.auth?.style ?? "bearer";
  return (
    <div data-testid="conn-src-summary" className="space-y-2">
      <p data-testid="conn-src-summary-base-url" className="break-all font-mono text-[12px]">{source.base_url}</p>
      <p className="text-mute">{t(`conn.src.authSummary.${style}`, { name: source.auth?.name ?? "" })}</p>
      {source.description && <p className="whitespace-pre-wrap">{source.description}</p>}
      {eps.length === 0 ? <p className="text-mute">{t("conn.src.anyPath")}</p> : (
        <ul className="space-y-1">
          {eps.map((e) => (
            <li key={e.name} data-testid={`conn-src-summary-endpoint-${e.name}`} className="rounded-xl border border-line bg-panel2 px-2 py-1.5">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <b>{e.name}</b>
                <span className="break-all font-mono text-[11px] text-mute">GET {e.path}</span>
              </div>
              {e.description && <p className="text-mute">{e.description}</p>}
              {!!e.params?.length && <p className="text-[11px] text-mute">{t("conn.src.paramsList", { list: e.params.join(", ") })}</p>}
            </li>
          ))}
        </ul>
      )}
      <p className="text-[11px] text-mute">{t("conn.src.agentsSee")}</p>
    </div>
  );
}

/** Drawer section of a custom_api connection: shows the source and lets admins edit it (PATCH source, full replace). */
export function ApiSourceSection({ c, canEdit }: { c: Connection; canEdit: boolean }) {
  const { t } = useT();
  const [draft, setDraftState] = useState<SourceDraft | null>(null);
  const [errors, setErrors] = useState<SourceErrors | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const setDraft: SetDraft = (fn) => setDraftState((d) => (d ? fn(d) : d));
  // re-validate as the owner types once the errors are visible, so they disappear when fixed
  const shownErrors = errors && draft ? validateSource(draft) : null;

  const save = async () => {
    if (!draft) return;
    const e = validateSource(draft);
    if (hasErrors(e)) { setErrors(e); setErr(t("conn.src.fixErrors")); return; }
    setBusy(true); setErr(null);
    try {
      await connApi.patch(c.id, { source: draftToSource(draft) });
      await useConnections.getState().loadConnections();
      setDraftState(null); setErrors(null);
    } catch (x) { setErr(errText(x)); }
    finally { setBusy(false); }
  };

  return (
    <section data-testid="conn-src-section">
      <div className="mb-1 flex items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.drawer.source")}</h3>
        {!draft && canEdit && <Btn data-testid="conn-src-edit" onClick={() => { setDraftState(sourceToDraft(c.source)); setErrors(null); setErr(null); }}>{t("conn.src.edit")}</Btn>}
      </div>
      {!draft ? <SourceSummary source={c.source} /> : (
        <div data-testid="conn-src-editor" className="space-y-3">
          <SourceFields draft={draft} setDraft={setDraft} errors={shownErrors} />
          <AuthStyleFields draft={draft} setDraft={setDraft} errors={shownErrors} />
          <p className="text-[11px] text-mute">{t("conn.src.keyStays")}</p>
          <ErrorLine msg={err} />
          <div className="flex justify-end gap-2">
            <Btn data-testid="conn-src-cancel" disabled={busy} onClick={() => { setDraftState(null); setErrors(null); setErr(null); }}>{t("conn.src.cancel")}</Btn>
            <Btn data-testid="conn-src-save" kind="primary" disabled={busy} onClick={save}>{busy ? t("conn.working") : t("conn.src.save")}</Btn>
          </div>
        </div>
      )}
    </section>
  );
}
