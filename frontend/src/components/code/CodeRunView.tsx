"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useDriveNav, parentOf } from "@/lib/drive";
import { codeApi, fmtBytes, isFinished, type CodeRun } from "@/lib/codeApi";

/** Full code with line numbers. Plain text only (React escapes it): never HTML, never evaluated. */
export function CodeListing({ code, maxHeight = "max-h-72" }: { code: string; maxHeight?: string }) {
  const lines = code.replace(/\n$/, "").split("\n");
  return (
    <div data-testid="code-listing" className={`${maxHeight} overflow-auto rounded-md border border-line bg-white font-mono text-[11.5px] leading-[1.45]`}>
      <table className="w-full border-collapse">
        <tbody>
          {lines.map((l, i) => (
            <tr key={i}>
              <td aria-hidden className="select-none border-r border-line bg-panel2 px-2 text-right align-top text-mute">{i + 1}</td>
              <td className="whitespace-pre px-2 align-top text-ink">{l || " "}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function useRun(approvalId: string, approved: boolean): { run: CodeRun | null; failed: boolean } {
  const [run, setRun] = useState<CodeRun | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      let r: CodeRun | null = null;
      try { r = await codeApi.byApproval(approvalId); setFailed(false); } catch { setFailed(true); /* older backend or no permission */ }
      if (!live) return;
      if (r) setRun(r);
      // while an approved run is still in flight keep looking for its result
      if (approved && (!r || !isFinished(r.status))) timer = setTimeout(load, 2000);
    };
    load();
    return () => { live = false; if (timer) clearTimeout(timer); };
  }, [approvalId, approved]);
  return { run, failed };
}

function Text({ label, text, truncated, testid }: { label: string; text: string; truncated: boolean; testid: string }) {
  const { t } = useT();
  if (!text) return null;
  return (
    <div>
      <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{label}{truncated ? ` · ${t("code.truncated")}` : ""}</div>
      <pre data-testid={testid} className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-md border border-line bg-white p-2 font-mono text-[11px] text-ink">{text}</pre>
    </div>
  );
}

/** The outcome of a run: status, exit code, stdout/stderr (as text) and the files it produced. */
export function CodeRunResult({ run }: { run: CodeRun }) {
  const { t } = useT();
  const show = useDriveNav((s) => s.show);
  const cls = run.status === "succeeded" ? "text-emerald-700" : run.status === "running" || run.status === "awaiting_approval" ? "text-amber-700" : "text-red-700";
  return (
    <div data-testid="code-run-result" data-status={run.status} className="space-y-2 text-[12px]">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className={`font-semibold ${cls}`} data-testid="code-run-status">{t(`code.status.${run.status}`)}</span>
        {run.exit_code !== undefined && isFinished(run.status) && <span className="text-mute">{t("code.exit", { code: run.exit_code })}</span>}
        {run.duration_ms > 0 && <span className="text-mute">{t("code.duration", { seconds: (run.duration_ms / 1000).toFixed(1) })}</span>}
        {run.deny_reason && <span className="font-mono text-[11px] text-mute">{run.deny_reason}</span>}
      </div>
      <Text label="stdout" text={run.stdout} truncated={run.stdout_truncated} testid="code-run-stdout" />
      <Text label="stderr" text={run.stderr} truncated={run.stderr_truncated} testid="code-run-stderr" />
      {run.outputs.length > 0 && (
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("code.outputs")}</div>
          <ul className="mt-1 space-y-1">
            {run.outputs.map((o) => (
              <li key={o.name} className="flex items-center justify-between gap-2 rounded-md border border-line bg-white px-2 py-1">
                <span className="min-w-0 truncate font-mono text-[11px]">{o.name} <span className="text-mute">({fmtBytes(o.size)})</span></span>
                {o.file_ref
                  ? <button type="button" data-testid="code-output-open" className="shrink-0 text-[11px] font-semibold text-accent hover:underline" onClick={() => show(parentOf(o.file_ref!))}>{t("code.openInFiles")}</button>
                  : <span className="shrink-0 text-[11px] text-red-700">{t("code.notSaved")}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

/**
 * What a person sees for a code.run approval: the FULL code with line numbers, the inputs, the purpose,
 * the limits and a clear warning; once decided, the result of the run. The code comes from the backend by
 * approval id (the approval itself only carries a summary).
 */
export function CodeRunView({ approvalId, status, compact = false }: { approvalId: string; status: "pending" | "approved" | "rejected"; compact?: boolean }) {
  const { t } = useT();
  const { run, failed } = useRun(approvalId, status === "approved");
  if (!run) return <p data-testid="code-run-loading" className="mt-2 text-[11px] text-mute">{failed ? t("code.unavailable") : t("code.loading")}</p>;
  const l = run.limits;
  if (status === "pending") {
    return (
      <div data-testid="code-run-card" className="mt-2 space-y-2">
        <div role="note" data-testid="code-run-warning" className="rounded-md border border-amber-500/50 bg-amber-50 p-2 text-[11.5px] leading-snug text-amber-900">
          {t("code.warning", { seconds: l.timeout_seconds, memory: l.memory_mb })}
        </div>
        <p className="text-[12px]"><span className="font-semibold">{t("code.purpose")}: </span><span data-testid="code-run-purpose">{run.purpose}</span></p>
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("code.inputs")}</div>
          {run.inputs.length
            ? <ul className="text-[11.5px]">{run.inputs.map((i) => <li key={i.id} data-testid="code-run-input" className="font-mono">{i.name} <span className="text-mute">({fmtBytes(i.size)})</span></li>)}</ul>
            : <p className="text-[11.5px] text-mute">{t("code.noInputs")}</p>}
        </div>
        {run.expected_outputs.length > 0 && (
          <p className="text-[11.5px]"><span className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("code.expectedOutputs")}: </span><span className="font-mono">{run.expected_outputs.join(", ")}</span></p>
        )}
        <p className="text-[11px] text-mute" data-testid="code-run-limits">{t("code.limits", { seconds: l.timeout_seconds, memory: l.memory_mb, cpus: l.cpus, files: l.max_output_files, size: fmtBytes(l.max_output_bytes) })}</p>
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("code.code", { lang: run.language })} · {t("code.lines", { count: run.code.replace(/\n$/, "").split("\n").length })}</div>
          <CodeListing code={run.code} maxHeight={compact ? "max-h-44" : "max-h-72"} />
        </div>
      </div>
    );
  }
  return (
    <div className="mt-2 space-y-2">
      <CodeRunResult run={run} />
      <details className="text-[11.5px]">
        <summary className="cursor-pointer font-semibold text-ink2">{t("code.showCode")}</summary>
        <p className="my-1"><span className="font-semibold">{t("code.purpose")}: </span>{run.purpose}</p>
        <CodeListing code={run.code} maxHeight="max-h-52" />
      </details>
    </div>
  );
}
