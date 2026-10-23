import type { Metadata } from "next";
import { PortalPage } from "@/components/portal/PortalPage";

// The client portal (B13). Public and read-only: never indexed, never sends a referrer (the token lives in
// the URL fragment anyway). The security headers of this route are in next.config.mjs.
export const metadata: Metadata = {
  title: "Portal",
  robots: { index: false, follow: false, nocache: true, googleBot: { index: false, follow: false } },
  referrer: "no-referrer",
};

export default function Page() {
  return <PortalPage />;
}
