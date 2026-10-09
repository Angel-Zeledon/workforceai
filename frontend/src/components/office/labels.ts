import { MEETING_CENTER, USER_POS } from "@/lib/meta";

/** Registro de etiquetas DOM (nombres, burbujas y tarjetas) que el solver de pantalla coloca sin solaparlas. */
export interface LabelEntry {
  el: HTMLElement;
  kind: "agent" | "bubble" | "popup";
  /** id del agente, "user", o un ancla fija de la escena (FIXED_ANCHORS) */
  anchor: string;
}
export const labelEntries = new Map<string, LabelEntry>();

/** World anchors of the office zones (popups hover above them). */
export const FIXED_ANCHORS: Record<string, [number, number, number]> = {
  user: [USER_POS[0], 1.7, USER_POS[2] - 0.2],
  approvals: [USER_POS[0], 1.6, USER_POS[2] - 0.95],
  meeting: [MEETING_CENTER[0], 1.3, MEETING_CENTER[1]],
};
