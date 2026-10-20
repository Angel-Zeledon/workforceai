"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { Btn } from "../ui";
import { download, gov, type PurgeResult } from "./governanceApi";
import { Field, inputCls, Notice, Panel, useErrText, useLoad, when } from "./AdminParts";

/** Data retention policy (owner), preview and manual run, plus the full organization export. */
export function AdminRetention() {
  const { t, locale } = useT();
  const errText = useErrText();
  const role = useSession((s) => s.role);
  const signedIn = useSession((s) => s.status === "authenticated");
  const owner = !signedIn || role === "owner";
  const { data, err, reload } = useLoad(() => gov.retention());
  const [enabled, setEnabled] = useState(false);
  const [chats, setChats] = useState("0");
  const [files, setFiles] = useState("0");
  const [aud, setAud] = useState("0");
  const [res, setRes] = useState<{ title: string; r: PurgeResult } | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  useEffect(() => {
    const p = data?.policy;
    if (!p) return;
    setEnabled(p.enabled); setChats(String(p.chats_days)); setFiles(String(p.files_days)); setAud(String(p.audit_days));
  }, [data]);
  const guard = async (fn: () => Promise<void>) => { setMsg(null); try { await fn(); } catch (e) { setMsg({ kind: "err", text: errText(e) }); } };
  const save = () => guard(async () => {
    await gov.putRetention({ enabled, chats_days: Number(chats) || 0, files_days: Number(files) || 0, audit_days: Number(aud) || 0 });
    setMsg({ kind: "ok", text: t("adm.saved") }); reload();
  });
  const preview = () => guard(async () => setRes({ title: t("adm.ret.previewTitle"), r: await gov.previewRetention() }));
  const run = () => guard(async () => {
    if (!window.confirm(t("adm.ret.runConfirm"))) return;
    setRes({ title: t("adm.ret.ranTitle"), r: await gov.runRetention() }); reload();
  });
  const exportAll = () => guard(async () => { await download("/org/export", "export.zip"); });
  const min = data?.min_days;
  const last = data?.policy.last_run_at;
  return (
    <div className="space-y-5" data-testid="adm-retention">
      <Panel title={t("adm.ret.title")} body={t("adm.ret.body")}>
        {err && <Notice kind="err">{err}</Notice>}
        {!owner && <Notice kind="warn">{t("adm.ret.ownerOnly")}</Notice>}
        <label className="flex items-center gap-2 text-[13px] text-ink">
          <input type="checkbox" disabled={!owner} checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          {t("adm.ret.enabled")}
        </label>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label={t("adm.ret.chats")} hint={t("adm.ret.min", { n: min?.chats ?? 30 })}><input className={inputCls} disabled={!owner} type="number" min={0} value={chats} onChange={(e) => setChats(e.target.value)} /></Field>
          <Field label={t("adm.ret.files")} hint={t("adm.ret.filesNote")}><input className={inputCls} disabled={!owner} type="number" min={0} value={files} onChange={(e) => setFiles(e.target.value)} /></Field>
          <Field label={t("adm.ret.audit")} hint={t("adm.ret.auditNote")}><input className={inputCls} disabled={!owner} type="number" min={0} value={aud} onChange={(e) => setAud(e.target.value)} /></Field>
        </div>
        <div className="flex flex-wrap gap-2">
          <Btn kind="primary" disabled={!owner} onClick={save}>{t("adm.ret.save")}</Btn>
          <Btn onClick={preview}>{t("adm.ret.preview")}</Btn>
          <Btn kind="danger" disabled={!owner || !data?.policy.enabled} onClick={run}>{t("adm.ret.run")}</Btn>
        </div>
        <p className="text-[12px] text-mute">{last ? t("adm.ret.last", { when: when(last, locale) }) : t("adm.ret.never")}</p>
        {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
        {res && (
          <div data-testid="adm-ret-result" className="rounded-lg border border-line bg-panel2/50 p-3">
            <b className="text-[12.5px] text-ink">{res.title}</b>
            <p className="mt-1 text-[12.5px]">{t("adm.ret.result", { messages: res.r.messages, conversations: res.r.conversations, files: res.r.files_trashed })}</p>
            {res.r.audit_entries !== undefined && <p data-testid="adm-ret-audit" className="mt-1 text-[12.5px]">{t("adm.ret.auditResult", { n: res.r.audit_entries })}</p>}
            {res.r.audit_skipped && <p className="mt-1 text-[12px] text-warn">{t("adm.ret.auditSkipped")}</p>}
            {res.r.capped && <p className="mt-1 text-[12px] text-warn">{t("adm.ret.capped")}</p>}
          </div>
        )}
      </Panel>
      <Panel title={t("adm.ret.exportTitle")} body={t("adm.ret.exportBody")}>
        <div><Btn onClick={exportAll}>{t("adm.ret.exportBtn")}</Btn></div>
      </Panel>
    </div>
  );
}
