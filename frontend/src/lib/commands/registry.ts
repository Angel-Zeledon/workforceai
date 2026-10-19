// Static fallback registry of the slash commands (mirrors GET /api/v1/commands). Used for the instant first paint
// and in mock mode. The API list, when it arrives, replaces it (the backend is the source of truth for `allowed`).

export type Lang = "es" | "en";
export type ArgKind = "agent" | "project" | "file" | "skill" | "template" | "text" | "enum" | "number";
export type CmdGroup = "control" | "office" | "org";
export type Surface = "office" | "general" | "agent";
export interface L10n { es: string; en: string }
export interface CmdArg { name: string; kind: ArgKind; required: boolean; enum?: string[]; variadic?: boolean }
export interface Command {
  id: string;
  aliases: { es: string[]; en: string[] };
  title: L10n; description: L10n; example: L10n;
  group: CmdGroup; icon: string; args: CmdArg[];
  permission: string; destructive: boolean; uses_llm: boolean; surfaces: Surface[];
  client_only?: boolean; allowed?: boolean;
}

const ALL: Surface[] = ["office", "general", "agent"];
type A = [string, ArgKind, boolean?, (string[] | "v")?];
function c(id: string, group: CmdGroup, icon: string, es: string[], title: [string, string], desc: [string, string], ex: [string, string], args: A[] = [], o: Partial<Command> = {}): Command {
  return {
    id, group, icon, aliases: { es, en: [] }, title: { es: title[0], en: title[1] }, description: { es: desc[0], en: desc[1] }, example: { es: ex[0], en: ex[1] },
    args: args.map(([name, kind, required = false, x]) => ({ name, kind, required, ...(Array.isArray(x) ? { enum: x } : {}), ...(x === "v" ? { variadic: true } : {}) })),
    permission: "chat", destructive: false, uses_llm: false, surfaces: ALL, ...o,
  };
}

export const STATIC_COMMANDS: Command[] = [
  c("clear", "control", "x", ["limpiar"], ["Limpiar conversación", "Clear conversation"], ["Vacía esta conversación (no borra la memoria).", "Empties this conversation (memory is kept)."], ["/limpiar", "/clear"], [], { destructive: true }),
  c("compact", "control", "shrink", ["resumir"], ["Resumir conversación", "Summarize conversation"], ["Condensa la conversación para ahorrar contexto.", "Condenses the conversation to save context."], ["/resumir", "/compact"], [], { uses_llm: true }),
  c("context", "control", "layers", ["contexto"], ["Ver contexto", "View context"], ["Muestra cuánto contexto lleva la conversación.", "Shows how much context the conversation uses."], ["/contexto", "/context"]),
  c("usage", "control", "pulse", ["uso", "costo"], ["Uso y costo", "Usage and cost"], ["Tokens y costo de esta conversación y del mes.", "Tokens and cost of this conversation and the month."], ["/uso", "/usage"]),
  c("model", "control", "settings", ["modelo"], ["Modelo", "Model"], ["Muestra o cambia el modelo del agente.", "Shows or changes the agent's model."], ["/modelo", "/model"], [["model", "text"]]),
  c("memory", "control", "layers", ["memoria"], ["Memoria", "Memory"], ["Consulta o borra la memoria de un agente.", "View or wipe an agent's memory."], ["/memoria @Valeria", "/memory @Valeria"], [["agent", "agent", true], ["action", "enum", false, ["ver", "borrar"]]], { destructive: true }),
  c("pause", "control", "power", ["pausa"], ["Pausar agente", "Pause agent"], ["Pausa a un agente sin perder su trabajo.", "Pauses an agent without losing its work."], ["/pausa @Valeria", "/pause @Valeria"], [["agent", "agent", true]]),
  c("resume", "control", "play", ["reanudar"], ["Reanudar agente", "Resume agent"], ["Reanuda a un agente pausado.", "Resumes a paused agent."], ["/reanudar @Valeria", "/resume @Valeria"], [["agent", "agent", true]]),
  c("stop", "control", "power", ["detener"], ["Detener trabajo", "Stop work"], ["Detiene lo que está haciendo un agente.", "Stops what an agent is doing."], ["/detener @Valeria", "/stop @Valeria"], [["agent", "agent", true]], { destructive: true }),
  c("status", "control", "pulse", ["estado"], ["Estado", "Status"], ["Qué está haciendo cada agente ahora.", "What every agent is doing right now."], ["/estado", "/status"]),
  c("trace", "control", "list", ["traza"], ["Traza", "Trace"], ["Pasos que dio el agente en su último turno.", "Steps the agent took in its last turn."], ["/traza @Valeria", "/trace @Valeria"], [["agent", "agent"]]),
  c("export", "control", "arrow", ["exportar"], ["Exportar conversación", "Export conversation"], ["Descarga esta conversación como archivo.", "Downloads this conversation as a file."], ["/exportar", "/export"], [["format", "enum", false, ["md", "pdf", "txt"]]]),
  c("undo", "control", "back", ["deshacer"], ["Deshacer", "Undo"], ["Deshace la última acción reversible.", "Undoes the last reversible action."], ["/deshacer", "/undo"]),
  c("excel", "office", "doc", [], ["Crear Excel", "Create Excel"], ["Genera una hoja de cálculo a partir de lo que pides.", "Builds a spreadsheet from your request."], ["/excel ventas por mes", "/excel sales by month"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("word", "office", "doc", [], ["Crear Word", "Create Word"], ["Redacta un documento Word.", "Drafts a Word document."], ["/word propuesta para cliente", "/word client proposal"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("pdf", "office", "doc", [], ["Crear PDF", "Create PDF"], ["Genera un PDF.", "Generates a PDF."], ["/pdf informe mensual", "/pdf monthly report"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("presentation", "office", "layout", ["presentacion"], ["Crear presentación", "Create presentation"], ["Genera una presentación de diapositivas.", "Builds a slide deck."], ["/presentacion resultados Q3", "/presentation Q3 results"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("meeting", "office", "users", ["reunion"], ["Reunión", "Meeting"], ["Reúne a varios agentes para tratar un tema.", "Gets several agents together on a topic."], ["/reunion lanzamiento de producto", "/meeting product launch"], [["topic", "text", true, "v"]], { uses_llm: true }),
  c("room", "office", "users", ["sala", "junta"], ["Abrir una sala", "Open a room"], ["Abre una reunión ahora en una sala libre, con video y los agentes invitados.", "Opens a meeting now in a free room, with video and the agents invited."], ["/sala ventas legal: revisar precios", "/room sales legal: review pricing"], [["agents", "agent", false, "v"], ["topic", "text", true]]), // meetings
  c("schedule", "office", "calendar", ["agendar", "programar"], ["Agendar reunión", "Schedule a meeting"], ["Abre el diálogo para agendar con el tema y los agentes ya puestos.", "Opens the schedule dialog with the topic and agents filled in."], ["/agendar ventas: revisar precios mañana 10:00", "/schedule sales: review pricing tomorrow 10:00"], [["agents", "agent", false, "v"], ["topic", "text"]]), // meetings
  c("minutes", "office", "doc", ["acta", "minuta"], ["Acta de reunión", "Meeting minutes"], ["Un agente redacta el acta de la última reunión desde tus notas y la guarda en el drive.", "An agent writes the minutes of the latest meeting from your notes and saves them to the drive."], ["/acta Decidimos subir el precio", "/minutes We decided to raise the price"], [["notes", "text", true, "v"]], { uses_llm: true }), // meetings
  c("assign", "office", "hand", ["encargar"], ["Encargar tarea", "Assign task"], ["Encarga una tarea a un agente.", "Assigns a task to an agent."], ["/encargar @Valeria revisar facturas", "/assign @Valeria review invoices"], [["agent", "agent", true], ["task", "text", true, "v"]]),
  c("project", "office", "folder", ["proyecto"], ["Proyecto", "Project"], ["Abre o crea un proyecto.", "Opens or creates a project."], ["/proyecto Lanzamiento", "/project Launch"], [["project", "project"]]),
  c("daily", "office", "sun", ["resumen-del-dia"], ["Resumen del día", "Daily summary"], ["Qué se hizo hoy y qué queda.", "What was done today and what remains."], ["/resumen-del-dia", "/daily"], [], { uses_llm: true }),
  c("pending", "office", "clock", ["pendientes"], ["Pendientes", "Pending"], ["Lo que espera tu aprobación o respuesta.", "What awaits your approval or reply."], ["/pendientes", "/pending"]),
  c("search", "office", "list", ["buscar"], ["Buscar", "Search"], ["Busca en conversaciones, archivos y memoria.", "Searches conversations, files and memory."], ["/buscar contrato", "/search contract"], [["query", "text", true, "v"]]),
  c("file", "office", "doc", ["archivo"], ["Adjuntar archivo", "Attach file"], ["Elige un archivo del Drive.", "Pick a file from the Drive."], ["/archivo", "/file"], [["file", "file"]], { client_only: true }),
  c("open", "office", "expand", ["abrir"], ["Abrir", "Open"], ["Abre un archivo o una sección.", "Opens a file or a section."], ["/abrir informe.pdf", "/open report.pdf"], [["file", "file"]], { client_only: true }),
  c("email", "office", "mail", ["correo"], ["Redactar correo", "Draft email"], ["Redacta un correo para revisar antes de enviar.", "Drafts an email for review before sending."], ["/correo cotización a Acme", "/email quote to Acme"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("linkedin", "office", "edit", [], ["Publicación LinkedIn", "LinkedIn post"], ["Redacta una publicación para LinkedIn.", "Drafts a LinkedIn post."], ["/linkedin nuevo producto", "/linkedin new product"], [["description", "text", true, "v"]], { uses_llm: true }),
  c("remember", "office", "layers", ["recordar"], ["Recordar", "Remember"], ["Guarda un dato en la memoria del agente.", "Saves a fact in the agent's memory."], ["/recordar prefiero tono formal", "/remember I prefer a formal tone"], [["text", "text", true, "v"]]),
  c("tone", "office", "edit", ["tono"], ["Tono", "Tone"], ["Cambia el tono de respuesta.", "Changes the reply tone."], ["/tono formal", "/tone formal"], [["tone", "enum", true, ["formal", "casual", "breve"]]]),
  c("language", "office", "flag", ["idioma"], ["Idioma", "Language"], ["Cambia el idioma de las respuestas.", "Changes the language of replies."], ["/idioma en", "/language es"], [["language", "enum", true, ["es", "en"]]]),
  c("template", "office", "layout", ["plantilla"], ["Plantilla", "Template"], ["Usa una plantilla de proyecto.", "Uses a project template."], ["/plantilla campaña", "/template campaign"], [["template", "template"]]),
  c("skill", "office", "plus", [], ["Habilidad", "Skill"], ["Asigna una habilidad a un agente.", "Assigns a skill to an agent."], ["/skill redaccion @Valeria", "/skill writing @Valeria"], [["skill", "skill", true], ["agent", "agent"]]),
  c("learn", "office", "layers", ["aprender"], ["Aprender", "Learn"], ["Enseña algo nuevo a un agente.", "Teaches an agent something new."], ["/aprender @Valeria estilo de marca", "/learn @Valeria brand style"], [["agent", "agent"], ["topic", "text", true, "v"]], { uses_llm: true }),
  c("team", "org", "users", ["equipo"], ["Equipo", "Team"], ["Lista tu equipo de agentes.", "Lists your team of agents."], ["/equipo", "/team"]),
  c("hire", "org", "plus", ["contratar"], ["Contratar", "Hire"], ["Contrata un agente desde una plantilla.", "Hires an agent from a template."], ["/contratar contador", "/hire accountant"], [["template", "template"]]),
  c("permissions", "org", "shield", ["permisos"], ["Permisos", "Permissions"], ["Qué puede hacer cada agente.", "What every agent is allowed to do."], ["/permisos @Valeria", "/permissions @Valeria"], [["agent", "agent"]]),
  c("shop", "org", "bag", ["tienda"], ["Tienda", "Shop"], ["Abre la tienda.", "Opens the shop."], ["/tienda", "/shop"], [], { client_only: true }),
  c("inventory", "org", "bag", ["inventario"], ["Inventario", "Inventory"], ["Abre tu inventario.", "Opens your inventory."], ["/inventario", "/inventory"], [], { client_only: true }),
  c("help", "control", "alert", ["ayuda"], ["Ayuda", "Help"], ["Lista todos los comandos con ejemplos.", "Lists all commands with examples."], ["/ayuda", "/help"], [], { client_only: true }),
];

// ---- API shapes (contract: GET /commands/complete, /commands/suggest, POST /commands/run) ----
export interface Completion { value: string; label: string; hint?: string }
export interface Suggestion { command: string; reason: L10n }
export type ResultKind = "message" | "table" | "navigate" | "download" | "card" | "request";
export interface CmdResult {
  kind: ResultKind; title?: string; text?: string;
  columns?: (string | { key?: string; label: string })[]; rows?: unknown[];
  href?: string; url?: string; filename?: string; request_id?: string; conversation_id?: string; data?: unknown;
}
export interface RunResponse {
  status: "ok" | "needs_confirmation" | "started" | "error";
  confirm?: { message: L10n | string };
  result?: CmdResult;
  error?: { code: string; message: string };
}
export interface RunBody {
  command: string; args: Record<string, string>; raw: string; lang: Lang;
  context: { surface: Surface; agent_id?: string; conversation_id?: string; project_id?: string };
  confirm?: boolean; idempotency_key?: string;
}
