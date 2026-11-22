// EXPERIMENTAL (spike /office-lab, desechable): página de comparación de motores y estilos de animación para la
// oficina. Fuera del menú, solo por URL. Cada panel se carga con import dinámico (ni three/glTF ni Pixi ni GSAP entran
// en el resto de la app) y pausa su render cuando no está en pantalla.
"use client";
import dynamic from "next/dynamic";
import type { ComponentType } from "react";
import { labRestart, labSetSpeed, useLabClockUi, useVisible } from "./clock";
import { LOOP, STEPS } from "./script";

const loading = () => <div className="flex h-full items-center justify-center text-[12px] text-slate-400">Cargando…</div>;
const PanelA = dynamic(() => import("./PanelA"), { ssr: false, loading });
const PanelB = dynamic(() => import("./PanelB"), { ssr: false, loading });
const PanelC = dynamic(() => import("./PanelC"), { ssr: false, loading });
const PanelD = dynamic(() => import("./PanelD"), { ssr: false, loading });

const PANELS: { id: string; name: string; tech: string; C: ComponentType<{ active: boolean }> }[] = [
  { id: "A", name: "Actual (referencia)", tech: "React Three Fiber con los muebles de producción y poses por fórmula (gestures.ts).", C: PanelA },
  { id: "B", name: "Estudio", tech: "Three.js + glTF con esqueleto: clips grabados mezclados con AnimationMixer, gestos aditivos, HDRI y AO.", C: PanelB },
  { id: "C", name: "Plano", tech: "PixiJS v8 en 2D isométrico: vectores ilustrados, interpolación y squash & stretch.", C: PanelC },
  { id: "D", name: "Pictograma", tech: "SVG + línea de tiempo GSAP: infografía en alzado con curvas de motion graphics.", C: PanelD },
];

function Panel({ id, name, tech, C }: (typeof PANELS)[number]) {
  const [ref, visible] = useVisible<HTMLDivElement>();
  return (
    <section className="overflow-hidden rounded-[10px] border border-[#E3E8EF] bg-white" data-panel={id}>
      <header className="flex items-start gap-3 border-b border-[#EEF1F5] px-4 py-3">
        <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-[#0A66C2] text-[12px] font-semibold text-white">{id}</span>
        <div className="min-w-0">
          <h2 className="text-[14px] font-semibold text-[#1B2B44]">{name}</h2>
          <p className="text-[12px] leading-snug text-[#5B6B80]">{tech}</p>
        </div>
        <span className={`ml-auto mt-1 shrink-0 rounded-full px-2 py-0.5 text-[10.5px] font-medium ${visible ? "bg-emerald-50 text-emerald-700" : "bg-slate-100 text-slate-500"}`}>{visible ? "en marcha" : "en pausa"}</span>
      </header>
      <div ref={ref} className="relative aspect-[16/10] w-full bg-white">
        <C active={visible} />
      </div>
    </section>
  );
}

const SPEEDS = [0.5, 1, 1.5, 2];

export function LabPage() {
  const { t, speed } = useLabClockUi();
  const step = STEPS.reduce((n, s) => (t >= s.from ? s.n : n), 1);
  return (
    <main className="min-h-screen bg-white text-[#1B2B44]" style={{ fontFamily: "'IBM Plex Sans', var(--font-sans), system-ui, sans-serif" }}>
      {/* eslint-disable-next-line @next/next/no-page-custom-font -- spike: la tipografía solo en esta página */}
      <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:wght@400;500;600&display=swap" precedence="default" />
      <div className="mx-auto max-w-[1480px] px-4 py-6 sm:px-6">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#0A66C2]">Laboratorio · experimental</p>
            <h1 className="mt-1 text-[22px] font-semibold">Cuatro maneras de animar la oficina</h1>
            <p className="mt-1 max-w-[720px] text-[13px] text-[#5B6B80]">La misma escena en los cuatro paneles: un agente llega, trabaja, se reúne con un compañero, lleva una carpeta a la jefa para aprobar y celebra.</p>
          </div>
          <div className="flex items-center gap-2">
            <button type="button" onClick={labRestart} className="rounded-[10px] bg-[#0A66C2] px-4 py-2 text-[13px] font-semibold text-white transition hover:bg-[#004182]">Repetir</button>
            <label className="flex items-center gap-2 text-[12.5px] text-[#5B6B80]">
              Velocidad
              <select value={speed} onChange={(e) => labSetSpeed(Number(e.target.value))} className="rounded-[10px] border border-[#D5DCE6] bg-white px-2.5 py-2 text-[13px] text-[#1B2B44]">
                {SPEEDS.map((s) => <option key={s} value={s}>{s}×</option>)}
              </select>
            </label>
          </div>
        </div>
        <ol className="mt-4 flex flex-wrap gap-1.5">
          {STEPS.map((s) => (
            <li key={s.n} className={`rounded-full border px-3 py-1 text-[12px] transition ${s.n === step ? "border-[#0A66C2] bg-[#EAF3FC] font-semibold text-[#0A66C2]" : "border-[#E3E8EF] text-[#5B6B80]"}`}>{s.n}. {s.label}</li>
          ))}
          <li className="ml-auto self-center text-[12px] tabular-nums text-[#8A96A8]">{t.toFixed(1)} s / {LOOP} s</li>
        </ol>
        <div className="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2">
          {PANELS.map((p) => <Panel key={p.id} {...p} />)}
        </div>
      </div>
    </main>
  );
}
