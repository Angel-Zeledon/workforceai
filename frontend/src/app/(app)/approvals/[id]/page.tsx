import { ApprovalsMobile } from "@/components/approvals/ApprovalsMobile";
import { AuthGate } from "@/components/auth/AuthScreen";

export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <AuthGate><ApprovalsMobile focusId={decodeURIComponent(id)} /></AuthGate>;
}
