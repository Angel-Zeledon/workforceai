"use client";
import { useT } from "@/lib/i18n";
import { fileKind, fmtBytes, useFileViewer, type FileKind } from "@/lib/fileView";
import type { FileRef } from "@/lib/types";

const BADGE: Record<FileKind, { label: string; cls: string }> = {
  markdown: { label: "MD", cls: "bg-info/15 text-info" },
  csv: { label: "CSV", cls: "bg-ok/15 text-ok" },
  sheet: { label: "XLS", cls: "bg-ok/15 text-ok" },
  doc: { label: "DOC", cls: "bg-info/15 text-info" },
  json: { label: "JSON", cls: "bg-warn/15 text-warn" },
  code: { label: "</>", cls: "bg-accent/15 text-accent-hover" },
  text: { label: "TXT", cls: "bg-mute/15 text-ink2" },
  pdf: { label: "PDF", cls: "bg-err/15 text-err" },
  image: { label: "IMG", cls: "bg-accent/15 text-accent-hover" },
  other: { label: "FILE", cls: "bg-mute/15 text-ink2" },
};

/** Type badge of a file (shared by cards and the viewer header). */
export function FileBadge({ kind, size = 36 }: { kind: FileKind; size?: number }) {
  const b = BADGE[kind];
  return (
    <span aria-hidden className={`flex shrink-0 items-center justify-center rounded-lg font-mono text-[10px] font-bold ${b.cls}`} style={{ width: size, height: size }}>{b.label}</span>
  );
}

/** A file in the conversation (what an agent produced or the user attached): click to open it in the side panel. */
export function FileCard({ f, mine = false }: { f: FileRef; mine?: boolean }) {
  const { t } = useT();
  const open = useFileViewer((s) => s.open);
  const active = useFileViewer((s) => s.ref?.path === f.path);
  const kind = fileKind(f.name, f.mime);
  return (
    <button
      type="button" data-testid="file-card" data-path={f.path} data-kind={kind} data-active={active} onClick={() => open(f)}
      aria-label={t("fileviewer.openFile", { name: f.name })}
      className={`group mt-1 flex w-full min-w-[220px] max-w-[320px] items-center gap-2.5 rounded-xl border bg-panel px-2.5 py-2 text-left shadow-pop transition hover:border-accent ${active ? "border-accent" : "border-line-strong"}`}
    >
      <FileBadge kind={kind} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[12.5px] font-semibold text-ink">{f.name}</span>
        <span className="block truncate text-[10.5px] text-mute">
          {t(`fileviewer.kind.${kind}`)}{f.size ? ` · ${fmtBytes(f.size)}` : ""}{f.version ? ` · v${f.version}` : ""}{mine ? "" : f.action === "write" ? ` · ${t("fileviewer.created")}` : ""}
        </span>
      </span>
      <span className="shrink-0 rounded-md border border-line px-2 py-1 text-[11px] font-semibold text-accent-hover group-hover:border-accent">{t("fileviewer.open")}</span>
    </button>
  );
}

export function FileCards({ files, mine = false }: { files?: FileRef[]; mine?: boolean }) {
  if (!files?.length) return null;
  return <div data-testid="file-cards" className="flex w-full flex-col items-stretch">{files.map((f) => <FileCard key={f.file_id || f.path} f={f} mine={mine} />)}</div>;
}
