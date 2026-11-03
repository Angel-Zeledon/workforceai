import { ApprovalsInbox } from "@/components/approvals/ApprovalsInbox";
import { AuthGate } from "@/components/auth/AuthScreen";

export default function Page() {
  return <AuthGate><ApprovalsInbox /></AuthGate>;
}
