"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { LLMError, llmApi, type LLMProvider, type LLMTestResult, type LLMView } from "@/lib/llmApi";

/**
 * Settings -> "Modelo de IA": bring your own model. Self-contained (own API client, own testids `llm-*`),
 * so it can be dropped into any settings screen. The typed API key lives only in this component's state,
 * is sent once and is cleared after saving; the server never returns it (only its last 4 characters).
 */
const OTHER = "__other__";

function ProviderCard({ p, active, encryption, onChange, onError }: {
  p: LLMProvider; active: boolean; encryption: boolean; onChange: (v: LLMView) => void; onError: (m: string) => void;
}) {
  const { t } = useT();
  const known = p.models.includes(p.model);
  const [open, setOpen] = useState(false);
  const [key, setKey] = useState("");
  const [show, setShow] = useState(false);
  const [sel, setSel] = useState(p.models.length === 0 ? OTHER : known ? p.model : OTHER);
  const [custom, setCustom] = useState(known ? "" : p.model);
  const [base, setBase] = useState(p.base_url ?? "");
  const [org, setOrg] = useState(p.org_id ?? "");
  const [proj, setProj] = useState(p.project_id ?? "");
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<LLMTestResult | null>(null);

  const model = sel === OTHER ? custom.trim() : sel;
  const body = () => ({ api_key: key.trim() || undefined, model, base_url: base.trim(), org_id: org.trim(), project_id: proj.trim() });
  const fail = (e: unknown) => {
    if (e instanceof LLMError && e.code === "encryption_unavailable") onError(t("llm.err.encryption"));
    else onError(t("llm.err.generic", { detail: e instanceof Error ? e.message : "" }));
  };
  const run = async (fn: () => Promise<LLMView | void>) => {
    setBusy(true); onError("");
    try { const v = await fn(); if (v) onChange(v); } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const canTest = !!key.trim() || p.configured;

  return (
    <div data-testid={`llm-card-${p.id}`} data-active={active} data-configured={p.configured}
      className={`rounded-xl border p-3 ${active ? "border-accent bg-accent/5" : "border-line bg-bg"}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded-md bg-panel2 px-2 py-[2px] text-[11px] font-bold uppercase tracking-wide text-ink2">{p.id}</span>
        <span className="text-[14px] font-semibold">{p.label}</span>
        {active && <span data-testid={`llm-active-${p.id}`} className="rounded bg-accent px-1.5 py-[2px] text-[10px] font-semibold uppercase text-white">{t("llm.inUse")}</span>}
        {p.platform && !p.configured && <span data-testid="llm-badge-default" className="rounded bg-emerald-500/15 px-1.5 py-[2px] text-[10px] font-semibold uppercase text-emerald-800">{t("llm.included")}</span>}
        {p.configured && <span className="rounded bg-sky-500/15 px-1.5 py-[2px] text-[10px] font-semibold text-sky-800" data-testid={`llm-saved-${p.id}`}>{t("llm.keySaved", { last4: p.key_last4 || "····" })}</span>}
      </div>
      <p className="mt-1 text-[12px] text-mute">{t(`llm.help.${p.id}`)}</p>
      <div className="mt-2 flex flex-wrap gap-2">
        <button type="button" data-testid={`llm-use-${p.id}`} disabled={busy || active || (!p.configured && !p.platform)}
          onClick={() => void run(() => llmApi.use(p.id))}
          className="rounded-md bg-accent px-3 py-1.5 text-xs font-semibold text-white disabled:opacity-40">{active ? t("llm.inUse") : t("llm.use")}</button>
        <button type="button" data-testid={`llm-config-${p.id}`} aria-expanded={open} onClick={() => setOpen(!open)}
          className="rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2 hover:text-ink">{p.configured ? t("llm.edit") : p.platform ? t("llm.useOwnKey") : t("llm.configure")}</button>
      </div>

      {open && (
        <div className="mt-3 grid gap-2 border-t border-line pt-3">
          <label className="grid gap-1 text-[12px] font-semibold">{t("llm.key")}
            <span className="flex gap-2">
              <input data-testid={`llm-key-${p.id}`} name={`llm-key-${p.id}`} type={show ? "text" : "password"} autoComplete="off" spellCheck={false}
                value={key} onChange={(e) => { setKey(e.target.value); setRes(null); }}
                placeholder={p.configured ? t("llm.keyKeep", { last4: p.key_last4 || "····" }) : t("llm.keyPlaceholder")}
                className="min-w-0 flex-1 rounded-md border border-line bg-panel px-2 py-1.5 font-mono text-[12px] font-normal" />
              <button type="button" data-testid={`llm-key-toggle-${p.id}`} aria-pressed={show} onClick={() => setShow(!show)}
                className="rounded-md border border-line px-2 text-[11px] font-semibold text-ink2">{show ? t("llm.hide") : t("llm.show")}</button>
            </span>
          </label>
          <label className="grid gap-1 text-[12px] font-semibold">{t("llm.model")}
            {p.models.length > 0 && (
              <select data-testid={`llm-model-${p.id}`} value={sel} onChange={(e) => setSel(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-1.5 text-[12px] font-normal">
                {p.models.map((m) => <option key={m} value={m}>{m}</option>)}
                <option value={OTHER}>{t("llm.modelOther")}</option>
              </select>
            )}
            {sel === OTHER && (
              <input data-testid={`llm-model-text-${p.id}`} value={custom} onChange={(e) => setCustom(e.target.value)} placeholder={t("llm.modelPlaceholder")}
                className="rounded-md border border-line bg-panel px-2 py-1.5 font-mono text-[12px] font-normal" />
            )}
          </label>
          {(p.supports_base_url || p.supports_org_id) && (
            <details open={p.requires_base_url} className="text-[12px]">
              <summary className="cursor-pointer font-semibold text-ink2" data-testid={`llm-advanced-${p.id}`}>{t("llm.advanced")}</summary>
              <div className="mt-2 grid gap-2">
                {p.supports_base_url && (
                  <label className="grid gap-1 font-semibold">{t("llm.baseUrl")}
                    <input data-testid={`llm-base-${p.id}`} value={base} onChange={(e) => setBase(e.target.value)} placeholder={p.default_base_url || "https://api.example.com/v1"}
                      className="rounded-md border border-line bg-panel px-2 py-1.5 font-mono text-[12px] font-normal" />
                  </label>
                )}
                {p.supports_org_id && (
                  <>
                    <label className="grid gap-1 font-semibold">{t("llm.orgId")}
                      <input data-testid={`llm-org-${p.id}`} value={org} onChange={(e) => setOrg(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-1.5 font-mono text-[12px] font-normal" /></label>
                    <label className="grid gap-1 font-semibold">{t("llm.projectId")}
                      <input data-testid={`llm-project-${p.id}`} value={proj} onChange={(e) => setProj(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-1.5 font-mono text-[12px] font-normal" /></label>
                  </>
                )}
              </div>
            </details>
          )}
          {p.key_url && <a href={p.key_url} target="_blank" rel="noopener noreferrer" data-testid={`llm-keylink-${p.id}`} className="text-[12px] font-semibold text-accent underline">{t("llm.getKey", { name: p.label })}</a>}
          <p className="text-[11px] text-mute">{t("llm.cost")}</p>
          {!encryption && <p className="text-[12px] font-semibold text-red-600">{t("llm.err.encryption")}</p>}
          <div className="flex flex-wrap gap-2">
            <button type="button" data-testid={`llm-test-${p.id}`} disabled={busy || !canTest || !model}
              onClick={() => void run(async () => { setRes(await llmApi.test(p.id, body())); })}
              className="rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2 disabled:opacity-40">{t("llm.test")}</button>
            <button type="button" data-testid={`llm-save-${p.id}`} disabled={busy || !model || (!key.trim() && !p.configured)}
              onClick={() => void run(async () => { const v = await llmApi.save(p.id, body()); setKey(""); setShow(false); return v; })}
              className="rounded-md bg-accent px-3 py-1.5 text-xs font-semibold text-white disabled:opacity-40">{t("llm.save")}</button>
            {p.configured && (
              <button type="button" data-testid={`llm-delete-${p.id}`} disabled={busy} onClick={() => void run(() => llmApi.remove(p.id))}
                className="rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-red-600">{t("llm.remove")}</button>
            )}
          </div>
          {res && (
            <div role="status" data-testid={`llm-test-result-${p.id}`} data-code={res.code} className={`text-[12px] font-semibold ${res.ok ? "text-emerald-700" : "text-red-600"}`}>
              {t(`llm.test.${res.code}`)}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function ModelProviderSettings() {
  const { t } = useT();
  const [view, setView] = useState<LLMView | null>(null);
  const [err, setErr] = useState("");
  const load = useCallback(() => { llmApi.get().then(setView).catch(() => setView(null)); }, []);
  useEffect(() => { load(); }, [load]);
  if (!view) return null; // owner/admin only, or the server does not expose it
  const platform = view.providers.find((p) => p.id === view.platform_default);
  return (
    <section className="mb-6" data-testid="llm-section">
      <h3 className="mb-1 flex flex-wrap items-center gap-2 font-display text-[15px] font-semibold">{t("llm.title")}
        {view.using_platform_model && <span data-testid="llm-default-badge" className="rounded bg-emerald-500/15 px-1.5 py-[2px] text-[10px] font-semibold uppercase text-emerald-800">{t("llm.defaultBadge", { name: platform?.label ?? "DeepSeek" })}</span>}
      </h3>
      <p className="mb-3 text-[12px] text-mute">{t("llm.intro")}</p>
      {err && <div role="alert" data-testid="llm-error" className="mb-2 text-[12px] font-semibold text-red-600">{err}</div>}
      <div className="grid gap-3 sm:grid-cols-2">
        {view.providers.map((p) => (
          <ProviderCard key={`${p.id}:${p.updated_at ?? ""}`} p={p} active={view.active_provider === p.id} encryption={view.encryption_available}
            onChange={setView} onError={setErr} />
        ))}
      </div>
    </section>
  );
}
