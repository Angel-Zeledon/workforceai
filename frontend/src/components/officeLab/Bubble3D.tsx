// EXPERIMENTAL (spike /office-lab, desechable): el bocadillo de los paneles 3D. Un div normal encima del lienzo que se
// coloca proyectando la cabeza de quien habla (sin drei <Html>, que crea y desmonta raíces de React dentro del render).
"use client";
import { useEffect, useMemo, type RefObject } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { labTime } from "./clock";
import { PLAN, sample } from "./script";

/** Con frameloop="demand": pide `fps` fotogramas por segundo mientras el panel está en pantalla (nada fuera de ella). */
export function FpsDriver({ fps, active }: { fps: number; active: boolean }) {
  const invalidate = useThree((s) => s.invalidate);
  useEffect(() => {
    if (!active) return;
    invalidate();
    const i = setInterval(() => invalidate(), 1000 / fps);
    return () => clearInterval(i);
  }, [fps, active, invalidate]);
  return null;
}

export function BubbleProjector({ el, standH, seatH, step }: { el: RefObject<HTMLDivElement | null>; standH: number; seatH: number; /** fps del stop-motion (sin: continuo) */ step?: number }) {
  const cam = useThree((s) => s.camera);
  const size = useThree((s) => s.size);
  const v = useMemo(() => new THREE.Vector3(), []);
  useFrame(() => {
    const d = el.current;
    if (!d) return;
    const t = labTime();
    const f = sample(step ? Math.floor(t * step) / step : t);
    const b = f.bubble;
    d.style.opacity = b ? "1" : "0";
    if (!b) return;
    if (d.textContent !== b.text) d.textContent = b.text;
    const p = b.who === "agent" ? f.pos : b.who === "partner" ? PLAN.partner : PLAN.boss;
    v.set(p[0], b.who === "boss" ? seatH : standH, p[1]).project(cam);
    d.style.transform = `translate(${((v.x * 0.5 + 0.5) * size.width).toFixed(1)}px, ${((-v.y * 0.5 + 0.5) * size.height).toFixed(1)}px) translate(-50%, -100%)`;
  });
  return null;
}
