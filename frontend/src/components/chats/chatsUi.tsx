"use client";
// Small pieces shared by the chats screen and the pedido panel: line icons, list times and the status pill of a pedido.
import { intlLocale, useT } from "@/lib/i18n";
import { fmtTime } from "@/lib/meta";
import type { PedidoStatus } from "@/lib/pedidosLogic";
import { kindOf, statusColor } from "@/lib/statusLogic";
import { StatusIcon } from "../ui/Status";
import { Icon, type IconName } from "../icons";

/** The chats screen draws the app's own icon set (components/icons.tsx); the glossary rule: clipboard = pedido,
 *  flag = project, folder = files only. */
const GLYPH = {
  search: "search", archive: "archive", trash: "trash", edit: "edit", users: "users", back: "back", office: "office",
  doc: "doc", folder: "folder", chat: "chat", x: "x", up: "up", restore: "restore", external: "external",
  pedido: "clipboard", project: "flag", chevron: "chevron", alert: "alert",
} as const satisfies Record<string, IconName>;
export function Glyph({ name, size = 15, className = "" }: { name: keyof typeof GLYPH; size?: number; className?: string }) {
  return <Icon name={GLYPH[name]} size={size} className={className} />;
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
