"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { useDriveNav } from "@/lib/drive";
import { memoryApi, refPath, type MemoryEntry } from "@/lib/memoryApi";
import type { MemoryItem } from "@/lib/types";
import { Card, Empty, timeAgo } from "../ui";

/** What the agent remembers (long-term memory): its lessons and the work it did, with the source of each
 * entry; a search shows what it would recall for a topic, drive files included (only the ones it may
 * read). Deleting an entry removes it from the agent's memory and is audited; the task itself stays. */
export function MemoryTab({ id, legacy }: { id: string; legacy: MemoryItem[] }) {
  const { t } = useT();
  const canDelete = useCan("memories:write");
  const [items, setItems] = useState<MemoryEntry[] | null>(null);
  const [q, setQ] = useState("");
  const [applied, setApplied] = useState("");
  const [err, setErr] = useState(false);

  const load = useCallback(async (query: string) => {
    try { setItems(await memoryApi.list(id, query)); setErr(false); } catch { setErr(true); setItems([]); }
  }, [id]);
  useEffect(() => { setItems(null); setQ(""); setApplied(""); load(""); }, [id, load]);

  const remove = async (e: MemoryEntry) => {
    if (!e.id) return;
    try { await memoryApi.remove(id, e.id); setItems((cur) => (cur ?? []).filter((x) => x.id !== e.id)); } catch { setErr(true); }
  };

  return (
    <div data-testid="memory-tab" className="space-y-3">
      <form className="flex gap-2" onSubmit={(ev) => { ev.preventDefault(); setApplied(q.trim()); load(q); }}>
        <input data-testid="memory-search" value={q} onChange={(ev) => { setQ(ev.target.value); if (!ev.target.value.trim() && applied) { setApplied(""); load(""); } }}
          placeholder={t("memory.search.placeholder")} aria-label={t("memory.search.label")}
          className="min-w-0 flex-1 rounded-lg border border-line bg-panel2 px-3 py-1.5 text-xs text-ink placeholder:text-mute" />
        <button type="submit" data-testid="memory-search-go" className="rounded-lg border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2">{t("memory.search.go")}</button>
      </form>
      {applied && <p className="text-[11px] text-mute">{t("memory.search.hint", { q: applied })}</p>}
      {err && <div className="text-xs text-err">{t("memory.error")}</div>}
      {items === null ? <div className="text-xs text-mute">{t("memory.loading")}</div> : items.length === 0 ? <Empty>{applied ? t("memory.noMatch") : t("panel.noMemory")}</Empty> : (
        <ul className="space-y-2">
          {items.map((m) => {
            const path = refPath(m.ref);
            return (
              <li key={m.ref} data-testid={`memory-entry-${m.kind}`} data-ref={m.ref} className="rounded-md border border-line bg-panel2 p-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 text-[13px] font-semibold text-ink">{m.title}</div>
                  <span className="shrink-0 rounded bg-mute/[0.08] px-1.5 py-0.5 text-[10px] uppercase text-mute">{t(`memory.kind.${m.kind}`)}</span>
                </div>
                {m.snippet && <p className="mt-1 text-xs text-ink2">{m.snippet}</p>}
                <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[10.5px] text-mute">
                  {path ? (
                    <button type="button" data-testid="memory-source-file" className="font-mono text-accent hover:underline" onClick={() => useDriveNav.getState().show(path)}>{path}</button>
                  ) : <span data-testid="memory-source" className="font-mono">{m.source_ref || m.ref}</span>}
                  {m.project && <span>{t("memory.project", { name: m.project })}</span>}
                  {m.at && <span>{timeAgo(m.at)}</span>}
                  {canDelete && m.deletable && m.id && (
                    <button type="button" data-testid="memory-delete" className="ml-auto text-mute hover:text-err" onClick={() => remove(m)}>{t("memory.delete")}</button>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {!applied && legacy.length > 0 && (
        <Card title={t("memory.quick")}>
          <div className="space-y-2">
            {legacy.map((m) => (
              <div key={m.key} className="rounded-md bg-panel2 p-2">
                <div className="flex items-center justify-between"><span className="font-mono text-[11px] text-accent">{m.key}</span><span className="rounded bg-mute/[0.08] px-1.5 text-[10px] uppercase text-mute">{m.scope}</span></div>
                <div className="mt-1 text-xs text-ink2">{m.value}</div>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}
