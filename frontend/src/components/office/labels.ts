/** Registro de etiquetas DOM (nombres y burbujas) que el solver de pantalla coloca sin solaparlas. */
export interface LabelEntry {
  el: HTMLElement;
  kind: "agent" | "bubble";
  /** id del agente (o "user") al que está anclada */
  anchor: string;
}
export const labelEntries = new Map<string, LabelEntry>();
