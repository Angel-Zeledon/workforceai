/** Brand mark: a friendly little agent (head with a visor, two eyes, antenna and
 *  headset) on a rounded square. Same drawing as public/icon.svg. */
export function AgentMark({ size = 28, tone = "dark", className = "" }: { size?: number; tone?: "dark" | "light"; className?: string }) {
  const bg = tone === "light" ? "#ffffff" : "#0B1F3A";
  const fg = tone === "light" ? "#0B1F3A" : "#ffffff";
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden className={`ac-mark ${className}`}>
      <rect width="32" height="32" rx="8" fill={bg} />
      <line x1="16" y1="6.4" x2="16" y2="9.5" stroke={fg} strokeWidth="1.8" strokeLinecap="round" />
      <circle className="ac-mark-led" cx="16" cy="5.4" r="2" fill="#2DD4BF" />
      <rect x="4.6" y="14" width="2.6" height="5.6" rx="1.3" fill={fg} />
      <rect x="24.8" y="14" width="2.6" height="5.6" rx="1.3" fill={fg} />
      <rect x="7.5" y="9.5" width="17" height="15" rx="6" fill={fg} />
      <rect x="10.2" y="13.2" width="11.6" height="6.4" rx="3.2" fill={bg} />
      <g className="ac-mark-eyes">
        <circle cx="13.6" cy="16.4" r="1.45" fill="#2DD4BF" />
        <circle cx="18.4" cy="16.4" r="1.45" fill="#2DD4BF" />
      </g>
      <path d="M13.4 21.9q2.6 1.5 5.2 0" fill="none" stroke={bg} strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}
