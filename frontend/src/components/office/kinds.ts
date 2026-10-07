/** Colores por tipo de enlace entre agentes (líneas 3D y burbujas): apagados y distinguibles entre sí. */
export const LINK_KIND: Record<string, { color: string; label: string }> = {
  consult: { color: "#3f8aa3", label: "Consulta" },
  answer: { color: "#3d8f6b", label: "Respuesta" },
  delegation: { color: "#7461b5", label: "Delegación" },
  chat: { color: "#b8832a", label: "Mensaje" },
};
