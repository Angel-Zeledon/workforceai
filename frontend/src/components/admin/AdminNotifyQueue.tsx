"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { notifyApi } from "@/lib/notifyApi";
import { Btn } from "../ui";
import { Notice, Panel, tdCls, thCls, useErrText, useLoad, when } from "./AdminParts";

/** Notifications that ran out of attempts (dead letters of the outbox): see why they failed and send them again. */
export function AdminNotifyQueue() {
  const { t, locale } = useT();
  const errText = useErrText();
  const { data, err, reload } = useLoad(() => notifyApi.deadLetters());
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const retry = async (id: string) => {
    setBusy(id); setMsg(null);
    try { await notifyApi.retryDead(id); setMsg({ kind: "ok", text: t("adm.dead.retried") }); reload(); }
    catch (e) { setMsg({ kind: "err", text: errText(e) }); reload(); }
    setBusy(null);
  };
  const items = data?.items ?? [];
  return (
    <Panel testid="adm-dead" title={t("adm.dead.title")} body={t("adm.dead.body")}>
      {err && <Notice kind="err">{err}</Notice>}
      {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      {data && !data.enabled && <p className="text-mute">{t("adm.dead.off")}</p>}
      {data && data.enabled && items.length === 0 && <p className="text-mute">{t("adm.dead.none")}</p>}
      {items.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[620px] border-collapse">
            <thead className="bg-panel2"><tr>
              <th className={thCls}>{t("adm.dead.col.time")}</th><th className={thCls}>{t("adm.dead.col.channel")}</th>
              <th className={thCls}>{t("adm.dead.col.to")}</th><th className={thCls}>{t("adm.dead.col.error")}</th><th className={thCls} />
            </tr></thead>
            <tbody>{items.map((d) => (
              <tr key={d.id} className="border-t border-line" data-testid="adm-dead-row">
                <td className={`${tdCls} whitespace-nowrap`}>{when(d.created_at, locale)}</td>
                <td className={tdCls}>{d.channel}</td>
                <td className={tdCls}>{d.recipient ?? ""}</td>
                <td className={tdCls}>{d.last_error} <span className="text-mute">({t("adm.dead.attempts", { n: d.attempts })})</span></td>
                <td className={tdCls}><Btn disabled={busy !== null} onClick={() => retry(d.id)}>{t("adm.dead.retry")}</Btn></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}
