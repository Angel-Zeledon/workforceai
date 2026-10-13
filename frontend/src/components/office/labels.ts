import { meetingCenter, userPos } from "./zones";

/** Registro de etiquetas DOM (nombres, burbujas y tarjetas) que el solver de pantalla coloca sin solaparlas. */
export interface LabelEntry {
  el: HTMLElement;
  kind: "agent" | "bubble" | "popup";
  /** id del agente, "user", o un ancla fija de la escena (FIXED_ANCHORS) */
  anchor: string;
}
export const labelEntries = new Map<string, LabelEntry>();

/** World anchors of the office zones (popups hover above them); they follow the rooms when the organization moves them. */
export const FIXED_ANCHORS: Record<string, [number, number, number]> = {
  get user(): [number, number, number] { const u = userPos(); return [u[0], 1.7, u[2] - 0.2]; },
  get approvals(): [number, number, number] { const u = userPos(); return [u[0], 1.6, u[2] - 0.95]; },
  get meeting(): [number, number, number] { const c = meetingCenter(); return [c[0], 1.3, c[1]]; },
};
