"use client";
import { useEffect, useRef, useState } from "react";
import { MOCK } from "@/lib/config";
import { artifactApi, PDF_MAX_BYTES, useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useConnections } from "@/lib/connections/store";
import { useT } from "@/lib/i18n";

interface Annotation { id: string; page: number; text: string; author?: { kind: string; id: string } }
interface PdfContent { schema: "aiw.pdf/1"; blob_id: string | null; pages: number; annotations: Annotation[]; filename?: string; size_bytes?: number }

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
  const res = await artifactApi.pdfBlob(blobId);
  return res ? res.arrayBuffer() : null;
}

/** Client-side pre-check (the server repeats both): size cap and the "%PDF-" signature. */
async function precheck(file: File): Promise<string | null> {
  if (file.size > PDF_MAX_BYTES) return "too_large";
  const head = new Uint8Array(await file.slice(0, 5).arrayBuffer());
  return String.fromCharCode(...head) === "%PDF-" ? null : "not_a_pdf";
}

const UPLOAD_ERRORS: Record<string, string> = {
  too_large: "pdf.errTooLarge", not_a_pdf: "pdf.errNotPdf", unsupported_media_type: "pdf.errNotPdf",
  read_only_mode: "pdf.errReadOnly", conflict: "pdf.errConflict", "409": "pdf.errConflict", "403": "pdf.errForbidden",
};

/** Upload / replace / download bar of a pdf artifact. Hidden for read-only embeds; disabled in demo mode and read-only mode. */
function PdfToolbar({ art, blobId, filename }: { art: StoredArtifact; blobId: string | null; filename?: string }) {
  const { t } = useT();
  const input = useRef<HTMLInputElement>(null);
  const orgReadOnly = useConnections((s) => s.controls?.mode === "read_only");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const disabled = MOCK || orgReadOnly || busy || art.status === "approved" || art.status === "sent" || art.status === "archived";
  const why = MOCK ? t("pdf.demoUnavailable") : orgReadOnly ? t("pdf.errReadOnly") : undefined;

  const upload = async (file: File | undefined) => {
    if (!file) return;
    setMsg(null);
    const bad = await precheck(file);
    if (bad) { setMsg({ kind: "error", text: t(UPLOAD_ERRORS[bad], { mb: PDF_MAX_BYTES >> 20 }) }); return; }
    setBusy(true);
    try {
      const r = await artifactApi.uploadPdf(art.id, file, file.name);
      await useArtifacts.getState().ensureContent(art.id, true);
      setMsg({ kind: "ok", text: t("pdf.uploaded", { version: r.version }) });
    } catch (e) {
      const code = e instanceof Error ? e.message : "";
      setMsg({ kind: "error", text: t(UPLOAD_ERRORS[code] ?? "pdf.errUpload", { mb: PDF_MAX_BYTES >> 20 }) });
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  };
  const download = async () => {
    if (!blobId) return;
    try {
      const res = await artifactApi.pdfBlob(blobId, true);
      if (!res) return;
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a"); a.href = url; a.download = filename || `${art.title}.pdf`; a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch { setMsg({ kind: "error", text: t("pdf.error") }); }
  };

  return (
    <div className="mx-auto mb-3 flex max-w-[720px] flex-wrap items-center gap-2 text-[11px]">
      <input ref={input} data-testid="pdf-upload-input" type="file" accept="application/pdf,.pdf" className="hidden" disabled={disabled}
        onChange={(e) => void upload(e.target.files?.[0])} />
      <button type="button" data-testid="pdf-upload" disabled={disabled} title={why} onClick={() => input.current?.click()}
        className="rounded-md border border-accent bg-accent px-3 py-1 font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50">
        {busy ? t("pdf.uploading") : blobId ? t("pdf.replace") : t("pdf.upload")}
      </button>
      {blobId && !MOCK && (
        <button type="button" data-testid="pdf-download" onClick={() => void download()} className="rounded-md border border-line px-3 py-1 font-semibold text-ink hover:border-accent">
          {t("pdf.download")}
        </button>
      )}
      {filename && <span data-testid="pdf-filename" className="truncate font-mono text-[10px] text-mute">{filename}</span>}
      {msg && <span data-testid="pdf-upload-status" data-kind={msg.kind} role={msg.kind === "error" ? "alert" : "status"}
        className={msg.kind === "error" ? "text-red-600" : "text-emerald-700"}>{msg.text}</span>}
      {!msg && why && <span className="text-mute">{why}</span>}
    </div>
  );
}

/** PDF viewer: pdf.js renders each page lazily when it scrolls into view; annotations are overlaid. The file is uploaded or replaced from the toolbar. */
export function PdfView({ art, readOnly }: { art: StoredArtifact; readOnly?: boolean }) {
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
    <div data-testid="art-pdf" data-blob={blobId ?? ""} className="h-full overflow-auto bg-panel2/50 p-4">
      {!readOnly && <PdfToolbar art={art} blobId={blobId} filename={c.filename} />}
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
