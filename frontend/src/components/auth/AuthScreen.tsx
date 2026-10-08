"use client";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { acceptInvitation, AuthError, bootstrapAuth, login, register, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";

type Mode = "login" | "register" | "invite";

/** Maps an auth error to a user-facing message key (never shows backend internals). */
function errorKey(e: unknown): string {
  if (!(e instanceof AuthError)) return "auth.err.network";
  switch (e.code) {
    case "invalid_credentials": return "auth.err.credentials";
    case "account_locked": return "auth.err.locked";
    case "rate_limited": return "auth.err.rateLimited";
    case "email_taken": return "auth.err.emailTaken";
    case "already_member": return "auth.err.alreadyMember";
    case "invalid_token": return "auth.err.invite";
    case "validation_failed": return e.fields?.password ? "auth.err.password" : "auth.err.validation";
  }
  return "auth.err.generic";
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block text-xs font-medium text-ink2">
      <span className="mb-1 block">{label}</span>
      {children}
    </label>
  );
}

/** Login, registration and invitation acceptance. With auth disabled it sends the visitor to the open demo. */
export function AuthScreen({ mode }: { mode: Mode }) {
  const { t } = useT();
  const router = useRouter();
  const status = useSession((s) => s.status);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [org, setOrg] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => {
    if (mode === "invite") setToken(new URLSearchParams(window.location.search).get("token") ?? "");
  }, [mode]);
  useEffect(() => {
    if (status === "disabled" || status === "authenticated") router.replace("/");
  }, [status, router]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try {
      if (mode === "login") await login(email, password);
      else if (mode === "register") await register({ email, password, name, org_name: org });
      else await acceptInvitation({ token, password, ...(name.trim() ? { name } : {}) });
      router.replace("/");
    } catch (x) {
      setErr(t(errorKey(x)));
      setBusy(false);
    }
  };

  if (status === "loading" || status === "disabled") {
    return <div data-testid="auth-loading" className="flex min-h-screen items-center justify-center text-sm text-mute">{t("auth.loading")}</div>;
  }
  const title = t(`auth.${mode}.title`);
  return (
    <main className="flex min-h-screen items-center justify-center bg-bg px-4 py-10">
      <form onSubmit={submit} data-testid={`auth-${mode}`} className="ac-pop w-full max-w-sm space-y-3 rounded-2xl border border-line bg-panel p-6 shadow-float">
        <div className="mb-2 flex items-center gap-2">
          <span className="h-6 w-6 rounded-md bg-accent" aria-hidden />
          <span className="text-sm font-semibold text-ink">AI Workforce OS</span>
        </div>
        <h1 className="font-display text-xl font-semibold text-ink">{title}</h1>
        {mode === "invite" && <p className="text-xs text-mute">{t("auth.invite.body")}</p>}
        {mode !== "invite" && (
          <Field label={t("auth.email")}>
            <input data-testid="auth-email" type="email" autoComplete="email" required className={inputCls} value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
        )}
        {mode !== "login" && (
          <Field label={mode === "invite" ? t("auth.nameIfNew") : t("auth.name")}>
            <input data-testid="auth-name" autoComplete="name" required={mode === "register"} className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
        )}
        {mode === "register" && (
          <Field label={t("auth.org")}>
            <input data-testid="auth-org" autoComplete="organization" required className={inputCls} value={org} onChange={(e) => setOrg(e.target.value)} />
          </Field>
        )}
        <Field label={t("auth.password")}>
          <input data-testid="auth-password" type="password" required autoComplete={mode === "login" ? "current-password" : "new-password"}
            className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {mode !== "login" && <p className="text-[11px] text-mute">{t("auth.passwordHint")}</p>}
        <ErrorLine msg={err} />
        <Btn type="submit" kind="primary" disabled={busy || (mode === "invite" && !token)} className="w-full py-2" data-testid="auth-submit">
          {t(`auth.${mode}.submit`)}
        </Btn>
        <div className="flex justify-between pt-1 text-xs">
          {mode !== "login" && <a className="text-accent hover:underline" href="/login" data-testid="auth-to-login">{t("auth.toLogin")}</a>}
          {mode !== "register" && <a className="text-accent hover:underline" href="/register" data-testid="auth-to-register">{t("auth.toRegister")}</a>}
        </div>
      </form>
    </main>
  );
}

/** Renders the app when the session allows it (open demo or signed in); otherwise sends the visitor to /login. */
export function AuthGate({ children }: { children: ReactNode }) {
  const { t } = useT();
  const router = useRouter();
  const status = useSession((s) => s.status);
  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => { if (status === "anonymous") router.replace("/login"); }, [status, router]);
  if (status === "disabled" || status === "authenticated") return <>{children}</>;
  return <div data-testid="auth-loading" className="flex min-h-screen items-center justify-center text-sm text-mute">{t("auth.loading")}</div>;
}
