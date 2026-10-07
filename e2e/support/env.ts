export const FRONTEND_URL = process.env.FRONTEND_URL ?? "http://localhost:3000";
export const API_URL = process.env.API_URL ?? "http://localhost:8080/api/v1";
export const WS_URL = process.env.WS_URL ?? "ws://localhost:8080/ws";
export const RUNTIME_URL = process.env.RUNTIME_URL ?? "http://localhost:8000";

export const AGENT_IDS = [
  "sales",
  "hr",
  "legal",
  "accounting",
  "analyst",
  "operations",
  "assistant",
] as const;

export const SCENARIO_TEXT =
  "Un cliente pidió una propuesta de $50,000. Prepara la propuesta, revisa el contrato, el margen, la rentabilidad y la capacidad operativa.";
