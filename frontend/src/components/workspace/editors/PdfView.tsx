"use client";
import type { StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";

interface PdfContent { schema: "aiw.pdf/1"; blob_id: string | null; pages: number; annotations: { id: string; page: number; text: string; author?: { kind: string; id: string } }[] }

/** Read-only PDF placeholder: page frames plus annotations. The pdf.js viewer (react-pdf) lands with blob storage (phase 4). */
export function PdfView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as PdfContent | undefined;
  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  return (
    <div data-testid="art-pdf" className="h-full overflow-auto bg-panel2/50 p-4">
      <p className="mb-3 text-center text-[11px] text-mute">{t("pdf.note", { count: c.pages })}</p>
      <div className="mx-auto flex max-w-[420px] flex-col gap-4">
        {Array.from({ length: c.pages }, (_, i) => {
          const notes = c.annotations.filter((a) => a.page === i + 1);
          return (
            <div key={i} className="relative aspect-[8.5/11] rounded-lg border border-line bg-white shadow-pop">
              <div className="space-y-2 p-6">{[70, 95, 85, 90, 60].map((w, k) => <div key={k} className="h-2 rounded-full bg-mute/[0.12]" style={{ width: `${w}%` }} />)}</div>
              <span className="absolute bottom-2 right-3 text-[10px] font-semibold text-mute">{i + 1} / {c.pages}</span>
              {notes.map((n) => <div key={n.id} className="absolute left-4 right-4 top-1/2 rounded-lg border border-amber-400 bg-amber-100 px-2 py-1 text-[11px] text-ink">{n.text}</div>)}
            </div>
          );
        })}
      </div>
    </div>
  );
}
