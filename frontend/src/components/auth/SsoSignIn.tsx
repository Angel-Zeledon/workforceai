"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { ssoAvailable, ssoStart } from "@/lib/identityApi";
import { Btn } from "../ui";

/** True when the typed address belongs to a domain with single sign-on (asked after a short pause; yes/no only). */
export function useSsoAvailable(email: string): boolean {
  const [on, setOn] = useState(false);
  useEffect(() => {
    let live = true;
    const id = setTimeout(() => { ssoAvailable(email.trim()).then((v) => { if (live) setOn(v); }); }, 350);
    return () => { live = false; clearTimeout(id); };
  }, [email]);
  return on;
}

/** Maps the short code the SSO callback leaves in the URL (?sso_error=) to a message key. */
export function ssoErrorKey(code: string): string {
  switch (code) {
    case "state": return "idn.signin.err.state";
    case "unavailable": return "idn.signin.err.unavailable";
    case "email_unverified": return "idn.signin.err.emailUnverified";
    case "domain": return "idn.signin.err.domain";
    case "token": return "idn.signin.err.token";
    default: return "idn.signin.err.provider";
  }
}

/** "Continue with single sign-on": shown under the password form once the address is on an SSO domain. */
export function SsoSignIn({ email, onError }: { email: string; onError: (msg: string) => void }) {
  const { t } = useT();
  const [busy, setBusy] = useState(false);
  const go = async () => {
    setBusy(true);
    try { window.location.assign(await ssoStart(email.trim())); }
    catch { onError(t("idn.signin.err.provider")); setBusy(false); }
  };
  return (
    <div className="space-y-1.5 border-t border-line pt-3" data-testid="auth-sso">
      <Btn kind="ghost" disabled={busy} onClick={go} className="w-full py-2" data-testid="auth-sso-go">{t("idn.signin.sso")}</Btn>
      <p className="text-center text-[11px] text-mute">{t("idn.signin.hint")}</p>
    </div>
  );
}
