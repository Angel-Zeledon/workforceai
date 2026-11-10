import { DepartmentDrive } from "@/components/dept/DepartmentDrive";

export default async function Page({ params }: { params: Promise<{ office: string }> }) {
  const { office } = await params;
  return <DepartmentDrive officeId={office} />;
}
