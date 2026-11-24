import { OfficePage } from "@/components/offices/OfficePage";

/** One office: Personas · Agentes · Puestos · Habilidades · Archivos · Memoria. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <OfficePage id={id} />;
}
