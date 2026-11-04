import { PendingPage } from "@/components/nav/RoutedPages";

/** Pendientes as a list (made for phones; notifications link here). Inside the app frame, with the menu. */
export default async function Page({ searchParams }: { searchParams: Promise<{ id?: string }> }) {
  const { id } = await searchParams;
  return <PendingPage view="list" focusId={id} />;
}
