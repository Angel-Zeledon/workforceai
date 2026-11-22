// EXPERIMENTAL (spike /office-lab, desechable): reloj compartido por los cuatro paneles, para que todos
// reproduzcan el mismo instante del guion. "Repetir" lo vuelve a 0; la velocidad se cambia sin saltos.
"use client";
import { useEffect, useRef, useState } from "react";
import { LOOP } from "./script";

let base = typeof performance !== "undefined" ? performance.now() : 0;
let offset = 0;
let speed = 1;
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());

/** Segundos de guion transcurridos (crece sin fin; cada panel aplica el bucle con `sample`). */
export function labTime(): number {
  return offset + ((performance.now() - base) / 1000) * speed;
}
export function labRestart() { base = performance.now(); offset = 0; emit(); }
export function labSetSpeed(s: number) { offset = labTime(); base = performance.now(); speed = s; emit(); }
export function labSpeed() { return speed; }
export function labSubscribe(f: () => void) { subs.add(f); return () => { subs.delete(f); }; }

/** Estado del reloj para la UI (se refresca ~6 veces por segundo, no en cada fotograma). */
export function useLabClockUi() {
  const [t, setT] = useState(0);
  const [s, setS] = useState(speed);
  useEffect(() => {
    const i = setInterval(() => setT(labTime() % LOOP), 160);
    const un = labSubscribe(() => { setS(speed); setT(labTime() % LOOP); });
    return () => { clearInterval(i); un(); };
  }, []);
  return { t, speed: s };
}

/** true mientras el elemento está (al menos un poco) en pantalla: cada panel pausa su render fuera de ella. */
export function useVisible<T extends Element>() {
  const ref = useRef<T>(null);
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((es) => setVisible(es.some((e) => e.isIntersecting)), { threshold: 0.01 });
    io.observe(el);
    const vis = () => { if (document.hidden) setVisible(false); else setVisible(el.getBoundingClientRect().bottom > 0 && el.getBoundingClientRect().top < innerHeight); };
    document.addEventListener("visibilitychange", vis);
    return () => { io.disconnect(); document.removeEventListener("visibilitychange", vis); };
  }, []);
  return [ref, visible] as const;
}
