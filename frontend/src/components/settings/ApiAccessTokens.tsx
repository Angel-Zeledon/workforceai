"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { API_URL } from "@/lib/config";
import { Btn } from "../ui";
import { gov, type ApiToken } from "../admin/governanceApi";
import { Field, inputCls, Notice, Panel, tdCls, thCls, useErrText, useLoad, when } from "../admin/AdminParts";

const SCOPE_LABEL: Record<string, string> = {
  "requests:read": "adm.api.scope.requestsRead",
  "requests:write": "adm.api.scope.requestsWrite",
  "reports:read": "adm.api.scope.reportsRead",
  "agents:read": "adm.api.scope.agentsRead",
  "usage:read": "adm.api.scope.usageRead",
};

/** Copy button that never throws (clipboard can be blocked); the text stays selectable. */
function CopyBox({ value, testid }: { value: string; testid: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <code data-testid={testid} className="min-w-0 flex-1 select-all break-all rounded-md border border-line-strong bg-panel2 px-2.5 py-1.5 text-[12px] text-ink">{value}</code>
      <Btn onClick={async () => { try { await navigator.clipboard.writeText(value); setDone(true); } catch { /* selectable */ } }}>{done ? t("adm.api.copied") : t("adm.api.copy")}</Btn>
    </div>
  );
}

/** Public API tokens: scoped, hashed at rest (shown once), revocable, with last use. */
export function ApiAccessTokens() {
  const { t, locale } = useT();
  const errText = useErrText();
  const { data, err, reload } = useLoad(() => gov.tokens());
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(["requests:read"]);
  const [days, setDays] = useState("90");
  const [fresh, setFresh] = useState<string | null>(null);
  const [bad, setBad] = useState<string | null>(null);
  const toggle = (s: string) => setScopes((p) => (p.includes(s) ? p.filter((x) => x !== s) : [...p, s]));
  const create = async () => {
    setBad(null);
    try { const r = await gov.createToken(name.trim(), scopes, Number(days) || 0); setFresh(r.token); setName(""); reload(); } catch (e) { setBad(errText(e)); }
  };
  const revoke = async (id: string) => { try { await gov.revokeToken(id); reload(); } catch (e) { setBad(errText(e)); } };
  const state = (x: ApiToken) => (x.revoked_at ? t("adm.api.revoked") : x.expires_at && new Date(x.expires_at) < new Date() ? t("adm.api.expired") : "");
  return (
    <Panel testid="api-tokens" title={t("adm.api.title")} body={t("adm.api.body")}>
      {(err || bad) && <Notice kind="err">{err || bad}</Notice>}
      <p className="text-[12px] text-mute">{t("adm.api.usage", { url: `${API_URL.replace(/\/api\/v1$/, "")}/api/public/v1` })}</p>
      {fresh && (
        <div data-testid="api-token-fresh" className="space-y-1.5 rounded-lg border border-warn/40 bg-warn/5 p-3">
          <p className="text-[12.5px] font-medium text-warn">{t("adm.api.once")}</p>
          <CopyBox value={fresh} testid="api-token-value" />
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-[1fr_180px]">
        <Field label={t("adm.api.name")}><input data-testid="api-token-name" className={inputCls} maxLength={80} value={name} onChange={(e) => setName(e.target.value)} /></Field>
        <Field label={t("adm.api.expires")}><input className={inputCls} type="number" min={0} max={730} value={days} onChange={(e) => setDays(e.target.value)} /></Field>
      </div>
      <fieldset className="space-y-1">
        <legend className="mb-1 text-[11.5px] font-medium text-mute">{t("adm.api.scopes")}</legend>
        <div className="flex flex-wrap gap-x-5 gap-y-1">
          {(data?.scopes ?? Object.keys(SCOPE_LABEL)).map((s) => (
            <label key={s} className="flex items-center gap-1.5 text-[12.5px] text-ink">
              <input type="checkbox" checked={scopes.includes(s)} onChange={() => toggle(s)} />
              {SCOPE_LABEL[s] ? t(SCOPE_LABEL[s]) : s}
            </label>
          ))}
        </div>
      </fieldset>
      <div><Btn kind="primary" disabled={!name.trim() || scopes.length === 0} onClick={create}>{t("adm.api.create")}</Btn></div>
      {data && data.items.length === 0 && <p className="text-mute">{t("adm.api.noTokens")}</p>}
      {data && data.items.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[620px] border-collapse">
            <thead className="bg-panel2"><tr><th className={thCls}>{t("adm.api.name")}</th><th className={thCls}>{t("adm.api.scopes")}</th><th className={thCls}>{t("adm.api.col.lastUsed")}</th><th className={thCls} /></tr></thead>
            <tbody>
              {data.items.map((x) => (
                <tr key={x.id} data-testid="api-token-row" className={`border-t border-line ${x.revoked_at ? "opacity-60" : ""}`}>
                  <td className={tdCls}><b>{x.name}</b><code className="block text-[11px] text-mute">{x.prefix}…</code></td>
                  <td className={`${tdCls} text-[11.5px]`}>{x.scopes.map((s) => (SCOPE_LABEL[s] ? t(SCOPE_LABEL[s]) : s)).join(", ")}{x.expires_at && <span className="block text-mute">{t("adm.api.expiresAt", { when: when(x.expires_at, locale) })}</span>}</td>
                  <td className={`${tdCls} text-[11.5px] text-mute`}>{x.last_used_at ? when(x.last_used_at, locale) : t("adm.api.neverUsed")}</td>
                  <td className={tdCls}>{state(x) ? <span className="text-[11px] font-semibold uppercase text-mute">{state(x)}</span> : <Btn kind="danger" onClick={() => revoke(x.id)}>{t("adm.api.revoke")}</Btn>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}
