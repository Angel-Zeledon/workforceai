"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, parentOf, type Listing } from "@/lib/drive";
import { errorDetail, projectsApi } from "@/lib/projects/api";
import type { ProjectInputItem, ProjectInputs } from "@/lib/projects/types";
import { Btn, Card } from "../ui";

const sizeLabel = (n: number) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : n >= 1024 ? `${Math.round(n / 1024)} KB` : `${n} B`);

/**
 * "Antes de empezar": the data the project needs. Each input can be uploaded (it lands in
 * /Projects/<project>/Entradas), picked from the drive (copied there) or filled with sample data.
 */
export function InputsCard({ projectId, inputs, onChange, proceed, onProceed }: {
  projectId: string; inputs: ProjectInputs; onChange: (v: ProjectInputs) => void; proceed: boolean; onProceed: (v: boolean) => void;
}) {
  const { t } = useT();
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [pick, setPick] = useState<ProjectInputItem | null>(null);
  const run = async (what: string, fn: () => Promise<ProjectInputs>) => {
    setBusy(what); setErr("");
    try { onChange(await fn()); } catch (e) {
      const d = errorDetail(e);
      setErr(d.includes("input_type_not_accepted") ? t("pv.inputs.err.type") : d.includes("invalid_input_value") ? t("pv.inputs.err.value") : t("pv.inputs.err.generic"));
    } finally { setBusy(""); }
  };
  const missingCount = inputs.missing.length;
  const hasOpen = inputs.items.some((i) => !i.satisfied);
  return (
    <Card title={t("pv.inputs.title")} right={<span data-testid="inputs-status" data-ready={inputs.ready ? "1" : "0"} data-missing={missingCount} className={`text-[11px] font-semibold ${inputs.ready ? "text-emerald-700" : "text-amber-700"}`}>
      {inputs.ready ? t("pv.inputs.ready") : t("pv.inputs.missing", { count: missingCount })}</span>}>
      <div data-testid="inputs-card" className="space-y-3">
        <div className="text-[11px] text-mute">{t("pv.inputs.intro")}</div>
        {inputs.folder && <div className="font-mono text-[10px] text-mute" data-testid="inputs-folder">{t("pv.inputs.folder", { path: inputs.folder })}</div>}
        {inputs.demo_set && hasOpen && (
          <div className="flex items-center gap-2 rounded-xl bg-violet-500/10 p-2">
            <Btn data-testid="inputs-use-demo" kind="primary" disabled={!!busy} onClick={() => run("demo", () => projectsApi.useDemoInputs(projectId))}>{busy === "demo" ? t("pv.inputs.loading") : t("pv.inputs.demo")}</Btn>
            <span className="text-[10px] text-violet-800">{t("pv.inputs.demoHint")}</span>
          </div>
        )}
        {inputs.demo_used && <div data-testid="inputs-demo-used" className="rounded-xl bg-violet-500/10 px-2 py-1 text-[11px] text-violet-800">{t("pv.inputs.demoUsed")}</div>}
        {inputs.items.map((it) => (
          <InputRow key={it.key} it={it} busy={!!busy}
            onUpload={(files) => run("up:" + it.key, async () => { let v: ProjectInputs = inputs; for (const f of files) v = await projectsApi.uploadInput(projectId, it.key, f); return v; })}
            onPick={() => setPick(it)}
            onValue={(val) => run("val:" + it.key, () => projectsApi.setInputValue(projectId, it.key, val))}
            onRemove={(p) => run("rm:" + it.key, () => projectsApi.removeInputFile(projectId, it.key, p))} />
        ))}
        {err && <div data-testid="inputs-error" className="text-[11px] text-red-700">{err}</div>}
        {!inputs.ready && (
          <label className="flex items-start gap-2 rounded-xl bg-amber-500/10 p-2 text-[11px] text-amber-900">
            <input data-testid="launch-without-inputs" type="checkbox" className="mt-0.5" checked={proceed} onChange={(e) => onProceed(e.target.checked)} />
            <span><b>{t("pv.inputs.without")}</b><br />{t("pv.inputs.withoutHint")}</span>
          </label>
        )}
      </div>
      {pick && <DrivePicker item={pick} onClose={() => setPick(null)} onChoose={(paths) => { const k = pick.key; setPick(null); void run("link:" + k, () => projectsApi.linkInput(projectId, k, paths)); }} />}
    </Card>
  );
}

function InputRow({ it, busy, onUpload, onPick, onValue, onRemove }: {
  it: ProjectInputItem; busy: boolean; onUpload: (f: File[]) => void; onPick: () => void; onValue: (v: string) => void; onRemove: (path: string) => void;
}) {
  const { t } = useT();
  const ref = useRef<HTMLInputElement>(null);
  const [val, setVal] = useState(it.value ?? "");
  useEffect(() => { setVal(it.value ?? ""); }, [it.value]);
  const files = it.kind === "file" || it.kind === "folder";
  const need = it.kind === "folder" ? Math.max(1, it.min_files ?? 1) : 1;
  return (
    <div data-testid={`input-row-${it.key}`} data-satisfied={it.satisfied ? "1" : "0"} className="rounded-xl border border-line bg-panel2 p-2">
      <div className="flex items-start gap-2">
        <span data-testid={`input-status-${it.key}`} className={`mt-0.5 text-[12px] font-bold ${it.satisfied ? "text-emerald-600" : it.required ? "text-amber-600" : "text-mute"}`}>{it.satisfied ? "✓" : "○"}</span>
        <div className="min-w-0 flex-1">
          <div className="text-[12px] font-semibold text-ink">{it.label} <span className="ml-1 text-[9px] font-semibold uppercase tracking-wide text-mute">{it.required ? t("pv.inputs.required") : t("pv.inputs.optional")}</span></div>
          <div className="text-[11px] text-mute">{it.description}</div>
          {it.example && !it.satisfied && <div className="font-mono text-[10px] text-mute">{t("pv.inputs.example", { example: it.example })}</div>}
          {files && <div className="text-[10px] text-mute" data-testid={`input-count-${it.key}`}>{t("pv.inputs.count", { have: it.files.length, need })}{it.accept?.length ? ` · ${it.accept.join(", ")}` : ""}</div>}
        </div>
      </div>
      {files && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <input ref={ref} data-testid={`input-upload-${it.key}`} type="file" className="hidden" multiple={it.kind === "folder"} accept={it.accept?.map((a) => "." + a).join(",")}
            onChange={(e) => { const f = Array.from(e.target.files ?? []); e.target.value = ""; if (f.length) onUpload(f); }} />
          <Btn data-testid={`input-upload-btn-${it.key}`} disabled={busy} onClick={() => ref.current?.click()}>{t("pv.inputs.upload")}</Btn>
          <Btn data-testid={`input-pick-btn-${it.key}`} disabled={busy} onClick={onPick}>{t("pv.inputs.pick")}</Btn>
        </div>
      )}
      {files && it.files.length > 0 && (
        <ul className="mt-2 space-y-0.5">
          {it.files.map((f) => (
            <li key={f.path} data-testid={`input-file-${it.key}`} className="flex items-center gap-2 text-[11px] text-ink">
              <span className="min-w-0 flex-1 truncate font-mono" title={f.path}>{f.name}</span>
              <span className="text-[9px] uppercase text-mute">{t(`pv.inputs.src.${f.source}`)}</span>
              <span className="text-[10px] text-mute">{sizeLabel(f.size)}</span>
              <button aria-label={t("pv.inputs.remove")} className="text-mute hover:text-red-700" onClick={() => onRemove(f.path)}>×</button>
            </li>
          ))}
        </ul>
      )}
      {!files && it.kind !== "select" && (
        <input data-testid={`input-value-${it.key}`} type={it.kind === "number" ? "number" : "text"} value={val} onChange={(e) => setVal(e.target.value)} onBlur={() => { if (val !== (it.value ?? "")) onValue(val); }}
          placeholder={it.example} className="mt-2 w-full rounded-lg border border-line-strong bg-panel px-2 py-1 text-[12px] text-ink outline-none focus:border-accent" />
      )}
      {it.kind === "select" && (
        <select data-testid={`input-value-${it.key}`} value={val} onChange={(e) => onValue(e.target.value)} className="mt-2 w-full rounded-lg border border-line-strong bg-panel px-2 py-1 text-[12px] text-ink">
          <option value="">—</option>
          {(it.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      )}
    </div>
  );
}

/** Pick files of the drive; they are COPIED into the project (the original stays untouched). */
function DrivePicker({ item, onClose, onChoose }: { item: ProjectInputItem; onClose: () => void; onChoose: (paths: string[]) => void }) {
  const { t } = useT();
  const [path, setPath] = useState("/");
  const [list, setList] = useState<Listing | null>(null);
  const [sel, setSel] = useState<string[]>([]);
  const [err, setErr] = useState(false);
  useEffect(() => { setErr(false); drive.list(path).then(setList).catch(() => { setList(null); setErr(true); }); }, [path]);
  const ok = (n: string) => !item.accept?.length || item.accept.includes(n.split(".").pop()?.toLowerCase() ?? "");
  const single = item.kind === "file";
  const toggle = (p: string) => setSel((s) => (s.includes(p) ? s.filter((x) => x !== p) : single ? [p] : [...s, p]));
  return (
    <div data-testid="input-drive-picker" role="dialog" className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" onClick={onClose}>
      <div className="max-h-[80vh] w-full max-w-md overflow-auto rounded-2xl bg-panel p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="text-sm font-semibold text-ink">{t("pv.inputs.pickTitle", { label: item.label })}</div>
        <div className="mt-1 font-mono text-[11px] text-mute">{path}</div>
        {path !== "/" && <button className="mt-1 text-[11px] text-accent" onClick={() => setPath(parentOf(path))}>← {t("pv.inputs.up")}</button>}
        {err && <div className="mt-2 text-[11px] text-red-700">{t("pv.inputs.err.generic")}</div>}
        <ul className="mt-2 space-y-0.5 text-[12px]">
          {list?.folders.map((f) => <li key={f.id}><button className="text-ink hover:underline" onClick={() => { setPath(f.path); }}>📁 {f.name}</button></li>)}
          {list?.files.map((f) => (
            <li key={f.id}>
              <label className={`flex items-center gap-2 ${ok(f.name) ? "text-ink" : "text-mute opacity-50"}`}>
                <input type="checkbox" disabled={!ok(f.name)} checked={sel.includes(f.path)} onChange={() => toggle(f.path)} />
                <span className="truncate">{f.name}</span>
              </label>
            </li>
          ))}
          {list && !list.folders.length && !list.files.length && <li className="text-mute">{t("pv.inputs.empty")}</li>}
        </ul>
        <div className="mt-3 flex justify-end gap-2">
          <Btn onClick={onClose}>{t("pv.inputs.cancel")}</Btn>
          <Btn data-testid="input-drive-confirm" kind="primary" disabled={!sel.length} onClick={() => onChoose(sel)}>{t("pv.inputs.choose", { count: sel.length })}</Btn>
        </div>
        <div className="mt-2 text-[10px] text-mute">{t("pv.inputs.copyHint")}</div>
      </div>
    </div>
  );
}
