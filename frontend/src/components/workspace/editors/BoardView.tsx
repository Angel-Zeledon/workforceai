"use client";
import { useEffect, useRef, useState } from "react";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useAgentName } from "@/components/ui";

interface Card { id: string; col: string; title: string; assignee?: { kind: string; id: string }; labels?: string[]; due?: string; checklist?: { t: string; done: boolean }[]; link?: { artifact_id: string } }
interface BoardContent { schema: "aiw.board/1"; view: "kanban" | "checklist"; columns: { id: string; title: string; wip?: number | null }[]; cards: Card[] }

/**
 * Kanban / checklist. Editable unless the artifact is locked or the pane is read-only: drag a card to another
 * column or position, or use the keyboard (Alt+arrows on a focused card, or the move buttons). Every change goes
 * through the store's versioned save (optimistic concurrency, three-way merge per card id).
 */
export function BoardView({ art, readOnly }: { art: StoredArtifact; readOnly?: boolean }) {
  const { t } = useT();
  const c = art.content as BoardContent | undefined;
  const artifacts = useArtifacts((s) => s.artifacts);
  const deskId = useArtifacts((s) => s.deskId);
  const openTab = useArtifacts((s) => s.openTab);
  const edit = useArtifacts((s) => s.edit);
  const name = useAgentName();
  const root = useRef<HTMLDivElement>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  const [overCol, setOverCol] = useState<string | null>(null);
  const [focusId, setFocusId] = useState<string | null>(null);
  const [announce, setAnnounce] = useState("");
  const locked = !!readOnly || art.locked;

  useEffect(() => {
    if (!focusId) return;
    root.current?.querySelector<HTMLElement>(`[data-card="${CSS.escape(focusId)}"]`)?.focus();
    setFocusId(null);
  }, [focusId, c]);

  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  const save = (cards: Card[], summary?: string) => edit(art.id, { ...c, cards }, summary);
  const colTitle = (id: string) => c.columns.find((x) => x.id === id)?.title ?? id;

  /** Moves a card to column `col`, before the `index`-th card of that column (end when out of range). */
  const place = (id: string, col: string, index: number) => {
    const card = c.cards.find((k) => k.id === id);
    if (!card) return;
    const rest = c.cards.filter((k) => k.id !== id);
    const before = rest.filter((k) => k.col === col)[index];
    const moved = { ...card, col };
    if (before) rest.splice(rest.indexOf(before), 0, moved); else rest.push(moved);
    save(rest);
    setFocusId(id);
    setAnnounce(t("board.moved", { title: card.title, column: colTitle(col) }));
  };
  const indexIn = (card: Card) => c.cards.filter((k) => k.col === card.col).findIndex((k) => k.id === card.id);
  const shiftCol = (card: Card, d: -1 | 1) => {
    const i = c.columns.findIndex((x) => x.id === card.col) + d;
    if (i >= 0 && i < c.columns.length) place(card.id, c.columns[i].id, Number.MAX_SAFE_INTEGER);
  };
  const shiftRow = (card: Card, d: -1 | 1) => {
    const i = indexIn(card), n = c.cards.filter((k) => k.col === card.col).length;
    if (i + d < 0 || i + d >= n) return;
    // `place` indexes the column without the moved card: one up = before its previous sibling, one down = before the sibling after next
    place(card.id, card.col, i + d);
  };
  const addCard = (col: string) => {
    const id = `k${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`;
    save([...c.cards, { id, col, title: t("board.newCard") }]);
    setFocusId(id);
  };
  const patchCard = (id: string, p: Partial<Card>) => save(c.cards.map((k) => (k.id === id ? { ...k, ...p } : k)));
  const removeCard = (card: Card) => { save(c.cards.filter((k) => k.id !== card.id)); setAnnounce(t("board.removed", { title: card.title })); };

  const onKey = (e: React.KeyboardEvent, card: Card) => {
    if (locked || (e.target as HTMLElement).tagName === "INPUT") return;
    if (e.altKey && e.key === "ArrowLeft") { e.preventDefault(); shiftCol(card, -1); }
    else if (e.altKey && e.key === "ArrowRight") { e.preventDefault(); shiftCol(card, 1); }
    else if (e.altKey && e.key === "ArrowUp") { e.preventDefault(); shiftRow(card, -1); }
    else if (e.altKey && e.key === "ArrowDown") { e.preventDefault(); shiftRow(card, 1); }
    else if (e.key === "Delete") { e.preventDefault(); removeCard(card); }
  };

  const btn = "rounded border border-line px-1.5 text-[11px] text-mute hover:text-ink disabled:opacity-30";
  return (
    <div ref={root} data-testid="art-board" className="flex h-full min-h-0 flex-col bg-panel">
      <p className="sr-only" role="status" aria-live="polite" data-testid="board-live">{announce}</p>
      {!locked && <p className="px-4 pt-2 text-[10.5px] text-mute">{t("board.keyboardHint")}</p>}
      <div className="flex min-h-0 flex-1 gap-3 overflow-auto p-4">
        {c.columns.map((col, ci) => {
          const cards = c.cards.filter((k) => k.col === col.id);
          return (
            <section
              key={col.id}
              data-testid={`board-col-${col.id}`}
              aria-label={col.title}
              onDragOver={(e) => { if (!locked && dragId) { e.preventDefault(); setOverCol(col.id); } }}
              onDragLeave={() => setOverCol((o) => (o === col.id ? null : o))}
              onDrop={(e) => { e.preventDefault(); if (!locked && dragId) place(dragId, col.id, cards.length); setDragId(null); setOverCol(null); }}
              className={`flex w-[240px] shrink-0 flex-col rounded-xl border bg-panel2/60 ${overCol === col.id ? "border-accent" : "border-line"}`}
            >
              <header className="flex items-center justify-between px-3 py-2">
                <h4 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{col.title}</h4>
                <span className="flex items-center gap-1.5">
                  <span className={`rounded-full px-1.5 text-[10px] font-semibold ${col.wip && cards.length > col.wip ? "bg-red-500/20 text-red-700" : "bg-mute/[0.12] text-mute"}`}>{cards.length}{col.wip ? `/${col.wip}` : ""}</span>
                  {!locked && <button type="button" data-testid={`board-add-${col.id}`} aria-label={t("board.addCard", { column: col.title })} onClick={() => addCard(col.id)} className={btn}>+</button>}
                </span>
              </header>
              <div className="min-h-[60px] space-y-2 px-2 pb-2">
                {cards.map((k, ki) => (
                  <article
                    key={k.id}
                    data-card={k.id}
                    data-testid="board-card"
                    tabIndex={0}
                    draggable={!locked}
                    aria-label={`${k.title} · ${col.title}`}
                    onKeyDown={(e) => onKey(e, k)}
                    onDragStart={(e) => { if (locked) return; e.dataTransfer.setData("text/plain", k.id); e.dataTransfer.effectAllowed = "move"; setDragId(k.id); }}
                    onDragEnd={() => { setDragId(null); setOverCol(null); }}
                    onDragOver={(e) => { if (!locked && dragId && dragId !== k.id) e.preventDefault(); }}
                    onDrop={(e) => { e.preventDefault(); e.stopPropagation(); if (!locked && dragId && dragId !== k.id) place(dragId, col.id, ki); setDragId(null); setOverCol(null); }}
                    className={`rounded-xl border border-line bg-panel p-2.5 shadow-pop outline-none focus-visible:ring-2 focus-visible:ring-accent ${dragId === k.id ? "opacity-40" : ""} ${locked ? "" : "cursor-grab"}`}
                  >
                    {locked
                      ? <div className="text-[12.5px] font-semibold text-ink">{k.title}</div>
                      : <input data-testid="board-card-title" aria-label={t("board.cardTitle")} value={k.title} onChange={(e) => patchCard(k.id, { title: e.target.value })} className="w-full bg-transparent text-[12.5px] font-semibold text-ink outline-none" />}
                    {(k.labels?.length ?? 0) > 0 && <div className="mt-1 flex flex-wrap gap-1">{k.labels!.map((l) => <span key={l} className="rounded-full bg-accent/15 px-2 text-[10px] font-semibold text-accent">{l}</span>)}</div>}
                    {k.checklist && (
                      <ul className="mt-1.5 space-y-0.5">
                        {k.checklist.map((it, i) => (
                          <li key={i} className="flex items-center gap-1.5 text-[11.5px] text-ink">
                            <input type="checkbox" checked={it.done} disabled={locked} onChange={() => patchCard(k.id, { checklist: k.checklist!.map((x, j) => (j === i ? { ...x, done: !x.done } : x)) })} className="accent-accent" />{it.t}
                          </li>
                        ))}
                      </ul>
                    )}
                    <div className="mt-1.5 flex items-center justify-between text-[10.5px] text-mute">
                      <span>{k.assignee ? name(k.assignee.id) : ""}</span><span>{k.due ?? ""}</span>
                    </div>
                    {k.link && artifacts[k.link.artifact_id] && (
                      <button type="button" onClick={() => deskId && openTab(deskId, k.link!.artifact_id)} className="mt-1 text-[11px] font-semibold text-accent hover:underline">&#8599; {artifacts[k.link.artifact_id].title}</button>
                    )}
                    {!locked && (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        <button type="button" data-testid="board-move-left" aria-label={t("board.moveLeft")} disabled={ci === 0} onClick={() => shiftCol(k, -1)} className={btn}>&larr;</button>
                        <button type="button" data-testid="board-move-right" aria-label={t("board.moveRight")} disabled={ci === c.columns.length - 1} onClick={() => shiftCol(k, 1)} className={btn}>&rarr;</button>
                        <button type="button" data-testid="board-move-up" aria-label={t("board.moveUp")} disabled={ki === 0} onClick={() => shiftRow(k, -1)} className={btn}>&uarr;</button>
                        <button type="button" data-testid="board-move-down" aria-label={t("board.moveDown")} disabled={ki === cards.length - 1} onClick={() => shiftRow(k, 1)} className={btn}>&darr;</button>
                        <button type="button" data-testid="board-delete" aria-label={t("board.delete")} onClick={() => removeCard(k)} className={`${btn} ml-auto`}>&times;</button>
                      </div>
                    )}
                  </article>
                ))}
                {cards.length === 0 && <div className="py-3 text-center text-[11px] text-mute">{t("board.emptyColumn")}</div>}
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}
