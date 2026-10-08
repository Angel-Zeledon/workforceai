"use client";
import { useRef, useState } from "react";
import { MOCK } from "@/lib/config";
import { useT } from "@/lib/i18n";
import { connApi, type CreateConnectionInput, type TestResult } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { CapabilityDef, Connection, ProviderDef } from "@/lib/connections/types";
import { useStore } from "@/lib/store";
import { Btn } from "../ui";
import { ErrorLine, errText, Field, inputCls, Modal } from "./shared";

const STEPS = 5;

/**
 * Connect wizard (spec 11.2). Secrets: the inputs are UNCONTROLLED and are read from the DOM only at submit time,
 * cleared right away, and never stored in React state, Zustand, localStorage or logs.
 */
export function ConnectWizard({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const providers = useConnections((s) => s.providers);
  const agentOrder = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const [step, setStep] = useState(1);
  const [prov, setProv] = useState<ProviderDef | null>(null);
  const [profile, setProfile] = useState<"read" | "write">("read");
  const [caps, setCaps] = useState<string[]>([]);
  const [label, setLabel] = useState("");
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [denyScope, setDenyScope] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [conn, setConn] = useState<Connection | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);
  const [assign, setAssign] = useState<string[]>([]);
  const idRef = useRef<HTMLInputElement>(null);
  const secretRef = useRef<HTMLInputElement>(null);

  const visibleCaps = (p: ProviderDef): CapabilityDef[] => p.capabilities.filter((c) => !p.split_read_write || c.profile === profile);
  const chooseProvider = (p: ProviderDef) => {
    setProv(p); setErr(null);
    const prof = "read" as const; setProfile(prof);
    setCaps(p.capabilities.filter((c) => c.default && (!p.split_read_write || c.profile === prof)).map((c) => c.id));
    setFilters(Object.fromEntries(p.resource_filters.filter((f) => f.default !== undefined).map((f) => [f.key, String(f.default)])));
    setLabel(t(`conn.provider.${p.id}`));
    setStep(2);
  };
  const chooseProfile = (p: "read" | "write") => {
    if (!prov) return;
    setProfile(p);
    // Write profile: Gmail starts with drafts only (sending is opt-in); other providers with their write capabilities.
    const writes = prov.capabilities.filter((c) => c.profile === "write");
    const safest = writes.filter((c) => c.id === "mail.draft");
    setCaps(p === "read" ? prov.capabilities.filter((c) => c.profile === "read").map((c) => c.id) : (safest.length ? safest : writes).map((c) => c.id));
  };
  const toggleCap = (id: string) => setCaps((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]));
  const hasWrite = !!prov && caps.some((id) => prov.capabilities.find((c) => c.id === id)?.side_effects);
  const wantsOAuth = !!prov && prov.auth.includes("oauth2_byo_app");

  const resourceScope = () => {
    const out: Record<string, string[] | number> = {};
    for (const f of prov?.resource_filters ?? []) {
      const v = (filters[f.key] ?? "").trim(); if (!v) continue;
      out[f.key] = f.type === "number" ? Number(v) || 0 : v.split(",").map((x) => x.trim()).filter(Boolean);
    }
    return out;
  };

  const finish = async (c: Connection) => {
    setConn(c);
    try { setTest(await connApi.test(c.id)); } catch (e) { setTest({ status: "failed", latency_ms: 0, account_label: null, granted_capabilities: [], error_code: "request_failed" }); setErr(errText(e)); }
    setStep(5);
  };

  const authorize = async () => {
    if (!prov) return;
    setBusy(true); setErr(null);
    // Read the secret values from the DOM, wipe the inputs, and only then talk to the network.
    const idVal = idRef.current?.value ?? "";
    const secretVal = secretRef.current?.value ?? "";
    if (secretRef.current) secretRef.current.value = "";
    const base: CreateConnectionInput = { provider: prov.id, kind: wantsOAuth ? "oauth2" : "api_key", label: label.trim() || t(`conn.provider.${prov.id}`), capabilities: caps, resource_scope: resourceScope() };
    try {
      let created: Connection;
      if (wantsOAuth) {
        created = await connApi.create({ ...base, oauth_client_id: idVal.trim(), oauth_client_secret: secretVal });
        const start = await connApi.oauthStart(created.id);
        if (MOCK) created = await connApi.oauthSimulateCallback(created.id, denyScope);
        else { window.location.href = start.auth_url; return; }
      } else {
        created = await connApi.create({ ...base, secret: secretVal });
      }
      await useConnections.getState().loadConnections();
      await finish(created);
    } catch (e) { setErr(errText(e)); }
    finally { setBusy(false); }
  };

  const done = async () => {
    if (conn) {
      setBusy(true);
      try {
        // Optional assignment, only for read-only capabilities. Write access is granted from the permissions matrix (with confirmation).
        for (const a of assign) await connApi.putGrant(conn.id, a, { capabilities: conn.granted_capabilities.filter((c) => !prov?.capabilities.find((d) => d.id === c)?.side_effects) });
      } catch (e) { setErr(errText(e)); setBusy(false); return; }
      await useConnections.getState().loadConnections();
    }
    onClose();
  };

  const canNext = (step === 2 && caps.length > 0 && label.trim().length > 0) || step === 3;

  return (
    <Modal onClose={onClose} testId="conn-wizard" wide>
      <div data-testid={`conn-wizard-step-${step}`} data-step={step}>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="font-display text-lg font-semibold">{t("conn.wizard.title")}</h2>
          <span className="text-[11px] font-semibold text-mute">{t("conn.wizard.step", { n: step, total: STEPS })}</span>
        </div>

        {step === 1 && (
          <div className="grid gap-2 sm:grid-cols-2">
            {providers.map((p) => (
              <button key={p.id} type="button" disabled={!p.available} data-testid={`conn-catalog-card-${p.id}`} onClick={() => chooseProvider(p)}
                className="rounded-xl border border-line bg-panel2 p-3 text-left transition hover:border-accent disabled:cursor-not-allowed disabled:opacity-50">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-semibold">{t(`conn.provider.${p.id}`)}</span>
                  {!p.available && <span className="rounded-full bg-line px-2 text-[10px] font-semibold text-mute">{t("conn.comingSoon", { phase: p.phase })}</span>}
                </div>
                <p className="mt-1 text-[11px] text-mute">{t(`conn.provider.${p.id}.desc`)}</p>
              </button>
            ))}
            {providers.length === 0 && <p className="text-xs text-mute">{t("conn.wizard.noProviders")}</p>}
          </div>
        )}

        {step === 2 && prov && (
          <div className="space-y-3">
            <Field label={t("conn.wizard.label")}>
              <input data-testid="conn-label-input" className={inputCls} value={label} onChange={(e) => setLabel(e.target.value)} maxLength={60} />
            </Field>
            {prov.split_read_write && (
              <div>
                <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.wizard.profile")}</div>
                <div className="grid gap-2 sm:grid-cols-2">
                  {(["read", "write"] as const).map((p) => (
                    <button key={p} type="button" data-testid={`conn-profile-${p}`} data-checked={profile === p} onClick={() => chooseProfile(p)}
                      className={`rounded-xl border p-2.5 text-left text-xs transition ${profile === p ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
                      <b className="block text-[13px]">{t(`conn.profile.${p}`)}</b>
                      <span className="text-mute">{t(`conn.profile.${p}.desc`)}</span>
                    </button>
                  ))}
                </div>
                <p className="mt-1 text-[11px] text-mute">{t("conn.profile.separateNote")}</p>
              </div>
            )}
            <div>
              <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.wizard.whatCanItDo")}</div>
              <p className="mb-2 text-[11px] text-mute">{t("conn.default_note")}</p>
              <div className="space-y-1.5">
                {visibleCaps(prov).map((c) => (
                  <label key={c.id} data-testid={`conn-cap-${c.id}`} data-checked={caps.includes(c.id)} data-risk={c.risk}
                    className={`flex cursor-pointer items-start gap-2 rounded-xl border px-3 py-2 text-xs ${caps.includes(c.id) ? "border-accent bg-accent/10" : "border-line bg-panel2"}`}>
                    <input type="checkbox" className="mt-0.5" checked={caps.includes(c.id)} onChange={() => toggleCap(c.id)} />
                    <span className="flex-1">
                      <b className="block text-[13px]">{t(`conn.cap.${c.id}`)}</b>
                      {c.side_effects && <span className="text-mute">{t(`conn.rev.${c.reversibility ?? "none"}`)}</span>}
                    </span>
                    <span className="rounded-full px-2 text-[10px] font-semibold uppercase" style={{ background: c.risk === "high" ? "#b4443c22" : c.risk === "medium" ? "#a8620822" : "#2f7d5522", color: c.risk === "high" ? "#b4443c" : c.risk === "medium" ? "#a86208" : "#2f7d55" }}>{t(`conn.risk.${c.risk}`)}</span>
                  </label>
                ))}
              </div>
              {hasWrite && <div data-testid="conn-write-warning" className="mt-2 rounded-xl border border-red-300 bg-red-50 px-3 py-2 text-xs font-semibold text-red-700">{t("conn.writeWarning")}</div>}
            </div>
          </div>
        )}

        {step === 3 && prov && (
          <div className="space-y-3">
            <p className="text-xs text-mute">{t("conn.wizard.resourcesHelp")}</p>
            {prov.resource_filters.length === 0 && <p className="rounded-xl border border-dashed border-line px-3 py-4 text-center text-xs text-mute">{t("conn.wizard.noFilters")}</p>}
            {prov.resource_filters.map((f) => (
              <Field key={f.key} label={t(`conn.filter.${f.key}`)} hint={t(f.type === "list" ? "conn.filter.listHint" : "conn.filter.numberHint")}>
                <input data-testid={`conn-resource-filter-${f.key}`} className={inputCls} type={f.type === "number" ? "number" : "text"} value={filters[f.key] ?? ""} onChange={(e) => setFilters((s) => ({ ...s, [f.key]: e.target.value }))} />
              </Field>
            ))}
          </div>
        )}

        {step === 4 && prov && (
          <div className="space-y-3">
            <p className="rounded-xl bg-panel2 px-3 py-2 text-xs text-mute">{t("conn.secretNote")}</p>
            {wantsOAuth ? (
              <>
                <p className="text-xs font-semibold">{t("conn.byoApp.title")}</p>
                <p className="text-[11px] text-mute">{t("conn.byoApp.help")}</p>
                <Field label={t("conn.byoApp.clientId")}>
                  <input ref={idRef} data-testid="conn-oauth-client-id" className={inputCls} autoComplete="off" spellCheck={false} />
                </Field>
                <Field label={t("conn.byoApp.clientSecret")}>
                  <input ref={secretRef} data-testid="conn-secret-input" data-sensitive type="password" autoComplete="off" data-1p-ignore data-lpignore="true" className={inputCls} />
                </Field>
                {MOCK && (
                  <label className="flex items-center gap-2 text-[11px] text-mute">
                    <input type="checkbox" data-testid="conn-sim-deny-scope" checked={denyScope} onChange={(e) => setDenyScope(e.target.checked)} />
                    {t("conn.mock.denyScope")}
                  </label>
                )}
                <Btn data-testid="conn-oauth-start" kind="primary" disabled={busy} onClick={authorize}>{busy ? t("conn.working") : t("conn.oauth.authorize")}</Btn>
              </>
            ) : (
              <>
                <Field label={t("conn.apiKey.label")}>
                  <input ref={secretRef} data-testid="conn-secret-input" data-sensitive type="password" autoComplete="off" data-1p-ignore data-lpignore="true" className={inputCls} />
                </Field>
                <Btn data-testid="conn-secret-submit" kind="primary" disabled={busy} onClick={authorize}>{busy ? t("conn.working") : t("conn.apiKey.save")}</Btn>
              </>
            )}
            <ErrorLine msg={err} />
          </div>
        )}

        {step === 5 && conn && prov && (
          <div className="space-y-3">
            <div data-testid="conn-test-result" data-status={test?.status ?? "pending"} className={`rounded-xl border px-3 py-2 text-xs font-semibold ${test?.status === "ok" ? "border-emerald-300 bg-emerald-50 text-emerald-700" : "border-red-300 bg-red-50 text-red-700"}`}>
              {test?.status === "ok" ? t("conn.test.ok", { ms: test.latency_ms, account: test.account_label ?? "—" }) : t("conn.test.failed")}
            </div>
            <div data-testid="conn-granted-caps" className="text-xs">
              <b>{t("conn.test.granted")}: </b>{conn.granted_capabilities.map((c) => t(`conn.capShort.${c}`)).join(", ") || "—"}
              {conn.requested_capabilities.some((c) => !conn.granted_capabilities.includes(c)) && (
                <p className="mt-1 font-semibold text-amber-700">{t("conn.test.lessGranted", { caps: conn.requested_capabilities.filter((c) => !conn.granted_capabilities.includes(c)).map((c) => t(`conn.capShort.${c}`)).join(", ") })}</p>
              )}
            </div>
            <div>
              <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.wizard.assign")}</div>
              <p className="mb-2 text-[11px] text-mute">{t("conn.not_granted_note")} {hasWrite ? t("conn.wizard.assignWriteNote") : ""}</p>
              <div className="grid grid-cols-2 gap-1.5">
                {agentOrder.map((a) => (
                  <label key={a} className="flex items-center gap-2 rounded-xl border border-line bg-panel2 px-2 py-1 text-xs">
                    <input type="checkbox" data-testid={`conn-assign-${a}`} checked={assign.includes(a)} onChange={() => setAssign((s) => (s.includes(a) ? s.filter((x) => x !== a) : [...s, a]))} />
                    {agents[a]?.name ?? a}
                  </label>
                ))}
              </div>
            </div>
            <ErrorLine msg={err} />
          </div>
        )}

        {step !== 4 && step !== 5 && step !== 1 && <div className="mt-4"><ErrorLine msg={err} /></div>}
        <div className="mt-5 flex items-center justify-between">
          <Btn onClick={step === 1 || step === 5 ? onClose : () => setStep(step - 1)} disabled={busy}>{step === 1 || step === 5 ? t("common.close") : t("common.back")}</Btn>
          {step >= 2 && step <= 3 && <Btn data-testid="conn-wizard-next" kind="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>{t("conn.wizard.next")}</Btn>}
          {step === 5 && <Btn data-testid="conn-wizard-done" kind="primary" disabled={busy} onClick={done}>{t("conn.wizard.done")}</Btn>}
        </div>
      </div>
    </Modal>
  );
}
