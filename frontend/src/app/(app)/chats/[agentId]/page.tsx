import { ChatsPage } from "@/components/nav/RoutedPages";

export default async function Page({ params }: { params: Promise<{ agentId: string }> }) {
  const { agentId } = await params;
  return <ChatsPage agentId={agentId} />;
}
