/** Botón de navegación del encabezado: ghost con icono; activo = índigo suave. */
export const navBtn = (active: boolean) =>
  `inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium transition ${
    active ? "border-accent/30 bg-accent-soft text-accent" : "border-transparent text-ink2 hover:bg-panel2 hover:text-ink"
  }`;
