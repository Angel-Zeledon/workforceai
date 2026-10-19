import { Suspense } from "react";
import { MeetingsView } from "@/components/meetings/MeetingsView";
import { PageSkeleton } from "@/components/nav/ui2";

export default function Page() {
  return (
    <Suspense fallback={<PageSkeleton />}>
      <MeetingsView />
    </Suspense>
  );
}
