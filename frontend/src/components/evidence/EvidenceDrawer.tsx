"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { call } from "@/lib/api";
import { useArtifacts } from "@/lib/artifacts";
import { fmtDateTime, useT } from "@/lib/i18n";
import { findFigures, figureOf, kindOf, sameFigure, type ReportCitation } from "@/lib/evidenceLogic";
import { useFileViewer } from "@/lib/fileView";
import type { Task } from "@/lib/types";
import { SheetEditor } from "../workspace/editors/SheetEditor";
import { Icon } from "../icons";
import { useAgentName } from "../ui";

const KIND_KEY = {
  task: "evidence.kind.task", report: "evidence.kind.report", memory: "evidence.kind.memory",
  file: "evidence.kind.file", cell: "evidence.kind.cell", conn: "evidence.kind.conn",
} as const;

/**
 * Where a figure of a report comes from (B6): one card per verified source. Opens the source task, the
 * artifact cell (highlighted), the drive file version, or shows the metadata of a connector item (provider,
 * id and when it was read; never third-party content beyond what the task already stored).
 */
export function EvidenceDrawer({ value, cites, onClose }: { value: string; cites: ReportCitation[]; onClose: () => void }) {
  const { t } = useT();
  const closeRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  // A portal on <body>: above the app header and any panel stacking context, on desktop and phones.
  if (typeof document === "undefined") return null;
  return createPortal(
    <div className="fixed inset-0 z-[9000] flex justify-end bg-black/30" onClick={onClose}>
      <aside role="dialog" aria-modal="true" aria-labelledby="evidence-title" data-testid="evidence-drawer"
        onClick={(e) => e.stopPropagation()}
        className="flex h-full w-full max-w-md flex-col border-l border-line bg-panel shadow-xl">
        <header className="flex items-start gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1">
            <h3 id="evidence-title" className="text-[14px] font-semibold text-ink">{t("evidence.title", { value })}</h3>
            <p className="mt-0.5 text-[11px] text-mute">{t("evidence.subtitle")}</p>
          </div>
          <button ref={closeRef} type="button" onClick={onClose} aria-label={t("common.close")} data-testid="evidence-close"
            className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={14} /></button>
        </header>
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
          {cites.map((c, i) => <SourceCard key={`${c.task_id}:${c.ref}:${i}`} c={c} value={value} onClose={onClose} />)}
        </div>
      </aside>
    </div>,
    document.body,
  );
}

function SourceCard({ c, value, onClose }: { c: ReportCitation; value: string; onClose: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const kind = kindOf(c);
  const s = c.source ?? {};
  return (
    <section data-testid={`evidence-source-${kind || "other"}`} className="rounded-lg border border-line bg-panel2/40 p-3">
      <div className="flex flex-wrap items-center gap-2 text-[10px] font-semibold uppercase tracking-widest text-accent">
        <span>{kind ? t(KIND_KEY[kind]) : c.ref}</span>
        <span className="rounded-full bg-ok/10 px-2 py-0.5 normal-case tracking-normal text-ok">{t("evidence.verified")}</span>
      </div>
      {c.label && <div className="mt-1 text-[12.5px] font-medium text-ink">{c.label}</div>}
      <div className="mt-0.5 text-[11px] text-mute">{t("evidence.citedBy", { agent: name(c.agent_id) })}</div>
      <div className={`mt-1 text-[11px] ${c.value_checked ? "text-ok" : "text-mute"}`}>
        {c.value_checked ? t("evidence.valueChecked") : t("evidence.valueNotChecked")}
      </div>
      <div className="mt-2">
        {kind === "task" && s.task_id && <TaskSource taskId={s.task_id} value={value} />}
        {kind === "cell" && s.artifact_id && <CellSource artifactId={s.artifact_id} sheet={s.sheet ?? ""} cell={s.cell ?? ""} cellValue={s.cell_value} />}
        {kind === "file" && (
          <div className="space-y-1 text-[12px] text-ink2">
            <div className="break-all font-mono text-[11.5px] text-ink">{s.path}</div>
            {!!s.version && <div>{t("evidence.file.version", { v: s.version })}</div>}
            {s.read_at && <div>{t("evidence.readAt", { when: fmtDateTime(s.read_at) })}</div>}
            {s.path && (
              <button type="button" data-testid="evidence-open-file" onClick={() => {
                useFileViewer.getState().open({ ...(s.file_id ? { file_id: s.file_id } : {}), path: s.path!, name: s.path!.split("/").pop() || s.path!, version: s.version, action: "evidence" });
                onClose();
              }} className="mt-1 inline-flex items-center gap-1 rounded-md border border-line bg-panel px-2 py-1 text-[11.5px] font-semibold text-accent hover:border-accent">
                <Icon name="doc" size={12} />{s.version ? t("evidence.file.open", { v: s.version }) : t("evidence.file.openLatest")}
              </button>
            )}
          </div>
        )}
        {(kind === "report" || kind === "memory") && (
          <div className="space-y-1 text-[12px] text-ink2">
            {s.title && <div className="font-medium text-ink">{s.title}</div>}
            <div className="font-mono text-[11px] text-mute">{t("evidence.memoryRef", { ref: c.ref })}</div>
          </div>
        )}
        {kind === "conn" && (
          <dl className="grid grid-cols-[auto,1fr] gap-x-3 gap-y-1 text-[12px]">
            <dt className="text-mute">{t("evidence.conn.provider")}</dt><dd className="text-ink">{s.provider || "–"}</dd>
            <dt className="text-mute">{t("evidence.conn.tool")}</dt><dd className="text-ink">{s.tool || "–"}</dd>
            <dt className="text-mute">{t("evidence.conn.item")}</dt><dd className="break-all font-mono text-[11px] text-ink">{s.item_id || "–"}</dd>
            {s.read_at && (<><dt className="text-mute">{t("evidence.conn.readAt")}</dt><dd className="text-ink">{fmtDateTime(s.read_at)}</dd></>)}
            <dd className="col-span-2 mt-1 text-[11px] text-mute">{t("evidence.conn.note")}</dd>
          </dl>
        )}
      </div>
    </section>
  );
}

/** The output of the source task, with the figure marked where it appears. */
function TaskSource({ taskId, value }: { taskId: string; value: string }) {
  const { t } = useT();
  const name = useAgentName();
  const [task, setTask] = useState<Task | null | undefined>(undefined);
  useEffect(() => {
    let live = true;
    call<Task>("GET", `/tasks/${encodeURIComponent(taskId)}`).then((x) => { if (live) setTask(x && x.id ? x : null); }).catch(() => { if (live) setTask(null); });
    return () => { live = false; };
  }, [taskId]);
  if (task === undefined) return <p className="text-[11.5px] text-mute">{t("evidence.task.loading")}</p>;
  if (!task) return <p className="text-[11.5px] text-mute">{t("evidence.task.missing")}</p>;
  const lines = [task.output?.summary ?? "", ...(task.output?.findings ?? []),
    ...Object.entries(task.output?.metrics ?? {}).map(([k, v]) => `${k.replace(/_/g, " ")}: ${v}`)].filter(Boolean);
  return (
    <div data-testid="evidence-task" className="text-[12px] text-ink2">
      <div className="font-medium text-ink">{task.title}</div>
      <div className="mb-1 text-[11px] text-mute">{name(task.agent_id)}</div>
      <ul className="space-y-1">
        {lines.map((l, i) => <li key={i} className="leading-snug"><Marked text={l} value={value} /></li>)}
      </ul>
    </div>
  );
}

function Marked({ text, value }: { text: string; value: string }) {
  const target = figureOf(value);
  const parts = useMemo(() => {
    if (!target) return [{ text, hit: false }];
    const out: { text: string; hit: boolean }[] = [];
    let at = 0;
    for (const f of findFigures(text)) {
      if (!sameFigure(target, f) && !(target.pct && !f.pct && Math.abs(target.n / 100 - f.n) < 1e-9)) continue;
      if (f.start > at) out.push({ text: text.slice(at, f.start), hit: false });
      out.push({ text: text.slice(f.start, f.end), hit: true });
      at = f.end;
    }
    if (at < text.length) out.push({ text: text.slice(at), hit: false });
    return out;
  }, [text, target]);
  return <>{parts.map((p, i) => p.hit ? <mark key={i} className="rounded-sm bg-amber-200/70 px-0.5 text-ink">{p.text}</mark> : <span key={i}>{p.text}</span>)}</>;
}

/** The cited cell inside its sheet, highlighted and scrolled into view (read-only). */
function CellSource({ artifactId, sheet, cell, cellValue }: { artifactId: string; sheet: string; cell: string; cellValue?: string }) {
  const { t } = useT();
  const art = useArtifacts((s) => s.artifacts[artifactId]);
  const [failed, setFailed] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let live = true;
    useArtifacts.setState({ anchor: { artifactId, anchor: { sheet, range: cell }, n: Date.now() } });
    useArtifacts.getState().ensureContent(artifactId).then(() => {
      if (live && !useArtifacts.getState().artifacts[artifactId]?.content) setFailed(true);
    });
    return () => { live = false; };
  }, [artifactId, sheet, cell]);
  useEffect(() => {
    if (!art?.content) return;
    const id = window.setTimeout(() => box.current?.querySelector(`[data-cell="${cell}"]`)?.scrollIntoView({ block: "center", inline: "center" }), 60);
    return () => window.clearTimeout(id);
  }, [art?.content, cell]);
  return (
    <div className="space-y-1.5">
      <div className="font-mono text-[11.5px] text-ink">{t("evidence.cell.address", { title: art?.title ?? artifactId, sheet, cell, value: cellValue ?? "" })}</div>
      {art?.content ? (
        <div ref={box} data-testid="evidence-sheet" className="h-56 overflow-hidden rounded-md border border-line">
          <SheetEditor art={art} readOnly />
        </div>
      ) : <p className="text-[11.5px] text-mute">{failed ? t("evidence.cell.missing") : t("evidence.cell.loading")}</p>}
    </div>
  );
}
