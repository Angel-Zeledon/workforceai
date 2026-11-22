// EXPERIMENTAL (spike /office-lab, desechable).
// Panel D, "Pictograma": infografía animada en SVG con una línea de tiempo de GSAP. Vista de alzado (de lado), figuras
// tipo pictograma de señalética (monocromas, geométricas, sin caras ni contornos) y movimiento de "motion graphics":
// curvas con rebote y sobreimpulso, una marca de aprobado que se dibuja sola. La línea de tiempo se construye UNA vez
// con las mismas horas del guion (PHASES / LINES) y cada fotograma se posiciona en el instante del reloj compartido
// (tl.seek), así va sincronizada con los otros paneles y se puede rebobinar.
"use client";
import { useEffect, useRef } from "react";
import { labTime } from "./clock";
import { LINES, LOOP, PHASES, STEPS } from "./script";

const NAVY = "#1B2B44";
const AGENT = "#0A66C2";
const GREY = "#8A96A8";
const BOSS = "#4A5568";
const LINE = "#D5DCE6";
const GROUND = 300;
/** Posiciones en el alzado (px). */
const XD = { door: 38, desk: 150, chair: 196, meet: 292, table: 340, partner: 388, bossStand: 468, bossDesk: 522, boss: 566 };

interface Fig { x: number; face: number; walk: number; cycle: number; sit: number; type: number; tcyc: number; talk: number; tk: number; reach: number; lift: number; sq: number; up: number; nod: number; folder: number }
const fig = (x: number, face: number): Fig => ({ x, face, walk: 0, cycle: 0, sit: 0, type: 0, tcyc: 0, talk: 0, tk: 0, reach: 0, lift: 0, sq: 1, up: 0, nod: 0, folder: 0 });

/** Extremidades: ángulo 0 = hacia abajo, positivo = hacia delante. Devuelve el path "M hombro L codo L mano". */
function limb(ox: number, oy: number, a: number, b: number, l1: number, l2: number) {
  const mx = ox + Math.sin(a) * l1, my = oy + Math.cos(a) * l1;
  const ex = mx + Math.sin(a + b) * l2, ey = my + Math.cos(a + b) * l2;
  return { d: `M${ox.toFixed(1)} ${oy.toFixed(1)} L${mx.toFixed(1)} ${my.toFixed(1)} L${ex.toFixed(1)} ${ey.toFixed(1)}`, ex, ey };
}

/** Pose del pictograma a partir de los canales que anima GSAP. */
function figurePaths(f: Fig, t: number) {
  const s = Math.sin(f.cycle), w = f.walk;
  const sit = f.sit;
  const hipY = -40 + 14 * sit - f.lift;
  const hipX = 0;
  let legA = [s * 0.55 * w + 1.5 * sit, -s * 0.55 * w + 1.4 * sit];
  let knee = [-Math.max(0, Math.sin(f.cycle + 1.3)) * 0.8 * w - 1.5 * sit, -Math.max(0, -Math.sin(f.cycle + 1.3)) * 0.8 * w - 1.4 * sit];
  // agacharse antes y después del salto (sq < 1)
  const crouch = Math.max(0, 1 - f.sq) * 4;
  if (crouch > 0) { legA = legA.map((v) => v + crouch * 0.4); knee = knee.map((v) => v - crouch * 0.8); }
  const shX = hipX + 4 * f.reach + 3 * f.type, shY = hipY - 32;
  let armA = [-s * 0.5 * w, s * 0.5 * w], el = [0.3 * w, 0.3 * w];
  if (f.type > 0) { armA = [1.25 * f.type, 1.2 * f.type]; el = [0.3 * f.type + Math.sin(f.tcyc) * 0.14 * f.type, 0.3 * f.type + Math.sin(f.tcyc + 2.2) * 0.14 * f.type]; }
  if (f.talk > 0) { armA = [armA[0] + (0.9 + Math.sin(f.tk) * 0.35) * f.talk, armA[1] + 0.3 * f.talk]; el = [el[0] + (0.8 + Math.sin(f.tk * 1.4) * 0.3) * f.talk, el[1] + 0.4 * f.talk]; }
  if (f.reach > 0) { armA[0] += 1.35 * f.reach; el[0] -= 0.2 * f.reach; }
  if (f.folder > 0 && w > 0) { armA[0] = 0.95; el[0] = 0.9; }
  if (f.up > 0) { armA = [armA[0] + (Math.PI * 0.86 - armA[0]) * f.up, armA[1] + (Math.PI * 0.78 - armA[1]) * f.up]; el = [el[0] * (1 - f.up), el[1] * (1 - f.up)]; }
  const legN = limb(hipX, hipY, legA[0], knee[0], 20, 20), legF = limb(hipX, hipY, legA[1], knee[1], 20, 20);
  const armN = limb(shX, shY + 3, armA[0], el[0], 15, 15), armF = limb(shX, shY + 3, armA[1], el[1], 15, 15);
  const nod = f.nod * Math.max(0, Math.sin(t * 6)) * 3;
  return { hipX, hipY, shX, shY, legN, legF, armN, armF, head: { x: shX + 3 + f.type * 2, y: shY - 13 + nod } };
}

export default function PanelD({ active }: { active: boolean }) {
  const svg = useRef<SVGSVGElement>(null);
  const activeRef = useRef(active);
  activeRef.current = active;

  useEffect(() => {
    const root = svg.current!;
    let raf = 0, killed = false;
    let tl: gsap.core.Timeline | null = null;
    const q = <T extends Element>(sel: string) => root.querySelector(sel) as T;
    (async () => {
      const { gsap } = await import("gsap");
      if (killed) return;
      const A = fig(XD.door, 1), Pt = fig(XD.partner, -1), B = fig(XD.boss, -1);
      B.sit = 1; B.type = 1;
      const scr = { on: 0, lines: 0 }, stamp = { k: 0, draw: 1, ring: 0 }, cheer = { ring: 0, ro: 0 }, folder = { table: 1, boss: 0 };
      const bub = LINES.map(() => ({ o: 0, y: 6 }));
      tl = gsap.timeline({ paused: true });
      const ph = Object.fromEntries(PHASES.map((p) => [p.name, p]));
      const walk = (name: string, x0: number, x1: number) => {
        const p = ph[name], d = p.t1 - p.t0;
        tl!.fromTo(A, { x: x0 }, { x: x1, duration: d, ease: "power1.inOut", immediateRender: false }, p.t0);
        tl!.fromTo(A, { walk: 0 }, { walk: 1, duration: 0.3, ease: "power1.out", immediateRender: false }, p.t0);
        tl!.fromTo(A, { walk: 1 }, { walk: 0, duration: 0.3, ease: "power1.in", immediateRender: false }, p.t1 - 0.3);
        tl!.fromTo(A, { cycle: 0 }, { cycle: (Math.abs(x1 - x0) / 13) * Math.PI, duration: d, ease: "power1.inOut", immediateRender: false }, p.t0);
        tl!.set(A, { face: x1 > x0 ? 1 : -1 }, p.t0);
      };
      walk("enter", XD.door, XD.chair);
      tl.set(A, { face: -1 }, ph.sit.t0);
      tl.fromTo(A, { sit: 0 }, { sit: 1, duration: 0.7, ease: "back.out(1.6)", immediateRender: false }, ph.sit.t0 + 0.3);
      tl.fromTo(scr, { on: 0 }, { on: 1, duration: 0.5, ease: "power2.out", immediateRender: false }, ph.sit.t0 + 0.7);
      tl.fromTo(A, { type: 0 }, { type: 1, duration: 0.35, ease: "power2.out", immediateRender: false }, ph.type.t0);
      tl.fromTo(A, { tcyc: 0 }, { tcyc: 70, duration: ph.type.t1 - ph.type.t0, ease: "none", immediateRender: false }, ph.type.t0);
      tl.fromTo(scr, { lines: 0 }, { lines: 1, duration: ph.type.t1 - ph.type.t0, ease: "steps(12)", immediateRender: false }, ph.type.t0);
      tl.fromTo(A, { type: 1 }, { type: 0, duration: 0.3, immediateRender: false }, ph.type.t1 - 0.3);
      tl.fromTo(A, { sit: 1 }, { sit: 0, duration: 0.6, ease: "power2.inOut", immediateRender: false }, ph.stand.t0);
      tl.fromTo(scr, { on: 1 }, { on: 0, duration: 0.6, immediateRender: false }, ph.stand.t0 + 0.2);
      walk("walkMeet", XD.chair, XD.meet);
      // conversación
      const l0 = LINES[0], l1 = LINES[1];
      tl.fromTo(A, { talk: 0 }, { talk: 1, duration: 0.25, immediateRender: false }, l0.t0);
      tl.fromTo(A, { tk: 0 }, { tk: 30, duration: l0.t1 - l0.t0, ease: "none", immediateRender: false }, l0.t0);
      tl.fromTo(A, { talk: 1 }, { talk: 0, duration: 0.25, immediateRender: false }, l0.t1 - 0.25);
      tl.fromTo(Pt, { nod: 0 }, { nod: 1, duration: 0.2, immediateRender: false }, l0.t0);
      tl.fromTo(Pt, { nod: 1 }, { nod: 0, duration: 0.2, immediateRender: false }, l0.t1 - 0.2);
      tl.fromTo(Pt, { talk: 0 }, { talk: 1, duration: 0.25, immediateRender: false }, l1.t0);
      tl.fromTo(Pt, { tk: 0 }, { tk: 30, duration: l1.t1 - l1.t0, ease: "none", immediateRender: false }, l1.t0);
      tl.fromTo(Pt, { talk: 1 }, { talk: 0, duration: 0.2, immediateRender: false }, 14.6);
      tl.fromTo(A, { nod: 0 }, { nod: 1, duration: 0.2, immediateRender: false }, l1.t0);
      tl.fromTo(A, { nod: 1 }, { nod: 0, duration: 0.2, immediateRender: false }, l1.t1 - 0.4);
      // el compañero le pasa la carpeta
      tl.fromTo(Pt, { reach: 0 }, { reach: 1, duration: 0.3, ease: "power2.out", immediateRender: false }, 14.7);
      tl.set(folder, { table: 0 }, 14.7);
      tl.set(Pt, { folder: 1 }, 14.7);
      tl.set(Pt, { folder: 0 }, 15.0);
      tl.set(A, { folder: 1 }, 15.0);
      tl.fromTo(Pt, { reach: 1 }, { reach: 0, duration: 0.35, immediateRender: false }, 15.0);
      walk("walkBoss", XD.meet, XD.bossStand);
      tl.fromTo(B, { type: 1 }, { type: 0, duration: 0.3, immediateRender: false }, 16.4);
      // entrega
      tl.fromTo(A, { reach: 0 }, { reach: 1, duration: 0.45, ease: "power3.out", immediateRender: false }, ph.give.t0);
      tl.fromTo(B, { reach: 0 }, { reach: 1, duration: 0.4, ease: "power3.out", immediateRender: false }, ph.give.t0 + 0.3);
      tl.set(A, { folder: 0 }, 18.5);
      tl.set(folder, { boss: 1 }, 18.5);
      tl.fromTo(A, { reach: 1 }, { reach: 0, duration: 0.35, immediateRender: false }, 18.5);
      tl.fromTo(B, { reach: 1 }, { reach: 0, duration: 0.35, immediateRender: false }, 18.55);
      // aprobado: la jefa asiente, aparece el sello y la marca se dibuja
      tl.fromTo(B, { nod: 0 }, { nod: 1, duration: 0.2, immediateRender: false }, ph.approve.t0);
      tl.fromTo(B, { nod: 1 }, { nod: 0, duration: 0.2, immediateRender: false }, ph.approve.t1);
      tl.fromTo(stamp, { k: 0 }, { k: 1, duration: 0.5, ease: "back.out(2.6)", immediateRender: false }, 19.1);
      tl.fromTo(stamp, { draw: 1 }, { draw: 0, duration: 0.45, ease: "power2.inOut", immediateRender: false }, 19.3);
      tl.fromTo(stamp, { ring: 0 }, { ring: 1, duration: 0.9, ease: "power2.out", immediateRender: false }, 19.2);
      tl.fromTo(stamp, { k: 1 }, { k: 0, duration: 0.35, ease: "back.in(2)", immediateRender: false }, 23.2);
      // celebración: brazos en Y, salto con anticipación, aro que se expande
      const c0 = ph.celebrate.t0;
      tl.set(A, { face: 1 }, c0);
      tl.to(A, { keyframes: [
        { sq: 0.82, duration: 0.22, ease: "power2.out" },
        { sq: 1.12, lift: 30, duration: 0.3, ease: "power3.out" },
        { sq: 1, lift: 0, duration: 0.26, ease: "power2.in" },
        { sq: 0.86, duration: 0.1, ease: "power1.out" },
        { sq: 1.06, lift: 12, duration: 0.2, ease: "power2.out" },
        { sq: 1, lift: 0, duration: 0.2, ease: "bounce.out" },
      ] }, c0);
      tl.fromTo(A, { up: 0 }, { up: 1, duration: 0.35, ease: "back.out(2)", immediateRender: false }, c0 + 0.15);
      tl.fromTo(A, { up: 1 }, { up: 0, duration: 0.4, ease: "power2.inOut", immediateRender: false }, ph.celebrate.t1 - 0.45);
      tl.fromTo(cheer, { ring: 0, ro: 1 }, { ring: 1, ro: 0, duration: 1.1, ease: "power2.out", immediateRender: false }, c0 + 0.45);
      tl.fromTo(B, { type: 0 }, { type: 1, duration: 0.3, immediateRender: false }, 21.4);
      walk("walkBack", XD.bossStand, XD.chair);
      tl.set(A, { face: -1 }, ph.sitBack.t0);
      tl.fromTo(A, { sit: 0 }, { sit: 1, duration: 0.7, ease: "back.out(1.6)", immediateRender: false }, ph.sitBack.t0 + 0.3);
      tl.fromTo(scr, { on: 0 }, { on: 1, duration: 0.5, immediateRender: false }, ph.sitBack.t0 + 0.8);
      LINES.forEach((l, i) => {
        tl!.fromTo(bub[i], { o: 0, y: 8 }, { o: 1, y: 0, duration: 0.3, ease: "back.out(2)", immediateRender: false }, l.t0);
        tl!.fromTo(bub[i], { o: 1, y: 0 }, { o: 0, y: -4, duration: 0.2, ease: "power1.in", immediateRender: false }, l.t1 - 0.2);
      });
      tl.set({}, {}, LOOP); // duración exacta del bucle
      tl.seek(0);

      // --- nodos SVG
      const parts = (id: string) => ({
        g: q<SVGGElement>(`#${id}`), legN: q<SVGPathElement>(`#${id}-legN`), legF: q<SVGPathElement>(`#${id}-legF`), armN: q<SVGPathElement>(`#${id}-armN`), armF: q<SVGPathElement>(`#${id}-armF`),
        torso: q<SVGLineElement>(`#${id}-torso`), head: q<SVGCircleElement>(`#${id}-head`), folder: q<SVGRectElement>(`#${id}-folder`),
      });
      const PA = parts("ag"), PP = parts("pt"), PB = parts("bs");
      const scrEl = q<SVGRectElement>("#scr"), scrGlow = q<SVGRectElement>("#scr-glow"), scrLines = Array.from(root.querySelectorAll<SVGRectElement>(".scr-line"));
      const stampG = q<SVGGElement>("#stamp"), stampCheck = q<SVGPathElement>("#stamp-check"), stampRing = q<SVGCircleElement>("#stamp-ring");
      const cheerRing = q<SVGCircleElement>("#cheer-ring");
      const fTable = q<SVGRectElement>("#f-table"), fBoss = q<SVGRectElement>("#f-boss");
      const bubEls = LINES.map((_, i) => q<SVGGElement>(`#bub-${i}`));
      const prog = q<SVGRectElement>("#prog");
      const draw = (P: ReturnType<typeof parts>, F: Fig, t: number) => {
        const p = figurePaths(F, t);
        P.g.setAttribute("transform", `translate(${F.x.toFixed(1)} ${GROUND}) scale(${(F.face * (2 - F.sq)).toFixed(3)} ${F.sq.toFixed(3)})`);
        P.legN.setAttribute("d", p.legN.d); P.legF.setAttribute("d", p.legF.d); P.armN.setAttribute("d", p.armN.d); P.armF.setAttribute("d", p.armF.d);
        P.torso.setAttribute("x1", String(p.hipX)); P.torso.setAttribute("y1", String(p.hipY - 4)); P.torso.setAttribute("x2", String(p.shX)); P.torso.setAttribute("y2", String(p.shY + 5));
        P.head.setAttribute("cx", p.head.x.toFixed(1)); P.head.setAttribute("cy", p.head.y.toFixed(1));
        if (P.folder) { P.folder.style.opacity = F.folder ? "1" : "0"; P.folder.setAttribute("transform", `translate(${(p.armN.ex - 2).toFixed(1)} ${(p.armN.ey - 14).toFixed(1)})`); }
      };
      const render = () => {
        const t = labTime() % LOOP;
        tl!.seek(t, false);
        draw(PA, A, t); draw(PP, Pt, t); draw(PB, B, t);
        scrEl.setAttribute("fill", scr.on > 0.05 ? "#EAF3FC" : NAVY);
        scrGlow.style.opacity = String(scr.on * (0.35 + 0.25 * A.type * (0.5 + 0.5 * Math.sin(t * 9))));
        const n = Math.round(scr.lines * scrLines.length);
        scrLines.forEach((l, i) => { l.style.opacity = scr.on > 0.05 && (A.type > 0 ? i < n : true) ? String(scr.on) : "0"; });
        stampG.setAttribute("transform", `translate(${XD.bossDesk} ${GROUND - 92}) scale(${stamp.k.toFixed(3)})`);
        stampCheck.style.strokeDashoffset = String(stamp.draw);
        stampRing.setAttribute("r", String(16 + stamp.ring * 16)); stampRing.style.opacity = String((1 - stamp.ring) * 0.8);
        cheerRing.setAttribute("cx", A.x.toFixed(1)); cheerRing.setAttribute("r", String(20 + cheer.ring * 50)); cheerRing.style.opacity = String(cheer.ro * 0.7);
        fTable.style.opacity = String(folder.table); fBoss.style.opacity = String(folder.boss);
        bubEls.forEach((el, i) => { el.style.opacity = String(bub[i].o); el.setAttribute("transform", `translate(0 ${bub[i].y.toFixed(1)})`); });
        prog.setAttribute("width", ((t / LOOP) * 584).toFixed(1));
      };
      const loop = () => { if (killed) return; if (activeRef.current) render(); raf = requestAnimationFrame(loop); };
      render();
      raf = requestAnimationFrame(loop);
    })();
    return () => { killed = true; cancelAnimationFrame(raf); tl?.kill(); };
  }, []);

  const figure = (id: string, color: string, folder = false) => (
    <g id={id}>
      <path id={`${id}-legF`} stroke={color} strokeOpacity={0.55} strokeWidth={9} strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <path id={`${id}-armF`} stroke={color} strokeOpacity={0.55} strokeWidth={7.5} strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <line id={`${id}-torso`} stroke={color} strokeWidth={20} strokeLinecap="round" />
      <path id={`${id}-legN`} stroke={color} strokeWidth={9} strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <circle id={`${id}-head`} r={10} fill={color} />
      <path id={`${id}-armN`} stroke={color} strokeWidth={7.5} strokeLinecap="round" strokeLinejoin="round" fill="none" />
      {folder && <rect id={`${id}-folder`} width={14} height={18} rx={2} fill="#F5B83D" style={{ opacity: 0 }} />}
    </g>
  );
  const bubbleAt = (i: number) => {
    const l = LINES[i];
    const x = l.who === "agent" ? (i < 2 ? XD.meet : XD.bossStand) : l.who === "partner" ? XD.partner : XD.boss;
    const w = l.text.length * 6.1 + 22;
    const left = Math.min(632 - w, Math.max(8, x - w / 2));
    const y = l.who === "boss" ? GROUND - 128 : GROUND - 140;
    return (
      <g key={i} id={`bub-${i}`} style={{ opacity: 0 }}>
        <rect x={left} y={y} width={w} height={26} rx={10} fill={l.who === "agent" ? AGENT : "#FFFFFF"} stroke={l.who === "agent" ? AGENT : LINE} />
        <path d={`M${x - 6} ${y + 25} L${x} ${y + 34} L${x + 6} ${y + 25}`} fill={l.who === "agent" ? AGENT : "#FFFFFF"} stroke={l.who === "agent" ? AGENT : LINE} />
        <rect x={x - 5.5} y={y + 23} width={11} height={3} fill={l.who === "agent" ? AGENT : "#FFFFFF"} />
        <text x={left + w / 2} y={y + 17} textAnchor="middle" fontSize={11.5} fontWeight={500} fill={l.who === "agent" ? "#FFFFFF" : NAVY}>{l.text}</text>
      </g>
    );
  };

  return (
    <svg ref={svg} viewBox="0 0 640 400" className="absolute inset-0 h-full w-full" style={{ fontFamily: "IBM Plex Sans, Inter, system-ui, sans-serif" }} preserveAspectRatio="xMidYMid meet">
      <rect width={640} height={400} fill="#FFFFFF" />
      {/* retícula de infografía */}
      {Array.from({ length: 11 }, (_, i) => <line key={i} x1={20 + i * 60} y1={40} x2={20 + i * 60} y2={GROUND} stroke="#F1F4F8" />)}
      <line x1={16} y1={GROUND} x2={624} y2={GROUND} stroke={NAVY} strokeWidth={2} />
      {/* puerta */}
      <rect x={XD.door - 18} y={GROUND - 84} width={36} height={84} fill="none" stroke={NAVY} strokeWidth={3} />
      <circle cx={XD.door + 10} cy={GROUND - 42} r={2.5} fill={NAVY} />
      {/* escritorio del agente con monitor */}
      <rect x={XD.desk - 46} y={GROUND - 46} width={92} height={6} fill={NAVY} />
      <rect x={XD.desk - 42} y={GROUND - 40} width={5} height={40} fill={NAVY} /><rect x={XD.desk + 37} y={GROUND - 40} width={5} height={40} fill={NAVY} />
      <rect id="scr-glow" x={XD.desk - 40} y={GROUND - 112} width={64} height={60} rx={16} fill="#9CC8F5" style={{ opacity: 0, filter: "blur(8px)" }} />
      <rect x={XD.desk - 32} y={GROUND - 100} width={48} height={36} rx={3} fill={NAVY} />
      <rect id="scr" x={XD.desk - 28} y={GROUND - 96} width={40} height={28} rx={1.5} fill={NAVY} />
      {[0, 1, 2, 3, 4].map((i) => <rect key={i} className="scr-line" x={XD.desk - 24} y={GROUND - 92 + i * 5} width={10 + ((i * 7) % 20)} height={2.4} fill={i === 0 ? AGENT : GREY} style={{ opacity: 0 }} />)}
      <rect x={XD.desk - 10} y={GROUND - 64} width={4} height={18} fill={NAVY} />
      {/* silla */}
      <rect x={XD.chair - 14} y={GROUND - 28} width={30} height={5} rx={2} fill={GREY} />
      <rect x={XD.chair + 12} y={GROUND - 58} width={5} height={35} rx={2} fill={GREY} />
      <rect x={XD.chair - 1} y={GROUND - 23} width={4} height={23} fill={GREY} />
      {/* mesa de reunión */}
      <rect x={XD.table - 34} y={GROUND - 46} width={68} height={6} rx={3} fill={NAVY} />
      <rect x={XD.table - 2.5} y={GROUND - 40} width={5} height={40} fill={NAVY} />
      <rect x={XD.table - 16} y={GROUND - 4} width={32} height={4} fill={NAVY} />
      <rect id="f-table" x={XD.table - 4} y={GROUND - 50} width={20} height={4} rx={1} fill="#F5B83D" />
      {/* escritorio de la jefa */}
      <rect x={XD.bossDesk - 30} y={GROUND - 46} width={60} height={6} fill={NAVY} />
      <rect x={XD.bossDesk - 30} y={GROUND - 40} width={60} height={40} fill={NAVY} fillOpacity={0.12} />
      <rect x={XD.bossDesk - 26} y={GROUND - 40} width={5} height={40} fill={NAVY} /><rect x={XD.bossDesk + 21} y={GROUND - 40} width={5} height={40} fill={NAVY} />
      <rect id="f-boss" x={XD.bossDesk - 18} y={GROUND - 50} width={20} height={4} rx={1} fill="#F5B83D" style={{ opacity: 0 }} />
      <path d={`M${XD.bossDesk + 4} ${GROUND - 46} L${XD.bossDesk + 20} ${GROUND - 46} L${XD.bossDesk + 26} ${GROUND - 66}`} stroke={NAVY} strokeWidth={4} fill="none" strokeLinecap="round" />
      {/* silla de la jefa */}
      <rect x={XD.boss - 16} y={GROUND - 28} width={30} height={5} rx={2} fill={GREY} />
      <rect x={XD.boss + 11} y={GROUND - 62} width={5} height={39} rx={2} fill={GREY} />
      <rect x={XD.boss - 2} y={GROUND - 23} width={4} height={23} fill={GREY} />

      <circle id="cheer-ring" cy={GROUND - 60} r={20} fill="none" stroke={AGENT} strokeWidth={3} style={{ opacity: 0 }} />
      {figure("pt", GREY, true)}
      {figure("bs", BOSS)}
      {figure("ag", AGENT, true)}
      {/* sello de aprobado */}
      <g id="stamp" transform={`translate(${XD.bossDesk} ${GROUND - 92}) scale(0)`}>
        <circle id="stamp-ring" r={16} fill="none" stroke="#16A34A" strokeWidth={2} />
        <circle r={16} fill="#16A34A" />
        <path id="stamp-check" d="M-7 0 L-2 5 L8 -6" pathLength={1} stroke="#FFFFFF" strokeWidth={3.5} fill="none" strokeLinecap="round" strokeLinejoin="round" style={{ strokeDasharray: 1, strokeDashoffset: 1 }} />
      </g>
      {LINES.map((_, i) => bubbleAt(i))}
      {/* barra de tiempo con los cinco pasos, como en una infografía */}
      <rect x={28} y={350} width={584} height={3} fill="#E8EDF3" rx={1.5} />
      <rect id="prog" x={28} y={350} width={0} height={3} fill={AGENT} rx={1.5} />
      {STEPS.map((s) => {
        const x = 28 + (s.from / LOOP) * 584;
        return (
          <g key={s.n}>
            <circle cx={x} cy={351.5} r={4} fill="#FFFFFF" stroke={NAVY} strokeWidth={1.5} />
            <text x={x + 7} y={370} fontSize={9.5} fill={NAVY} fontWeight={600}>{String(s.n).padStart(2, "0")}</text>
            <text x={x + 7} y={382} fontSize={8.5} fill="#5B6B80">{s.label.split(":")[0]}</text>
          </g>
        );
      })}
    </svg>
  );
}
