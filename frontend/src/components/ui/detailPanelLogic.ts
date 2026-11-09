// The pure part of the single side detail panel (components/ui/DetailPanel.tsx, proposal 5.4 point 2): the URL parameter
// that says which item is open (so the browser's Back closes the panel), which open panel takes Esc, and where Tab goes
// when the focus is kept inside a full-screen panel. No React, no window: tested in detailPanelLogic.test.mjs.

const params = (search: string) => new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);

/** The open item of a location search ("?tarea=<id>"), or "" when the panel is closed. */
export function detailParam(search: string, param: string): string {
  return params(search).get(param) ?? "";
}

/** The URL with the item open (value) or closed (null); every other parameter is kept, `drop` ones are removed. */
export function detailHref(pathname: string, search: string, param: string, value: string | null, drop: string[] = []): string {
  const p = params(search);
  for (const k of drop) p.delete(k);
  p.delete(param);
  if (value) p.set(param, value);
  const s = p.toString();
  return s ? `${pathname}?${s}` : pathname;
}

export type HistoryStep = "none" | "push" | "replace" | "remove";

/**
 * What the history needs so the URL says what the panel shows: opening adds an entry (Back closes it), switching to
 * another item replaces it (Back still closes the panel, it does not walk through every item seen), closing removes it.
 */
export function historyStep(current: string | null | undefined, next: string | null | undefined): HistoryStep {
  const cur = current || "";
  const nxt = next || "";
  if (cur === nxt) return "none";
  if (!nxt) return "remove";
  return cur ? "replace" : "push";
}

export type ReleaseAction = "none" | "back" | "strip";

/**
 * Closing a panel whose item is still in the URL: go Back when the current entry is exactly the one the panel added
 * (the person returns to where they were), otherwise just strip the parameter (the entry was a link, or the page
 * changed something else since). Never twice for one closing (`backPending`).
 */
export function releaseAction(o: { current: string; here: string; pushedHref?: string | null; backPending?: boolean }): ReleaseAction {
  if (o.backPending || !o.current) return "none";
  return o.pushedHref && o.pushedHref === o.here ? "back" : "strip";
}

export interface OpenPanel { id: string; layer: number; seq: number }

/** The panel that takes Esc: the highest layer (the file viewer sits above the others), then the last one opened. */
export function topPanel(open: OpenPanel[]): string | null {
  let best: OpenPanel | null = null;
  for (const p of open) if (!best || p.layer > best.layer || (p.layer === best.layer && p.seq > best.seq)) best = p;
  return best?.id ?? null;
}

/**
 * Tab inside a panel that keeps the focus (phone, full screen): the index to focus next, or -1 to let the browser move
 * on. `index` is the position of the focused element among the `count` focusable ones (-1 when it is not one of them).
 */
export function trapIndex(count: number, index: number, shift: boolean): number {
  if (count <= 0) return -1;
  if (index < 0) return shift ? count - 1 : 0;
  if (shift && index === 0) return count - 1;
  if (!shift && index === count - 1) return 0;
  return -1;
}

/** Desktop and tablet: a fixed width beside the content. Phone: the whole screen with "Volver". */
export const PHONE_QUERY = "(max-width: 767px)";
