"use client";
import { useMemo, useState } from "react";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";

export interface TableColumn { key: string; label: string; type?: "text" | "number" | "money" | "date" | "select" | "checkbox" | "link"; options?: string[] }
export interface TableContent { schema: "aiw.table/1"; columns: TableColumn[]; rows: { id: string; cells: Record<string, string | number | boolean> }[] }

export function TableEditor({ art, readOnly, compact }: { art: StoredArtifact; readOnly?: boolean; compact?: boolean }) {
  const { t } = useT();
  const content = art.content as TableContent | undefined;
  const edit = useArtifacts((s) => s.edit);
  const [sortKey, setSortKey] = useState<string | null>(null);
  const [asc, setAsc] = useState(true);
  const [filter, setFilter] = useState("");
  const locked = !!readOnly || art.locked;

  const rows = useMemo(() => {
    if (!content) return [];
    const q = filter.trim().toLowerCase();
    let r = content.rows.filter((row) => !q || Object.values(row.cells).some((v) => String(v).toLowerCase().includes(q)));
    if (sortKey) r = [...r].sort((a, b) => {
      const x = a.cells[sortKey] ?? "", y = b.cells[sortKey] ?? "";
      const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
      return asc ? c : -c;
    });
    return compact ? r.slice(0, 8) : r;
  }, [content, filter, sortKey, asc, compact]);

  if (!content) return <div className="p-4 text-xs text-mute">…</div>;
  const setCell = (rowId: string, key: string, v: string | number | boolean) =>
    edit(art.id, { ...content, rows: content.rows.map((r) => (r.id === rowId ? { ...r, cells: { ...r.cells, [key]: v } } : r)) });
  const addRow = () => edit(art.id, { ...content, rows: [...content.rows, { id: `r${Date.now().toString(36)}`, cells: {} }] });
  const delRow = (id: string) => edit(art.id, { ...content, rows: content.rows.filter((r) => r.id !== id) });

  return (
    <div data-testid="art-table" className="flex h-full min-h-0 flex-col">
      {!compact && (
        <div className="flex items-center gap-2 border-b border-line bg-panel2/60 px-3 py-1.5">
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("table.filter")} className="w-48 rounded-md border border-line bg-panel px-3 py-1 text-[11px] text-ink outline-none focus:border-accent" />
          <span className="text-[11px] text-mute">{t("table.rows", { count: content.rows.length })}</span>
          {!locked && <button type="button" onClick={addRow} className="ml-auto rounded-md border border-accent bg-accent px-3 py-0.5 text-[11px] font-semibold text-white hover:bg-accent-hover">{t("table.addRow")}</button>}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto bg-panel">
        <table className="w-full border-collapse text-[12px]">
          <thead className="sticky top-0 z-10 bg-panel2">
            <tr>
              {content.columns.map((c) => (
                <th key={c.key} className="border-b-2 border-line px-3 py-2 text-left">
                  <button type="button" onClick={() => { if (sortKey === c.key) setAsc(!asc); else { setSortKey(c.key); setAsc(true); } }} className="text-[10px] font-semibold uppercase tracking-wide text-mute hover:text-ink">
                    {c.label}{sortKey === c.key ? (asc ? " ▲" : " ▼") : ""}
                  </button>
                </th>
              ))}
              {!compact && !locked && <th className="w-8 border-b-2 border-line" />}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.id} data-row={row.id} className="border-b border-line/70 hover:bg-accent/5">
                {content.columns.map((c) => {
                  const v = row.cells[c.key] ?? (c.type === "checkbox" ? false : "");
                  return (
                    <td key={c.key} className="px-2 py-1">
                      {locked ? (
                        <span className={`px-1 ${c.type === "number" || c.type === "money" ? "font-mono" : ""}`}>{c.type === "checkbox" ? (v ? "✓" : "") : c.type === "money" ? `$${Number(v).toLocaleString("en-US")}` : String(v)}</span>
                      ) : c.type === "select" ? (
                        <select value={String(v)} onChange={(e) => setCell(row.id, c.key, e.target.value)} className="w-full rounded-md bg-transparent px-1 py-0.5 text-ink outline-none focus:bg-panel2">
                          <option value="" />
                          {(c.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
                        </select>
                      ) : c.type === "checkbox" ? (
                        <input type="checkbox" checked={!!v} onChange={(e) => setCell(row.id, c.key, e.target.checked)} className="accent-accent" />
                      ) : (
                        <input type={c.type === "date" ? "date" : "text"} value={String(v)} onChange={(e) => setCell(row.id, c.key, c.type === "number" || c.type === "money" ? (e.target.value === "" || Number.isNaN(Number(e.target.value)) ? e.target.value : Number(e.target.value)) : e.target.value)}
                          className={`w-full rounded-md bg-transparent px-1 py-0.5 text-ink outline-none focus:bg-panel2 ${c.type === "number" || c.type === "money" ? "font-mono" : ""}`} />
                      )}
                    </td>
                  );
                })}
                {!compact && !locked && <td className="px-1 text-center"><button type="button" aria-label={t("table.deleteRow")} onClick={() => delRow(row.id)} className="text-mute hover:text-red-600">&#10005;</button></td>}
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && <div className="p-6 text-center text-xs text-mute">{t("table.empty")}</div>}
      </div>
    </div>
  );
}
