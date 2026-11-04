import { PendingPage } from "@/components/nav/RoutedPages";

/** One approval from a notification link (/approvals/<id>), inside the app frame. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <PendingPage view="list" focusId={decodeURIComponent(id)} />;
}
