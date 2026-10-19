"use client";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useT } from "@/lib/i18n";
import { identityApi, type DomainView } from "@/lib/identityApi";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { CopyField, IdCard, idErrorKey } from "./identityShared";

/** Verified domains: proof, by a DNS TXT record, that the organization owns an e-mail domain. SSO and auto-join work only for them. */
export function IdentityDomains({ onChanged }: { onChanged: () => void }) {
  const { t } = useT();
  const [list, setList] = useState<DomainView[]>([]);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [errFor, setErrFor] = useState<string | null>(null);

  const load = useCallback(() => identityApi.domains().then(setList).catch((x) => setErr(idErrorKey(x))), []);
  useEffect(() => { void load(); }, [load]);

  const act = async (key: string, fn: () => Promise<void>) => {
    setBusy(key); setErr(null); setErrFor(null);
    try { await fn(); await load(); onChanged(); } catch (x) { setErr(idErrorKey(x)); setErrFor(key); }
    setBusy(null);
  };
  const add = (e: FormEvent) => { e.preventDefault(); return act("add", async () => { await identityApi.addDomain(name); setName(""); }); };

  return (
    <IdCard testid="idn-domains" title={t("idn.dom.title")} desc={t("idn.dom.body")}>
      <form onSubmit={add} className="flex gap-2">
        <input data-testid="idn-dom-input" aria-label={t("idn.dom.input")} placeholder="example.com" spellCheck={false} className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
        <Btn type="submit" kind="primary" disabled={busy !== null || !name.trim()} data-testid="idn-dom-add">{t("idn.dom.add")}</Btn>
      </form>
      {errFor === "add" && <ErrorLine msg={err ? t(err) : null} />}
      <ul className="space-y-3">
        {list.map((d) => (
          <li key={d.domain} data-testid={`idn-dom-${d.domain}`} className="space-y-2 rounded-lg border border-line bg-bg p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <b className="break-all text-[13px] text-ink">{d.domain}</b>
              <span className={`rounded-full px-2 py-0.5 text-[11px] font-semibold ${d.verified ? "bg-ok/10 text-ok" : "bg-warn/10 text-warn"}`}>
                {t(d.verified ? "idn.dom.verified" : "idn.dom.pending")}
              </span>
            </div>
            {!d.verified && (
              <>
                <p className="text-[12.5px] leading-relaxed text-ink2">{t("idn.dom.howto")}</p>
                <CopyField label={t("idn.dom.recordName")} value={d.challenge.name} />
                <CopyField label={t("idn.dom.recordValue")} value={d.challenge.value} />
                <p className="text-[11.5px] text-mute">{t("idn.dom.ttl")}</p>
              </>
            )}
            {errFor === d.domain && <ErrorLine msg={err ? t(err) : null} />}
            <div className="flex gap-2">
              {!d.verified && <Btn kind="primary" disabled={busy !== null} onClick={() => act(d.domain, async () => { await identityApi.verifyDomain(d.domain); })} data-testid={`idn-dom-verify-${d.domain}`}>{t("idn.dom.check")}</Btn>}
              <Btn kind="danger" disabled={busy !== null} onClick={() => { if (window.confirm(t("idn.dom.removeConfirm", { domain: d.domain }))) void act(d.domain, async () => { await identityApi.removeDomain(d.domain); }); }}>{t("idn.dom.remove")}</Btn>
            </div>
          </li>
        ))}
        {list.length === 0 && <li className="text-[12.5px] text-mute">{t("idn.dom.empty")}</li>}
      </ul>
    </IdCard>
  );
}
