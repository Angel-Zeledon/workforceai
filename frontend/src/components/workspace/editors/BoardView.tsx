"use client";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useAgentName } from "@/components/ui";

interface Card { id: string; col: string; title: string; assignee?: { kind: string; id: string }; labels?: string[]; due?: string; checklist?: { t: string; done: boolean }[]; link?: { artifact_id: string } }
interface BoardContent { schema: "aiw.board/1"; view: "kanban" | "checklist"; columns: { id: string; title: string; wip?: number | null }[]; cards: Card[] }

/** Read-only kanban / checklist (first version). */
export function BoardView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as BoardContent | undefined;
  const artifacts = useArtifacts((s) => s.artifacts);
  const deskId = useArtifacts((s) => s.deskId);
  const openTab = useArtifacts((s) => s.openTab);
  const name = useAgentName();
  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  return (
    <div data-testid="art-board" className="flex h-full min-h-0 gap-3 overflow-auto bg-panel p-4">
      {c.columns.map((col) => {
        const cards = c.cards.filter((k) => k.col === col.id);
        return (
          <section key={col.id} className="flex w-[240px] shrink-0 flex-col rounded-xl border border-line bg-panel2/60">
            <header className="flex items-center justify-between px-3 py-2">
              <h4 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{col.title}</h4>
              <span className={`rounded-full px-1.5 text-[10px] font-semibold ${col.wip && cards.length > col.wip ? "bg-red-500/20 text-red-700" : "bg-mute/[0.12] text-mute"}`}>{cards.length}{col.wip ? `/${col.wip}` : ""}</span>
            </header>
            <div className="min-h-[60px] space-y-2 px-2 pb-2">
              {cards.map((k) => (
                <article key={k.id} data-card={k.id} className="rounded-xl border border-line bg-panel p-2.5 shadow-pop">
                  <div className="text-[12.5px] font-semibold text-ink">{k.title}</div>
                  {(k.labels?.length ?? 0) > 0 && <div className="mt-1 flex flex-wrap gap-1">{k.labels!.map((l) => <span key={l} className="rounded-full bg-accent/15 px-2 text-[10px] font-semibold text-accent">{l}</span>)}</div>}
                  {k.checklist && (
                    <ul className="mt-1.5 space-y-0.5">{k.checklist.map((it, i) => <li key={i} className="flex items-center gap-1.5 text-[11.5px] text-ink"><input type="checkbox" checked={it.done} readOnly className="accent-accent" />{it.t}</li>)}</ul>
                  )}
                  <div className="mt-1.5 flex items-center justify-between text-[10.5px] text-mute">
                    <span>{k.assignee ? name(k.assignee.id) : ""}</span><span>{k.due ?? ""}</span>
                  </div>
                  {k.link && artifacts[k.link.artifact_id] && (
                    <button type="button" onClick={() => deskId && openTab(deskId, k.link!.artifact_id)} className="mt-1 text-[11px] font-semibold text-accent hover:underline">&#8599; {artifacts[k.link.artifact_id].title}</button>
                  )}
                </article>
              ))}
              {cards.length === 0 && <div className="py-3 text-center text-[11px] text-mute">{t("board.emptyColumn")}</div>}
            </div>
          </section>
        );
      })}
    </div>
  );
}
