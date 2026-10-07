"use client";
import type { StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";

interface Field { key: string; type: string; label: string; required?: boolean; max?: number; options?: string[] }
interface FormContent { schema: "aiw.form/1"; fields: Field[]; answers: Record<string, unknown> | null; answered_by: string | null }

/** Read-only form preview. Answering (request_input + approval decision answers) is a backend-dependent phase 4 feature. */
export function FormView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as FormContent | undefined;
  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  return (
    <div data-testid="art-form" className="h-full overflow-auto bg-panel p-6">
      <div className="mx-auto max-w-[480px] space-y-4 rounded-xl border border-line bg-panel2/40 p-5">
        {c.fields.map((f) => (
          <label key={f.key} className="block">
            <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{f.label}{f.required ? " *" : ""}{f.max ? ` (max ${f.max})` : ""}</span>
            {f.type === "textarea" ? <textarea disabled rows={3} className="w-full rounded-lg border border-line bg-panel px-2 py-1 text-[12px] opacity-70" />
              : f.type === "checkbox" ? <input type="checkbox" disabled className="accent-accent" />
              : <input disabled type="text" className="w-full rounded-lg border border-line bg-panel px-2 py-1 text-[12px] opacity-70" />}
          </label>
        ))}
        <p className="text-center text-[11px] text-mute">{c.answers ? t("form.answered") : t("form.pending")}</p>
      </div>
    </div>
  );
}
