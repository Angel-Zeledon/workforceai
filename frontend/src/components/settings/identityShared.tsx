"use client";
import { useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { IdentityError } from "@/lib/identityApi";
import { Btn } from "../ui";

export function IdCard({ title, desc, testid, children }: { title: string; desc?: string; testid: string; children: ReactNode }) {
  return (
    <section data-testid={testid} className="space-y-3 rounded-xl border border-line bg-panel p-5">
      <div>
        <h2 className="text-[13.5px] font-semibold text-ink">{title}</h2>
        {desc && <p className="mt-1 text-[12.5px] leading-relaxed text-mute">{desc}</p>}
      </div>
      {children}
    </section>
  );
}

export function IdCheck({ label, hint, checked, onChange, disabled, testid }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; testid?: string }) {
  return (
    <label className={`flex items-start gap-2.5 text-[13px] text-ink ${disabled ? "opacity-60" : ""}`}>
      <input data-testid={testid} type="checkbox" className="mt-0.5 h-4 w-4 accent-[var(--accent,#2563eb)]" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}{hint && <span className="mt-0.5 block text-[12px] leading-snug text-mute">{hint}</span>}</span>
    </label>
  );
}

/** A read-only value with a copy button (URLs, DNS records, tokens). */
export function CopyField({ label, value, testid }: { label: string; value: string; testid?: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const copy = () => {
    try { void navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* the value is on screen */ }
  };
  return (
    <div className="text-xs font-medium text-ink2">
      <span className="mb-1 block">{label}</span>
      <div className="flex items-stretch gap-2">
        <code data-testid={testid} className="min-w-0 flex-1 select-all break-all rounded-md border border-line bg-bg px-2.5 py-1.5 font-mono text-[12px] font-normal text-ink">{value}</code>
        <Btn onClick={copy}>{done ? t("idn.copied") : t("idn.copy")}</Btn>
      </div>
    </div>
  );
}

/** Message key for an API failure (never the backend's text). */
export function idErrorKey(e: unknown): string {
  if (!(e instanceof IdentityError)) return "idn.err.network";
  switch (e.code) {
    case "no_encryption": return "idn.err.noEncryption";
    case "free_mail": return "idn.err.freeMail";
    case "bad_domain": return "idn.err.badDomain";
    case "challenge_missing": return "idn.err.challengeMissing";
    case "domain_taken": return "idn.err.domainTaken";
    case "conflict": return "idn.err.conflict";
    case "validation_failed": return e.fields?.issuer ? "idn.err.issuer" : e.fields?.client_id ? "idn.err.clientId" : "idn.err.validation";
    case "forbidden": return "idn.err.forbidden";
  }
  return "idn.err.generic";
}
