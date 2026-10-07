"use client";
import type { BuildState } from "@/lib/artifacts";

const STATE_COLOR: Record<BuildState, string> = { queued: "#66707f", building: "#2b6cb0", ready_for_review: "#a86208", done: "#2f7d55", blocked: "#b4443c" };

/** Progress ring for a tab (0-100) driven only by artifact.progress_changed events. */
export function TabProgressRing({ id, progress, buildState, color }: { id: string; progress: number; buildState: BuildState; color?: string }) {
  const r = 7; const c = 2 * Math.PI * r;
  const stroke = color ?? STATE_COLOR[buildState];
  return (
    <svg data-testid={`ws-tab-progress-${id}`} data-progress={progress} data-build-state={buildState} width={18} height={18} viewBox="0 0 18 18" className={buildState === "building" ? "ac-bounce" : undefined}>
      <circle cx={9} cy={9} r={r} fill="none" stroke="#64748b" strokeOpacity={0.18} strokeWidth={2.5} />
      <circle cx={9} cy={9} r={r} fill="none" stroke={stroke} strokeWidth={2.5} strokeLinecap="round" strokeDasharray={c} strokeDashoffset={c * (1 - Math.max(0, Math.min(100, progress)) / 100)} transform="rotate(-90 9 9)" style={{ transition: "stroke-dashoffset .5s" }} />
      {buildState === "ready_for_review" && <circle cx={9} cy={9} r={2.6} fill="#a86208" />}
    </svg>
  );
}
