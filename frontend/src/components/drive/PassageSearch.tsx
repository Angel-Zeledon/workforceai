"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { citeOf, docSearch, indexBacklog, snippetParts, type IndexStatus, type Passage, type PassageResult } from "@/lib/docsearch";
import { Btn, Empty } from "../ui";

/** Snippet with the matched words highlighted (the text is rendered as text, never as HTML). */
export function SnippetText({ text }: { text: string }) {
  return (
    <>
      {snippetParts(text).map((p, i) => (p.hit ? <mark key={i} className="rounded bg-accent/15 px-0.5 text-ink">{p.text}</mark> : <span key={i}>{p.text}</span>))}
    </>
  );
}

/** Results of "search by meaning": one card per cited passage (file, page or line, version, snippet). */
export function PassageResults({ result, onOpen, onClose }: { result: PassageResult; onOpen: (p: Passage) => void; onClose: () => void }) {
  const { t } = useT();
  const modeLabel = result.mode === "hybrid" ? t("drive.meaning.modeHybrid") : result.mode === "semantic" ? t("drive.meaning.modeSemantic") : t("drive.meaning.modeKeyword");
  return (
    <div data-testid="drive-passages" className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-ink">{t("drive.meaning.results", { n: result.hits.length })} <span className="ml-1 text-[11px] font-normal text-mute">· {modeLabel}</span></h2>
        <Btn onClick={onClose}>{t("drive.close")}</Btn>
      </div>
      {result.mode === "keyword" && result.note && <p data-testid="drive-passages-note" className="text-[11px] text-mute">{t("drive.meaning.keywordOnly")}</p>}
      {!result.hits.length && <Empty>{t("drive.meaning.none")}</Empty>}
      {result.hits.map((h) => {
        const cite = citeOf(h, t("drive.meaning.page"), t("drive.meaning.line"));
        return (
          <button type="button" key={`${h.file_id}:${h.start}`} data-testid="drive-passage" onClick={() => onOpen(h)}
            className="block w-full rounded-lg border border-line bg-panel px-3 py-2 text-left hover:border-accent">
            <div className="flex items-baseline gap-2">
              <span className="truncate text-sm font-medium text-ink">{h.name}</span>
              {cite && <span className="shrink-0 rounded bg-accent/10 px-1.5 text-[10.5px] font-semibold text-accent">{cite}</span>}
              <span className="shrink-0 text-[10.5px] text-mute">v{h.version}</span>
            </div>
            <div className="truncate text-[11px] text-mute">{h.path}{h.heading ? ` · ${h.heading}` : ""}</div>
            {h.snippet && <div className="mt-1 line-clamp-3 text-xs text-ink/80"><SnippetText text={h.snippet} /></div>}
          </button>
        );
      })}
    </div>
  );
}

/** Small, unobtrusive index state of a folder: "Indexing 12 files..." while the backlog drains. */
export function IndexStatusChip({ path, refresh }: { path: string; refresh?: string }) {
  const { t } = useT();
  const [st, setSt] = useState<IndexStatus | null>(null);
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      const s = await docSearch.status(path);
      if (!alive) return;
      setSt(s);
      if (s?.enabled && indexBacklog(s.status) > 0) timer = setTimeout(load, 8000); // poll only while something is pending
    };
    load();
    return () => { alive = false; if (timer) clearTimeout(timer); };
  }, [path, refresh]);
  if (!st?.enabled || !st.status) return null;
  const n = indexBacklog(st.status);
  const big = st.status.too_large;
  if (!n && !big) return null;
  return (
    <span data-testid="drive-index-status" title={st.embeddings.available ? t("drive.index.semanticOn") : t("drive.index.keywordOnly")}
      className="inline-flex items-center gap-1 rounded-full bg-panel2 px-2 py-0.5 text-[10.5px] text-mute">
      {n > 0 && <><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent" />{t("drive.index.indexing", { n })}</>}
      {n > 0 && big > 0 && " · "}
      {big > 0 && t("drive.index.tooLarge", { n: big })}
    </span>
  );
}
