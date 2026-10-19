"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { Btn } from "../ui";
import { gov, type WebhookDelivery, type WebhookEndpoint } from "../admin/governanceApi";
import { Field, inputCls, Notice, Panel, tdCls, thCls, useErrText, useLoad, when } from "../admin/AdminParts";

const EVENT_LABEL: Record<string, string> = {
  "request.done": "adm.wh.event.requestDone",
  "approval.requested": "adm.wh.event.approvalRequested",
  "report.created": "adm.wh.event.reportCreated",
};

function Deliveries({ id }: { id: string }) {
  const { t, locale } = useT();
  const { data, err } = useLoad(() => gov.deliveries(id));
  if (err) return <Notice kind="err">{err}</Notice>;
  const items: WebhookDelivery[] = data?.items ?? [];
  if (data && items.length === 0) return <p className="text-[12px] text-mute">{t("adm.wh.noDeliveries")}</p>;
  return (
    <div className="overflow-x-auto rounded-lg border border-line">
      <table className="w-full min-w-[480px] border-collapse">
        <thead className="bg-panel2"><tr><th className={thCls}>{t("adm.wh.col.event")}</th><th className={thCls}>{t("adm.wh.col.state")}</th><th className={thCls}>{t("adm.wh.col.attempts")}</th><th className={thCls}>{t("adm.wh.col.when")}</th></tr></thead>
        <tbody>{items.map((d) => (
          <tr key={d.id} className="border-t border-line">
            <td className={tdCls}>{d.event}</td>
            <td className={tdCls}>{d.status}{d.last_status ? ` (HTTP ${d.last_status})` : d.last_error ? ` (${d.last_error})` : ""}</td>
            <td className={tdCls}>{d.attempts}</td>
            <td className={`${tdCls} whitespace-nowrap text-mute`}>{when(d.created_at, locale)}</td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}

/** Outbound webhooks: HMAC-signed events with retries. The signing secret is shown once. */
export function ApiAccessWebhooks() {
  const { t } = useT();
  const errText = useErrText();
  const { data, err, reload } = useLoad(() => gov.webhooks());
  const [url, setUrl] = useState("");
  const [desc, setDesc] = useState("");
  const [events, setEvents] = useState<string[]>(["request.done"]);
  const [secret, setSecret] = useState<string | null>(null);
  const [bad, setBad] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const guard = async (fn: () => Promise<void>) => { setBad(null); setInfo(null); try { await fn(); } catch (e) { setBad(errText(e)); } };
  const toggle = (e: string) => setEvents((p) => (p.includes(e) ? p.filter((x) => x !== e) : [...p, e]));
  const create = () => guard(async () => { const r = await gov.createWebhook(url.trim(), desc.trim(), events); setSecret(r.secret); setUrl(""); setDesc(""); reload(); });
  const test = (ep: WebhookEndpoint) => guard(async () => {
    const d = await gov.testWebhook(ep.id);
    setInfo(t("adm.wh.testResult", { status: d.last_status || 0, state: d.status }));
    reload();
  });
  return (
    <Panel testid="api-webhooks" title={t("adm.wh.title")} body={t("adm.wh.body")}>
      {(err || bad) && <Notice kind="err">{err || bad}</Notice>}
      {info && <Notice kind="ok">{info}</Notice>}
      {secret && (
        <div data-testid="webhook-secret" className="space-y-1.5 rounded-lg border border-warn/40 bg-warn/5 p-3">
          <p className="text-[12.5px] font-medium text-warn">{t("adm.wh.secretOnce")}</p>
          <code className="block select-all break-all rounded-md border border-line-strong bg-panel2 px-2.5 py-1.5 text-[12px] text-ink">{secret}</code>
          <p className="text-[11.5px] text-mute">{t("adm.wh.verifyHint")}</p>
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("adm.wh.url")}><input data-testid="webhook-url" className={inputCls} type="url" placeholder="https://example.com/hooks/aiwos" value={url} onChange={(e) => setUrl(e.target.value)} /></Field>
        <Field label={t("adm.wh.desc")}><input className={inputCls} maxLength={200} value={desc} onChange={(e) => setDesc(e.target.value)} /></Field>
      </div>
      <fieldset>
        <legend className="mb-1 text-[11.5px] font-medium text-mute">{t("adm.wh.events")}</legend>
        <div className="flex flex-wrap gap-x-5 gap-y-1">
          {(data?.events ?? Object.keys(EVENT_LABEL)).map((e) => (
            <label key={e} className="flex items-center gap-1.5 text-[12.5px] text-ink"><input type="checkbox" checked={events.includes(e)} onChange={() => toggle(e)} />{EVENT_LABEL[e] ? t(EVENT_LABEL[e]) : e}</label>
          ))}
        </div>
      </fieldset>
      <div><Btn kind="primary" disabled={!url.trim() || events.length === 0} onClick={create}>{t("adm.wh.create")}</Btn></div>
      {data && data.items.length === 0 && <p className="text-mute">{t("adm.wh.noEndpoints")}</p>}
      <ul className="space-y-3">
        {(data?.items ?? []).map((ep) => (
          <li key={ep.id} data-testid="webhook-row" className={`rounded-lg border border-line p-3 ${ep.active ? "" : "opacity-70"}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <b className="block truncate text-[13px] text-ink">{ep.url}</b>
                <span className="text-[11.5px] text-mute">{ep.description || ep.events.join(", ")} · …{ep.secret_hint}</span>
                {!ep.active && <span className="ml-2 text-[11px] font-semibold text-warn">{ep.disabled_reason === "too_many_failures" ? t("adm.wh.tooManyFailures") : t("adm.wh.paused")}</span>}
              </div>
              <div className="flex flex-wrap gap-2">
                <Btn onClick={() => test(ep)}>{t("adm.wh.test")}</Btn>
                <Btn onClick={() => setOpen(open === ep.id ? null : ep.id)}>{t("adm.wh.deliveries")}</Btn>
                <Btn onClick={() => guard(async () => { await gov.setWebhookActive(ep.id, !ep.active); reload(); })}>{ep.active ? t("adm.wh.pause") : t("adm.wh.resume")}</Btn>
                <Btn kind="danger" onClick={() => guard(async () => { await gov.deleteWebhook(ep.id); reload(); })}>{t("adm.wh.delete")}</Btn>
              </div>
            </div>
            {open === ep.id && <div className="mt-3"><Deliveries id={ep.id} /></div>}
          </li>
        ))}
      </ul>
    </Panel>
  );
}
