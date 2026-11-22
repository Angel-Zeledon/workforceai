"use client";
import { useEffect } from "react";
import { tr, detectLocale } from "@/lib/i18n-core";

/** Last resort: the root layout itself failed, so this replaces the whole document (own html/body, no providers). */
export default function GlobalError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => { console.error("[global error]", error); }, [error]);
  const l = detectLocale();
  return (
    <html lang={l}>
      <body style={{ margin: 0, background: "#fff", color: "#1d2433", fontFamily: "'IBM Plex Sans', system-ui, sans-serif" }}>
        <div role="alert" style={{ maxWidth: 420, margin: "0 auto", padding: "96px 24px", textAlign: "center" }}>
          <h2 style={{ fontSize: 18, fontWeight: 600, margin: "0 0 8px" }}>{tr("err.boundary.title", undefined, l)}</h2>
          <p style={{ fontSize: 14, color: "#5b6678", margin: "0 0 16px" }}>{tr("err.boundary.detail", undefined, l)}</p>
          {error.digest && <p style={{ fontSize: 11, color: "#8a93a3" }}>{tr("err.ref", { ref: error.digest }, l)}</p>}
          <button type="button" onClick={reset} style={{ background: "#0A66C2", color: "#fff", border: 0, borderRadius: 10, padding: "8px 14px", fontSize: 14, fontWeight: 500, cursor: "pointer", marginRight: 8 }}>{tr("err.boundary.retry", undefined, l)}</button>
          {/* a full page load on purpose: after a crash the client router may be the broken part */}
          {/* eslint-disable-next-line @next/next/no-html-link-for-pages */}
          <a href="/" style={{ border: "1px solid #d8dee8", borderRadius: 10, padding: "8px 14px", fontSize: 14, color: "#3d4757", textDecoration: "none" }}>{tr("err.boundary.home", undefined, l)}</a>
        </div>
      </body>
    </html>
  );
}
