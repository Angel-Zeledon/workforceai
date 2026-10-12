"use client";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { acceptInvitation, AuthError, bootstrapAuth, login, loginPath, register, resendVerification, safeReturnPath, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";
import { Brand } from "../landing/Brand";

type Mode = "login" | "register" | "invite";

/** Maps an auth error to a user-facing message key (never shows backend internals). */
function errorKey(e: unknown): string {
  if (!(e instanceof AuthError)) return "auth.err.network";
  switch (e.code) {
    case "invalid_credentials": return "auth.err.credentials";
    case "account_locked": return "auth.err.locked";
    case "email_not_verified": return "auth.err.emailNotVerified";
    case "trial_expired": return "auth.err.trialExpired";
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

const withNext = (page: string, next: string) => (next === "/" ? page : `${page}?next=${encodeURIComponent(next)}`);

/** Login, registration and invitation acceptance. With auth disabled it sends the visitor to the open demo. */
export function AuthScreen({ mode }: { mode: Mode }) {
  const { t, locale } = useT();
  const router = useRouter();
  const status = useSession((s) => s.status);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [org, setOrg] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const flags = useSession((s) => s.flags);
  // After registering with e-mail verification on: nothing signs in until the link is opened.
  const [sent, setSent] = useState<{ email: string; emailSent: boolean } | null>(null);
  const [unverified, setUnverified] = useState(false); // login answered "verify your e-mail first"
  const [cooldown, setCooldown] = useState(0);
  const [resent, setResent] = useState(false);
  // Where to go after signing in (e.g. /approvals/<id> from a notification); only same-origin paths.
  // null until read from the URL, so a signed-in visitor is not sent to "/" first.
  const [next, setNext] = useState<string | null>(null);

  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    if (mode === "invite") setToken(q.get("token") ?? "");
    setNext(safeReturnPath(q.get("next")));
  }, [mode]);
  useEffect(() => {
    if (next !== null && (status === "disabled" || status === "authenticated")) router.replace(next);
  }, [status, router, next]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const id = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(id);
  }, [cooldown]);
  useEffect(() => {
    if (mode === "login" && new URLSearchParams(window.location.search).get("trial") === "expired") setErr(t("auth.err.trialExpired"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  const resend = async (addr: string) => {
    setErr(null); setResent(false);
    try { await resendVerification(addr, locale); setResent(true); setCooldown(60); }
    catch (x) { setErr(t(x instanceof AuthError && x.status === 429 ? "auth.err.rateLimited" : "auth.err.network")); }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null); setUnverified(false);
    try {
      if (mode === "login") await login(email, password);
      else if (mode === "register") {
        const r = await register({ email, password, name, org_name: org, locale });
        if (r.verificationRequired) { setSent({ email: r.email, emailSent: r.emailSent }); setCooldown(60); setBusy(false); return; }
      } else await acceptInvitation({ token, password, ...(name.trim() ? { name } : {}) });
      router.replace(next ?? "/");
    } catch (x) {
      if (x instanceof AuthError && x.code === "email_not_verified") setUnverified(true);
      setErr(t(errorKey(x)));
      setBusy(false);
    }
  };

  if (status === "loading" || status === "disabled") {
    return <div data-testid="auth-loading" className="flex min-h-screen items-center justify-center text-sm text-mute">{t("auth.loading")}</div>;
  }
  const title = t(`auth.${mode}.title`);
  return (
    <main className="grid min-h-screen bg-bg lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
      <aside className="relative hidden flex-col justify-between gap-10 overflow-hidden bg-navy p-12 text-white lg:flex">
        <div aria-hidden className="absolute inset-0 opacity-[0.07]" style={{ backgroundImage: "linear-gradient(rgba(255,255,255,.6) 1px, transparent 1px), linear-gradient(90deg, rgba(255,255,255,.6) 1px, transparent 1px)", backgroundSize: "56px 56px" }} />
        <a href="/welcome" className="relative w-fit" aria-label="AI Workforce OS"><Brand tone="light" /></a>
        <div className="relative max-w-md">
          <h2 className="text-4xl font-semibold leading-[1.1] tracking-[-0.02em]">{t("auth.brand.title")}</h2>
          <p className="mt-5 text-base leading-relaxed text-white/70">{t("auth.brand.sub")}</p>
          <ul className="mt-9 space-y-4">
            {[0, 1, 2].map((i) => (
              <li key={i} className="flex gap-3 text-[15px] leading-snug text-white/90">
                <svg width="18" height="18" viewBox="0 0 20 20" aria-hidden className="mt-0.5 shrink-0 text-[#7fd1c8]"><path d="M4 10.5l4 4 8-9" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
                {t(`auth.brand.${i}`)}
              </li>
            ))}
          </ul>
        </div>
      </aside>
      <div className="flex items-center justify-center px-4 py-10 sm:px-8">
      {sent ? (
        <div data-testid="auth-sent" className="ac-pop w-full max-w-md space-y-3 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
          <a href="/welcome" className="mb-3 block w-fit lg:hidden" aria-label={t("auth.back")}><Brand /></a>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("auth.sent.title")}</h1>
          <p className="text-sm leading-relaxed text-ink2">{t(sent.emailSent ? "auth.sent.body" : "auth.sent.failed", { email: sent.email })}</p>
          <p className="text-xs text-mute">{t("auth.sent.hint")}</p>
          <ErrorLine msg={err} />
          {resent && <p role="status" className="text-xs font-medium text-ok">{t("auth.resent")}</p>}
          <Btn kind="primary" disabled={cooldown > 0} onClick={() => resend(sent.email)} className="w-full py-2" data-testid="auth-resend">
            {cooldown > 0 ? t("auth.resendIn", { n: cooldown }) : t("auth.resend")}
          </Btn>
          <div className="pt-1 text-xs"><a className="text-accent hover:underline" href="/login">{t("auth.toLogin")}</a></div>
        </div>
      ) : (
      <form onSubmit={submit} data-testid={`auth-${mode}`} className="ac-pop w-full max-w-md space-y-3 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
        <a href="/welcome" className="mb-3 block w-fit lg:hidden" aria-label={t("auth.back")}><Brand /></a>
        <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{title}</h1>
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
        {unverified && (
          <div className="flex items-center gap-2 text-xs">
            <button type="button" disabled={cooldown > 0} onClick={() => resend(email)} data-testid="auth-resend" className="font-semibold text-accent hover:underline disabled:text-mute disabled:no-underline">
              {cooldown > 0 ? t("auth.resendIn", { n: cooldown }) : t("auth.resend")}
            </button>
            {resent && <span role="status" className="text-ok">{t("auth.resent")}</span>}
          </div>
        )}
        <Btn type="submit" kind="primary" disabled={busy || (mode === "invite" && !token)} className="w-full py-2" data-testid="auth-submit">
          {t(`auth.${mode}.submit`)}
        </Btn>
        <div className="flex justify-between pt-1 text-xs">
          {mode !== "login" && <a className="text-accent hover:underline" href={withNext("/login", next ?? "/")} data-testid="auth-to-login">{t("auth.toLogin")}</a>}
          {mode !== "register" && <a className="text-accent hover:underline" href={withNext("/register", next ?? "/")} data-testid="auth-to-register">{t("auth.toRegister")}</a>}
        </div>
        {flags.trial && mode !== "invite" && (
          <div className="border-t border-line pt-3 text-center text-xs text-mute">
            <a className="font-medium text-accent hover:underline" href="/prueba" data-testid="auth-to-trial">{t("auth.toTrial")}</a>
          </div>
        )}
      </form>
      )}
      </div>
    </main>
  );
}

/**
 * Renders the app when the session allows it (open demo or signed in); otherwise sends the visitor to
 * /login, keeping the current path (e.g. /approvals/<id>) to come back to after signing in.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const { t } = useT();
  const router = useRouter();
  const status = useSession((s) => s.status);
  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => {
    if (status === "anonymous") router.replace(loginPath(window.location.pathname + window.location.search));
  }, [status, router]);
  if (status === "disabled" || status === "authenticated") return <>{children}</>;
  return <div data-testid="auth-loading" className="flex min-h-screen items-center justify-center text-sm text-mute">{t("auth.loading")}</div>;
}
