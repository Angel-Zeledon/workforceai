"use client";
import { useEffect, useState } from "react";
import { LOCALES, useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { Brand } from "./Brand";

const IDX = [0, 1, 2, 3];
const wrap = "mx-auto w-full max-w-6xl px-5 sm:px-8";
const eyebrow = "text-xs font-semibold uppercase tracking-[0.14em] text-accent";
const h2 = "mt-3 text-3xl font-semibold leading-tight tracking-[-0.02em] text-ink sm:text-[2.5rem] sm:leading-[1.1]";
const btnPrimary = "inline-flex items-center justify-center rounded-md bg-accent px-5 py-3 text-sm font-semibold text-white transition hover:bg-accent-hover";

function Icon({ n }: { n: number }) {
  const p = { fill: "none", stroke: "currentColor", strokeWidth: 1.6, strokeLinecap: "round", strokeLinejoin: "round" } as const;
  const paths = [
    <g key="0" {...p}><path d="M12 3l7 3v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10V6z" /><path d="M9 12l2 2 4-4" /></g>,
    <g key="1" {...p}><rect x="5" y="3.5" width="14" height="17" rx="2" /><path d="M8.5 8h7M8.5 12h7M8.5 16h4" /></g>,
    <g key="2" {...p}><ellipse cx="12" cy="6" rx="7" ry="3" /><path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6" /></g>,
    <g key="3" {...p}><circle cx="12" cy="12" r="3" /><path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.6 5.6l2.1 2.1M16.3 16.3l2.1 2.1M18.4 5.6l-2.1 2.1M7.7 16.3l-2.1 2.1" /></g>,
  ];
  return <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden>{paths[n]}</svg>;
}

function Check() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" aria-hidden className="mt-0.5 shrink-0 text-accent">
      <circle cx="10" cy="10" r="9" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M6 10.3l2.8 2.8L14 7.6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/** Static product mock (HTML/CSS): the office dashboard with agents, KPIs and a pending approval. */
function ProductMock() {
  const { t } = useT();
  const dot = ["bg-ok", "bg-accent", "bg-warn", "bg-line-strong"];
  const kpis = [["4", "land.mock.kpi1"], ["12", "land.mock.kpi2"], ["1", "land.mock.kpi3"], ["$18.40", "land.mock.kpi4"]];
  return (
    <div aria-hidden className="select-none overflow-hidden rounded-xl border border-white/10 bg-panel shadow-[0_30px_80px_-20px_rgba(0,0,0,0.55)]">
      <div className="flex items-center gap-2 border-b border-line bg-panel2 px-4 py-2.5">
        <span className="flex gap-1.5"><i className="h-2.5 w-2.5 rounded-full bg-line-strong" /><i className="h-2.5 w-2.5 rounded-full bg-line-strong" /><i className="h-2.5 w-2.5 rounded-full bg-line-strong" /></span>
        <span className="ml-2 truncate text-[11px] font-medium text-ink2">{t("land.mock.title")}</span>
        <span className="ml-auto flex items-center gap-1.5 text-[10px] font-medium text-ok"><i className="h-1.5 w-1.5 rounded-full bg-ok" />{t("land.mock.live")}</span>
      </div>
      <div className="grid gap-4 p-4 sm:grid-cols-[1fr_1.15fr]">
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-mute">{t("land.mock.team")}</div>
          <ul className="space-y-1.5">
            {IDX.map((i) => (
              <li key={i} className="flex items-center gap-3 rounded-md border border-line bg-panel px-3 py-2">
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-navy text-[11px] font-semibold text-white">{t(`land.mock.a${i + 1}.n`).slice(0, 1)}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-semibold text-ink">{t(`land.mock.a${i + 1}.n`)} <span className="font-normal text-mute">· {t(`land.mock.a${i + 1}.r`)}</span></span>
                  <span className="block truncate text-[11px] text-mute">{t(`land.mock.a${i + 1}.s`)}</span>
                </span>
                <i className={`h-2 w-2 rounded-full ${dot[i]}`} />
              </li>
            ))}
          </ul>
        </div>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-2">
            {kpis.map(([v, k]) => (
              <div key={k} className="rounded-md border border-line bg-panel2 px-3 py-2">
                <div className="text-lg font-semibold tracking-tight text-ink">{v}</div>
                <div className="truncate text-[10px] text-mute">{t(k)}</div>
              </div>
            ))}
          </div>
          <div className="rounded-md border border-warn/40 bg-warn/5 p-3">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-warn">{t("land.mock.appr.label")}</div>
            <div className="mt-1 text-xs font-semibold text-ink">{t("land.mock.appr.title")}</div>
            <div className="text-[11px] text-mute">{t("land.mock.appr.meta")}</div>
            <div className="mt-2 flex items-center gap-2">
              <span className="rounded bg-accent px-2.5 py-1 text-[11px] font-medium text-white">{t("land.mock.appr.ok")}</span>
              <span className="rounded border border-line-strong bg-panel px-2.5 py-1 text-[11px] font-medium text-ink2">{t("land.mock.appr.no")}</span>
            </div>
          </div>
          <div className="flex items-center gap-2 rounded-md border border-line px-3 py-2 text-[11px] text-ink2">
            <svg width="14" height="14" viewBox="0 0 24 24" className="text-ok" aria-hidden><rect x="4" y="3" width="16" height="18" rx="2" fill="none" stroke="currentColor" strokeWidth="1.8" /><path d="M8 9h8M8 13h8M8 17h5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>
            <span className="truncate">{t("land.mock.file")}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

export function Landing() {
  const { t, locale } = useT();
  const setPref = usePreferences((s) => s.set);
  const [open, setOpen] = useState(false);
  useEffect(() => { usePreferences.getState().hydrate(); }, []);
  const links: [string, string][] = [["#producto", "land.nav.features"], ["#como", "land.nav.how"], ["#casos", "land.nav.cases"], ["#seguridad", "land.nav.security"]];
  const close = () => setOpen(false);

  return (
    <div data-testid="landing" className="min-h-screen bg-panel text-ink">
      <header className="sticky top-0 z-30 border-b border-line bg-panel/95 backdrop-blur">
        <div className={`${wrap} flex h-16 items-center gap-6`}>
          <a href="#top" aria-label="AI Workforce OS"><Brand /></a>
          <nav aria-label={t("land.nav.aria")} className="ml-6 hidden items-center gap-6 md:flex">
            {links.map(([h, k]) => <a key={h} href={h} className="text-sm text-ink2 transition hover:text-ink">{t(k)}</a>)}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <div role="group" aria-label={t("land.lang")} className="hidden items-center rounded-md border border-line text-[11px] font-semibold sm:flex">
              {LOCALES.map((l, i) => (
                <button key={l} type="button" aria-pressed={locale === l} onClick={() => setPref({ locale: l })}
                  className={`px-2 py-1 ${locale === l ? "bg-navy text-white" : "text-mute hover:text-ink"} ${i === 0 ? "rounded-l" : i === LOCALES.length - 1 ? "rounded-r" : ""}`}>{l.toUpperCase()}</button>
              ))}
            </div>
            <a href="/login" data-testid="land-login" className="hidden text-sm font-medium text-ink hover:text-accent sm:inline">{t("land.nav.login")}</a>
            <a href="/register" data-testid="land-signup" className="inline-flex items-center rounded-md bg-accent px-4 py-2 text-sm font-semibold text-white transition hover:bg-accent-hover">{t("land.nav.signup")}</a>
            <button type="button" aria-label={t("land.nav.menu")} aria-expanded={open} onClick={() => setOpen(!open)} className="rounded-md border border-line p-2 md:hidden">
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden><path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>
            </button>
          </div>
        </div>
        {open && (
          <nav aria-label={t("land.nav.aria")} className="border-t border-line bg-panel px-5 pb-4 md:hidden">
            {links.map(([h, k]) => <a key={h} href={h} onClick={close} className="block border-b border-line py-3 text-sm text-ink">{t(k)}</a>)}
            <a href="/login" className="block py-3 text-sm font-medium text-ink">{t("land.nav.login")}</a>
          </nav>
        )}
      </header>

      <main id="top">
        {/* Hero */}
        <section className="relative overflow-hidden bg-navy text-white">
          <div aria-hidden className="absolute inset-0 opacity-[0.07]" style={{ backgroundImage: "linear-gradient(rgba(255,255,255,.6) 1px, transparent 1px), linear-gradient(90deg, rgba(255,255,255,.6) 1px, transparent 1px)", backgroundSize: "56px 56px" }} />
          <div className={`${wrap} relative grid items-center gap-12 py-16 sm:py-24 lg:grid-cols-[1fr_1.1fr] lg:gap-14`}>
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[#7fd1c8]">{t("land.hero.eyebrow")}</p>
              <h1 className="mt-5 text-[2.5rem] font-semibold leading-[1.05] tracking-[-0.025em] sm:text-6xl">{t("land.hero.title")}</h1>
              <p className="mt-6 max-w-xl text-base leading-relaxed text-white/75 sm:text-lg">{t("land.hero.sub")}</p>
              <div className="mt-9 flex flex-col gap-3 sm:flex-row">
                <a href="/register" data-testid="land-hero-signup" className="inline-flex items-center justify-center rounded-md bg-white px-6 py-3 text-sm font-semibold text-navy transition hover:bg-[#e6f2f1]">{t("land.hero.cta1")}</a>
                <a href="/login" className="inline-flex items-center justify-center rounded-md border border-white/30 px-6 py-3 text-sm font-semibold text-white transition hover:bg-white/10">{t("land.hero.cta2")}</a>
              </div>
              <p className="mt-5 text-sm text-white/60">{t("land.hero.note")}</p>
            </div>
            <ProductMock />
          </div>
        </section>

        {/* Trust row */}
        <section aria-label="trust" className="border-b border-line bg-panel2">
          <div className={`${wrap} grid gap-px sm:grid-cols-2 lg:grid-cols-4`}>
            {IDX.map((i) => (
              <div key={i} className="py-7 sm:px-5 sm:first:pl-0">
                <div className="flex items-center gap-2.5 text-accent"><Icon n={i} /><h3 className="text-sm font-semibold text-ink">{t(`land.trust.${i}.t`)}</h3></div>
                <p className="mt-2 text-sm leading-relaxed text-mute">{t(`land.trust.${i}.d`)}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Product */}
        <section id="producto" className="scroll-mt-16 py-20 sm:py-28">
          <div className={wrap}>
            <p className={eyebrow}>{t("land.feat.eyebrow")}</p>
            <h2 className={`${h2} max-w-3xl`}>{t("land.feat.title")}</h2>
            <p className="mt-4 max-w-2xl text-base leading-relaxed text-mute">{t("land.feat.sub")}</p>
            <div className="mt-12 grid gap-px overflow-hidden rounded-xl border border-line bg-line sm:grid-cols-2">
              {IDX.map((i) => (
                <div key={i} className="bg-panel p-7 sm:p-9">
                  <span className="flex h-10 w-10 items-center justify-center rounded-md bg-accent-soft text-accent"><Icon n={i} /></span>
                  <h3 className="mt-5 text-lg font-semibold tracking-tight">{t(`land.feat.${i}.t`)}</h3>
                  <p className="mt-2 text-sm leading-relaxed text-mute">{t(`land.feat.${i}.d`)}</p>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* How it works */}
        <section id="como" className="scroll-mt-16 border-y border-line bg-bg py-20 sm:py-28">
          <div className={wrap}>
            <p className={eyebrow}>{t("land.how.eyebrow")}</p>
            <h2 className={`${h2} max-w-3xl`}>{t("land.how.title")}</h2>
            <ol className="mt-12 grid gap-6 md:grid-cols-3">
              {[0, 1, 2].map((i) => (
                <li key={i} className="rounded-xl border border-line bg-panel p-7">
                  <span className="text-sm font-semibold text-accent">0{i + 1}</span>
                  <h3 className="mt-4 text-lg font-semibold tracking-tight">{t(`land.how.${i}.t`)}</h3>
                  <p className="mt-2 text-sm leading-relaxed text-mute">{t(`land.how.${i}.d`)}</p>
                </li>
              ))}
            </ol>
          </div>
        </section>

        {/* Use cases */}
        <section id="casos" className="scroll-mt-16 py-20 sm:py-28">
          <div className={wrap}>
            <p className={eyebrow}>{t("land.cases.eyebrow")}</p>
            <h2 className={`${h2} max-w-3xl`}>{t("land.cases.title")}</h2>
            <div className="mt-12 grid gap-x-10 gap-y-0 md:grid-cols-2">
              {IDX.map((i) => (
                <div key={i} className="border-t border-line-strong py-7">
                  <h3 className="text-lg font-semibold tracking-tight">{t(`land.cases.${i}.t`)}</h3>
                  <p className="mt-2 text-sm leading-relaxed text-mute">{t(`land.cases.${i}.d`)}</p>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Security */}
        <section id="seguridad" className="scroll-mt-16 bg-navy py-20 text-white sm:py-28">
          <div className={`${wrap} grid gap-12 lg:grid-cols-[1fr_1.1fr] lg:gap-20`}>
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[#7fd1c8]">{t("land.sec.eyebrow")}</p>
              <h2 className="mt-3 text-3xl font-semibold leading-tight tracking-[-0.02em] sm:text-[2.5rem] sm:leading-[1.1]">{t("land.sec.title")}</h2>
              <p className="mt-4 max-w-md text-base leading-relaxed text-white/70">{t("land.sec.sub")}</p>
            </div>
            <ul className="divide-y divide-white/10 border-y border-white/10">
              {[0, 1, 2, 3, 4].map((i) => (
                <li key={i} className="flex gap-3 py-4 text-[15px] leading-relaxed text-white/85">
                  <svg width="18" height="18" viewBox="0 0 20 20" aria-hidden className="mt-0.5 shrink-0 text-[#7fd1c8]"><path d="M4 10.5l4 4 8-9" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
                  {t(`land.sec.${i}`)}
                </li>
              ))}
            </ul>
          </div>
        </section>

        {/* Final CTA */}
        <section className="py-20 sm:py-24">
          <div className={`${wrap} flex flex-col items-start gap-8 rounded-2xl border border-line bg-panel2 px-7 py-12 sm:px-12 md:flex-row md:items-center md:justify-between`}>
            <div className="max-w-xl">
              <h2 className="text-2xl font-semibold tracking-[-0.02em] sm:text-3xl">{t("land.cta.title")}</h2>
              <p className="mt-3 text-base leading-relaxed text-mute">{t("land.cta.sub")}</p>
            </div>
            <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row">
              <a href="/register" data-testid="land-final-signup" className={btnPrimary}>{t("land.hero.cta1")}</a>
              <a href="/login" className="inline-flex items-center justify-center rounded-md border border-line-strong bg-panel px-5 py-3 text-sm font-semibold text-ink transition hover:bg-bg">{t("land.hero.cta2")}</a>
            </div>
          </div>
        </section>
      </main>

      <footer className="border-t border-line bg-panel">
        <div className={`${wrap} grid gap-10 py-12 md:grid-cols-[1.4fr_1fr_1fr]`}>
          <div>
            <Brand />
            <p className="mt-4 max-w-xs text-sm leading-relaxed text-mute">{t("land.foot.tag")}</p>
          </div>
          <div>
            <h4 className="text-xs font-semibold uppercase tracking-wider text-ink">{t("land.foot.product")}</h4>
            <ul className="mt-4 space-y-2.5 text-sm text-mute">
              {links.map(([h, k]) => <li key={h}><a href={h} className="hover:text-ink">{t(k)}</a></li>)}
            </ul>
          </div>
          <div>
            <h4 className="text-xs font-semibold uppercase tracking-wider text-ink">{t("land.foot.account")}</h4>
            <ul className="mt-4 space-y-2.5 text-sm text-mute">
              <li><a href="/login" className="hover:text-ink">{t("land.nav.login")}</a></li>
              <li><a href="/register" className="hover:text-ink">{t("land.nav.signup")}</a></li>
            </ul>
          </div>
        </div>
        <div className="border-t border-line">
          <p className={`${wrap} py-5 text-xs text-mute`}>© {new Date().getFullYear()} AI Workforce OS. {t("land.foot.rights")}</p>
        </div>
      </footer>
    </div>
  );
}
