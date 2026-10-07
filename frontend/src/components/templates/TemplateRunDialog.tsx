"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { trOr } from "@/lib/i18n-core";
import { orgApi, type WorkflowTemplate } from "@/lib/orgConfigApi";
import { Modal } from "./Modal";
import { StepFlow } from "./StepFlow";

/** Parameter form + execution order preview for one template; starts it as a normal request. */
export function TemplateRunDialog({ template, onClose, onStarted }: {
  template: WorkflowTemplate; onClose: () => void; onStarted: () => void;
}) {
  const { t, locale } = useT();
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const name = trOr(template.name_key, template.name);
  const missing = template.params.some((p) => p.required && !(values[p.key] ?? "").trim());

  const start = async () => {
    if (missing) { setError(t("tpl.run.missing")); return; }
    setBusy(true); setError(null);
    try {
      const params: Record<string, string> = {};
      for (const p of template.params) { const v = (values[p.key] ?? "").trim(); if (v) params[p.key] = v; }
      await orgApi.instantiate(template.key, params, locale);
      onStarted();
    } catch {
      setError(t("tpl.run.error"));
      setBusy(false);
    }
  };

  return (
    <Modal title={t("tpl.run.title", { name })} onClose={onClose} testid="wf-template-run" wide>
      <p className="mb-4 text-sm text-mute">{trOr(template.description_key, template.description)}</p>
      {template.params.length > 0 && (
        <form className="mb-5 grid gap-3 sm:grid-cols-2" onSubmit={(e) => { e.preventDefault(); void start(); }}>
          <div className="col-span-full text-[11px] font-semibold uppercase tracking-wide text-mute">{t("tpl.run.params")}</div>
          {template.params.map((p) => (
            <label key={p.key} className="flex flex-col gap-1 text-[12px] font-semibold">
              <span>{trOr(p.label_key, p.label)} <span className="font-normal text-mute">({p.required ? t("tpl.run.required") : t("tpl.run.optional")})</span></span>
              <input data-testid={`wf-template-param-${p.key}`} value={values[p.key] ?? ""} maxLength={200}
                onChange={(e) => setValues({ ...values, [p.key]: e.target.value })} placeholder={p.default}
                className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-normal outline-none focus:border-accent" />
            </label>
          ))}
        </form>
      )}
      <div className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("tpl.flow.title")}</div>
      <StepFlow steps={template.steps} />
      {error && <div role="alert" data-testid="wf-template-error" className="mt-3 text-sm font-semibold text-red-600">{error}</div>}
      <div className="mt-5 flex justify-end gap-2">
        <button type="button" onClick={onClose} className="rounded-md border border-line px-4 py-2 text-xs font-semibold text-mute hover:text-ink">{t("common.back")}</button>
        <button type="button" data-testid="wf-template-submit" disabled={busy || missing} onClick={() => void start()}
          className="rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white disabled:opacity-50">
          {busy ? t("tpl.run.starting") : t("tpl.run.start")}
        </button>
      </div>
    </Modal>
  );
}
