"use client";
import type { ArtifactKind } from "@/lib/artifacts";

/** Accent color per artifact kind: categóricos apagados, legibles sobre blanco. */
export const KIND_COLOR: Record<ArtifactKind, string> = {
  sheet: "#2f7d55", doc: "#3451b2", table: "#2a8590", board: "#a8741f", chart: "#7461b5",
  pdf: "#b4443c", form: "#5d6fa8", inbox: "#a35b80", agenda: "#5f8a3f",
};

const PATHS: Record<ArtifactKind, string> = {
  sheet: "M3 4h14v12H3zM3 9h14M3 13h14M8 4v12M13 4v12",
  doc: "M5 3h7l3 3v11H5zM12 3v3h3M7.5 9h5M7.5 12h5",
  table: "M3 4h14v12H3zM3 8h14M7 8v8",
  board: "M3 4h4v12H3zM8 4h4v8H8zM13 4h4v10h-4z",
  chart: "M4 16V8M9 16V4M14 16v-6M3 16h14",
  pdf: "M5 3h7l3 3v11H5zM7 13h6M7 10h6",
  form: "M5 3h10v14H5zM8 7h4M8 10h4M8 13h2",
  inbox: "M3 11l2-6h10l2 6v5H3zM3 11h4l1 2h4l1-2h4",
  agenda: "M4 5h12v11H4zM4 8h12M7 3v3M13 3v3",
};

export function KindIcon({ kind, size = 16, color }: { kind: ArtifactKind; size?: number; color?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 20 20" fill="none" stroke={color ?? KIND_COLOR[kind]} strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d={PATHS[kind]} />
    </svg>
  );
}
