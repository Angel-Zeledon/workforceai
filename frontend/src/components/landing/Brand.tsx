/** Wordmark: navy square with a check (same mark as the app icon) + "AI Workforce OS". */
export function Brand({ tone = "dark", className = "" }: { tone?: "dark" | "light"; className?: string }) {
  const text = tone === "light" ? "text-white" : "text-ink";
  return (
    <span className={`inline-flex items-center gap-2.5 ${className}`}>
      <svg width="26" height="26" viewBox="0 0 32 32" aria-hidden>
        <rect width="32" height="32" rx="7" fill={tone === "light" ? "#ffffff" : "#0B1F3A"} />
        <path d="M9 16.5l4.8 4.8L23 11" fill="none" stroke={tone === "light" ? "#0B1F3A" : "#ffffff"} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <span className={`text-[15px] font-semibold tracking-tight ${text}`}>AI Workforce OS</span>
    </span>
  );
}
