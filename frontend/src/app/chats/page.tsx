import { AuthGate } from "@/components/auth/AuthScreen";
import { ChatsView } from "@/components/chats/ChatsView";

/** Chat history: General (the whole office) and one thread per agent. */
export default function Page() {
  return (
    <AuthGate>
      <div className="h-screen w-screen"><ChatsView /></div>
    </AuthGate>
  );
}
