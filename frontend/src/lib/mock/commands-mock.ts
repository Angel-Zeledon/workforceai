/**
 * Mock of the slash commands contract (GET /commands, /commands/complete, /commands/suggest, POST /commands/run).
 * Replies in the language the UI sends in `lang` (derived from the alias typed), like the real backend.
 */
import { STATIC_COMMANDS, type Command, type Completion, type Lang, type RunBody, type RunResponse, type Suggestion } from "../commands/registry";
import type { Agent } from "../types";

export interface CommandsHost {
  agents(): Agent[];
  /** messages in a chat conversation ("office" | "agent:<id>") */
  chatCount(conv: string): number;
  startRequest(text: string): Promise<string>;
}

const PROJECTS = ["Lanzamiento de producto", "Campaña de otoño", "Cierre trimestral"];
const FILES = ["/Shared/Informe-Q3.pdf", "/Shared/Ventas.xlsx", "/Shared/Propuesta.docx", "/Desk/notas.md"];
const SKILLS = ["redaccion", "analisis-financiero", "atencion-al-cliente"];
const TEMPLATES = ["campana", "contador", "lanzamiento"];

const pick = (l: Lang, es: string, en: string) => (l === "es" ? es : en);
const first = (a: Agent) => a.name.split(" ")[0];

export class MockCommands {
  constructor(private host: CommandsHost) {}

  matches(seg: string[]) { return seg[0] === "commands"; }

  async handle(method: string, seg: string[], q: URLSearchParams, body: any): Promise<unknown> {
    const sub = seg[1];
    if (method === "GET" && !sub) return STATIC_COMMANDS.map((c) => ({ ...c, allowed: c.id !== "hire" ? true : false }));
    if (method === "GET" && sub === "complete") return this.complete(q.get("kind") || "", q.get("q") || "", Number(q.get("limit") || 8));
    if (method === "GET" && sub === "suggest") return this.suggest(q.get("surface") || "office", q.get("conversation_id") || "");
    if (method === "POST" && sub === "run") return this.run(body as RunBody);
    throw new Error(`mock commands: ${method} /${seg.join("/")} -> 404`);
  }

  private complete(kind: string, q: string, limit: number): Completion[] {
    const n = q.replace(/^@/, "").toLowerCase();
    const f = (xs: Completion[]) => xs.filter((x) => !n || x.label.toLowerCase().includes(n) || x.value.toLowerCase().includes(n)).slice(0, limit);
    switch (kind) {
      case "agent": return f(this.host.agents().map((a) => ({ value: first(a), label: a.name, hint: a.role })));
      case "project": return f(PROJECTS.map((p) => ({ value: p, label: p, hint: "" })));
      case "file": return f(FILES.map((p) => ({ value: p, label: p.split("/").pop()!, hint: p })));
      case "skill": return f(SKILLS.map((s) => ({ value: s, label: s, hint: "" })));
      case "template": return f(TEMPLATES.map((s) => ({ value: s, label: s, hint: "" })));
      default: return [];
    }
  }

  private async suggest(surface: string, conv: string): Promise<Suggestion[]> {
    const c = conv || (surface === "office" || surface === "general" ? "office" : "");
    // the commands' cards live only in this browser (local), so the mock also counts what the screen holds
    const { useStore } = await import("../store");
    if (Math.max(this.host.chatCount(c), useStore.getState().chat[c]?.length ?? 0) >= 14) {
      return [{ command: "compact", reason: { es: "La conversación es larga: prueba /resumir", en: "This conversation is long: try /compact" } }];
    }
    return [];
  }

  private async run(b: RunBody): Promise<RunResponse> {
    const lang: Lang = b.lang === "en" ? "en" : "es";
    const cmd: Command | undefined = STATIC_COMMANDS.find((c) => c.id === b.command);
    if (!cmd) return { status: "error", error: { code: "unknown_command", message: pick(lang, "No conozco ese comando. Prueba /ayuda.", "Unknown command. Try /help.") } };
    const a = b.args || {};
    const agents = this.host.agents();
    const agentOf = (n?: string) => (n ? agents.find((x) => first(x).toLowerCase() === n.toLowerCase() || x.name.toLowerCase() === n.toLowerCase()) : undefined);
    const msg = (text: string, title?: string): RunResponse => ({ status: "ok", result: { kind: "message", title, text } });
    const need = (name: string) => cmd.args.find((x) => x.name === name)?.required && !a[name];
    for (const x of cmd.args) if (x.required && !a[x.name]) return { status: "error", error: { code: "missing_arg", message: pick(lang, `Falta el argumento: ${x.name}`, `Missing argument: ${x.name}`) } };
    void need;
    const target = agentOf(a.agent);
    if (a.agent && !target && cmd.args.some((x) => x.name === "agent" && x.kind === "agent")) {
      return { status: "error", error: { code: "agent_not_found", message: pick(lang, `No encuentro al agente "${a.agent}".`, `I can't find the agent "${a.agent}".`) } };
    }

    switch (cmd.id) {
      case "clear":
        if (!b.confirm) return { status: "needs_confirmation", confirm: { message: { es: "¿Seguro que quieres limpiar esta conversación?", en: "Are you sure you want to clear this conversation?" } } };
        return { status: "ok", result: { kind: "message", text: pick(lang, "Conversación limpiada. La memoria de los agentes se mantiene.", "Conversation cleared. Agent memory is kept."), data: { clear: true } } };
      case "compact":
        return msg(pick(lang, "Resumí la conversación: 14 mensajes condensados en 3 puntos, ahorrando unos 1.800 tokens de contexto.", "I summarized the conversation: 14 messages condensed into 3 points, saving about 1,800 context tokens."), pick(lang, "Conversación resumida", "Conversation summarized"));
      case "context":
        return { status: "ok", result: { kind: "card", title: pick(lang, "Contexto", "Context"), data: { [pick(lang, "Mensajes", "Messages")]: this.host.chatCount(b.context.conversation_id || "office"), [pick(lang, "Tokens usados", "Tokens used")]: "3.240 / 128.000", [pick(lang, "Ocupación", "Fill")]: "2,5%" } } };
      case "usage":
        return { status: "ok", result: { kind: "table", title: pick(lang, "Uso y costo", "Usage and cost"), columns: [pick(lang, "Periodo", "Period"), "Tokens", pick(lang, "Costo (USD)", "Cost (USD)")], rows: [[pick(lang, "Esta conversación", "This conversation"), 3240, 0.012], [pick(lang, "Hoy", "Today"), 18450, 0.071], [pick(lang, "Este mes", "This month"), 412300, 1.64]] } };
      case "memory":
        if ((a.action === "borrar" || a.action === "wipe" || a.action === "delete") && !b.confirm) {
          return { status: "needs_confirmation", confirm: { message: { es: `¿Seguro que quieres borrar la memoria de ${target ? first(target) : a.agent}?`, en: `Are you sure you want to wipe ${target ? first(target) : a.agent}'s memory?` } } };
        }
        return msg(a.action ? pick(lang, `Memoria de ${first(target!)} borrada.`, `${first(target!)}'s memory wiped.`) : pick(lang, `${first(target!)} recuerda 4 cosas: tu tono formal, el cierre mensual, la plantilla de facturas y el proveedor principal.`, `${first(target!)} remembers 4 things: your formal tone, the monthly close, the invoice template and the main supplier.`));
      case "pause": return msg(pick(lang, `${first(target!)} está en pausa.`, `${first(target!)} is paused.`));
      case "resume": return msg(pick(lang, `${first(target!)} sigue trabajando.`, `${first(target!)} is back at work.`));
      case "stop": return msg(pick(lang, `Detuve el trabajo de ${first(target!)}.`, `I stopped ${first(target!)}'s work.`));
      case "status":
        return { status: "ok", result: { kind: "table", title: pick(lang, "Estado del equipo", "Team status"), columns: [pick(lang, "Agente", "Agent"), pick(lang, "Estado", "State"), pick(lang, "Actividad", "Activity")], rows: agents.slice(0, 8).map((x) => [first(x), x.state, x.activity]) } };
      case "team":
        return { status: "ok", result: { kind: "table", title: pick(lang, "Tu equipo", "Your team"), columns: [pick(lang, "Agente", "Agent"), pick(lang, "Rol", "Role")], rows: agents.map((x) => [x.name, x.role]) } };
      case "export":
        return { status: "ok", result: { kind: "download", title: pick(lang, "Conversación exportada", "Conversation exported"), filename: "conversacion.txt", url: "data:text/plain;charset=utf-8," + encodeURIComponent(pick(lang, "Conversación exportada (demo)\n", "Exported conversation (demo)\n")) } };
      case "excel": case "word": case "pdf": case "presentation": {
        const ext = { excel: "xlsx", word: "docx", pdf: "pdf", presentation: "pptx" }[cmd.id]!;
        const name = (a.description || "documento").toLowerCase().replace(/[^a-z0-9áéíóúñ]+/gi, "-").slice(0, 28).replace(/^-|-$/g, "") || "documento";
        return { status: "ok", result: { kind: "download", title: pick(lang, `Listo: ${name}.${ext}`, `Ready: ${name}.${ext}`), text: pick(lang, "Lo guardé en tu Drive.", "I saved it to your Drive."), filename: `${name}.${ext}`, url: "data:text/csv;charset=utf-8," + encodeURIComponent("col1,col2\n1,2\n") } };
      }
      case "meeting": case "assign": case "daily": case "email": case "linkedin": case "learn": {
        const text = cmd.id === "assign" ? `${target ? first(target) : ""}: ${a.task}` : a.topic || a.description || pick(lang, "Resumen del día", "Daily summary");
        const id = await this.host.startRequest(text);
        return { status: "started", result: { kind: "request", title: pick(lang, "Empecé a trabajar en esto", "I started working on this"), text: text, request_id: id, href: "/chats" } };
      }
      case "debate": { // B5: the request is marked; the mock engine plays the debate before the plan runs
        const names: string[] = Array.isArray(a.agents) ? a.agents.map(String) : String(a.agents ?? "").split(/[\s,]+/).filter(Boolean);
        const AREA: Record<string, string> = { ventas: "sales", legal: "legal", contabilidad: "accounting", finanzas: "accounting", operaciones: "operations", rrhh: "hr", analista: "analyst", sales: "sales", accounting: "accounting", operations: "operations", hr: "hr", analyst: "analyst" };
        const ids = names.map((n) => agentOf(n)?.id ?? agents.find((x) => x.id === n || x.role === n || x.role === AREA[n.toLowerCase()])?.id ?? n).join(",");
        const text = `[[debate${ids ? ":" + ids : ""}]] ${a.topic || ""}`.trim();
        const id = await this.host.startRequest(text);
        return { status: "started", result: { kind: "request", title: pick(lang, "El equipo va a debatir", "The team will debate"), text: a.topic || "", request_id: id, href: "/meetings?view=debates" } };
      }
      case "customer": { // B12: the real backend scopes the request to the customer before it exists
        const text = String(a.task || "").trim();
        const id = await this.host.startRequest(text);
        return { status: "started", result: { kind: "request", title: pick(lang, `Solicitud para ${a.customer}`, `Request for ${a.customer}`), text, request_id: id } };
      }
      case "pending": return msg(pick(lang, "No tienes aprobaciones pendientes.", "You have no pending approvals."));
      case "search":
        return { status: "ok", result: { kind: "table", title: pick(lang, `Resultados para "${a.query}"`, `Results for "${a.query}"`), columns: [pick(lang, "Dónde", "Where"), pick(lang, "Texto", "Text")], rows: [[pick(lang, "Chat", "Chat"), pick(lang, "…mencionó el contrato…", "…mentioned the contract…")], ["Drive", "Contrato-marco.pdf"]] } };
      case "project": return { status: "ok", result: { kind: "navigate", title: pick(lang, "Abriendo proyectos", "Opening projects"), href: "/projects" } };
      case "permissions": return { status: "ok", result: { kind: "card", title: pick(lang, "Permisos", "Permissions"), data: { [pick(lang, "Leer archivos", "Read files")]: pick(lang, "sí", "yes"), [pick(lang, "Escribir en el Drive", "Write to the Drive")]: pick(lang, "con aprobación", "with approval"), [pick(lang, "Enviar correos", "Send emails")]: pick(lang, "no", "no") } } };
      case "hire": return { status: "ok", result: { kind: "navigate", title: pick(lang, "Abriendo contratación", "Opening hiring"), href: "/" } };
      case "model": return msg(a.model ? pick(lang, `Modelo cambiado a ${a.model}.`, `Model changed to ${a.model}.`) : pick(lang, "Modelo actual: deepseek-chat.", "Current model: deepseek-chat."));
      case "remember": return msg(pick(lang, "Anotado, lo recordaré.", "Noted, I'll remember it."));
      case "tone": return msg(pick(lang, `Tono cambiado a ${a.tone}.`, `Tone changed to ${a.tone}.`));
      case "language": return msg(pick(lang, `Idioma de respuesta: ${a.language}.`, `Reply language: ${a.language}.`));
      case "undo": return msg(pick(lang, "No hay nada que deshacer.", "Nothing to undo."));
      case "trace": return msg(pick(lang, "Último turno: 3 pasos (leer archivo, calcular, responder).", "Last turn: 3 steps (read file, compute, reply)."));
      default: return msg(pick(lang, `Comando /${cmd.id} ejecutado (simulado).`, `Command /${cmd.id} executed (simulated).`));
    }
  }
}
