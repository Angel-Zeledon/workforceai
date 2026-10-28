// Security headers of the public client portal (/portal, B13, docs/architecture/client-portal.md): never indexed,
// never framed, no referrer, and a CSP that only allows this origin (plus the API origin when it is absolute).
// Next.js inlines its bootstrap scripts, so script-src needs 'unsafe-inline' (no nonce middleware here); dev also
// needs 'unsafe-eval' and the HMR websocket.
function portalCsp() {
  const dev = process.env.NODE_ENV !== "production";
  const connect = ["'self'"];
  try {
    const api = process.env.NEXT_PUBLIC_API_URL;
    if (api && /^https?:\/\//.test(api)) connect.push(new URL(api).origin);
  } catch { /* relative or malformed: same origin only */ }
  if (dev) connect.push("ws:", "wss:");
  return [
    "default-src 'self'",
    `script-src 'self' 'unsafe-inline'${dev ? " 'unsafe-eval'" : ""}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' blob: data:",
    "font-src 'self'",
    `connect-src ${connect.join(" ")}`,
    "worker-src 'self'",
    "manifest-src 'self'",
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
    "frame-ancestors 'none'",
  ].join("; ");
}

const portalHeaders = [
  { key: "X-Robots-Tag", value: "noindex, nofollow, noarchive" },
  { key: "Referrer-Policy", value: "no-referrer" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Content-Security-Policy", value: portalCsp() },
];

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "standalone",
  reactStrictMode: false,
  transpilePackages: ["three"],
  // Permite un directorio de build aparte (p. ej. `NEXT_DIST_DIR=.next-dev next dev`) sin pisar el de otros procesos.
  ...(process.env.NEXT_DIST_DIR ? { distDir: process.env.NEXT_DIST_DIR } : {}),
  async headers() {
    return [
      { source: "/portal", headers: portalHeaders },
      { source: "/portal/:path*", headers: portalHeaders },
    ];
  },
};
export default nextConfig;
