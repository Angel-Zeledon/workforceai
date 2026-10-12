import type { Metadata } from "next";
import { Landing } from "@/components/landing/Landing";

export const metadata: Metadata = {
  title: "AI Workforce OS: un equipo de IA que trabaja como una empresa",
  description: "Agentes con roles, archivos, aprobaciones humanas y proyectos de gran escala.",
};

export default function WelcomePage() {
  return <Landing />;
}
