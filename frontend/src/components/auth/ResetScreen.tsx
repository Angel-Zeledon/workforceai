"use client";
import { useEffect, useState, type FormEvent } from "react";
import { useT } from "@/lib/i18n";
import { AuthError, bootstrapAuth, resetPassword } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { Brand } from "../landing/Brand";

/** Landing page of the reset link (/reset?token=...): sets the new password, then sends the person to sign in. */
export function ResetScreen() {
  const { t } = useT();
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { bootstrapAuth(); setToken(new URLSearchParams(window.location.search).get("token") ?? ""); }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try { await resetPassword(token, password); setDone(true); }
    catch (x) {
      const code = x instanceof AuthError ? x.code : "";
      setErr(t(code === "validation_failed" ? "auth.err.password" : code === "invalid_token" ? "auth.reset.bad" : x instanceof AuthError && x.status === 429 ? "auth.err.rateLimited" : "auth.err.network"));
    }
    setBusy(false);
  };

  return (
    <main className="flex min-h-dvh items-center justify-center bg-bg px-4 py-10">
      <div data-testid="reset" className="ac-pop w-full max-w-md space-y-4 rounded-xl border border-line bg-panel p-5 shadow-soft sm:p-8">
        <a href="/welcome" className="block w-fit" aria-label={t("auth.back")}><Brand /></a>
        {done ? (
          <>
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("auth.reset.doneTitle")}</h1>
            <p role="status" className="text-sm text-ink2">{t("auth.reset.doneBody")}</p>
            <a href="/login" className="block"><Btn kind="primary" className="w-full min-h-10 py-2">{t("auth.login.submit")}</Btn></a>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-3">
            <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("auth.reset.title")}</h1>
            <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("auth.reset.password")}</span>
              <input data-testid="reset-password" type="password" required autoComplete="new-password" className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} />
            </label>
            <p className="text-[11px] text-mute">{t("auth.passwordHint")}</p>
            <ErrorLine msg={err} />
            <Btn type="submit" kind="primary" disabled={busy || !token} className="w-full min-h-10 py-2" data-testid="reset-submit">{t("auth.reset.submit")}</Btn>
            <div className="text-xs"><a className="inline-block py-2 text-accent hover:underline" href="/forgot">{t("auth.reset.again")}</a></div>
          </form>
        )}
      </div>
    </main>
  );
}
