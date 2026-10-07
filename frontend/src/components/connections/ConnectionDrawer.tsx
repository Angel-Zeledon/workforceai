"use client";
import { useRef, useState } from "react";
import { MOCK } from "@/lib/config";
import { fmtDateTime, useT } from "@/lib/i18n";
import { connApi, type TestResult } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import { useStore } from "@/lib/store";
import { Btn } from "../ui";
import { CapChip, ErrorLine, errText, inputCls, Modal, StatusBadge, useViewer } from "./shared";

const REVOKE_STEPS = ["suspend", "cancel", "provider", "destroy", "grants", "audit"] as const;

function RevokeDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useT();
  const c = useConnections((s) => s.connections[id]);
  const grants = useConnections((s) => s.grants[id] ?? []);
  const drafts = useConnections((s) => s.drafts);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  if (!c) return null;
  const affectedTasks = drafts.filter((d) => d.connection_id === id && ["held", "pending_approval", "draft"].includes(d.status)).length;
  const go = async () => {
    setBusy(true); setErr(null);
    try { await connApi.revoke(id, name); await useConnections.getState().loadConnections(); await useConnections.getState().loadOutbox(); setDone(true); }
    catch (e) { setErr(errText(e)); }
    finally { setBusy(false); }
  };
  return (
    <Modal onClose={onClose} testId="conn-revoke-confirm">
      <h2 className="font-display text-lg font-semibold">{t("conn.revoke.title")}</h2>
      <p className="mt-1 text-xs text-mute">{t("conn.revoke.body", { provider: t(`conn.provider.${c.provider}`), n: grants.length, m: affectedTasks })}</p>
      {!done ? (
        <div className="mt-3 space-y-3">
          <label className="block text-xs font-semibold">{t("conn.revoke.typeName", { name: c.label })}
            <input data-testid="conn-revoke-name-input" className={`${inputCls} mt-1`} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </label>
          <ErrorLine msg={err} />
          <div className="flex justify-end gap-2">
            <Btn onClick={onClose}>{t("common.close")}</Btn>
            <Btn data-testid="conn-revoke-submit" kind="danger" disabled={busy || name !== c.label} onClick={go}>{t("conn.revoke.confirm")}</Btn>
          </div>
        </div>
      ) : (
        <div className="mt-3 space-y-2">
          <ol data-testid="conn-revoke-progress" className="space-y-1 text-xs">
            {REVOKE_STEPS.map((s) => <li key={s} className="flex items-center gap-2"><span className="text-emerald-600">✓</span>{t(`conn.revoke.step.${s}`)}</li>)}
          </ol>
          {c.pending_provider_revocation && <p className="text-xs font-semibold text-amber-700">{t("conn.revoke.manualProvider")}</p>}
          <div className="flex justify-end"><Btn kind="primary" onClick={onClose}>{t("common.close")}</Btn></div>
        </div>
      )}
    </Modal>
  );
}

export function ConnectionDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useT();
  const c = useConnections((s) => s.connections[id]);
  const grants = useConnections((s) => s.grants[id] ?? []);
  const providers = useConnections((s) => s.providers);
  const usage = useConnections((s) => s.usage).filter((u) => u.connection_id === id).slice(0, 6);
  const agents = useStore((s) => s.agents);
  const { isAdmin } = useViewer();
  const [test, setTest] = useState<TestResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [revoke, setRevoke] = useState(false);
  const [simAgent, setSimAgent] = useState("assistant");
  const rotateRef = useRef<HTMLInputElement>(null);
  if (!c) return null;
  const prov = providers.find((p) => p.id === c.provider);
  const defs = prov?.capabilities ?? [];
  const live = c.status !== "revoked";

  const run = async (fn: () => Promise<unknown>) => {
    setErr(null);
    try { await fn(); await useConnections.getState().loadConnections(); } catch (e) { setErr(errText(e)); }
  };
  const doTest = () => run(async () => setTest(await connApi.test(id)));
  const rotate = () => run(async () => {
    const v = rotateRef.current?.value ?? "";
    if (rotateRef.current) rotateRef.current.value = ""; // the secret leaves the DOM before the request is sent
    await connApi.rotate(id, v);
  });
  const simulate = (cap: string) => run(async () => { await connApi.simulateUse(id, simAgent, cap); await useConnections.getState().loadUsage(); });

  return (
    <>
      <aside data-testid="conn-drawer" data-status={c.status} className="fixed inset-y-0 right-0 z-[70] flex w-[420px] max-w-full flex-col border-l-2 border-line bg-panel shadow-pop">
        <header className="flex items-start justify-between gap-2 border-b border-line p-4">
          <div className="min-w-0">
            <h2 className="truncate font-display text-lg font-semibold">{c.label}</h2>
            <p className="text-[11px] text-mute">{t(`conn.provider.${c.provider}`)} · {c.account_label ?? "—"}</p>
            <div className="mt-1 flex items-center gap-2"><StatusBadge status={c.status} testId="conn-drawer-status" />{c.mode === "simulated" && <span className="rounded-full bg-violet-500/15 px-2 text-[10px] font-semibold text-violet-700">{t("conn.mode.simulated")}</span>}</div>
          </div>
          <Btn onClick={onClose}>{t("common.close")}</Btn>
        </header>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 text-xs">
          <ErrorLine msg={err} />
          <section>
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.drawer.capabilities")}</h3>
            <div className="flex flex-wrap gap-1">
              {c.granted_capabilities.map((cap) => <CapChip key={cap} cap={cap} risk={defs.find((d) => d.id === cap)?.risk ?? "low"} />)}
              {c.requested_capabilities.filter((x) => !c.granted_capabilities.includes(x)).map((cap) => <CapChip key={cap} cap={cap} risk="low" dim />)}
            </div>
            {Object.keys(c.resource_scope).length > 0 && (
              <p className="mt-1 text-mute">{Object.entries(c.resource_scope).map(([k, v]) => `${t(`conn.filter.${k}`)}: ${Array.isArray(v) ? v.join(", ") : v}`).join(" · ")}</p>
            )}
          </section>
          <section>
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.drawer.credential")}</h3>
            {c.credential ? (
              <p className="text-mute">
                {t(`conn.cred.${c.credential.kind}`)} · v{c.credential.version}
                {c.credential.hint ? <> · <span data-testid="conn-credential-hint">…{c.credential.hint}</span></> : null}
                {c.credential.rotated_at ? <> · {fmtDateTime(c.credential.rotated_at)}</> : null}
              </p>
            ) : <p className="text-mute">{t("conn.cred.none")}</p>}
            <p className="mt-1 text-[11px] text-mute">{t("conn.secretNeverShown")}</p>
            {c.kind === "api_key" && live && (
              <div className="mt-2 flex gap-2">
                <input ref={rotateRef} data-testid="conn-secret-input" data-sensitive type="password" autoComplete="off" data-1p-ignore data-lpignore="true" className={inputCls} placeholder={t("conn.apiKey.newPlaceholder")} />
                <Btn data-testid="conn-rotate" disabled={!isAdmin} onClick={rotate}>{t("conn.rotate")}</Btn>
              </div>
            )}
          </section>
          <section>
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.drawer.grants")}</h3>
            {grants.length === 0 ? <p className="text-mute">{t("conn.not_granted_note")}</p> : (
              <ul className="space-y-1">
                {grants.map((g) => (
                  <li key={g.id} className="flex items-center justify-between rounded-xl border border-line bg-panel2 px-2 py-1">
                    <span className="font-semibold">{agents[g.agent_id]?.name ?? g.agent_id}</span>
                    <span className="flex flex-wrap items-center justify-end gap-1">
                      {g.capabilities.map((cap) => <CapChip key={cap} cap={cap} risk={defs.find((d) => d.id === cap)?.risk ?? "low"} />)}
                      {g.status === "pending_second_approval" && <span className="rounded-full bg-amber-500/20 px-2 text-[10px] font-semibold text-amber-700">{t("conn.grant.pending")}</span>}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section>
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.drawer.recentUse")}</h3>
            {usage.length === 0 ? <p className="text-mute">{t("conn.usage.empty")}</p> : (
              <ul className="space-y-0.5">
                {usage.map((u) => <li key={u.id} className="flex justify-between text-mute"><span>{agents[u.agent_id ?? ""]?.name ?? u.agent_id} · {u.tool}</span><span>{t(`conn.decision.${u.decision}`)}</span></li>)}
              </ul>
            )}
            {MOCK && live && (
              <div className="mt-2 rounded-xl border border-dashed border-line p-2">
                <div className="mb-1 text-[10px] font-semibold uppercase text-mute">{t("conn.mock.simulate")}</div>
                <select data-testid="conn-sim-agent" className={inputCls} value={simAgent} onChange={(e) => setSimAgent(e.target.value)}>
                  {Object.values(agents).map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
                </select>
                <div className="mt-1 flex flex-wrap gap-1">
                  {c.requested_capabilities.map((cap) => <Btn key={cap} data-testid={`conn-sim-use-${cap}`} onClick={() => simulate(cap)}>{t(`conn.capShort.${cap}`)}</Btn>)}
                </div>
              </div>
            )}
          </section>
          {test && (
            <div data-testid="conn-test-result" data-status={test.status} className={`rounded-xl border px-3 py-2 font-semibold ${test.status === "ok" ? "border-emerald-300 bg-emerald-50 text-emerald-700" : "border-red-300 bg-red-50 text-red-700"}`}>
              {test.status === "ok" ? t("conn.test.ok", { ms: test.latency_ms, account: test.account_label ?? "—" }) : t("conn.test.failed")}
            </div>
          )}
        </div>
        <footer className="flex flex-wrap gap-2 border-t border-line p-3">
          <Btn data-testid="conn-test" disabled={!isAdmin || !live} onClick={doTest}>{t("conn.test")}</Btn>
          {c.status === "active" && <Btn data-testid="conn-suspend" disabled={!isAdmin} onClick={() => run(() => connApi.suspend(id))}>{t("conn.suspend")}</Btn>}
          {c.status === "suspended" && <Btn data-testid="conn-resume" disabled={!isAdmin} onClick={() => run(() => connApi.resume(id))}>{t("conn.resume")}</Btn>}
          {live && <Btn data-testid="conn-readonly-toggle" disabled={!isAdmin} onClick={() => run(() => connApi.patch(id, { read_only: !c.read_only }))}>{c.read_only ? t("conn.readOnly.off") : t("conn.readOnly.on")}</Btn>}
          {live && <Btn data-testid="conn-revoke" kind="danger" disabled={!isAdmin} onClick={() => setRevoke(true)}>{t("conn.revoke")}</Btn>}
        </footer>
      </aside>
      {revoke && <RevokeDialog id={id} onClose={() => setRevoke(false)} />}
    </>
  );
}
