"use client";
import { useEffect, useRef, useState } from "react";
import { API_URL, MOCK } from "@/lib/config";
import { authFetch } from "@/lib/session";
import type { StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";

interface Annotation { id: string; page: number; text: string; author?: { kind: string; id: string } }
interface PdfContent { schema: "aiw.pdf/1"; blob_id: string | null; pages: number; annotations: Annotation[] }

// Minimal structural types of the pdf.js objects used here (the library is loaded lazily, never at module scope).
interface PdfPage { getViewport(o: { scale: number }): { width: number; height: number }; render(o: { canvasContext: CanvasRenderingContext2D; viewport: unknown }): { promise: Promise<void>; cancel(): void } }
interface PdfDoc { numPages: number; getPage(n: number): Promise<PdfPage>; destroy(): Promise<void> }

const MAX_PAGES = 500;
let pdfjsPromise: Promise<typeof import("pdfjs-dist")> | null = null;

/** Loads pdf.js on first use. The worker is a bundled asset (no CDN): webpack emits it next to the app. */
function loadPdfjs() {
  pdfjsPromise ??= import("pdfjs-dist").then((lib) => {
    if (!lib.GlobalWorkerOptions.workerPort) {
      lib.GlobalWorkerOptions.workerPort = new Worker(new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url), { type: "module" });
    }
    return lib;
  });
  return pdfjsPromise;
}

function PageCanvas({ doc, n, notes, total }: { doc: PdfDoc; n: number; notes: Annotation[]; total: number }) {
  const host = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [visible, setVisible] = useState(false);
  const [ratio, setRatio] = useState(8.5 / 11);

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const io = new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) { setVisible(true); io.disconnect(); } }, { rootMargin: "300px" });
    io.observe(el);
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    if (!visible) return;
    let task: { cancel(): void } | null = null;
    let dead = false;
    (async () => {
      const page = await doc.getPage(n);
      const el = canvas.current;
      if (dead || !el) return;
      const base = page.getViewport({ scale: 1 });
      const scale = Math.min(2, (host.current?.clientWidth ?? base.width) / base.width) * (window.devicePixelRatio || 1);
      const vp = page.getViewport({ scale });
      el.width = Math.floor(vp.width); el.height = Math.floor(vp.height);
      setRatio(base.width / base.height);
      const ctx = el.getContext("2d");
      if (!ctx) return;
      const rt = page.render({ canvasContext: ctx, viewport: vp });
      task = rt;
      await rt.promise;
    })().catch(() => { /* cancelled render or destroyed document */ });
    return () => { dead = true; task?.cancel(); };
  }, [visible, doc, n]);

  return (
    <div ref={host} data-testid="pdf-page" data-page={n} className="relative w-full rounded-lg border border-line bg-white shadow-pop" style={{ aspectRatio: String(ratio) }}>
      <canvas ref={canvas} className="h-full w-full rounded-lg" />
      <span className="absolute bottom-2 right-3 rounded bg-white/80 px-1 text-[10px] font-semibold text-mute">{n} / {total}</span>
      {notes.map((a) => <div key={a.id} className="absolute left-4 right-4 top-1/2 rounded-lg border border-amber-400 bg-amber-100 px-2 py-1 text-[11px] text-ink">{a.text}</div>)}
    </div>
  );
}

/** Bytes of a stored PDF blob (GET /artifact-blobs/{id}); null in demo mode, where there is no blob storage. */
async function fetchBlob(blobId: string): Promise<ArrayBuffer | null> {
  if (MOCK) return null;
  const res = await authFetch(`${API_URL}/artifact-blobs/${encodeURIComponent(blobId)}`, { cache: "no-store" });
  if (!res.ok) throw new Error(String(res.status));
  return res.arrayBuffer();
}

/** PDF viewer: pdf.js renders each page lazily when it scrolls into view; annotations are overlaid. Read-only. */
export function PdfView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as PdfContent | undefined;
  const blobId = c?.blob_id ?? null;
  const [doc, setDoc] = useState<PdfDoc | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");

  useEffect(() => {
    if (!blobId) { setDoc(null); setState("idle"); return; }
    let dead = false;
    let loaded: PdfDoc | null = null;
    setState("loading");
    (async () => {
      const bytes = await fetchBlob(blobId);
      if (!bytes || dead) { if (!dead) setState("idle"); return; }
      const lib = await loadPdfjs();
      const task = lib.getDocument({ data: new Uint8Array(bytes), isEvalSupported: false, enableScripting: false } as Parameters<typeof lib.getDocument>[0]);
      loaded = (await task.promise) as unknown as PdfDoc;
      if (dead) { void loaded.destroy(); return; }
      setDoc(loaded); setState("idle");
    })().catch(() => { if (!dead) setState("error"); });
    return () => { dead = true; void loaded?.destroy(); };
  }, [blobId]);

  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  const total = doc ? Math.min(doc.numPages, MAX_PAGES) : c.pages;
  const notesOf = (n: number) => c.annotations.filter((a) => a.page === n);
  return (
    <div data-testid="art-pdf" className="h-full overflow-auto bg-panel2/50 p-4">
      <p className="mb-3 text-center text-[11px] text-mute" data-testid="pdf-status">
        {state === "loading" ? t("pdf.loading") : state === "error" ? t("pdf.error") : !blobId ? t("pdf.noFile") : t("pdf.note", { count: total })}
      </p>
      <div className="mx-auto flex max-w-[720px] flex-col gap-4">
        {doc
          ? Array.from({ length: total }, (_, i) => <PageCanvas key={i} doc={doc} n={i + 1} total={total} notes={notesOf(i + 1)} />)
          : Array.from({ length: c.pages }, (_, i) => (
            <div key={i} className="relative aspect-[8.5/11] rounded-lg border border-line bg-white shadow-pop">
              <div className="space-y-2 p-6">{[70, 95, 85, 90, 60].map((w, k) => <div key={k} className="h-2 rounded-full bg-mute/[0.12]" style={{ width: `${w}%` }} />)}</div>
              <span className="absolute bottom-2 right-3 text-[10px] font-semibold text-mute">{i + 1} / {c.pages}</span>
              {notesOf(i + 1).map((a) => <div key={a.id} className="absolute left-4 right-4 top-1/2 rounded-lg border border-amber-400 bg-amber-100 px-2 py-1 text-[11px] text-ink">{a.text}</div>)}
            </div>
          ))}
      </div>
    </div>
  );
}
