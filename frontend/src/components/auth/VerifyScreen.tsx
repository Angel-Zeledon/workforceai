"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { AuthError, bootstrapAuth, resendVerification, verifyEmail, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { Brand } from "../landing/Brand";

/** Landing page of the verification link (/verify?token=...): opens the session when the link is good, or offers a new one. */
export function VerifyScreen() {
  const { t, locale } = useT();
  const router = useRouter();
  const status = useSession((s) => s.status);
  const [phase, setPhase] = useState<"checking" | "bad" | "resent">("checking");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const started = useRef(false); // the link works once: never spend it twice (development runs effects twice)

  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => {
    if (status === "loading" || started.current) return;
    started.current = true;
    const token = new URLSearchParams(window.location.search).get("token") ?? "";
    verifyEmail(token)
      .then((r) => router.replace(r.mfaRequired ? "/login" : "/")) // with a second factor the link only proves the address
      .catch(() => setPhase("bad"));
  }, [status, router]);

  const resend = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try { await resendVerification(email, locale); setPhase("resent"); }
    catch (x) { setErr(t(x instanceof AuthError && x.status === 429 ? "auth.err.rateLimited" : "auth.err.network")); }
    setBusy(false);
  };

  return (
    <main className="flex min-h-screen items-center justify-center bg-bg px-4 py-10">
      <div data-testid="verify" className="ac-pop w-full max-w-md space-y-4 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
        <a href="/welcome" className="block w-fit" aria-label={t("auth.back")}><Brand /></a>
        {phase === "checking" && (<><h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("verify.checking")}</h1><p role="status" className="text-sm text-mute">{t("verify.checkingBody")}</p></>)}
        {phase === "resent" && (<><h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("verify.resentTitle")}</h1><p className="text-sm text-ink2">{t("verify.resentBody")}</p></>)}
        {phase === "bad" && (
          <form onSubmit={resend} className="space-y-3">
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("verify.badTitle")}</h1>
            <p className="text-sm text-ink2">{t("verify.badBody")}</p>
            <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("auth.email")}</span>
              <input data-testid="verify-email" type="email" required autoComplete="email" className={inputCls} value={email} onChange={(e) => setEmail(e.target.value)} />
            </label>
            <ErrorLine msg={err} />
            <Btn type="submit" kind="primary" disabled={busy} className="w-full py-2" data-testid="verify-resend">{t("auth.resend")}</Btn>
          </form>
        )}
        <div className="pt-1 text-xs"><a className="text-accent hover:underline" href="/login">{t("auth.toLogin")}</a></div>
      </div>
    </main>
  );
}
