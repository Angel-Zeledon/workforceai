"use client";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { AuthError, bootstrapAuth, createTrial, login, useSession, type TrialAccount } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine } from "../connections/shared";
import { Brand } from "../landing/Brand";
import { Icon } from "../icons";

/**
 * "Try it without signing up". Arriving here mints a throwaway account on the server and shows its user and
 * password, which the visitor can copy to come back within the trial, or use straight away.
 *
 * The credentials are kept in sessionStorage so a reload shows the same account instead of minting another one
 * (the backend allows only a few per connection a day, and a refresh must not spend them). The password lives
 * nowhere else: the server stores only its hash, so this screen is the only copy.
 */
const STORE_KEY = "wf.trial.account";

function readStored(): TrialAccount | null {
  try {
    const raw = sessionStorage.getItem(STORE_KEY);
    if (!raw) return null;
    const a = JSON.parse(raw) as TrialAccount;
    return Date.parse(a.expires_at) > Date.now() ? a : null;
  } catch { return null; }
}
function store(a: TrialAccount) { try { sessionStorage.setItem(STORE_KEY, JSON.stringify(a)); } catch { /* private mode */ } }

function CopyField({ id, label, value }: { id: string; label: string; value: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1600); } catch { /* the value is on screen and selectable */ }
  };
  return (
    <div>
      <label htmlFor={id} className="mb-1 block text-xs font-medium text-ink2">{label}</label>
      <div className="relative">
        <input id={id} readOnly value={value} data-testid={id} onFocus={(e) => e.currentTarget.select()}
          className="w-full rounded-md border border-line-strong bg-panel2 py-2 pl-3 pr-10 font-mono text-[14px] tracking-wide text-ink outline-none focus:border-accent" />
        <button type="button" onClick={copy} aria-label={copied ? t("trial.copied") : t("trial.copy")} title={copied ? t("trial.copied") : t("trial.copy")}
          className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1.5 text-mute hover:bg-panel hover:text-ink"><Icon name={copied ? "check" : "doc"} size={15} /></button>
      </div>
    </div>
  );
}

function leftText(ms: number, t: (k: string, v?: Record<string, string | number>) => string) {
  const h = Math.max(0, Math.floor(ms / 3_600_000));
  const m = Math.max(0, Math.floor((ms % 3_600_000) / 60_000));
  return h > 0 ? t("trial.left.hm", { h, m }) : t("trial.left.m", { m });
}

export function TrialScreen() {
  const { t, locale } = useT();
  const router = useRouter();
  const flags = useSession((s) => s.flags);
  const status = useSession((s) => s.status);
  const [acct, setAcct] = useState<TrialAccount | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [entering, setEntering] = useState(false);
  const started = useRef(false); // effects run twice in development: never mint two accounts

  useEffect(() => { bootstrapAuth(); }, []);
  useEffect(() => {
    if (status === "loading" || started.current) return;
    started.current = true;
    if (!flags.trial) { setErr(t("trial.off")); return; }
    const stored = readStored();
    if (stored) { setAcct(stored); return; }
    createTrial(locale)
      .then((a) => { store(a); setAcct(a); })
      .catch((e) => setErr(e instanceof AuthError && e.status === 429 ? t("trial.limit") : t("trial.failed")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status, flags.trial]);

  const enter = async () => {
    if (!acct) return;
    setEntering(true); setErr(null);
    try { await login(acct.email, acct.password); router.replace("/"); }
    catch { setErr(t("trial.enterFailed")); setEntering(false); }
  };
  const copyBoth = async () => {
    if (!acct) return;
    try { await navigator.clipboard.writeText(`${t("trial.user")}: ${acct.email}\n${t("auth.password")}: ${acct.password}`); } catch { /* selectable fields above */ }
  };

  const left = acct ? Date.parse(acct.expires_at) - Date.now() : 0;
  return (
    <main className="flex min-h-screen items-center justify-center bg-bg px-4 py-10">
      <div data-testid="trial" className="ac-pop w-full max-w-md space-y-4 rounded-xl border border-line bg-panel p-7 shadow-soft sm:p-8">
        <a href="/welcome" className="block w-fit" aria-label={t("auth.back")}><Brand /></a>
        <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">{t("trial.title")}</h1>
        {!acct && !err && <p role="status" className="text-sm text-mute">{t("trial.creating")}</p>}
        {acct && (
          <>
            <p className="text-[13px] leading-relaxed text-ink2">{t("trial.body", { time: leftText(left, t) })}</p>
            <CopyField id="trial-user" label={t("trial.user")} value={acct.email} />
            <CopyField id="trial-password" label={t("auth.password")} value={acct.password} />
            <button type="button" onClick={copyBoth} className="text-xs font-medium text-accent hover:underline">{t("trial.copyBoth")}</button>
            <div role="note" className="flex gap-2 rounded-lg border border-warn/30 bg-warn/10 px-3 py-2.5 text-[12.5px] leading-snug text-warn">
              <Icon name="alert" size={16} className="mt-0.5 shrink-0" />
              <span>{t("trial.warn")}</span>
            </div>
            <label className="flex items-start gap-2 text-[13px] text-ink2">
              <input data-testid="trial-saved" type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} className="mt-0.5" />
              {t("trial.saved")}
            </label>
          </>
        )}
        <ErrorLine msg={err} />
        {acct && <Btn kind="primary" disabled={!saved || entering} onClick={enter} className="w-full py-2" data-testid="trial-enter">{t("trial.enter")}</Btn>}
        <div className="flex justify-between pt-1 text-xs">
          <a className="text-accent hover:underline" href="/register">{t("trial.toRegister")}</a>
          <a className="text-accent hover:underline" href="/login">{t("auth.toLogin")}</a>
        </div>
      </div>
    </main>
  );
}
