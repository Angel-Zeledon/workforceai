"use client";
import { useArtifact, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { AgendaView } from "./editors/AgendaView";
import { BoardView } from "./editors/BoardView";
import { ChartView } from "./editors/ChartView";
import { DocEditor } from "./editors/DocEditor";
import { FormView } from "./editors/FormView";
import { InboxView } from "./editors/InboxView";
import { PdfView } from "./editors/PdfView";
import { SheetEditor } from "./editors/SheetEditor";
import { TableEditor } from "./editors/TableEditor";
import { KindIcon } from "./kindMeta";

/** Renders the right editor/viewer for an artifact kind. */
export function ArtifactBody({ art, readOnly, compact, depth = 0 }: { art: StoredArtifact; readOnly?: boolean; compact?: boolean; depth?: number }) {
  switch (art.kind) {
    case "sheet": return <SheetEditor art={art} readOnly={readOnly} compact={compact} />;
    case "doc": return <DocEditor art={art} readOnly={readOnly} compact={compact} depth={depth} />;
    case "table": return <TableEditor art={art} readOnly={readOnly} compact={compact} />;
    case "board": return <BoardView art={art} readOnly={readOnly} />;
    case "chart": return <ChartView art={art} compact={compact} />;
    case "pdf": return <PdfView art={art} />;
    case "form": return <FormView art={art} />;
    case "inbox": return <InboxView art={art} />;
    case "agenda": return <AgendaView art={art} readOnly={readOnly} />;
  }
}

const MAX_EMBED_DEPTH = 3;

/** Embedded artifact inside a doc (sec. 3.11): read-only, live, depth-limited (the backend also forbids cycles). */
export function ArtifactEmbed({ id, height = 220, depth }: { id: string; height?: number; depth: number }) {
  const { t } = useT();
  const art = useArtifact(id);
  if (depth > MAX_EMBED_DEPTH) return <div className="rounded-lg border border-dashed border-line px-3 py-2 text-[11px] text-mute">{t("embed.tooDeep")}</div>;
  if (!art) return <div className="rounded-lg border border-dashed border-line px-3 py-2 text-[11px] text-mute">{t("embed.locked")}</div>;
  return (
    <figure className="overflow-hidden rounded-xl border border-line bg-panel2/40">
      <figcaption className="flex items-center gap-1.5 border-b border-line px-3 py-1 text-[11px] font-semibold text-mute"><KindIcon kind={art.kind} size={13} />{art.title}</figcaption>
      <div style={{ height }} className="overflow-hidden"><ArtifactBody art={art} readOnly compact depth={depth} /></div>
    </figure>
  );
}
