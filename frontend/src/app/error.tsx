"use client";
import { ErrorScreen } from "@/components/ui/ErrorScreen";

/** Boundary of the pages outside the app shell (sign in, invitations, portal...). */
export default function RootError(props: { error: Error & { digest?: string }; reset: () => void }) {
  return <ErrorScreen {...props} />;
}
