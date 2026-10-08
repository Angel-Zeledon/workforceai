import { ApprovalsMobile } from "@/components/approvals/ApprovalsMobile";

export default async function Page({ searchParams }: { searchParams: Promise<{ id?: string }> }) {
  const { id } = await searchParams;
  return <ApprovalsMobile focusId={id} />;
}
