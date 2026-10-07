"use client";
import { useEffect, useRef, useState } from "react";
import { artifactApi, orderedKinds, useArtifacts, type ArtifactTemplate } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { KindIcon } from "./kindMeta";

/** Minimal CSV parser (quotes, commas, newlines). Cells starting with = + - @ are kept as plain text. */
function parseCsv(text: string): string[][] {
  const rows: string[][] = []; let row: string[] = []; let cur = ""; let q = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (q) { if (ch === '"' && text[i + 1] === '"') { cur += '"'; i++; } else if (ch === '"') q = false; else cur += ch; }
    else if (ch === '"') q = true;
    else if (ch === ",") { row.push(cur); cur = ""; }
    else if (ch === "\n" || ch === "\r") { if (ch === "\r" && text[i + 1] === "\n") i++; row.push(cur); rows.push(row); row = []; cur = ""; }
    else cur += ch;
  }
  if (cur !== "" || row.length) { row.push(cur); rows.push(row); }
  return rows.filter((r) => r.some((c) => c !== ""));
}

/** "+" menu (sec. 2.2): every kind is always available; kinds suggested for the agent's role come first. */
export function NewArtifactMenu({ deskId }: { deskId: string }) {
  const { t, locale } = useT();
  const agent = useStore((s) => s.agents[deskId]);
  const create = useArtifacts((s) => s.create);
  const [open, setOpen] = useState(false);
  const [templates, setTemplates] = useState<ArtifactTemplate[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const box = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    artifactApi.templates(agent ? deskId : undefined).then((r) => setTemplates(r.items)).catch(() => setTemplates([]));
    const close = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open, deskId, agent]);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setMsg("");
    try { await fn(); setOpen(false); } catch { setMsg(t("menu.createError")); } finally { setBusy(false); }
  };

  const onImport = async (file: File) => {
    if (!/\.csv$/i.test(file.name)) { setMsg(t("menu.importUnsupported")); return; }
    const rows = parseCsv(await file.text());
    if (!rows.length) { setMsg(t("menu.importEmpty")); return; }
    const [header, ...body] = rows;
    const columns = header.map((h, i) => ({ key: `c${i}`, label: h || `${i + 1}`, type: "text" }));
    const content = { schema: "aiw.table/1", columns, rows: body.slice(0, 5000).map((r, i) => ({ id: `r${i + 1}`, cells: Object.fromEntries(columns.map((c, k) => [c.key, r[k] ?? ""])) })) };
    await run(() => create(deskId, { kind: "table", title: file.name.replace(/\.csv$/i, ""), content, locale }));
  };

  return (
    <div ref={box} className="relative">
      <button type="button" data-testid="ws-tab-new" aria-label={t("menu.new")} title={t("menu.new")} onClick={() => setOpen(!open)} className="flex h-7 w-7 items-center justify-center rounded-md border border-line bg-panel text-base font-semibold text-accent hover:border-accent">+</button>
      {open && (
        <div className="ac-pop absolute left-0 top-9 z-50 max-h-[70vh] w-[260px] overflow-y-auto rounded-xl border border-line bg-panel p-2 shadow-pop">
          <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("menu.kinds")}</div>
          {orderedKinds(agent?.role).map(({ kind, suggested }) => (
            <button key={kind} type="button" disabled={busy} data-testid={`ws-new-kind-${kind}`} data-suggested={suggested ? "true" : "false"}
              onClick={() => run(() => create(deskId, { kind, title: t(`artifact.new.${kind}`), locale }))}
              className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12px] font-semibold text-ink hover:bg-accent/10 disabled:opacity-50">
              <KindIcon kind={kind} />{t(`artifact.kind.${kind}`)}{suggested && <span title={t("menu.suggested")} className="ml-auto text-amber-500">&#9733;</span>}
            </button>
          ))}
          {templates.length > 0 && <div className="mt-1 border-t border-line px-2 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("menu.templates")}</div>}
          {templates.map((tp) => (
            <button key={tp.id} type="button" disabled={busy} data-testid={`ws-new-template-${tp.id}`} onClick={() => run(() => create(deskId, { kind: tp.kind, title: "", template_id: tp.id, locale }))}
              className="flex w-full items-center gap-2 rounded-lg px-2 py-1 text-left text-[11.5px] text-ink hover:bg-accent/10 disabled:opacity-50">
              <KindIcon kind={tp.kind} size={13} />{tp.title[locale]}
            </button>
          ))}
          <label className="mt-1 block cursor-pointer rounded-lg border-t border-line px-2 pb-1 pt-2 text-[11.5px] font-semibold text-accent hover:underline">
            {t("menu.import")}
            <input data-testid="ws-import-input" type="file" accept=".csv,text/csv" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) onImport(f); e.target.value = ""; }} />
          </label>
          {msg && <div className="px-2 pb-1 text-[11px] font-semibold text-red-600">{msg}</div>}
        </div>
      )}
    </div>
  );
}
