"use client";
import { useEffect, type ReactNode } from "react";
import { useT, trOr, tr } from "@/lib/i18n";
import { errCode } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { ConnStatus, Human, Risk } from "@/lib/connections/types";

/** Translates an error thrown by the API layer into a localized, provider-agnostic message. */
export function errText(e: unknown): string {
  return trOr(`conn.err.${errCode(e)}`, tr("conn.err.generic"));
}

/** Current (mock or real) viewer; permissions follow the table in section 10 of the spec. */
export function useViewer() {
  const controls = useConnections((s) => s.controls);
  const viewer: Human | null = controls?.viewer ?? null;
  const role = viewer?.role ?? "member";
  return {
    viewer,
    isOwner: role === "owner",
    isAdmin: role === "owner" || role === "admin",
  };
}

const STATUS_COLOR: Record<ConnStatus, string> = {
  pending: "#5b6678", active: "#2f7d55", needs_reauth: "#a86208", error: "#b4443c", suspended: "#a86208", revoked: "#5b6678", expired: "#a86208",
};
export function StatusBadge({ status, testId = "conn-status" }: { status: ConnStatus; testId?: string }) {
  const { t } = useT();
  const c = STATUS_COLOR[status] ?? "#5b6678";
  return (
    <span data-testid={testId} data-status={status} className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}1a`, color: c }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: c }} />
      {t(`conn.status.${status}`)}
    </span>
  );
}

const RISK_COLOR: Record<Risk, string> = { low: "#2f7d55", medium: "#a86208", high: "#b4443c" };
export function CapChip({ cap, risk, dim }: { cap: string; risk: Risk; dim?: boolean }) {
  const { t } = useT();
  const c = RISK_COLOR[risk];
  return (
    <span className={`inline-flex items-center whitespace-nowrap rounded-md border px-2 py-[1px] text-[10px] font-semibold ${dim ? "opacity-50" : ""}`} style={{ borderColor: `${c}66`, background: `${c}18`, color: c }}>
      {t(`conn.capShort.${cap}`)}
    </span>
  );
}

export function Modal({ children, onClose, testId, wide }: { children: ReactNode; onClose: () => void; testId?: string; wide?: boolean }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-[80] flex items-center justify-center bg-ink/35 p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div data-testid={testId} role="dialog" aria-modal="true" className={`ac-pop max-h-[90vh] w-full overflow-y-auto rounded-xl border border-line bg-panel p-5 shadow-float ${wide ? "max-w-2xl" : "max-w-md"}`}>
        {children}
      </div>
    </div>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] text-mute">{hint}</span>}
    </label>
  );
}
export const inputCls = "w-full rounded-md border border-line-strong bg-panel px-3 py-1.5 text-sm text-ink outline-none transition placeholder:text-mute/70 focus:border-accent disabled:bg-panel2 disabled:opacity-60";

export function ErrorLine({ msg }: { msg: string | null }) {
  if (!msg) return null;
  return <div role="alert" data-testid="conn-error" className="rounded-md border border-err/30 bg-err/5 px-3 py-1.5 text-xs font-medium text-err">{msg}</div>;
}
