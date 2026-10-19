"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { identityApi, type IdentityPolicy } from "@/lib/identityApi";
import { ErrorLine } from "../connections/shared";
import { IdCard, IdCheck, idErrorKey } from "./identityShared";
import { IdentitySso } from "./IdentitySso";
import { IdentityDomains } from "./IdentityDomains";
import { IdentityScim } from "./IdentityScim";

/** What the organization demands at sign-in. Turning on SSO enforcement is refused by the server until SSO works. */
function PolicyCard({ version }: { version: number }) {
  const { t } = useT();
  const [p, setP] = useState<IdentityPolicy | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { identityApi.policy().then(setP).catch((x) => setErr(t(idErrorKey(x)))); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [version]);
  const set = async (next: IdentityPolicy) => {
    setBusy(true); setErr(null);
    try { setP(await identityApi.savePolicy(next)); } catch (x) { setErr(t(idErrorKey(x))); }
    setBusy(false);
  };
  return (
    <IdCard testid="idn-policy" title={t("idn.policy.title")} desc={t("idn.policy.body")}>
      {p && (
        <div className="space-y-3">
          <IdCheck testid="idn-policy-mfa" disabled={busy} label={t("idn.policy.mfa")} hint={t("idn.policy.mfaHint")} checked={p.require_mfa} onChange={(v) => set({ ...p, require_mfa: v })} />
          <IdCheck testid="idn-policy-sso" disabled={busy} label={t("idn.policy.sso")} hint={t("idn.policy.ssoHint")} checked={p.enforce_sso} onChange={(v) => set({ ...p, enforce_sso: v })} />
        </div>
      )}
      <ErrorLine msg={err} />
    </IdCard>
  );
}

/** Settings -> Single sign-on and identity: policy, OIDC SSO, verified domains and SCIM (owners and admins). */
export function IdentitySettings() {
  const { t } = useT();
  const [version, setVersion] = useState(0);
  const bump = useCallback(() => setVersion((v) => v + 1), []);
  return (
    <div data-testid="identity-settings" className="mx-auto max-w-2xl space-y-4 px-5 py-6 sm:px-6">
      <p className="text-[12.5px] leading-relaxed text-mute">{t("idn.intro")}</p>
      <PolicyCard version={version} />
      <IdentityDomains onChanged={bump} />
      <IdentitySso onChanged={bump} />
      <IdentityScim />
    </div>
  );
}
