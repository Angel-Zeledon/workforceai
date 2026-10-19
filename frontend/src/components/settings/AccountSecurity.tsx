"use client";
import { useMemo, useState, type FormEvent } from "react";
import qrcode from "qrcode-generator";
import { useT } from "@/lib/i18n";
import { AuthError, logoutEverywhere, mfaDisable, mfaEnable, mfaSetup, useSession, type MfaSetup } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";

function qrSvg(uri: string): string {
  const qr = qrcode(0, "M");
  qr.addData(uri);
  qr.make();
  return qr.createSvgTag({ cellSize: 4, margin: 2, scalable: true });
}

function Card({ title, children, testid }: { title: string; children: React.ReactNode; testid?: string }) {
  return (
    <section data-testid={testid} className="space-y-3 rounded-xl border border-line bg-panel p-5">
      <h2 className="text-[13.5px] font-semibold text-ink">{title}</h2>
      {children}
    </section>
  );
}

/** Two-step sign-in (authenticator app) and "sign out everywhere", for the person's own account. */
export function AccountSecurity() {
  const { t } = useT();
  const enabled = useSession((s) => s.user?.mfa_enabled === true);
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [disabling, setDisabling] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const svg = useMemo(() => (setup ? qrSvg(setup.otpauth_uri) : ""), [setup]);

  const fail = (x: unknown) => {
    const c = x instanceof AuthError ? x.code : "";
    setErr(t(c === "invalid_mfa_code" ? "auth.err.mfaCode" : c === "invalid_credentials" ? "auth.err.credentials" : c === "account_locked" ? "auth.err.locked" : c === "rate_limited" ? "auth.err.rateLimited" : c === "mfa_required_by_org" ? "idn.mfa.requiredByOrg" : "auth.err.generic"));
  };
  const run = async (fn: () => Promise<void>) => { setBusy(true); setErr(null); try { await fn(); } catch (x) { fail(x); } setBusy(false); };

  const start = () => run(async () => { setSetup(await mfaSetup()); setCode(""); });
  const confirm = (e: FormEvent) => { e.preventDefault(); return run(async () => { setRecovery(await mfaEnable(code)); setSetup(null); setCode(""); }); };
  const turnOff = (e: FormEvent) => { e.preventDefault(); return run(async () => { await mfaDisable(password, code); setDisabling(false); setPassword(""); setCode(""); }); };
  const copy = () => { try { void navigator.clipboard.writeText((recovery ?? []).join("\n")); } catch { /* the codes are on screen */ } };

  return (
    <div data-testid="account-security" className="mx-auto max-w-2xl space-y-4 px-5 py-6 sm:px-6">
      <Card title={t("mfa.title")} testid="mfa-card">
        <p className="text-[12.5px] leading-relaxed text-mute">{t("mfa.body")}</p>

        {recovery ? (
          <div className="space-y-3" data-testid="mfa-recovery">
            <p role="status" className="text-sm font-medium text-ok">{t("mfa.on")}</p>
            <p className="text-[12.5px] leading-relaxed text-ink2">{t("mfa.recoveryBody")}</p>
            <ul className="grid grid-cols-2 gap-1.5 rounded-lg border border-line bg-bg p-3 font-mono text-[13px] text-ink">
              {recovery.map((c) => <li key={c}>{c}</li>)}
            </ul>
            <div className="flex gap-2">
              <Btn onClick={copy}>{t("mfa.copy")}</Btn>
              <Btn kind="primary" onClick={() => setRecovery(null)} data-testid="mfa-done">{t("mfa.saved")}</Btn>
            </div>
          </div>
        ) : setup ? (
          <form onSubmit={confirm} className="space-y-3" data-testid="mfa-setup">
            <p className="text-[12.5px] text-ink2">{t("mfa.scan")}</p>
            <div className="w-44 rounded-lg border border-line bg-white p-1" aria-label={t("mfa.qr")} role="img" dangerouslySetInnerHTML={{ __html: svg }} />
            <p className="text-[12px] text-mute">{t("mfa.manual")} <code className="select-all break-all font-mono text-ink">{setup.secret}</code></p>
            <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("mfa.firstCode")}</span>
              <input data-testid="mfa-code" inputMode="numeric" autoComplete="one-time-code" required className={inputCls} value={code} onChange={(e) => setCode(e.target.value)} />
            </label>
            <ErrorLine msg={err} />
            <div className="flex gap-2">
              <Btn type="submit" kind="primary" disabled={busy || code.trim() === ""} data-testid="mfa-enable">{t("mfa.enable")}</Btn>
              <Btn onClick={() => { setSetup(null); setErr(null); }}>{t("mfa.cancel")}</Btn>
            </div>
          </form>
        ) : enabled ? (
          disabling ? (
            <form onSubmit={turnOff} className="space-y-3" data-testid="mfa-disable-form">
              <p className="text-[12.5px] text-ink2">{t("mfa.offBody")}</p>
              <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("auth.password")}</span>
                <input data-testid="mfa-password" type="password" autoComplete="current-password" required className={inputCls} value={password} onChange={(e) => setPassword(e.target.value)} />
              </label>
              <label className="block text-xs font-medium text-ink2"><span className="mb-1 block">{t("auth.mfa.code")}</span>
                <input data-testid="mfa-off-code" autoComplete="one-time-code" required className={inputCls} value={code} onChange={(e) => setCode(e.target.value)} />
              </label>
              <ErrorLine msg={err} />
              <div className="flex gap-2">
                <Btn type="submit" kind="danger" disabled={busy || !password || !code.trim()} data-testid="mfa-disable">{t("mfa.disable")}</Btn>
                <Btn onClick={() => { setDisabling(false); setErr(null); }}>{t("mfa.cancel")}</Btn>
              </div>
            </form>
          ) : (
            <div className="flex items-center justify-between gap-3">
              <p role="status" className="text-sm font-medium text-ok" data-testid="mfa-status">{t("mfa.on")}</p>
              <Btn kind="danger" onClick={() => setDisabling(true)} data-testid="mfa-off">{t("mfa.disable")}</Btn>
            </div>
          )
        ) : (
          <div className="space-y-2">
            <ErrorLine msg={err} />
            <Btn kind="primary" disabled={busy} onClick={start} data-testid="mfa-start">{t("mfa.start")}</Btn>
          </div>
        )}
      </Card>

      <Card title={t("mfa.sessionsTitle")}>
        <p className="text-[12.5px] leading-relaxed text-mute">{t("mfa.sessionsBody")}</p>
        <Btn kind="danger" onClick={() => { logoutEverywhere().then(() => { window.location.href = "/login"; }); }} data-testid="logout-everywhere">{t("mfa.logoutAll")}</Btn>
      </Card>
    </div>
  );
}
