"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { loadPdfjs, PageCanvas, type PdfDoc } from "../workspace/editors/PdfView";

const num = (s: string) => (s.trim() !== "" && /^-?[\d.,]+%?$/.test(s.trim()) ? Number(s.replace(/[,%]/g, "")) : NaN);

/** Sortable table (click a header). Cells are text, never HTML. */
export function TableView({ rows, testId = "viewer-table" }: { rows: string[][]; testId?: string }) {
  const { t } = useT();
  const [sort, setSort] = useState<{ col: number; dir: 1 | -1 } | null>(null);
  const [head, ...body] = rows;
  const sorted = useMemo(() => {
    if (!sort) return body;
    const { col, dir } = sort;
    return [...body].sort((a, b) => {
      const x = a[col] ?? "", y = b[col] ?? "";
      const nx = num(x), ny = num(y);
      return (!isNaN(nx) && !isNaN(ny) ? nx - ny : x.localeCompare(y)) * dir;
    });
  }, [body, sort]);
  if (!head) return <div className="p-4 text-xs text-mute">{t("fileviewer.empty")}</div>;
  return (
    <div className="overflow-auto">
      <table data-testid={testId} className="w-full border-collapse text-[12px]">
        <thead className="sticky top-0 bg-panel">
          <tr>
            <th className="w-8 border-b border-line-strong px-2 py-1 text-right font-mono text-[10px] font-normal text-mute">#</th>
            {head.map((c, j) => (
              <th key={j} aria-sort={sort?.col === j ? (sort.dir === 1 ? "ascending" : "descending") : "none"} className="border-b border-line-strong p-0 text-left font-semibold">
                <button type="button" data-testid={`viewer-sort-${j}`} onClick={() => setSort((s) => (s?.col === j ? (s.dir === 1 ? { col: j, dir: -1 } : null) : { col: j, dir: 1 }))}
                  title={t("fileviewer.sort")} className="flex w-full items-center gap-1 px-2 py-1 text-left hover:bg-panel2">
                  {c}<span className="text-[9px] text-mute">{sort?.col === j ? (sort.dir === 1 ? "▲" : "▼") : ""}</span>
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.map((r, i) => (
            <tr key={i} className="border-b border-line hover:bg-panel2/60">
              <td className="px-2 py-1 text-right font-mono text-[10px] text-mute">{i + 1}</td>
              {head.map((_, j) => <td key={j} className={`px-2 py-1 ${!isNaN(num(r[j] ?? "")) ? "text-right font-mono" : ""}`}>{r[j] ?? ""}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Text with line numbers (code, logs, plain text). */
export function CodeView({ text, testId = "viewer-code" }: { text: string; testId?: string }) {
  const lines = text.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return (
    <div data-testid={testId} className="overflow-auto font-mono text-[12px] leading-[1.55]">
      <table className="border-collapse">
        <tbody>
          {lines.map((l, i) => (
            <tr key={i}>
              <td className="select-none px-2 text-right align-top text-[10.5px] text-mute">{i + 1}</td>
              <td className="whitespace-pre pr-4">{l || " "}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function JsonNode({ k, v, depth }: { k?: string; v: unknown; depth: number }) {
  const [open, setOpen] = useState(depth < 2);
  const isObj = v !== null && typeof v === "object";
  const label = k !== undefined ? <span className="text-info">{JSON.stringify(k)}<span className="text-mute">: </span></span> : null;
  if (!isObj) {
    const cls = typeof v === "string" ? "text-ok" : typeof v === "number" ? "text-warn" : "text-accent";
    return <div className="pl-4">{label}<span className={cls}>{JSON.stringify(v)}</span></div>;
  }
  const entries: [string, unknown][] = Array.isArray(v) ? v.map((x, i) => [String(i), x]) : Object.entries(v as object);
  const [o, c] = Array.isArray(v) ? ["[", "]"] : ["{", "}"];
  return (
    <div className="pl-4">
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="text-left hover:bg-panel2">
        <span className="inline-block w-3 text-[9px] text-mute">{open ? "▼" : "▶"}</span>{label}<span className="text-mute">{o}{!open && ` ${entries.length} … `}{!open && c}</span>
      </button>
      {open && <>{entries.map(([ek, ev]) => <JsonNode key={ek} k={Array.isArray(v) ? undefined : ek} v={ev} depth={depth + 1} />)}<div className="pl-3 text-mute">{c}</div></>}
    </div>
  );
}

/** Collapsible JSON; if it does not parse it is shown as plain code. */
export function JsonView({ text }: { text: string }) {
  const parsed = useMemo(() => { try { return { ok: true as const, v: JSON.parse(text) as unknown }; } catch { return { ok: false as const }; } }, [text]);
  if (!parsed.ok) return <CodeView text={text} />;
  return <div data-testid="viewer-json" className="-ml-4 overflow-auto font-mono text-[12px] leading-[1.6]"><JsonNode v={parsed.v} depth={0} /></div>;
}

/** Image from our own API (fetched with the session, shown from a blob URL; the server sends nosniff). */
export function ImageView({ blob, name }: { blob: Blob; name: string }) {
  const [url, setUrl] = useState("");
  useEffect(() => {
    const u = URL.createObjectURL(blob);
    setUrl(u);
    return () => URL.revokeObjectURL(u);
  }, [blob]);
  // eslint-disable-next-line @next/next/no-img-element
  return <div className="flex justify-center p-3">{url && <img data-testid="viewer-image" src={url} alt={name} className="max-h-full max-w-full rounded-lg border border-line bg-white object-contain" />}</div>;
}

/** PDF from bytes with the same pdf.js setup as PDF artifacts (lazy pages, no scripting). */
export function PdfBytesView({ blob }: { blob: Blob }) {
  const { t } = useT();
  const [doc, setDoc] = useState<PdfDoc | null>(null);
  const [state, setState] = useState<"loading" | "ok" | "error">("loading");
  useEffect(() => {
    let dead = false;
    let task: { destroy(): Promise<void> } | null = null;
    setState("loading");
    (async () => {
      const bytes = new Uint8Array(await blob.arrayBuffer());
      const lib = await loadPdfjs();
      const lt = lib.getDocument({ data: bytes, isEvalSupported: false, enableScripting: false } as Parameters<typeof lib.getDocument>[0]);
      task = lt;
      const loaded = (await lt.promise) as unknown as PdfDoc;
      if (dead) return;
      setDoc(loaded); setState("ok");
    })().catch(() => { if (!dead) setState("error"); });
    return () => { dead = true; void task?.destroy().catch(() => {}); };
  }, [blob]);
  const total = doc ? Math.min(doc.numPages, 200) : 0;
  return (
    <div data-testid="viewer-pdf" className="p-3">
      {state === "loading" && <p className="text-center text-[11px] text-mute">{t("pdf.loading")}</p>}
      {state === "error" && <p role="alert" className="text-center text-[11px] text-err">{t("pdf.error")}</p>}
      <div className="mx-auto flex max-w-[720px] flex-col gap-3">
        {doc && Array.from({ length: total }, (_, i) => <PageCanvas key={i} doc={doc} n={i + 1} total={total} notes={[]} />)}
      </div>
    </div>
  );
}

/** Line diff between two versions (+ added, - removed). */
export function DiffView({ lines, added, removed }: { lines: { op: string; text: string }[]; added: number; removed: number }) {
  const { t } = useT();
  return (
    <div data-testid="viewer-diff" className="font-mono text-[12px] leading-[1.5]">
      <div className="mb-2 font-sans text-[11px] text-mute">{t("fileviewer.diffSummary", { added, removed })}</div>
      {lines.map((l, i) => (
        <div key={i} data-op={l.op} className={`whitespace-pre-wrap break-words px-2 ${l.op === "+" ? "bg-ok/10 text-ok" : l.op === "-" ? "bg-err/10 text-err" : "text-ink2"}`}>
          <span className="mr-2 select-none text-mute">{l.op === "+" || l.op === "-" ? l.op : " "}</span>{l.text || " "}
        </div>
      ))}
    </div>
  );
}
