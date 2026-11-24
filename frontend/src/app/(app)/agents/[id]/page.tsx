import { AgentSheet } from "@/components/agents/AgentSheet";

/** The agent's sheet: Quién es · Dónde y para quién trabaja · Qué sabe hacer · Permisos. */
export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <AgentSheet id={id} />;
}
