"use client";
import { useEffect, useState, type FormEvent } from "react";
import { useT } from "@/lib/i18n";
import { identityApi, ISSUER_EXAMPLES, type IdentityRole, type SsoView } from "@/lib/identityApi";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { CopyField, IdCard, IdCheck, idErrorKey } from "./identityShared";

const ROLES: IdentityRole[] = ["member", "viewer", "admin"];

/** OIDC single sign-on for the organization (Okta, Microsoft Entra ID, Google Workspace or any OpenID Connect provider). */
export function IdentitySso({ onChanged }: { onChanged: () => void }) {
  const { t } = useT();
  const [view, setView] = useState<SsoView | null>(null);
  const [issuer, setIssuer] = useState("");
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [role, setRole] = useState<IdentityRole>("member");
  const [enabled, setEnabled] = useState(true);
  const [jit, setJit] = useState(false);
  const [noVerified, setNoVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const load = (v: SsoView) => {
    setView(v); setIssuer(v.issuer); setClientId(v.client_id); setRole(v.default_role); setEnabled(v.configured ? v.enabled : true);
    setJit(v.jit); setNoVerified(v.allow_unverified_email); setSecret("");
  };
  useEffect(() => { identityApi.sso().then(load).catch((x) => setErr(t(idErrorKey(x)))); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const run = async (fn: () => Promise<void>) => { setBusy(true); setErr(null); setNote(null); try { await fn(); } catch (x) { setErr(t(idErrorKey(x))); } setBusy(false); };
  const save = (e: FormEvent) => { e.preventDefault(); return run(async () => {
    load(await identityApi.saveSso({ enabled, issuer: issuer.trim(), client_id: clientId.trim(), client_secret: secret || undefined, default_role: role, jit, allow_unverified_email: noVerified }));
    setNote(t("idn.saved")); onChanged();
  }); };
  const test = () => run(async () => { const r = await identityApi.testSso(); setNote(t(r.ok ? "idn.sso.testOk" : "idn.sso.testFail")); });
  const remove = () => { if (!window.confirm(t("idn.sso.removeConfirm"))) return; return run(async () => {
    await identityApi.removeSso(); load(await identityApi.sso()); onChanged();
  }); };

  return (
    <IdCard testid="idn-sso" title={t("idn.sso.title")} desc={t("idn.sso.body")}>
      {view && !view.encryption_available && <p role="status" className="rounded-md border border-warn/40 bg-warn/10 px-3 py-1.5 text-xs text-ink2">{t("idn.err.noEncryption")}</p>}
      {view && <CopyField testid="idn-sso-redirect" label={t("idn.sso.redirect")} value={view.redirect_url} />}
      <form onSubmit={save} className="space-y-3">
        <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("idn.sso.issuer")}</span>
          <input data-testid="idn-sso-issuer" required inputMode="url" spellCheck={false} placeholder="https://" className={inputCls} value={issuer} onChange={(e) => setIssuer(e.target.value)} />
          <span className="mt-1 block text-[11.5px] font-normal text-mute">{ISSUER_EXAMPLES.map((x) => `${t(`idn.sso.ex.${x.id}`)}: ${x.issuer}`).join("  ·  ")}</span>
        </label>
        <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("idn.sso.clientId")}</span>
          <input data-testid="idn-sso-client" required spellCheck={false} autoComplete="off" className={inputCls} value={clientId} onChange={(e) => setClientId(e.target.value)} />
        </label>
        <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("idn.sso.secret")}</span>
          <input data-testid="idn-sso-secret" type="password" autoComplete="new-password" className={inputCls} value={secret} onChange={(e) => setSecret(e.target.value)}
            placeholder={view?.has_secret ? t("idn.sso.secretKept") : ""} />
        </label>
        <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("idn.sso.role")}</span>
          <select data-testid="idn-sso-role" className={inputCls} value={role} onChange={(e) => setRole(e.target.value as IdentityRole)}>
            {ROLES.map((r) => <option key={r} value={r}>{t(`members.roles.${r}`)}</option>)}
          </select>
        </label>
        <IdCheck testid="idn-sso-enabled" label={t("idn.sso.enabled")} checked={enabled} onChange={setEnabled} />
        <IdCheck testid="idn-sso-jit" label={t("idn.sso.jit")} hint={t("idn.sso.jitHint")} checked={jit} onChange={setJit} />
        <IdCheck testid="idn-sso-unverified" label={t("idn.sso.unverified")} hint={t("idn.sso.unverifiedHint")} checked={noVerified} onChange={setNoVerified} />
        <ErrorLine msg={err} />
        {note && <p role="status" className="text-xs font-medium text-ok">{note}</p>}
        <div className="flex flex-wrap gap-2">
          <Btn type="submit" kind="primary" disabled={busy || !issuer.trim() || !clientId.trim()} data-testid="idn-sso-save">{t("idn.save")}</Btn>
          {view?.configured && <Btn onClick={test} disabled={busy} data-testid="idn-sso-test">{t("idn.sso.test")}</Btn>}
          {view?.configured && <Btn kind="danger" onClick={remove} disabled={busy} data-testid="idn-sso-remove">{t("idn.sso.remove")}</Btn>}
        </div>
      </form>
    </IdCard>
  );
}
