"use client";
import { useEffect, useState } from "react";
import { fmtDateTime, intlLocale, LOCALES, langName, useT, type Locale } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { PortalUnavailable, portalPublic } from "@/lib/portalApi";
import { safeAccent, textOn, tokenFromHash, type PublicTaskStatus, type PublicView } from "@/lib/portalLogic";

/**
 * The client portal (B13): a read-only page with the progress of ONE project and the firm's branding.
 * The token is read from the URL fragment and sent in a POST body, so it never reaches a server log, the
 * browser's Referer or another site. Everything shown comes from the server's allowlisted view.
 */

type Phase = { kind: "loading" } | { kind: "missing" } | { kind: "unavailable" } | { kind: "error" } | { kind: "ok"; view: PublicView; logo: string | null };

const KINDS = new Set(["sheet", "doc", "table", "board", "chart", "pdf", "form", "inbox", "agenda"]);
const DOT: Record<PublicTaskStatus, string> = { done: "#2f7d55", in_progress: "#c58a2c", pending: "#9aa3b2" };

function StatusIcon({ status }: { status: PublicTaskStatus }) {
  if (status === "done") {
    return <svg aria-hidden width="16" height="16" viewBox="0 0 16 16" className="shrink-0"><circle cx="8" cy="8" r="7.5" fill={DOT.done} /><path d="M4.5 8.2l2.2 2.2 4.6-4.8" stroke="#fff" strokeWidth="1.8" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>;
  }
  if (status === "in_progress") {
    return <svg aria-hidden width="16" height="16" viewBox="0 0 16 16" className="shrink-0"><circle cx="8" cy="8" r="6.5" fill="none" stroke={DOT.in_progress} strokeWidth="2" /><path d="M8 1.5a6.5 6.5 0 0 1 0 13z" fill={DOT.in_progress} /></svg>;
  }
  return <svg aria-hidden width="16" height="16" viewBox="0 0 16 16" className="shrink-0"><circle cx="8" cy="8" r="6.5" fill="none" stroke={DOT.pending} strokeWidth="1.6" strokeDasharray="2.5 2" /></svg>;
}

function Bar({ value, color }: { value: number; color: string }) {
  return (
    <div className="h-2.5 w-full overflow-hidden rounded-full bg-[#e7eaf0]" role="progressbar" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full transition-[width] duration-700" style={{ width: `${Math.max(0, Math.min(100, value))}%`, background: color }} />
    </div>
  );
}

const fmtDate = (iso: string | null) => {
  if (!iso) return "—";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "—" : new Intl.DateTimeFormat(intlLocale(), { dateStyle: "medium" }).format(d);
};

function Shell({ children, accent }: { children: React.ReactNode; accent?: string }) {
  return (
    <div className="min-h-screen bg-[#f5f6f8] text-[#151b26]" style={{ ["--portal-accent" as string]: accent ?? "#0b1f3a" }}>
      {children}
    </div>
  );
}

function Message({ title, body, testId }: { title: string; body: string; testId: string }) {
  return (
    <Shell>
      <main data-testid={testId} className="mx-auto flex min-h-screen max-w-md flex-col items-center justify-center px-6 text-center">
        <svg aria-hidden width="44" height="44" viewBox="0 0 24 24" className="mb-4 text-[#9aa3b2]"><path fill="currentColor" d="M12 2a5 5 0 0 0-5 5v3H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8a2 2 0 0 0-2-2h-1V7a5 5 0 0 0-5-5zm-3 8V7a3 3 0 1 1 6 0v3z" /></svg>
        <h1 className="text-lg font-semibold">{title}</h1>
        <p className="mt-2 text-sm leading-relaxed text-[#5b6476]">{body}</p>
      </main>
    </Shell>
  );
}

export function PortalPage() {
  const { t, locale } = useT();
  const hydrate = usePreferences((s) => s.hydrate);
  const setPrefs = usePreferences((s) => s.set);
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });

  useEffect(() => { hydrate(); }, [hydrate]);

  useEffect(() => {
    let alive = true;
    let logoUrl: string | null = null;
    const load = async () => {
      const token = tokenFromHash(window.location.hash);
      if (!token) { setPhase({ kind: "missing" }); return; }
      try {
        const view = await portalPublic.view(token);
        if (view.firm.has_logo) {
          const blob = await portalPublic.logo(token).catch(() => null);
          if (blob && /^image\/(png|jpeg|gif)$/.test(blob.type)) logoUrl = URL.createObjectURL(blob);
        }
        if (alive) setPhase({ kind: "ok", view, logo: logoUrl });
      } catch (e) {
        if (alive) setPhase({ kind: e instanceof PortalUnavailable ? "unavailable" : "error" });
      }
    };
    load();
    window.addEventListener("hashchange", load);
    return () => { alive = false; window.removeEventListener("hashchange", load); if (logoUrl) URL.revokeObjectURL(logoUrl); };
  }, []);

  useEffect(() => {
    const v = phase.kind === "ok" ? phase.view : null;
    document.title = v ? `${v.project.title_key ? t(v.project.title_key, v.project.title_params) : v.project.title}${v.firm.name ? ` · ${v.firm.name}` : ""}` : t("portal.public.docTitle");
  }, [phase, t]);

  if (phase.kind === "loading") {
    return <Shell><main data-testid="portal-loading" className="mx-auto max-w-3xl px-4 py-10"><div className="h-24 animate-pulse rounded-xl bg-[#e7eaf0]" /><div className="mt-4 h-40 animate-pulse rounded-xl bg-[#e7eaf0]" /></main></Shell>;
  }
  if (phase.kind === "missing" || phase.kind === "unavailable") return <Message testId="portal-unavailable" title={t("portal.public.unavailableTitle")} body={t("portal.public.unavailableBody")} />;
  if (phase.kind === "error") return <Message testId="portal-error" title={t("portal.public.errorTitle")} body={t("portal.public.errorBody")} />;

  const v = phase.view;
  const accent = safeAccent(v.firm.accent_color);
  const onAccent = textOn(accent);
  const title = (x: { title: string; title_key?: string; title_params?: Record<string, string> }) => (x.title_key ? t(x.title_key, x.title_params) : x.title);
  const firm = v.firm.name;

  return (
    <Shell accent={accent}>
      <header data-testid="portal-header" style={{ background: accent, color: onAccent }}>
        <div className="mx-auto flex max-w-3xl items-center gap-3 px-4 py-4 sm:px-6">
          {phase.logo && <img data-testid="portal-logo" src={phase.logo} alt={firm ? t("portal.public.logoAlt", { firm }) : ""} className="h-10 w-auto max-w-[140px] rounded bg-white/95 object-contain p-1" />}
          <div className="min-w-0">
            {firm && <div data-testid="portal-firm" className="truncate text-[15px] font-semibold">{firm}</div>}
            <div className="text-[11px] opacity-80">{t("portal.public.kicker")}</div>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-3xl space-y-4 px-4 py-5 sm:px-6 sm:py-8">
        <section data-testid="portal-summary" className="rounded-xl border border-[#dfe3ea] bg-white p-4 shadow-sm sm:p-5">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <h1 data-testid="portal-project-title" className="min-w-0 flex-1 text-xl font-semibold leading-tight sm:text-2xl">{title(v.project)}</h1>
            <span data-testid="portal-status" data-status={v.project.status} className="rounded-full border border-[#dfe3ea] px-2.5 py-1 text-[11px] font-semibold text-[#3d4556]">{t(`portal.status.${v.project.status}`)}</span>
          </div>
          <div className="mt-4 flex items-end justify-between gap-3">
            <div className="text-[12.5px] text-[#5b6476]">{t("portal.public.tasksDone", { done: v.project.tasks_done, total: v.project.tasks_total })}</div>
            <div data-testid="portal-progress" className="font-mono text-2xl font-semibold" style={{ color: accent === "#ffffff" ? "#151b26" : accent }}>{v.project.progress_pct}%</div>
          </div>
          <div className="mt-2"><Bar value={v.project.progress_pct} color={accent} /></div>
          <div data-testid="portal-updated" className="mt-3 text-[11.5px] text-[#5b6476]">{t("portal.public.updated", { when: fmtDateTime(v.updated_at) })}</div>
        </section>

        {v.milestones.length > 0 && (
          <section aria-labelledby="portal-ms" className="space-y-3">
            <h2 id="portal-ms" className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-[#5b6476]">{t("portal.public.milestones")}</h2>
            {v.milestones.map((m, i) => (
              <article key={i} data-testid="portal-milestone" className="rounded-xl border border-[#dfe3ea] bg-white p-4 shadow-sm">
                <div className="flex items-center gap-2">
                  <StatusIcon status={m.status} />
                  <h3 className="min-w-0 flex-1 text-[14.5px] font-semibold">{m.title || m.title_key ? title(m) : t("portal.public.otherWork")}</h3>
                  <span className="font-mono text-[12px] text-[#5b6476]">{m.progress_pct}%</span>
                </div>
                <div className="mt-2"><Bar value={m.progress_pct} color={accent} /></div>
                <ul className="mt-3 space-y-2">
                  {m.tasks.map((tk, j) => (
                    <li key={j} data-testid="portal-task" data-status={tk.status} className="flex items-start gap-2.5 text-[13.5px] leading-snug">
                      <span className="mt-0.5"><StatusIcon status={tk.status} /></span>
                      <span className={`min-w-0 flex-1 ${tk.status === "done" ? "text-[#5b6476]" : ""}`}>{title(tk)}</span>
                      <span className="sr-only">{t(`portal.task.${tk.status}`)}</span>
                    </li>
                  ))}
                </ul>
              </article>
            ))}
          </section>
        )}

        <section aria-labelledby="portal-del" className="rounded-xl border border-[#dfe3ea] bg-white p-4 shadow-sm">
          <h2 id="portal-del" className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#5b6476]">{t("portal.public.deliverables")}</h2>
          {v.deliverables.length === 0 ? (
            <p data-testid="portal-no-deliverables" className="mt-2 text-[13px] text-[#5b6476]">{t("portal.public.noDeliverables")}</p>
          ) : (
            <ul className="mt-2 divide-y divide-[#eef0f4]">
              {v.deliverables.map((d, i) => (
                <li key={i} data-testid="portal-deliverable" className="flex items-center gap-3 py-2.5">
                  <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-[10px] font-bold uppercase" style={{ background: `${accent}1a`, color: accent === "#ffffff" ? "#151b26" : accent }}>{t(`portal.kind.${KINDS.has(d.kind) ? d.kind : "file"}`)}</span>
                  <span className="min-w-0 flex-1 text-[13.5px] font-medium">{d.title}</span>
                  <span className="shrink-0 text-[11.5px] text-[#5b6476]">{fmtDate(d.completed_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>

        <footer className="space-y-2 px-1 pb-8 pt-2 text-[11.5px] leading-relaxed text-[#5b6476]">
          <p data-testid="portal-readonly">{firm ? t("portal.public.readOnlyBy", { firm }) : t("portal.public.readOnly")}{v.expires_at ? ` ${t("portal.public.expires", { date: fmtDate(v.expires_at) })}` : ""}</p>
          <label className="inline-flex items-center gap-1.5">
            <span>{t("portal.public.language")}</span>
            <select data-testid="portal-lang" value={locale} onChange={(e) => setPrefs({ locale: e.target.value as Locale })} className="rounded border border-[#dfe3ea] bg-white px-1.5 py-1 text-base text-[#151b26] sm:text-[12px]">
              {LOCALES.map((l) => <option key={l} value={l}>{langName(l)}</option>)}
            </select>
          </label>
        </footer>
      </main>
    </Shell>
  );
}
