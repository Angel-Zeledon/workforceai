import { PositionsScreen } from "@/components/positions/PositionsScreen";

export default async function Page({ params }: { params: Promise<{ office: string }> }) {
  const { office } = await params;
  return <PositionsScreen officeId={office} />;
}
