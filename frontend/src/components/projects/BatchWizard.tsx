"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { customersApi, type Customer, type ImportResult } from "@/lib/customersApi";
import { batchApi, useBatchUi, type BatchPreview } from "@/lib/projects/batchApi";
import { useProjects } from "@/lib/projects/store";
import { insertVar, parseList, type ParsedRow } from "@/lib/batchLogic";
import { Btn } from "../ui";
import { fmtMoney } from "./shared";
import { useTplText } from "./templateUi";

const VARS = ["nombre", "empresa", "correo", "telefono", "notas"] as const;
type Step = "customers" | "params" | "review";

/** "Lanzar para varios clientes": choose (or import) the customers, fill the template once ({{cliente.*}} for the
 * data of each one), review what is missing and the total cost, launch. Each customer gets its own project. */
export function BatchWizard({ templateId, onClose }: { templateId: string; onClose: () => void }) {
  const { t } = useT();
  const tx = useTplText();
  const templates = useProjects((s) => s.templates);
  const tpl = templates.find((x) => x.id === templateId);
  const openBatch = useBatchUi((s) => s.open);
  const [step, setStep] = useState<Step>("customers");
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [q, setQ] = useState("");
  const [mode, setMode] = useState<"pick" | "import">("pick");
  const [importText, setImportText] = useState("");
  const [parsed, setParsed] = useState<ParsedRow[]>([]);
  const [preview, setPreview] = useState<ImportResult[] | null>(null);
  const [choices, setChoices] = useState<Record<number, string>>({});
  const [goal, setGoal] = useState(templateId === "tpl-generic" ? t("batch.goalExample") : "");
  const [params, setParams] = useState<Record<string, string>>(() => Object.fromEntries((tpl?.params ?? []).map((p) => [p.key, p.default ?? ""])));
  const [focus, setFocus] = useState<{ key: string; pos: number } | null>(null);
  const [review, setReview] = useState<BatchPreview | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [allowMissing, setAllowMissing] = useState(false);
  const [budget, setBudget] = useState<number | "">("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const loadCustomers = async () => { try { setCustomers(await customersApi.list()); } catch { setErr(t("batch.error.customers")); } };
  useEffect(() => { void loadCustomers(); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const shown = useMemo(() => customers.filter((c) => !q || `${c.name} ${c.contact_name ?? ""} ${c.email ?? ""}`.toLowerCase().includes(q.toLowerCase())), [customers, q]);
  const chosen = Object.keys(sel).filter((k) => sel[k]);

  const readImport = async (text: string) => {
    setErr(""); setPreview(null);
    const r = parseList(text);
    if (r.errors.length) { setErr(t(`batch.import.error.${r.errors[0]}`)); return; }
    setParsed(r.rows);
    try { setPreview((await customersApi.importPreview(r.rows)).rows); } catch { setErr(t("batch.error.generic")); }
  };
  const applyImport = async () => {
    setBusy(true); setErr("");
    try {
      const res = (await customersApi.importApply(parsed, choices)).rows;
      const ids = res.filter((r) => r.customer_id && (r.action === "existing" || r.action === "created")).map((r) => r.customer_id!);
      await loadCustomers();
      setSel((s) => ({ ...s, ...Object.fromEntries(ids.map((i) => [i, true])) }));
      setMode("pick"); setPreview(null); setImportText("");
    } catch { setErr(t("batch.error.generic")); } finally { setBusy(false); }
  };
  const body = () => ({ template_id: templateId, goal: goal.trim() || undefined, params, customer_ids: chosen,
    budget_per_project_usd: budget === "" ? undefined : Number(budget), confirm, allow_missing: allowMissing });
  const toReview = async () => {
    setBusy(true); setErr("");
    try { const p = await batchApi.preview(body()); setReview(p); setStep("review"); if (budget === "") setBudget(p.budget_per_project_usd); }
    catch (e) { setErr(String(e).includes("402") || String(e).includes("409") ? t("batch.error.limit") : t("batch.error.generic")); }
    finally { setBusy(false); }
  };
  const launch = async () => {
    setBusy(true); setErr("");
    try { const b = await batchApi.create(body()); onClose(); openBatch(b.id); }
    catch (e) { setErr(String(e).includes("409") ? t("batch.error.conflict") : String(e).includes("402") ? t("batch.error.limit") : t("batch.error.generic")); }
    finally { setBusy(false); }
  };
  const addVar = (v: string) => {
    if (!focus) return;
    const cur = focus.key === "__goal" ? goal : params[focus.key] ?? "";
    const r = insertVar(cur, focus.pos, v);
    if (focus.key === "__goal") setGoal(r.text); else setParams((p) => ({ ...p, [focus.key]: r.text }));
    setFocus({ key: focus.key, pos: r.cursor });
  };
  const field = "w-full rounded-md border border-line bg-white px-2 py-1.5 text-[12px] text-ink outline-none focus:border-accent";

  return (
    <div role="dialog" aria-modal="true" aria-labelledby="batch-title" data-testid="batch-wizard" className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30 p-4">
      <div className="max-h-[92dvh] w-full max-w-[760px] overflow-y-auto rounded-[10px] border border-line bg-white p-5 shadow-soft">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <h2 id="batch-title" className="text-[16px] font-semibold text-ink">{t("batch.title")}</h2>
            <p className="text-[12px] text-mute">{tpl ? tx.name(tpl) : templateId} · {t(`batch.step.${step}`)}</p>
          </div>
          <button type="button" onClick={onClose} aria-label={t("common.close")} className="rounded-md border border-line px-2 text-mute hover:text-ink">×</button>
        </div>

        {step === "customers" && (
          <div className="space-y-3">
            <div role="tablist" className="flex gap-1.5">
              {(["pick", "import"] as const).map((m) => (
                <button key={m} type="button" role="tab" aria-selected={mode === m} onClick={() => setMode(m)}
                  className={`rounded-md border px-3 py-1 text-[12px] font-medium ${mode === m ? "border-accent bg-accent/10 text-accent" : "border-line text-ink2"}`}>{t(`batch.mode.${m}`)}</button>
              ))}
            </div>
            {mode === "pick" ? (
              <>
                <div className="flex gap-2">
                  <input className={field} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("batch.search")} aria-label={t("batch.search")} />
                  <Btn onClick={() => setSel((s) => ({ ...s, ...Object.fromEntries(shown.map((c) => [c.id, true])) }))}>{t("batch.selectAll")}</Btn>
                  <Btn onClick={() => setSel({})}>{t("batch.clear")}</Btn>
                </div>
                <ul data-testid="batch-customers" className="max-h-[300px] divide-y divide-line overflow-y-auto rounded-md border border-line">
                  {shown.map((c) => (
                    <li key={c.id}><label className="flex items-center gap-2 px-2 py-1.5 text-[12px]">
                      <input type="checkbox" checked={!!sel[c.id]} onChange={(e) => setSel((s) => ({ ...s, [c.id]: e.target.checked }))} />
                      <span className="font-medium text-ink">{c.name}</span>
                      <span className="truncate text-mute">{[c.contact_name, c.email, c.phone].filter(Boolean).join(" · ")}</span>
                    </label></li>
                  ))}
                  {!shown.length && <li className="px-2 py-3 text-center text-[12px] text-mute">{t("batch.noCustomers")}</li>}
                </ul>
                <div className="flex items-center justify-between">
                  <span className="text-[12px] text-ink2">{t("batch.chosen", { count: chosen.length })}</span>
                  <Btn kind="primary" disabled={!chosen.length} data-testid="batch-next" onClick={() => setStep("params")}>{t("batch.next")}</Btn>
                </div>
              </>
            ) : (
              <div className="space-y-2">
                <p className="text-[12px] text-mute">{t("batch.import.hint")}</p>
                <textarea className={`${field} min-h-[110px] font-mono text-[11px]`} value={importText} onChange={(e) => setImportText(e.target.value)} placeholder={"Nombre,Correo,Teléfono,Empresa,Notas"} aria-label={t("batch.import.paste")} />
                <div className="flex flex-wrap gap-2">
                  <input ref={fileRef} type="file" accept=".csv,.txt,.tsv" className="hidden" onChange={async (e) => { const f = e.target.files?.[0]; if (f) { const txt = await f.text(); setImportText(txt); await readImport(txt); } }} />
                  <Btn onClick={() => fileRef.current?.click()}>{t("batch.import.file")}</Btn>
                  <Btn kind="primary" disabled={!importText.trim()} onClick={() => void readImport(importText)}>{t("batch.import.preview")}</Btn>
                </div>
                {preview && (
                  <>
                    <table className="w-full text-left text-[11px]">
                      <thead className="text-mute"><tr><th className="py-1">{t("batch.import.col.row")}</th><th>{t("batch.import.col.customer")}</th><th>{t("batch.import.col.result")}</th><th>{t("batch.import.col.choice")}</th></tr></thead>
                      <tbody className="divide-y divide-line">
                        {preview.map((r) => (
                          <tr key={r.row} data-action={r.action}>
                            <td className="py-1 font-mono text-mute">{r.row + 2}</td>
                            <td className="text-ink">{r.name || "—"}{r.email ? <span className="text-mute"> · {r.email}</span> : null}</td>
                            <td className={r.action === "error" ? "text-err" : r.action === "existing" ? "text-ink2" : "text-ok"}>
                              {r.action === "existing" ? t("batch.import.existing", { name: r.customer_name ?? "", by: t(`batch.import.by.${r.matched_by ?? "name"}`) })
                                : r.action === "new" ? t("batch.import.new") : t(`batch.import.reason.${r.reason ?? "invalid"}`)}
                            </td>
                            <td>
                              {r.action !== "error" && (
                                <select value={choices[r.row] ?? ""} onChange={(e) => setChoices((c) => ({ ...c, [r.row]: e.target.value }))} aria-label={t("batch.import.col.choice")} className="rounded border border-line px-1 py-0.5 text-[11px]">
                                  <option value="">{r.action === "existing" ? t("batch.import.useMatch") : t("batch.import.create")}</option>
                                  {r.action === "existing" && <option value="new">{t("batch.import.createInstead")}</option>}
                                  <option value="skip">{t("batch.import.skip")}</option>
                                </select>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <div className="flex justify-end"><Btn kind="primary" disabled={busy} onClick={() => void applyImport()}>{t("batch.import.apply")}</Btn></div>
                  </>
                )}
              </div>
            )}
          </div>
        )}

        {step === "params" && (
          <div className="space-y-3">
            <p className="text-[12px] text-mute">{t("batch.params.hint")}</p>
            <div className="flex flex-wrap gap-1.5" aria-label={t("batch.params.vars")}>
              {VARS.map((v) => <button key={v} type="button" disabled={!focus} onClick={() => addVar(v)} className="rounded-md border border-accent/40 bg-accent/5 px-2 py-0.5 font-mono text-[11px] text-accent disabled:opacity-40">{`{{cliente.${v}}}`}</button>)}
            </div>
            <label className="block text-[11px] font-medium text-mute">{t("batch.params.goal")}
              <textarea className={`${field} mt-1 min-h-[70px]`} value={goal} onChange={(e) => { setGoal(e.target.value); setFocus({ key: "__goal", pos: e.target.selectionStart }); }}
                onSelect={(e) => setFocus({ key: "__goal", pos: (e.target as HTMLTextAreaElement).selectionStart })} />
            </label>
            {(tpl?.params ?? []).map((p) => (
              <label key={p.key} className="block text-[11px] font-medium text-mute">{p.label_key ? t(p.label_key) : p.label || p.key}
                <input className={`${field} mt-1`} value={params[p.key] ?? ""} onChange={(e) => { setParams((x) => ({ ...x, [p.key]: e.target.value })); setFocus({ key: p.key, pos: e.target.selectionStart ?? 0 }); }}
                  onSelect={(e) => setFocus({ key: p.key, pos: (e.target as HTMLInputElement).selectionStart ?? 0 })} />
              </label>
            ))}
            <div className="flex justify-between">
              <Btn onClick={() => setStep("customers")}>{t("batch.back")}</Btn>
              <Btn kind="primary" disabled={busy} data-testid="batch-review" onClick={() => void toReview()}>{t("batch.toReview")}</Btn>
            </div>
          </div>
        )}

        {step === "review" && review && (
          <div className="space-y-3" data-testid="batch-review-panel">
            <div className="grid gap-2 sm:grid-cols-3">
              <Fact k={t("batch.review.customers")} v={`${review.customers.length} / ${review.max_customers}`} />
              <Fact k={t("batch.review.estimate")} v={`${fmtMoney(review.total_p50_usd)} – ${fmtMoney(review.total_p90_usd)}`} />
              <Fact k={t("batch.review.pace")} v={t("batch.review.paceValue", { count: review.parallel })} />
            </div>
            <label className="block text-[11px] font-medium text-mute">{t("batch.review.budget")}
              <input type="number" min={0} step="0.01" className={`${field} mt-1 w-40`} value={budget} onChange={(e) => setBudget(e.target.value === "" ? "" : Number(e.target.value))} />
              <span className="ml-2 text-ink2">{t("batch.review.budgetTotal", { total: fmtMoney((budget === "" ? review.budget_per_project_usd : Number(budget)) * review.customers.length) })}</span>
            </label>
            {review.missing > 0 && (
              <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-[12px] text-amber-900">
                <p className="font-semibold">{t("batch.review.missing", { count: review.missing })}</p>
                <ul className="mt-1 list-disc pl-4">
                  {review.customers.filter((c) => c.missing.length || c.error).slice(0, 12).map((c) => (
                    <li key={c.customer_id}>{c.name || c.customer_id}: {c.error ? t("batch.review.unknown") : c.missing.map((m) => `{{cliente.${m}}}`).join(", ")}</li>
                  ))}
                </ul>
                <label className="mt-1 flex items-center gap-1.5"><input type="checkbox" checked={allowMissing} onChange={(e) => setAllowMissing(e.target.checked)} />{t("batch.review.allowMissing")}</label>
              </div>
            )}
            {review.needs_confirm && (
              <label className="flex items-center gap-1.5 text-[12px] text-ink"><input type="checkbox" data-testid="batch-confirm" checked={confirm} onChange={(e) => setConfirm(e.target.checked)} />
                {t("batch.review.confirm", { total: fmtMoney(review.total_budget_usd), threshold: fmtMoney(review.confirm_threshold_usd) })}</label>
            )}
            <p className="text-[11px] text-mute">{t("batch.review.note", { count: fmtNumber(review.customers.length, 0) })}</p>
            <div className="flex justify-between">
              <Btn onClick={() => setStep("params")}>{t("batch.back")}</Btn>
              <Btn kind="primary" data-testid="batch-launch" disabled={busy || (review.needs_confirm && !confirm) || (review.missing > 0 && !allowMissing)} onClick={() => void launch()}>
                {t("batch.launch", { count: review.customers.length })}
              </Btn>
            </div>
          </div>
        )}
        {err && <p role="alert" className="mt-2 text-[12px] text-err">{err}</p>}
      </div>
    </div>
  );
}

function Fact({ k, v }: { k: string; v: string }) {
  return <div className="rounded-md border border-line bg-panel2 px-2 py-1.5"><div className="text-[10px] uppercase tracking-wide text-mute">{k}</div><div className="text-[13px] font-semibold text-ink">{v}</div></div>;
}
