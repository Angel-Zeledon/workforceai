"use client";
import { useEffect, useMemo, useState } from "react";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useAgentColor } from "@/components/ui";
import {
  addr, cellValue, colName, formatValue, parseAddr, recalcSheets, refAlias,
  type CellValue, type RefResolver, type SheetContent,
} from "@/lib/sheetCalc";

/** Builds the AIW_REF resolver: reads cached/computed values of other loaded artifacts (sec. 5.9). */
export function useRefResolver(content: SheetContent | undefined): RefResolver {
  const artifacts = useArtifacts((s) => s.artifacts);
  const ensure = useArtifacts((s) => s.ensureContent);
  useEffect(() => { content?.imports?.forEach((i) => ensure(i.artifact_id)); }, [content?.imports, ensure]);
  return useMemo(() => {
    const resolve: RefResolver = (artifactId, ref) => {
      const src = artifacts[artifactId];
      const c = src?.content as SheetContent | undefined;
      if (!c || !c.sheets) return undefined;
      const i = ref.indexOf("!");
      const name = i >= 0 ? ref.slice(0, i) : undefined;
      const sheet = c.sheets.find((s) => !name || s.name === name) ?? c.sheets[0];
      const [a, b] = ref.slice(i + 1).split(":");
      const pa = parseAddr(a); if (!pa) return undefined;
      const ctx = { sheets: c.sheets, sheet, imports: c.imports, resolve };
      if (!b) return cellValue(ctx, addr(pa.c, pa.r));
      const pb = parseAddr(b); if (!pb) return undefined;
      const out: CellValue[][] = [];
      for (let r = pa.r; r <= pb.r; r++) { const row: CellValue[] = []; for (let cc = pa.c; cc <= pb.c; cc++) row.push(cellValue(ctx, addr(cc, r))); out.push(row); }
      return out;
    };
    return resolve;
  }, [artifacts]);
}

const MAX_COLS = 26;
const MAX_ROWS = 200;
const rangeCells = (range?: string): Set<string> => {
  const out = new Set<string>();
  if (!range) return out;
  const [a, b] = range.split(":"); const pa = parseAddr(a); const pb = parseAddr(b || a);
  if (!pa || !pb) return out;
  for (let r = Math.min(pa.r, pb.r); r <= Math.max(pa.r, pb.r); r++) for (let c = Math.min(pa.c, pb.c); c <= Math.max(pa.c, pb.c); c++) out.add(addr(c, r));
  return out;
};

export function SheetEditor({ art, readOnly, compact }: { art: StoredArtifact; readOnly?: boolean; compact?: boolean }) {
  const { t } = useT();
  const content = art.content as SheetContent | undefined;
  const edit = useArtifacts((s) => s.edit);
  const anchor = useArtifacts((s) => s.anchor);
  const focus = useArtifacts((s) => s.focus[art.id]);
  const deskId = useArtifacts((s) => s.deskId);
  const openTab = useArtifacts((s) => s.openTab);
  const agentColor = useAgentColor();
  const resolve = useRefResolver(content);
  const [si, setSi] = useState(0);
  const [sel, setSel] = useState("A1");
  const [draft, setDraft] = useState<string | null>(null);
  const locked = !!readOnly || art.locked;

  const sheet = content?.sheets?.[Math.min(si, (content?.sheets?.length ?? 1) - 1)];
  // jump to the anchored range when another artifact links here (sec. 2.3b)
  useEffect(() => {
    if (!anchor || anchor.artifactId !== art.id || !content) return;
    const a = anchor.anchor;
    if (a?.sheet) { const i = content.sheets.findIndex((s) => s.name === a.sheet); if (i >= 0) setSi(i); }
    if (a?.range) setSel(a.range.split(":")[0]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [anchor?.n, art.id]);

  const hl = useMemo(() => (anchor && anchor.artifactId === art.id ? rangeCells(anchor.anchor?.range) : new Set<string>()), [anchor, art.id]);
  const focusCells = useMemo(() => {
    if (!focus?.region || !sheet) return new Set<string>();
    const [sn, rg] = focus.region.includes("!") ? focus.region.split("!") : [sheet.name, focus.region];
    return sn === sheet.name ? rangeCells(rg) : new Set<string>();
  }, [focus, sheet]);

  if (!content || !sheet) return <div className="p-4 text-xs text-mute">…</div>;
  const ctx = { sheets: content.sheets, sheet, imports: content.imports, resolve };
  const used = Object.keys(sheet.cells).map((a) => parseAddr(a)).filter(Boolean) as { c: number; r: number }[];
  const cols = Math.min(MAX_COLS, Math.max(compact ? 3 : 8, ...used.map((u) => u.c + 2)));
  const rows = Math.min(MAX_ROWS, Math.max(compact ? 6 : 24, ...used.map((u) => u.r + 4)));
  const shownRows = compact ? Math.min(rows, Math.max(6, ...used.map((u) => u.r + 2))) : rows;
  const raw = (a: string) => { const c = sheet.cells[a]; return c ? (c.f ?? (c.v === null || c.v === undefined ? "" : String(c.v))) : ""; };

  const commit = (a: string, text: string) => {
    if (locked || text === raw(a)) { setDraft(null); return; }
    const cells = { ...sheet.cells };
    const prev = cells[a];
    if (text === "") delete cells[a];
    else if (text.startsWith("=")) cells[a] = { ...prev, f: text, v: 0 };
    else if (text.trim() !== "" && !Number.isNaN(Number(text))) cells[a] = { fmt: prev?.fmt, v: Number(text) };
    else cells[a] = { v: text };
    const sheets = content.sheets.map((s) => (s === sheet ? { ...s, cells } : s));
    edit(art.id, recalcSheets({ ...content, sheets }, resolve), `${sheet.name}!${a}`);
    setDraft(null);
  };
  const move = (dc: number, dr: number) => {
    const p = parseAddr(sel) ?? { c: 0, r: 0 };
    setSel(addr(Math.max(0, Math.min(cols - 1, p.c + dc)), Math.max(0, Math.min(rows - 1, p.r + dr))));
  };
  const selVal = draft ?? raw(sel);

  return (
    <div data-testid="art-sheet" className="flex h-full min-h-0 flex-col">
      {!compact && (
        <div className="flex items-center gap-2 border-b border-line bg-panel2/60 px-3 py-1.5">
          <input data-testid="art-sheet-namebox" aria-label={t("sheet.namebox")} value={sel} onChange={(e) => setSel(e.target.value.toUpperCase())}
            className="w-16 rounded-lg border border-line bg-panel px-2 py-1 text-center font-mono text-[11px] text-ink outline-none focus:border-accent" />
          <span className="font-mono text-xs text-mute">fx</span>
          <input data-testid="art-sheet-formulabar" aria-label={t("sheet.formulabar")} value={selVal} disabled={locked} onChange={(e) => setDraft(e.target.value)}
            onBlur={() => { if (draft !== null) commit(sel, draft); }}
            onKeyDown={(e) => { if (e.key === "Enter") { commit(sel, selVal); move(0, 1); } if (e.key === "Escape") setDraft(null); }}
            className="min-w-0 flex-1 rounded-lg border border-line bg-panel px-2 py-1 font-mono text-[11px] text-ink outline-none focus:border-accent disabled:opacity-60" />
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto bg-panel">
        <table className="border-collapse text-[11.5px]" style={{ tableLayout: "fixed" }}>
          <thead className="sticky top-0 z-10">
            <tr>
              <th className="w-9 border border-line bg-panel2" />
              {Array.from({ length: cols }, (_, c) => (
                <th key={c} className="border border-line bg-panel2 px-1 py-0.5 text-[10px] font-semibold text-mute" style={{ width: sheet.colw?.[colName(c)] ?? 96 }}>{colName(c)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {Array.from({ length: shownRows }, (_, r) => (
              <tr key={r}>
                <th className="border border-line bg-panel2 px-1 text-[10px] font-semibold text-mute">{r + 1}</th>
                {Array.from({ length: cols }, (_, c) => {
                  const a = addr(c, r);
                  const cell = sheet.cells[a];
                  const val = cell ? cellValue(ctx, a) : null;
                  const isSel = a === sel;
                  const alias = refAlias(cell?.f);
                  const err = typeof val === "string" && val.startsWith("#");
                  return (
                    <td key={a} data-cell={a}
                      onClick={(e) => {
                        setSel(a); setDraft(null);
                        if (alias && (e.ctrlKey || e.metaKey) && deskId) {
                          const imp = content.imports?.find((i) => i.alias === alias);
                          const m = /AIW_REF\(\s*"[^"]+"\s*,\s*"([^"]+)"/i.exec(cell?.f ?? "");
                          if (imp && m) { const [sn, rg] = m[1].includes("!") ? m[1].split("!") : [undefined, m[1]]; openTab(deskId, imp.artifact_id, { sheet: sn, range: rg }); }
                        }
                      }}
                      className={`relative h-6 border border-line p-0 ${hl.has(a) ? "bg-amber-200/70" : focusCells.has(a) ? "bg-accent/10" : ""} ${isSel ? "outline outline-2 outline-accent" : ""}`}
                      style={focusCells.has(a) && focus ? { boxShadow: `inset 0 0 0 2px ${agentColor(focus.agent_id)}` } : undefined}>
                      {isSel && !locked ? (
                        <input autoFocus value={selVal} onChange={(e) => setDraft(e.target.value)} onBlur={() => { if (draft !== null) commit(a, draft); }}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") { e.preventDefault(); commit(a, selVal); move(0, 1); }
                            else if (e.key === "Tab") { e.preventDefault(); commit(a, selVal); move(e.shiftKey ? -1 : 1, 0); }
                            else if (e.key === "ArrowDown" && draft === null) { e.preventDefault(); move(0, 1); }
                            else if (e.key === "ArrowUp" && draft === null) { e.preventDefault(); move(0, -1); }
                            else if (e.key === "Escape") setDraft(null);
                          }}
                          className="h-full w-full bg-panel px-1.5 font-mono text-[11.5px] text-ink outline-none" />
                      ) : (
                        <div className={`h-full truncate px-1.5 leading-6 ${typeof val === "number" ? "text-right font-mono" : ""} ${err ? "text-red-600" : "text-ink"}`}>
                          {alias && <span data-testid={`art-sheet-ref-${a}`} title={t("sheet.linkedCell")} className="mr-1 text-[9px] font-semibold text-accent">&#8599;</span>}
                          {formatValue(val, cell?.fmt)}
                        </div>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!compact && content.sheets.length > 1 && (
        <div className="flex gap-1 border-t border-line bg-panel2/60 px-2 py-1">
          {content.sheets.map((s, i) => (
            <button key={s.id} onClick={() => setSi(i)} className={`rounded-md px-3 py-0.5 text-[11px] font-medium ${i === si ? "bg-accent-soft text-accent" : "text-mute hover:text-ink"}`}>{s.name}</button>
          ))}
        </div>
      )}
    </div>
  );
}
