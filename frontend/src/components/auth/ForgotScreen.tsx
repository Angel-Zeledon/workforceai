"use client";
import { useEffect, useState, type FormEvent } from "react";
import { useT } from "@/lib/i18n";
import { AuthError, bootstrapAuth, forgotPassword } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { Brand } from "../landing/Brand";

/** Asks for the address and e-mails a reset link. The answer is the same whether or not the address has an account. */
export function ForgotScreen() {
  const { t, locale } = useT();
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { bootstrapAuth(); }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try { await forgotPassword(email, locale); setSent(true); }
    catch (x) { setErr(t(x instanceof AuthError && x.status === 429 ? "auth.err.rateLimited" : "auth.err.network")); }
    setBusy(false);
  };

  return (
    <main className="flex min-h-screen items-center justify-center bg-bg px-4 py-10">
      <div data-testid="forgot" className="ac-pop w-full max-w-md space-y-4 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
        <a href="/welcome" className="block w-fit" aria-label={t("auth.back")}><Brand /></a>
        {sent ? (
          <>
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("auth.forgot.sentTitle")}</h1>
            <p role="status" className="text-sm leading-relaxed text-ink2">{t("auth.forgot.sentBody", { email })}</p>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-3">
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("auth.forgot.title")}</h1>
            <p className="text-sm text-ink2">{t("auth.forgot.body")}</p>
            <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("auth.email")}</span>
              <input data-testid="forgot-email" type="email" required autoComplete="email" className={inputCls} value={email} onChange={(e) => setEmail(e.target.value)} />
            </label>
            <ErrorLine msg={err} />
            <Btn type="submit" kind="primary" disabled={busy} className="w-full py-2" data-testid="forgot-submit">{t("auth.forgot.submit")}</Btn>
          </form>
        )}
        <div className="pt-1 text-xs"><a className="text-accent hover:underline" href="/login">{t("auth.toLogin")}</a></div>
      </div>
    </main>
  );
}
