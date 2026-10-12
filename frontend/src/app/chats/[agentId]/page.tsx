import { AuthGate } from "@/components/auth/AuthScreen";
import { ChatsView } from "@/components/chats/ChatsView";

/** Chat history of one agent (the same messages General shows, filtered to the turns it took part in). */
export default async function Page({ params }: { params: Promise<{ agentId: string }> }) {
  const { agentId } = await params;
  return (
    <AuthGate>
      <div className="h-screen w-screen"><ChatsView agentId={decodeURIComponent(agentId)} /></div>
    </AuthGate>
  );
}
