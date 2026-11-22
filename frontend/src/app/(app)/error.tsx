"use client";
import { ErrorScreen } from "@/components/ui/ErrorScreen";

/** Boundary of every page inside the app shell: the navigation stays, only the page shows the notice. */
export default function PageError(props: { error: Error & { digest?: string }; reset: () => void }) {
  return <ErrorScreen {...props} />;
}
