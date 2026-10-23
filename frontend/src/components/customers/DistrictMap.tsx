"use client";
import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { useT } from "@/lib/i18n";
import type { DistrictItem } from "@/lib/customersApi";
import { customerColor, districtColumns, districtLots, floorsFor, normalizeStatus, shade, STATUS_COLOR } from "@/lib/customersLogic";

const TILE = 160; // width of one lot diamond in SVG units (its height is TILE / 2)
const FLOOR = 17; // height of one floor
const TOP = 150; // room above the first row for the tallest building and its status light
const BOTTOM = 34; // room below the last row for the names
const SIDE = 36; // room on both sides for the names of the corner lots

function useWidth() {
  const [w, setW] = useState(1024);
  useEffect(() => {
    const on = () => setW(window.innerWidth);
    on();
    window.addEventListener("resize", on);
    return () => window.removeEventListener("resize", on);
  }, []);
  return w;
}

const short = (s: string, n = 20) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

/**
 * The firm's neighbourhood: one isometric building per customer, tinted with its color, with a status light
 * (on track / waiting for an approval / blocked / idle). Plain SVG: no 3D assets. Every building is a button.
 */
export function DistrictMap({ items, onOpen }: { items: DistrictItem[]; onOpen: (id: string) => void }) {
  const { t } = useT();
  const width = useWidth();
  const cols = districtColumns(items.length, width);
  const layout = useMemo(() => districtLots(items.length, cols, TILE), [items.length, cols]);
  const hw = TILE / 2, hh = TILE / 4;
  const key = (id: string) => (e: KeyboardEvent<SVGGElement>) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(id); }
  };
  return (
    <svg data-testid="customers-district-map" role="group" aria-label={t("customers.map.aria")}
      viewBox={`${-SIDE} ${-TOP} ${layout.width + 2 * SIDE} ${layout.height + TOP + BOTTOM}`} className="h-auto w-full max-h-[70vh]" preserveAspectRatio="xMidYMid meet">
      {/* streets: the lots sit on a soft ground */}
      {layout.lots.map((l) => (
        <polygon key={`g${l.index}`} points={`${l.x},${l.y - hh} ${l.x + hw},${l.y} ${l.x},${l.y + hh} ${l.x - hw},${l.y}`} fill="#e9edf2" stroke="#d5dbe3" strokeWidth={1.5} />
      ))}
      {layout.lots.map((l) => {
        const it = items[l.index];
        const c = it.customer;
        const st = normalizeStatus(it.status);
        const base = customerColor(c.color, c.id);
        const bw = hw * 0.55, bh = hh * 0.55;
        const H = floorsFor(it.assignments, it.projects_active) * FLOOR;
        const { x, y } = l;
        const floors = Math.round(H / FLOOR);
        const label = t("customers.map.building", { name: c.name, status: t(`customers.status.${st}`) });
        return (
          <g key={c.id} data-testid={`customers-building-${c.id}`} role="button" tabIndex={0} aria-label={label}
            className="cursor-pointer outline-none [&:focus-visible>polygon.lot]:stroke-accent" onClick={() => onOpen(c.id)} onKeyDown={key(c.id)}>
            <title>{label}</title>
            <polygon className="lot" points={`${x},${y - hh * 0.8} ${x + hw * 0.8},${y} ${x},${y + hh * 0.8} ${x - hw * 0.8},${y}`} fill="transparent" stroke="transparent" strokeWidth={3} />
            {/* left and right faces, then the roof */}
            <polygon points={`${x - bw},${y} ${x},${y + bh} ${x},${y + bh - H} ${x - bw},${y - H}`} fill={shade(base, -0.15)} />
            <polygon points={`${x},${y + bh} ${x + bw},${y} ${x + bw},${y - H} ${x},${y + bh - H}`} fill={shade(base, -0.35)} />
            <polygon points={`${x},${y - bh - H} ${x + bw},${y - H} ${x},${y + bh - H} ${x - bw},${y - H}`} fill={shade(base, 0.25)} />
            {/* windows: one row per floor on each face */}
            {Array.from({ length: floors }, (_, f) => {
              const fy = y - f * FLOOR - FLOOR * 0.55;
              return (
                <g key={f} fill="#fdf6d8" opacity={0.85}>
                  <polygon points={`${x - bw * 0.78},${fy - bh * 0.1} ${x - bw * 0.48},${fy + bh * 0.2} ${x - bw * 0.48},${fy + bh * 0.2 - 7} ${x - bw * 0.78},${fy - bh * 0.1 - 7}`} />
                  <polygon points={`${x - bw * 0.36},${fy + bh * 0.32} ${x - bw * 0.1},${fy + bh * 0.58} ${x - bw * 0.1},${fy + bh * 0.58 - 7} ${x - bw * 0.36},${fy + bh * 0.32 - 7}`} />
                  <polygon points={`${x + bw * 0.12},${fy + bh * 0.56} ${x + bw * 0.42},${fy + bh * 0.26} ${x + bw * 0.42},${fy + bh * 0.26 - 7} ${x + bw * 0.12},${fy + bh * 0.56 - 7}`} />
                  <polygon points={`${x + bw * 0.54},${fy + bh * 0.14} ${x + bw * 0.82},${fy - bh * 0.14} ${x + bw * 0.82},${fy - bh * 0.14 - 7} ${x + bw * 0.54},${fy + bh * 0.14 - 7}`} />
                </g>
              );
            })}
            {/* status light on a short pole */}
            <line x1={x} y1={y - H} x2={x} y2={y - H - 22} stroke="#5b6472" strokeWidth={2} />
            <circle cx={x} cy={y - H - 30} r={9} fill={STATUS_COLOR[st]} stroke="#fff" strokeWidth={2.5}>
              {st === "blocked" || st === "waiting_approval" ? <animate attributeName="r" values="9;11;9" dur="1.6s" repeatCount="indefinite" /> : null}
            </circle>
            {it.approvals_waiting > 0 && (
              <g>
                <circle cx={x + 14} cy={y - H - 40} r={8} fill="#a86208" />
                <text x={x + 14} y={y - H - 37} textAnchor="middle" fontSize={10} fontWeight={700} fill="#fff">{it.approvals_waiting}</text>
              </g>
            )}
            <text x={x} y={y + hh + 16} textAnchor="middle" fontSize={13} fontWeight={600} fill="currentColor" className="text-ink">{short(c.name)}</text>
          </g>
        );
      })}
    </svg>
  );
}
