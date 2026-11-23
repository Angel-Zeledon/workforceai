"use client";
// The name tag over a person of the office, as a plain DOM element placed by projecting a point of the scene every frame.
// It replaces drei's <Html>, which mounts a React root of its own per tag and unmounts it inside the commit of the Canvas:
// closing the office (or switching its style) while a tag was alive logged "Attempted to synchronously unmount a root while
// React was already rendering". No React root is created here: the element is built and removed with the DOM API.
import { useEffect, useMemo, useRef } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";

const CLASS = "flex items-center gap-1.5 whitespace-nowrap rounded-full border border-line bg-panel/95 px-2.5 py-0.5 text-[11px] font-semibold text-ink shadow-soft";
const P = new THREE.Vector3();

interface Props { position: [number, number, number]; name: string; color: string; self: boolean; away: boolean; testId: string }

export function PersonTag({ position, name, color, self, away, testId }: Props) {
  const gl = useThree((s) => s.gl);
  const camera = useThree((s) => s.camera);
  const size = useThree((s) => s.size);
  const anchor = useRef<THREE.Group>(null);
  const host = useMemo(() => {
    if (typeof document === "undefined") return null;
    const el = document.createElement("div");
    el.style.cssText = "position:absolute;top:0;left:0;pointer-events:none;transform-origin:0 0;will-change:transform;";
    const tag = document.createElement("div");
    tag.className = CLASS;
    const dot = document.createElement("span");
    dot.setAttribute("aria-hidden", "true");
    dot.className = "h-2 w-2 rounded-full";
    const label = document.createElement("span");
    tag.append(dot, label);
    el.append(tag);
    return { el, tag, dot, label };
  }, []);

  useEffect(() => {
    if (!host) return;
    const parent = gl.domElement.parentNode as HTMLElement | null;
    parent?.appendChild(host.el);
    return () => { host.el.remove(); };
  }, [gl, host]);

  useEffect(() => {
    if (!host) return;
    host.label.textContent = name;
    host.dot.style.background = color;
    host.tag.setAttribute("data-testid", testId);
    host.tag.style.opacity = away ? "0.7" : "1";
  }, [host, name, color, away, testId, self]);

  useFrame(() => {
    const g = anchor.current;
    if (!g || !host) return;
    g.getWorldPosition(P);
    const dist = P.distanceTo(camera.position);
    P.project(camera);
    const behind = P.z > 1 || P.z < -1;
    host.el.style.display = behind ? "none" : "block";
    if (behind) return;
    const x = (P.x * 0.5 + 0.5) * size.width, y = (-P.y * 0.5 + 0.5) * size.height;
    host.el.style.transform = `translate3d(${x.toFixed(1)}px,${y.toFixed(1)}px,0) translate(-50%,-50%)`;
    // nearer people over farther ones, always under the interface (0..6, as the drei tag was)
    const cam = camera as THREE.PerspectiveCamera;
    const k = (dist - cam.near) / Math.max(1, cam.far - cam.near);
    host.el.style.zIndex = String(Math.max(0, Math.round(6 * (1 - Math.min(1, k * 6)))));
  });

  return <group ref={anchor} position={position} />;
}
