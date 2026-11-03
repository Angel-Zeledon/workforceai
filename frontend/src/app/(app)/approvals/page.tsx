import { ApprovalsMobile } from "@/components/approvals/ApprovalsMobile";
import { AuthGate } from "@/components/auth/AuthScreen";

export default async function Page({ searchParams }: { searchParams: Promise<{ id?: string }> }) {
  const { id } = await searchParams;
  return <AuthGate><ApprovalsMobile focusId={id} /></AuthGate>;
}
