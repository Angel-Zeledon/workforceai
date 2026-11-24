"use client";
import { useEffect, useState } from "react";
import { api, type RoleTemplate } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { registerRoleTemplates } from "@/lib/meta";
import { useStore } from "@/lib/store";
import { Modal } from "../templates/Modal";

/** Lists the profession templates (GET /role-templates) and hires one as a new agent (POST /agents/from-template). */
export function HireFromTemplateDialog({ onClose }: { onClose: () => void }) {
  const { t, locale } = useT();
  const [items, setItems] = useState<RoleTemplate[] | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [names, setNames] = useState<Record<string, string>>({});
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    let alive = true;
    setItems(null); setError(false);
    api.roleTemplates(locale).then((r) => { if (alive) { registerRoleTemplates(r); setItems(r); } }).catch(() => { if (alive) setError(true); });
    return () => { alive = false; };
  }, [locale]);

  const hire = async (tpl: RoleTemplate) => {
    setBusy(tpl.id); setResult(null);
    try {
      const res = await api.hireFromTemplate(tpl.id, locale, names[tpl.id]);
      await useStore.getState().loadAll();
      setItems((cur) => cur && cur.map((x) => (x.id === tpl.id ? { ...x, hired: x.hired + 1 } : x)));
      setResult({ ok: true, text: t("hire.done", { name: res.agent?.name ?? tpl.title, title: tpl.title }) });
      setNames((n) => ({ ...n, [tpl.id]: "" }));
    } catch (e) {
      const m = String((e as Error)?.message ?? "");
      setResult({ ok: false, text: m.endsWith("403") ? t("hire.error.forbidden") : m.endsWith("409") ? t("hire.error.conflict") : t("hire.error") });
    } finally { setBusy(null); }
  };

  return (
    <Modal title={t("hire.title")} onClose={onClose} testid="hire-dialog" wide>
      <p className="mb-4 text-sm text-mute">{t("hire.subtitle")}</p>
      {result && (
        <div role={result.ok ? "status" : "alert"} data-testid={result.ok ? "hire-result" : "hire-error"}
          className={`mb-3 rounded-xl border px-3 py-2 text-sm font-semibold ${result.ok ? "border-emerald-400/50 bg-emerald-500/10 text-emerald-700" : "border-red-400/50 bg-red-500/10 text-red-600"}`}>{result.text}</div>
      )}
      {error && <div role="alert" data-testid="hire-load-error" className="text-sm font-semibold text-red-600">{t("hire.loadError")}</div>}
      {!error && items === null && <div className="text-sm text-mute">{t("hire.loading")}</div>}
      {items && items.length === 0 && <div className="text-sm text-mute">{t("hire.empty")}</div>}
      <ul className="grid gap-3 sm:grid-cols-2">
        {(items ?? []).map((tpl) => (
          <li key={tpl.id} data-testid={`hire-card-${tpl.id}`} className="flex flex-col rounded-xl border border-line bg-bg p-4">
            <div className="mb-1 flex items-start justify-between gap-2">
              <h3 className="flex items-center gap-2 font-display text-[15px] font-semibold leading-tight">
                <span aria-hidden className="h-3 w-3 shrink-0 rounded-full" style={{ background: tpl.display?.color }} />{tpl.title}
              </h3>
              {tpl.hired > 0 && <span data-testid={`hire-count-${tpl.id}`} className="shrink-0 rounded bg-accent/15 px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide text-accent">{t("hire.hired", { count: tpl.hired })}</span>}
            </div>
            <p className="mb-2 text-[12px] leading-snug text-mute">{tpl.description}</p>
            {tpl.disclaimers?.length > 0 && <p data-testid={`hire-disclaimer-${tpl.id}`} className="mb-2 text-[11px] leading-snug text-warn">{tpl.disclaimers[0]}</p>}
            <div className="mt-auto flex items-center gap-2">
              <input type="text" value={names[tpl.id] ?? ""} maxLength={40} aria-label={t("hire.name", { title: tpl.title })} placeholder={t("hire.namePlaceholder")} data-testid={`hire-name-${tpl.id}`}
                onChange={(e) => setNames((n) => ({ ...n, [tpl.id]: e.target.value }))}
                className="min-w-0 flex-1 rounded-md border border-line bg-panel px-2 py-1.5 text-xs text-ink" />
              <button type="button" data-testid={`hire-button-${tpl.id}`} disabled={busy !== null} onClick={() => hire(tpl)}
                className="shrink-0 rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white disabled:opacity-50">{busy === tpl.id ? t("hire.hiring") : t("hire.action")}</button>
            </div>
          </li>
        ))}
      </ul>
    </Modal>
  );
}
