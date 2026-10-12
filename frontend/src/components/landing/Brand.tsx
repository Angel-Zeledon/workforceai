import { AgentMark } from "../brand/AgentMark";

/** Wordmark: the agent mark (same drawing as the app icon) + "AI Workforce OS". */
export function Brand({ tone = "dark", className = "" }: { tone?: "dark" | "light"; className?: string }) {
  const text = tone === "light" ? "text-white" : "text-ink";
  return (
    <span className={`inline-flex items-center gap-2.5 ${className}`}>
      <AgentMark size={28} tone={tone} />
      <span className={`text-[15px] font-semibold tracking-tight ${text}`}>AI Workforce OS</span>
    </span>
  );
}
