/** Pure helpers of the customer district (B12, docs/architecture/customer-district.md). No React, no network. */

export type CustomerStatus = "on_track" | "waiting_approval" | "blocked" | "idle";

export const CUSTOMER_STATUSES: CustomerStatus[] = ["blocked", "waiting_approval", "on_track", "idle"];

/** Status light colors (the same palette as the task and quality badges). */
export const STATUS_COLOR: Record<CustomerStatus, string> = {
  on_track: "#2f7d55", waiting_approval: "#a86208", blocked: "#b4443c", idle: "#7b8494",
};

/** Lower is more urgent: blocked first, then waiting for an approval, then on track, then idle. */
export function statusRank(s: string): number {
  const i = CUSTOMER_STATUSES.indexOf(s as CustomerStatus);
  return i < 0 ? CUSTOMER_STATUSES.length : i;
}

/** Any unknown value from a newer server is shown as idle rather than breaking the view. */
export function normalizeStatus(s: string | null | undefined): CustomerStatus {
  return (CUSTOMER_STATUSES as string[]).includes(String(s)) ? (s as CustomerStatus) : "idle";
}

/** The most urgent of several statuses (a customer's projects and requests roll up to one light). */
export function worstStatus(list: (string | null | undefined)[]): CustomerStatus {
  let best: CustomerStatus = "idle";
  for (const s of list) {
    const n = normalizeStatus(s);
    if (statusRank(n) < statusRank(best)) best = n;
  }
  return best;
}

/** Sort a district: most urgent first, then by name (locale-aware, accent-insensitive). */
export function sortDistrict<T extends { status: string; customer: { name: string } }>(items: T[]): T[] {
  return [...items].sort((a, b) => statusRank(a.status) - statusRank(b.status) || a.customer.name.localeCompare(b.customer.name, "es", { sensitivity: "base" }));
}

const HEX = /^#[0-9a-fA-F]{6}$/;
const PALETTE = ["#2b6cb0", "#2f7d55", "#a86208", "#7c6bb0", "#b4443c", "#2c7a7b", "#975a16", "#4a5568"];

/** A valid "#rrggbb" color, or a stable palette color derived from the seed (the customer id or name). */
export function customerColor(color: string | null | undefined, seed: string): string {
  if (color && HEX.test(color)) return color.toLowerCase();
  let h = 0;
  for (const ch of seed) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

/** Lighten (amount > 0) or darken (amount < 0) a "#rrggbb" color; used to shade the faces of a building. */
export function shade(hex: string, amount: number): string {
  const c = HEX.test(hex) ? hex : "#4a5568";
  const ch = [1, 3, 5].map((i) => parseInt(c.slice(i, i + 2), 16));
  const out = ch.map((v) => Math.max(0, Math.min(255, Math.round(amount >= 0 ? v + (255 - v) * amount : v * (1 + amount)))));
  return `#${out.map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

/** The clone's default name: "<agent title> · <customer name>" ("Contador · Ferretería López"). */
export function defaultCloneName(agentTitle: string, customerName: string): string {
  const a = agentTitle.trim(), c = customerName.trim();
  if (!a) return c;
  if (!c) return a;
  return `${a} · ${c}`;
}

/** Columns of the district grid for a number of buildings and a viewport width (phones get 2). */
export function districtColumns(count: number, width: number): number {
  if (count <= 1) return 1;
  const max = width < 480 ? 2 : width < 900 ? 3 : 4;
  return Math.max(1, Math.min(max, Math.ceil(Math.sqrt(count))));
}

export interface Lot { index: number; col: number; row: number; x: number; y: number }

/**
 * Isometric lots of the district: building i sits at (col, row) of a grid with `cols` columns; x/y is the
 * screen position of the lot's center in an SVG whose origin is the top corner of the neighbourhood.
 * `tile` is the width of one lot diamond (its height is half of it).
 */
export function districtLots(count: number, cols: number, tile = 160): { lots: Lot[]; width: number; height: number; originX: number } {
  const c = Math.max(1, cols);
  const rows = Math.max(1, Math.ceil(count / c));
  const hw = tile / 2, hh = tile / 4;
  const originX = rows * hw; // the leftmost lot of the last row must stay inside the canvas
  const lots: Lot[] = [];
  for (let i = 0; i < count; i++) {
    const col = i % c, row = Math.floor(i / c);
    lots.push({ index: i, col, row, x: originX + (col - row) * hw, y: (col + row + 1) * hh });
  }
  const width = (c + rows) * hw;
  const height = (c + rows) * hh;
  return { lots, width, height, originX };
}

/** Floors of a building: bigger customers (more clones and active projects) are taller, within 2..5. */
export function floorsFor(assignments: number, projectsActive: number): number {
  return Math.max(2, Math.min(5, 2 + Math.floor((assignments + projectsActive) / 2)));
}

/** Basic validation of the customer form (the server validates again). */
export function validateCustomer(input: { name: string; color?: string }): "name_required" | "name_too_long" | "color_invalid" | null {
  const n = input.name.trim();
  if (!n) return "name_required";
  if (n.length > 120) return "name_too_long";
  if (input.color && !HEX.test(input.color)) return "color_invalid";
  return null;
}
