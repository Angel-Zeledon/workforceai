"use client";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import qrcode from "qrcode-generator";
import { useT } from "@/lib/i18n";
import { AuthError, mfaEnrollEnable, mfaEnrollSetup, type MfaSetup } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";

function qrSvg(uri: string): string {
  const qr = qrcode(0, "M");
  qr.addData(uri);
  qr.make();
  return qr.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
}

/**
 * Forced enrolment at sign-in: the organization requires two-step verification and this account has none. The password was
 * right but no session exists yet; `token` (short-lived, single purpose) lets the person set up an authenticator app and
 * only then opens the session. The recovery codes are shown once, before the person continues.
 */
export function MfaEnroll({ token, onDone, onCancel }: { token: string; onDone: () => void; onCancel: () => void }) {
  const { t } = useT();
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const svg = useMemo(() => (setup ? qrSvg(setup.otpauth_uri) : ""), [setup]);

  useEffect(() => {
    let live = true;
    mfaEnrollSetup(token).then((s) => { if (live) setSetup(s); }).catch((x) => {
      if (!live) return;
      const gone = x instanceof AuthError && (x.code === "invalid_token" || x.code === "token_expired");
      setErr(t(gone ? "auth.err.mfaExpired" : "auth.err.generic"));
    });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const confirm = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try { setRecovery(await mfaEnrollEnable(token, code)); }
    catch (x) { setErr(t(x instanceof AuthError && x.code === "invalid_mfa_code" ? "auth.err.mfaCode" : x instanceof AuthError && x.code === "rate_limited" ? "auth.err.rateLimited" : "auth.err.generic")); }
    setBusy(false);
  };
  const copy = () => { try { void navigator.clipboard.writeText((recovery ?? []).join("\n")); } catch { /* the codes are on screen */ } };

  if (recovery) {
    return (
      <div data-testid="auth-enroll-done" className="ac-pop w-full max-w-md space-y-3 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
        <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("mfa.on")}</h1>
        <p className="text-sm leading-relaxed text-ink2">{t("mfa.recoveryBody")}</p>
        <ul className="grid grid-cols-2 gap-1.5 rounded-lg border border-line bg-bg p-3 font-mono text-[13px] text-ink">
          {recovery.map((c) => <li key={c}>{c}</li>)}
        </ul>
        <div className="flex gap-2">
          <Btn onClick={copy}>{t("mfa.copy")}</Btn>
          <Btn kind="primary" onClick={onDone} data-testid="auth-enroll-continue">{t("mfa.saved")}</Btn>
        </div>
      </div>
    );
  }
  return (
    <form onSubmit={confirm} data-testid="auth-enroll" className="ac-pop w-full max-w-md space-y-3 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
      <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("idn.enroll.title")}</h1>
      <p className="text-sm leading-relaxed text-ink2">{t("idn.enroll.body")}</p>
      {setup && (
        <>
          <p className="text-[12.5px] text-ink2">{t("mfa.scan")}</p>
          <div className="w-44 rounded-lg border border-line bg-white p-1" aria-label={t("mfa.qr")} role="img" dangerouslySetInnerHTML={{ __html: svg }} />
          <p className="text-[12px] text-mute">{t("mfa.manual")} <code className="select-all break-all font-mono text-ink">{setup.secret}</code></p>
          <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("mfa.firstCode")}</span>
            <input data-testid="auth-enroll-code" autoFocus inputMode="numeric" autoComplete="one-time-code" required className={inputCls} value={code} onChange={(e) => setCode(e.target.value)} />
          </label>
        </>
      )}
      <ErrorLine msg={err} />
      <div className="flex gap-2">
        <Btn type="submit" kind="primary" disabled={busy || !setup || code.trim() === ""} data-testid="auth-enroll-submit">{t("mfa.enable")}</Btn>
        <Btn onClick={onCancel}>{t("mfa.cancel")}</Btn>
      </div>
    </form>
  );
}
