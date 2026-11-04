"use client";
// Small pieces shared by the chats screen and the pedido panel: line icons, list times and the status pill of a pedido.
import { intlLocale, useT } from "@/lib/i18n";
import { fmtTime } from "@/lib/meta";
import type { PedidoStatus } from "@/lib/pedidosLogic";
import { kindOf, statusColor } from "@/lib/statusLogic";
import { StatusIcon } from "../ui/Status";

/** Small line icons (kept local: this screen owns its glyphs). */
export const GLYPH = {
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
  archive: "M4 5h16v4H4zM6 9v10h12V9M10 13h4",
  trash: "M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13M10 11v6M14 11v6",
  edit: "M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4",
  users: "M9 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM3 20a6 6 0 0 1 12 0M16 5.2a3 3 0 0 1 0 5.6M18 14.5a6 6 0 0 1 3 5.5",
  back: "M19 12H5M11 6l-6 6 6 6",
  office: "M4 21V6l8-3 8 3v15M4 21h16M9 9h2M13 9h2M9 13h2M13 13h2M10 21v-4h4v4",
  doc: "M6 3h8l5 5v13H6zM14 3v5h5M9 13h7M9 17h7",
  folder: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
  chat: "M4 5h16v11H10l-5 4v-4H4z",
  x: "M6 6l12 12M18 6L6 18",
  up: "M6 15l6-6 6 6",
  restore: "M4 12a8 8 0 1 0 3-6.2M4 4v5h5",
  external: "M14 4h6v6M20 4l-9 9M18 14v5H5V6h5",
  pedido: "M8 4h8v3H8zM6 6H5a1 1 0 0 0-1 1v13a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V7a1 1 0 0 0-1-1h-1M8 12h8M8 16h5",
  chevron: "M9 6l6 6-6 6",
  alert: "M12 4l9 16H3zM12 10v4M12 17v.5",
};
export function Glyph({ name, size = 15, className = "" }: { name: keyof typeof GLYPH; size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" aria-hidden focusable="false" className={className}>
      <path d={GLYPH[name]} />
    </svg>
  );
}

/** "12:40", "Ayer", "mar", "3 mar": short time of a list row. */
export function shortTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso), now = new Date();
  if (isNaN(+d) || d.getFullYear() < 2000) return ""; // the backend sends a zero time for a thread without messages
  const days = Math.floor((+new Date(now.getFullYear(), now.getMonth(), now.getDate()) - +new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 86400000);
  const loc = intlLocale();
  if (days <= 0) return fmtTime(iso);
  if (days === 1) return new Intl.RelativeTimeFormat(loc, { numeric: "auto" }).format(-1, "day");
  if (days < 7) return new Intl.DateTimeFormat(loc, { weekday: "short" }).format(d);
  return new Intl.DateTimeFormat(loc, { day: "numeric", month: "short" }).format(d);
}

// Colors from the single palette (lib/statusLogic.ts): the same "En curso", "Te necesita" and "Listo" as in Proyectos.

/** Status of a pedido as a small pill (same words in the list, the chat card and the panel). */
export function StatusPill({ status, testId = "pedido-status" }: { status: PedidoStatus; testId?: string }) {
  const { t } = useT();
  const c = statusColor("pedido", status);
  const kind = kindOf("pedido", status);
  return (
    <span data-testid={testId} data-status={status} data-kind={kind} className="inline-flex shrink-0 items-center gap-1 rounded-full px-1.5 py-[1px] text-[10.5px] font-semibold" style={{ background: `${c}1f`, color: c }}>
      <StatusIcon kind={kind} size={10} />{t(`pedido.status.${status}`)}
    </span>
  );
}
