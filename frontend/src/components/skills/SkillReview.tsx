"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { skillsApi, SkillError, type SkillDetail, type SkillWarning } from "@/lib/skills";
import { hasPending } from "@/lib/skillsLogic";
import { Btn, Card } from "../ui";
import { fmtSize } from "./shared";

const WARN_CLS: Record<string, string> = { injection: "border-err/40 bg-err/10 text-err", script: "border-warn/40 bg-warn/10 text-warn", allowed_tools: "border-line bg-panel2 text-ink2", import_note: "border-line bg-panel2 text-ink2" };

function Warning({ w }: { w: SkillWarning }) {
  const { t } = useT();
  return (
    <li data-testid={`skill-warning-${w.code}`} className={`rounded-lg border px-3 py-2 text-xs ${WARN_CLS[w.code] ?? WARN_CLS.import_note}`}>
      <b>{t(`skills.warning.${w.code}`)}</b>
      {w.path && <span className="ml-1 font-mono">{w.path}</span>}
      {w.detail && <span className="ml-1 font-mono">{w.detail}</span>}
      {!!w.hits?.length && <span className="ml-1 text-[11px]">({w.hits.join(", ")})</span>}
      <div className="mt-0.5 text-[11px] opacity-90">{t(`skills.warning.${w.code}.help`)}</div>
    </li>
  );
}

/** The review screen: everything the agent would receive (instructions and every file, scripts
 * highlighted) with the scan warnings, and the decision (admins). Nothing reaches an agent before
 * a person approves it here. */
export function SkillReview({ detail, canReview, onChanged, onDeleted }: { detail: SkillDetail; canReview: boolean; onChanged: () => void; onDeleted: () => void }) {
  const { t } = useT();
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const warnings = detail.version.warnings ?? [];
  const pending = hasPending(detail) || detail.status === "draft" || detail.status === "disabled";
  const fail = (e: unknown) => setErr(e instanceof SkillError && t(`skills.error.${e.code}`) !== `skills.error.${e.code}` ? t(`skills.error.${e.code}`) : t("skills.error.generic"));
  const run = async (fn: () => Promise<unknown>) => { setBusy(true); setErr(""); try { await fn(); onChanged(); } catch (e) { fail(e); } finally { setBusy(false); } };

  return (
    <div data-testid="skill-review" className="space-y-4">
      <div className="rounded-lg border border-line bg-panel2 px-3 py-2.5 text-xs text-ink2">
        <div className="font-semibold text-ink">{t("skills.review.title", { v: detail.version.version })}</div>
        <p className="mt-0.5">{t("skills.review.intro")}</p>
        <p className="mt-1 text-mute">{t("skills.review.meta", { author: detail.version.author, source: t(`skills.source.${detail.source}`) })}{detail.version.changelog ? ` · ${detail.version.changelog}` : ""}</p>
      </div>

      {warnings.length > 0 ? (
        <section aria-label={t("skills.review.warnings")}>
          <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("skills.review.warnings")}</h3>
          <ul data-testid="skill-warnings" className="space-y-1.5">{warnings.map((w, i) => <Warning key={i} w={w} />)}</ul>
        </section>
      ) : <div data-testid="skill-no-warnings" className="rounded-lg bg-ok/10 px-3 py-2 text-xs text-ok">{t("skills.review.noWarnings")}</div>}

      <Card title={t("skills.review.description")}><p data-testid="skill-review-description" className="text-[13px] text-ink">{detail.description}</p></Card>
      <Card title="SKILL.md"><pre data-testid="skill-review-instructions" className="max-h-[420px] overflow-auto whitespace-pre-wrap rounded-lg bg-panel2 p-3 font-mono text-[12px] text-ink2">{detail.instructions}</pre></Card>
      {detail.allowed_tools.length > 0 && (
        <Card title={t("skills.editor.tools")}>
          <div className="flex flex-wrap gap-1">{detail.allowed_tools.map((x) => <span key={x} className="rounded bg-mute/[0.08] px-1.5 py-0.5 font-mono text-[11px] text-ink2">{x}</span>)}</div>
          <p className="mt-1.5 text-[11px] text-mute">{t("skills.editor.toolsHint")}</p>
        </Card>
      )}
      {detail.files.map((f) => {
        const meta = detail.version.files.find((m) => m.path === f.path);
        const script = !!meta?.script;
        return (
          <Card key={f.path} className={script ? "!border-warn/60" : ""} title={<span data-testid="skill-review-file" className="flex items-center gap-2 font-mono text-[12px]">{f.path}
            <span className="font-sans text-[10px] font-normal text-mute">{fmtSize(meta?.size ?? f.content.length)}</span>
            {script && <span data-testid="skill-review-script" className="rounded bg-warn/15 px-1.5 py-0.5 font-sans text-[10px] font-semibold uppercase text-warn">{t("skills.script")}</span>}</span>}>
            {script && <p className="mb-2 text-[11px] text-warn">{t("skills.review.scriptNote")}</p>}
            <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap rounded-lg bg-panel2 p-3 font-mono text-[12px] text-ink2">{f.content}</pre>
          </Card>
        );
      })}

      {err && <div data-testid="skill-review-error" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{err}</div>}
      {canReview ? (
        <Card title={t("skills.review.decision")}>
          {pending ? (
            <div className="space-y-2">
              <p className="text-xs text-mute">{t("skills.review.decisionHelp")}</p>
              <input data-testid="skill-review-note" value={note} onChange={(e) => setNote(e.target.value)} maxLength={400} placeholder={t("skills.review.notePh")} className="w-full rounded-lg border border-line bg-panel2 px-3 py-2 text-xs outline-none focus:border-accent" />
              <div className="flex flex-wrap gap-2">
                <Btn kind="ok" data-testid="skill-approve" disabled={busy} onClick={() => run(() => skillsApi.review(detail.id, "approve", detail.current_version))}>{detail.status === "disabled" ? t("skills.review.enable") : t("skills.review.approve")}</Btn>
                {detail.status !== "disabled" && <Btn kind="danger" data-testid="skill-reject" disabled={busy} onClick={() => run(() => skillsApi.review(detail.id, "reject", detail.current_version, note))}>{t("skills.review.reject")}</Btn>}
              </div>
            </div>
          ) : <p className="text-xs text-mute">{t("skills.review.nothingPending")}</p>}
          <div className="mt-3 flex flex-wrap gap-2 border-t border-line pt-3">
            {detail.status !== "disabled" && <Btn data-testid="skill-disable" disabled={busy} onClick={() => run(() => skillsApi.disable(detail.id))}>{t("skills.review.disable")}</Btn>}
            <Btn kind="danger" data-testid="skill-delete" disabled={busy} onClick={() => { if (window.confirm(t("skills.review.deleteConfirm", { name: detail.name }))) run(async () => { await skillsApi.remove(detail.id); onDeleted(); }); }}>{t("skills.review.delete")}</Btn>
          </div>
        </Card>
      ) : <div className="rounded-lg bg-mute/10 px-3 py-2 text-xs text-mute">{t("skills.review.adminOnly")}</div>}
      {detail.review_note && <div data-testid="skill-review-saved-note" className="rounded-lg bg-warn/10 px-3 py-2 text-xs text-warn">{t("skills.review.lastNote", { note: detail.review_note })}</div>}
    </div>
  );
}
