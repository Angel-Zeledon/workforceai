/** File name stem of the draft an agent saves on its desk for a task (same rule as the simulated runtime). */
export const draftSlug = (title: string) => (title || "tarea").toLowerCase().replace(/[^\p{L}\p{N}]+/gu, "-").replace(/^-|-$/g, "").slice(0, 60);
