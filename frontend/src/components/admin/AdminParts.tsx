"use client";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { inputCls } from "../connections/shared";
import { GovError } from "./governanceApi";

export const thCls = "px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wide text-mute";
export const tdCls = "px-3 py-2 text-[12.5px] text-ink";
export { inputCls };

export function Panel({ title, body, children, testid }: { title: string; body?: string; children: ReactNode; testid?: string }) {
  return (
    <section data-testid={testid} className="ac-rise rounded-xl border border-line bg-panel p-5">
      <h2 className="text-[14px] font-semibold text-ink">{title}</h2>
      {body && <p className="mt-1 max-w-3xl text-[12.5px] leading-relaxed text-mute">{body}</p>}
      <div className="mt-4 space-y-3 text-[13px] text-ink2">{children}</div>
    </section>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block min-w-0 text-[11.5px] font-medium text-mute">
      <span className="mb-1 block">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-[11px] font-normal text-mute">{hint}</span>}
    </label>
  );
}

export function Notice({ kind, children }: { kind: "ok" | "err" | "warn"; children: ReactNode }) {
  const cls = kind === "ok" ? "border-ok/30 bg-ok/5 text-ok" : kind === "warn" ? "border-warn/30 bg-warn/5 text-warn" : "border-err/30 bg-err/5 text-err";
  return <div role={kind === "err" ? "alert" : "status"} className={`rounded-md border px-3 py-1.5 text-xs font-medium ${cls}`}>{children}</div>;
}

/** Message for a failed call (the server's own text is short and safe to show). */
export function useErrText() {
  const { t } = useT();
  return useCallback((e: unknown) => t("adm.err", { msg: e instanceof GovError || e instanceof Error ? e.message : String(e) }), [t]);
}

/** Loads on mount and on reload(); keeps the last value while reloading. */
export function useLoad<T>(fn: () => Promise<T>) {
  const errText = useErrText();
  const [data, setData] = useState<T | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    let live = true;
    fn().then((v) => { if (live) { setData(v); setErr(null); } }).catch((e) => { if (live) setErr(errText(e)); });
    return () => { live = false; };
    // fn identity changes every render; the counter drives reloads
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [n, errText]);
  return { data, err, reload: () => setN((x) => x + 1), setData };
}

export const usd = (n: number) => `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

export function when(iso?: string, locale?: string) {
  if (!iso) return "";
  try { return new Date(iso).toLocaleString(locale === "en" ? "en-US" : locale === "pt" ? "pt-BR" : "es-ES", { dateStyle: "medium", timeStyle: "short" }); } catch { return iso; }
}
