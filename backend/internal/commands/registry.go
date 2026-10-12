// Package commands is the registry and the parser of the slash commands
// (docs/architecture/commands.md). It is pure: no I/O, no HTTP. The executors
// live next to the services they wrap (internal/api/commands_*.go).
package commands

import (
	"sort"
	"strings"
	"unicode"
)

// L is a text in both supported languages.
type L struct {
	ES string `json:"es"`
	EN string `json:"en"`
}

// Pick returns the text of a language ("en" -> EN, anything else -> ES).
func (l L) Pick(lang string) string {
	if lang == "en" {
		return l.EN
	}
	return l.ES
}

// Aliases are the names a command answers to, per language. A name may be in
// both lists of ONE command (/pdf); it can never belong to two commands.
type Aliases struct {
	ES []string `json:"es"`
	EN []string `json:"en"`
}

// Argument kinds.
const (
	KindAgent    = "agent"
	KindProject  = "project"
	KindFile     = "file"
	KindSkill    = "skill"
	KindTemplate = "template"
	KindText     = "text"
	KindEnum     = "enum"
	KindNumber   = "number"
)

// Arg describes one argument of a command.
type Arg struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
	Variadic bool     `json:"variadic,omitempty"`
}

// Groups.
const (
	GroupControl = "control"
	GroupOffice  = "office"
	GroupOrg     = "org"
)

// Surfaces a command can run on.
const (
	SurfaceOffice  = "office"
	SurfaceGeneral = "general"
	SurfaceAgent   = "agent"
)

// Command is one slash command.
type Command struct {
	ID          string  `json:"id"`
	Aliases     Aliases `json:"aliases"`
	Title       L       `json:"title"`
	Description L       `json:"description"`
	Example     L       `json:"example"`
	Group       string  `json:"group"`
	Icon        string  `json:"icon"`
	Args        []Arg   `json:"args"`
	// Permission is the RBAC permission ("conversations:write") the caller needs. Some
	// commands need a stronger one for part of what they do (WritePermission).
	Permission  string   `json:"permission"`
	Destructive bool     `json:"destructive"`
	UsesLLM     bool     `json:"uses_llm"`
	Surfaces    []string `json:"surfaces"`
	ClientOnly  bool     `json:"client_only"`

	// WritePermission (not part of the public contract) is required when the
	// arguments change something (/model deepseek, /memory forget ...).
	WritePermission string `json:"-"`
}

var allSurfaces = []string{SurfaceOffice, SurfaceGeneral, SurfaceAgent}

func agentArg(required bool) Arg { return Arg{Name: "agent", Kind: KindAgent, Required: required} }

// all is the catalog. Order = order in the UI inside each group.
var all = []Command{
	// ---- control ----
	{ID: "clear", Aliases: Aliases{ES: []string{"limpiar", "nuevo"}, EN: []string{"clear", "new"}},
		Title:       L{"Conversación nueva", "New conversation"},
		Description: L{"Empieza una conversación limpia: el modelo deja de ver lo anterior, que sigue en el historial.", "Starts a clean conversation: the model stops seeing what came before, which stays in the history."},
		Example:     L{"/limpiar", "/clear"}, Group: GroupControl, Icon: "eraser", Permission: "conversations:write", Surfaces: allSurfaces},
	{ID: "compact", Aliases: Aliases{ES: []string{"resumir", "compactar"}, EN: []string{"compact", "summarize"}},
		Title:       L{"Resumir la conversación", "Compact the conversation"},
		Description: L{"Resume la conversación con el modelo activo; los siguientes turnos solo envían el resumen.", "Summarizes the conversation with the active model; later turns send only the summary."},
		Example:     L{"/resumir", "/compact"}, Group: GroupControl, Icon: "minimize", Permission: "conversations:write", UsesLLM: true, Surfaces: allSurfaces},
	{ID: "context", Aliases: Aliases{ES: []string{"contexto"}, EN: []string{"context"}},
		Title:       L{"Ver el contexto", "Show the context"},
		Description: L{"Qué recibe el agente: perfil, skills, memoria, archivos y dependencias, con tokens aproximados.", "What the agent receives: profile, skills, memory, files and dependencies, with approximate tokens."},
		Example:     L{"/contexto @ventas", "/context @sales"}, Group: GroupControl, Icon: "layers", Args: []Arg{agentArg(false)}, Permission: "agents:read", Surfaces: allSurfaces},
	{ID: "usage", Aliases: Aliases{ES: []string{"uso", "costo", "gasto"}, EN: []string{"usage", "cost"}},
		Title:       L{"Uso y costo", "Usage and cost"},
		Description: L{"Gasto de hoy y del mes, por agente y por proyecto, presupuesto restante y monedas ganadas.", "Spend today and this month, per agent and project, budget left and coins earned."},
		Example:     L{"/uso", "/usage"}, Group: GroupControl, Icon: "wallet", Permission: "metrics:read", Surfaces: allSurfaces},
	{ID: "model", Aliases: Aliases{ES: []string{"modelo"}, EN: []string{"model"}},
		Title:       L{"Modelo", "Model"},
		Description: L{"Muestra o cambia el proveedor de modelo de la organización o de un agente.", "Shows or changes the model provider of the organization or of one agent."},
		Example:     L{"/modelo deepseek @ventas", "/model deepseek @sales"}, Group: GroupControl, Icon: "cpu",
		Args:       []Arg{{Name: "value", Kind: KindText}, agentArg(false)},
		Permission: "agents:read", WritePermission: "org:manage", Surfaces: allSurfaces},
	{ID: "memory", Aliases: Aliases{ES: []string{"memoria"}, EN: []string{"memory"}},
		Title:       L{"Memoria del agente", "Agent memory"},
		Description: L{"Ver, borrar u olvidar lo que el agente recuerda.", "View, delete or forget what the agent remembers."},
		Example:     L{"/memoria olvidar precio", "/memory forget price"}, Group: GroupControl, Icon: "brain",
		Args:       []Arg{{Name: "action", Kind: KindEnum, Enum: []string{"view", "delete", "forget"}}, {Name: "text", Kind: KindText, Variadic: true}, agentArg(false)},
		Permission: "memories:read", WritePermission: "memories:write", Destructive: true, Surfaces: allSurfaces},
	{ID: "pause", Aliases: Aliases{ES: []string{"pausar"}, EN: []string{"pause"}},
		Title:       L{"Pausar", "Pause"},
		Description: L{"Pausa un agente o un proyecto.", "Pauses an agent or a project."},
		Example:     L{"/pausar @ventas", "/pause @sales"}, Group: GroupControl, Icon: "pause", Args: []Arg{agentArg(false)}, Permission: "controls:pause", Surfaces: allSurfaces},
	{ID: "resume", Aliases: Aliases{ES: []string{"reanudar", "continuar"}, EN: []string{"resume"}},
		Title:       L{"Reanudar", "Resume"},
		Description: L{"Reanuda un agente o un proyecto pausado.", "Resumes a paused agent or project."},
		Example:     L{"/reanudar @ventas", "/resume @sales"}, Group: GroupControl, Icon: "play", Args: []Arg{agentArg(false)}, Permission: "controls:pause", Surfaces: allSurfaces},
	{ID: "stop", Aliases: Aliases{ES: []string{"detener", "parar"}, EN: []string{"stop"}},
		Title:       L{"Detener", "Stop"},
		Description: L{"Detiene de inmediato un agente o cancela un proyecto.", "Stops an agent immediately or cancels a project."},
		Example:     L{"/detener @ventas", "/stop @sales"}, Group: GroupControl, Icon: "stop", Args: []Arg{agentArg(false)}, Permission: "controls:pause", Destructive: true, Surfaces: allSurfaces},
	{ID: "status", Aliases: Aliases{ES: []string{"estado"}, EN: []string{"status"}},
		Title:       L{"Estado del equipo", "Team status"},
		Description: L{"Qué hace cada agente, tareas en curso y aprobaciones pendientes.", "What each agent is doing, running tasks and pending approvals."},
		Example:     L{"/estado", "/status"}, Group: GroupControl, Icon: "activity", Permission: "agents:read", Surfaces: allSurfaces},
	{ID: "trace", Aliases: Aliases{ES: []string{"traza"}, EN: []string{"trace"}},
		Title:       L{"Última traza", "Last trace"},
		Description: L{"Abre la traza de la última tarea del agente.", "Opens the trace of the agent's last task."},
		Example:     L{"/traza @ventas", "/trace @sales"}, Group: GroupControl, Icon: "route", Args: []Arg{agentArg(false)}, Permission: "audit:read", Surfaces: allSurfaces},
	{ID: "export", Aliases: Aliases{ES: []string{"exportar"}, EN: []string{"export"}},
		Title:       L{"Exportar la conversación", "Export the conversation"},
		Description: L{"Guarda la conversación como Markdown o PDF en el drive.", "Saves the conversation as Markdown or PDF in the drive."},
		Example:     L{"/exportar pdf", "/export pdf"}, Group: GroupControl, Icon: "download",
		Args: []Arg{{Name: "format", Kind: KindEnum, Enum: []string{"md", "pdf"}}}, Permission: "conversations:read", Surfaces: allSurfaces},

	// ---- office ----
	deliverable("excel", []string{"excel", "hoja"}, []string{"excel", "sheet"}, "table",
		L{"Entregable Excel", "Excel deliverable"},
		L{"Pide el trabajo como un archivo Excel real.", "Asks for the work as a real Excel file."},
		L{"/excel 10 productos con precio y stock", "/excel 10 products with price and stock"}),
	deliverable("word", []string{"word", "documento"}, []string{"word", "document"}, "file-text",
		L{"Entregable Word", "Word deliverable"},
		L{"Pide el trabajo como un documento Word real.", "Asks for the work as a real Word document."},
		L{"/word informe de ventas del mes", "/word monthly sales report"}),
	deliverable("pdf", []string{"pdf"}, []string{"pdf"}, "file",
		L{"Entregable PDF", "PDF deliverable"},
		L{"Pide el trabajo como un PDF real.", "Asks for the work as a real PDF."},
		L{"/pdf resumen para la junta", "/pdf summary for the board"}),
	deliverable("presentation", []string{"presentacion", "diapositivas"}, []string{"presentation", "slides"}, "presentation",
		L{"Presentación", "Presentation"},
		L{"Pide una presentación (PDF con una diapositiva por sección).", "Asks for a presentation (a PDF with one slide per section)."},
		L{"/presentacion plan comercial 2027", "/presentation 2027 sales plan"}),
	{ID: "meeting", Aliases: Aliases{ES: []string{"reunion"}, EN: []string{"meeting"}},
		Title:       L{"Reunión de agentes", "Agents meeting"},
		Description: L{"Los agentes nombrados discuten el tema y la asistente redacta la conclusión.", "The named agents discuss the topic and the assistant writes the conclusion."},
		Example:     L{"/reunion ventas legal contabilidad: precios 2027", "/meeting sales legal accounting: 2027 pricing"}, Group: GroupOffice, Icon: "users",
		Args:       []Arg{{Name: "agents", Kind: KindAgent, Required: true, Variadic: true}, {Name: "topic", Kind: KindText, Required: true}},
		Permission: "requests:create", UsesLLM: true, Surfaces: allSurfaces},
	{ID: "assign", Aliases: Aliases{ES: []string{"encargar", "asignar"}, EN: []string{"assign"}},
		Title:       L{"Encargar una tarea", "Assign a task"},
		Description: L{"Da una tarea directa a un agente.", "Gives a direct task to one agent."},
		Example:     L{"/encargar @ventas prepara la cotización", "/assign @sales prepare the quote"}, Group: GroupOffice, Icon: "send",
		Args:       []Arg{agentArg(true), {Name: "task", Kind: KindText, Required: true, Variadic: true}},
		Permission: "requests:create", UsesLLM: true, Surfaces: allSurfaces},
	{ID: "project", Aliases: Aliases{ES: []string{"proyecto"}, EN: []string{"project"}},
		Title:       L{"Nuevo proyecto", "New project"},
		Description: L{"Abre el asistente de proyectos, opcionalmente con una plantilla.", "Opens the project wizard, optionally with a template."},
		Example:     L{"/proyecto", "/project"}, Group: GroupOffice, Icon: "folder-plus",
		Args:       []Arg{{Name: "template", Kind: KindTemplate}},
		Permission: "requests:create", ClientOnly: true, Surfaces: allSurfaces},
	{ID: "daily-summary", Aliases: Aliases{ES: []string{"resumen-del-dia", "resumendeldia", "hoy"}, EN: []string{"daily-summary", "daily", "today"}},
		Title:       L{"Resumen del día", "Summary of the day"},
		Description: L{"Qué pasó hoy: tareas, informes, aprobaciones y gasto.", "What happened today: tasks, reports, approvals and spend."},
		Example:     L{"/resumen-del-dia", "/daily-summary"}, Group: GroupOffice, Icon: "sun", Permission: "reports:read", UsesLLM: true, Surfaces: allSurfaces},
	{ID: "pending", Aliases: Aliases{ES: []string{"pendientes"}, EN: []string{"pending"}},
		Title:       L{"Pendientes", "Pending"},
		Description: L{"Aprobaciones por decidir y tareas abiertas.", "Approvals waiting for a decision and open tasks."},
		Example:     L{"/pendientes", "/pending"}, Group: GroupOffice, Icon: "inbox", Permission: "approvals:read", Surfaces: allSurfaces},
	{ID: "search", Aliases: Aliases{ES: []string{"buscar"}, EN: []string{"search", "find"}},
		Title:       L{"Buscar", "Search"},
		Description: L{"Busca en archivos, chats, informes y memoria.", "Searches files, chats, reports and memory."},
		Example:     L{"/buscar contrato Acme", "/search Acme contract"}, Group: GroupOffice, Icon: "search",
		Args:       []Arg{{Name: "query", Kind: KindText, Required: true, Variadic: true}},
		Permission: "conversations:read", Surfaces: allSurfaces},
	{ID: "file", Aliases: Aliases{ES: []string{"archivo", "abrir"}, EN: []string{"file", "open"}},
		Title:       L{"Abrir un archivo", "Open a file"},
		Description: L{"Abre un archivo del drive en el visor.", "Opens a drive file in the viewer."},
		Example:     L{"/abrir /Proyectos/cotizacion.pdf", "/open /Projects/quote.pdf"}, Group: GroupOffice, Icon: "file",
		Args:       []Arg{{Name: "file", Kind: KindFile, Required: true, Variadic: true}},
		Permission: "files:read", Surfaces: allSurfaces},
	draft("email", []string{"correo"}, "mail",
		L{"Borrador de correo", "Email draft"},
		L{"Un agente redacta el correo; el envío queda pendiente de tu aprobación.", "An agent drafts the email; sending waits for your approval."},
		L{"/correo avisar al cliente del retraso", "/email tell the client about the delay"}),
	draft("linkedin", []string{"linkedin"}, "linkedin",
		L{"Borrador de LinkedIn", "LinkedIn draft"},
		L{"Un agente redacta la publicación; publicarla queda pendiente de tu aprobación.", "An agent drafts the post; publishing waits for your approval."},
		L{"/linkedin anunciar el lanzamiento", "/linkedin announce the launch"}),
	{ID: "remember", Aliases: Aliases{ES: []string{"recordar"}, EN: []string{"remember"}},
		Title:       L{"Recordar algo", "Remember something"},
		Description: L{"Guarda un dato en la memoria del agente.", "Stores a fact in the agent's memory."},
		Example:     L{"/recordar el cliente prefiere entregas los lunes", "/remember the client prefers Monday deliveries"}, Group: GroupOffice, Icon: "bookmark",
		Args:       []Arg{{Name: "text", Kind: KindText, Required: true, Variadic: true}, agentArg(false)},
		Permission: "memories:write", Surfaces: allSurfaces},
	{ID: "tone", Aliases: Aliases{ES: []string{"tono"}, EN: []string{"tone"}},
		Title:       L{"Tono", "Tone"},
		Description: L{"Cambia el tono regional de un agente o de la organización.", "Changes the regional tone of an agent or of the organization."},
		Example:     L{"/tono mx @ventas", "/tone mx @sales"}, Group: GroupOffice, Icon: "message-circle",
		Args:       []Arg{{Name: "value", Kind: KindEnum, Required: true, Enum: []string{"neutral", "mx", "co", "ar", "cl", "es"}}, agentArg(false)},
		Permission: "agents:write", WritePermission: "org:manage", Surfaces: allSurfaces},
	{ID: "language", Aliases: Aliases{ES: []string{"idioma", "lenguaje"}, EN: []string{"language", "lang"}},
		Title:       L{"Idioma", "Language"},
		Description: L{"Cambia el idioma de la organización (es o en).", "Changes the organization language (es or en)."},
		Example:     L{"/idioma en", "/language es"}, Group: GroupOffice, Icon: "globe",
		Args:       []Arg{{Name: "value", Kind: KindEnum, Required: true, Enum: []string{"es", "en"}}},
		Permission: "org:manage", Surfaces: allSurfaces},
	{ID: "save-template", Aliases: Aliases{ES: []string{"plantilla"}, EN: []string{"template"}},
		Title:       L{"Guardar como plantilla", "Save as template"},
		Description: L{"Convierte la última solicitud (o el proyecto actual) en una plantilla de la organización.", "Turns the last request (or the current project) into an organization template."},
		Example:     L{"/plantilla guardar Cotización mensual", "/template save Monthly quote"}, Group: GroupOffice, Icon: "copy",
		Args:       []Arg{{Name: "name", Kind: KindText, Variadic: true}},
		Permission: "requests:create", Surfaces: allSurfaces},
	{ID: "skill", Aliases: Aliases{ES: []string{"skill", "habilidad"}, EN: []string{"skill"}},
		Title:       L{"Dar una skill", "Give a skill"},
		Description: L{"Asigna una skill de la biblioteca al agente para sus próximas tareas.", "Assigns a library skill to the agent for its next tasks."},
		Example:     L{"/skill redaccion-formal @ventas", "/skill formal-writing @sales"}, Group: GroupOffice, Icon: "sparkles",
		Args:       []Arg{{Name: "skill", Kind: KindSkill, Required: true}, agentArg(false)},
		Permission: "skills:read", WritePermission: "agents:write", Surfaces: allSurfaces},
	{ID: "learn", Aliases: Aliases{ES: []string{"aprender"}, EN: []string{"learn"}},
		Title:       L{"Aprender de la tarea", "Learn from the task"},
		Description: L{"Guarda la última tarea terminada como skill (borrador para revisión).", "Saves the last finished task as a skill (draft for review)."},
		Example:     L{"/aprender cotizacion-mensual", "/learn monthly-quote"}, Group: GroupOffice, Icon: "graduation-cap",
		Args:       []Arg{{Name: "name", Kind: KindText}, agentArg(false)},
		Permission: "skills:write", Surfaces: allSurfaces},
	{ID: "undo", Aliases: Aliases{ES: []string{"deshacer"}, EN: []string{"undo"}},
		Title:       L{"Deshacer el último cambio", "Undo the last change"},
		Description: L{"Vuelve a la versión anterior del último archivo que cambió el agente (o tú).", "Goes back to the previous version of the last file the agent (or you) changed."},
		Example:     L{"/deshacer @ventas", "/undo @sales"}, Group: GroupOffice, Icon: "undo", Args: []Arg{agentArg(false)},
		Permission: "files:write", Destructive: true, Surfaces: allSurfaces},

	// ---- org ----
	{ID: "team", Aliases: Aliases{ES: []string{"equipo"}, EN: []string{"team"}},
		Title:       L{"Equipo", "Team"},
		Description: L{"Quién está en el equipo y qué hace cada uno.", "Who is on the team and what each one does."},
		Example:     L{"/equipo", "/team"}, Group: GroupOrg, Icon: "users", Permission: "agents:read", Surfaces: allSurfaces},
	{ID: "hire", Aliases: Aliases{ES: []string{"contratar"}, EN: []string{"hire"}},
		Title:       L{"Contratar un rol", "Hire a role"},
		Description: L{"Suma un agente desde una plantilla de rol.", "Adds an agent from a role template."},
		Example:     L{"/contratar legal", "/hire legal"}, Group: GroupOrg, Icon: "user-plus",
		Args:       []Arg{{Name: "role", Kind: KindText, Required: true}, {Name: "name", Kind: KindText, Variadic: true}},
		Permission: "agents:write", Surfaces: allSurfaces},
	{ID: "permissions", Aliases: Aliases{ES: []string{"permisos"}, EN: []string{"permissions"}},
		Title:       L{"Permisos", "Permissions"},
		Description: L{"Quién aprueba qué: reglas de aprobación y autonomía de cada agente.", "Who approves what: approval rules and each agent's autonomy."},
		Example:     L{"/permisos", "/permissions"}, Group: GroupOrg, Icon: "shield", Permission: "approvals:read", Surfaces: allSurfaces},
	{ID: "shop", Aliases: Aliases{ES: []string{"tienda"}, EN: []string{"shop", "store"}},
		Title:       L{"Tienda", "Shop"},
		Description: L{"Abre la tienda de monedas.", "Opens the coin shop."},
		Example:     L{"/tienda", "/shop"}, Group: GroupOrg, Icon: "shopping-bag", Permission: "shop:read", ClientOnly: true, Surfaces: allSurfaces},
	{ID: "inventory", Aliases: Aliases{ES: []string{"inventario"}, EN: []string{"inventory"}},
		Title:       L{"Inventario", "Inventory"},
		Description: L{"Abre tus objetos comprados.", "Opens your purchased items."},
		Example:     L{"/inventario", "/inventory"}, Group: GroupOrg, Icon: "package", Permission: "shop:read", ClientOnly: true, Surfaces: allSurfaces},
}

func deliverable(id string, es, en []string, icon string, title, desc, example L) Command {
	return Command{ID: id, Aliases: Aliases{ES: es, EN: en}, Title: title, Description: desc, Example: example,
		Group: GroupOffice, Icon: icon, Args: []Arg{{Name: "text", Kind: KindText, Required: true, Variadic: true}},
		Permission: "requests:create", UsesLLM: true, Surfaces: allSurfaces}
}

func draft(id string, es []string, icon string, title, desc, example L) Command {
	return Command{ID: id, Aliases: Aliases{ES: es, EN: []string{id}}, Title: title, Description: desc, Example: example,
		Group: GroupOffice, Icon: icon, Args: []Arg{{Name: "text", Kind: KindText, Required: true, Variadic: true}},
		Permission: "requests:create", UsesLLM: true, Surfaces: allSurfaces}
}

// Fold lower-cases and removes accents and a leading slash: "/Reunión" -> "reunion".
func Fold(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "/"))
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		case 'ñ':
			r = 'n'
		}
		b.WriteRune(r)
	}
	return b.String()
}

var (
	byID    = map[string]int{}
	byAlias = map[string]aliasHit{}
)

type aliasHit struct {
	idx  int
	lang string // "es" | "en" | "" (the same word in both languages)
}

func init() {
	for i, c := range all {
		byID[c.ID] = i
		for _, a := range c.Aliases.ES {
			addAlias(i, Fold(a), "es")
		}
		for _, a := range c.Aliases.EN {
			addAlias(i, Fold(a), "en")
		}
		addAlias(i, Fold(c.ID), "en")
	}
}

func addAlias(idx int, alias, lang string) {
	if h, ok := byAlias[alias]; ok {
		if h.idx == idx && h.lang != lang {
			h.lang = ""
			byAlias[alias] = h
		}
		return
	}
	byAlias[alias] = aliasHit{idx: idx, lang: lang}
}

// All returns a copy of the catalog.
func All() []Command {
	out := make([]Command, len(all))
	copy(out, all)
	return out
}

// Get returns a command by id.
func Get(id string) (Command, bool) {
	i, ok := byID[id]
	if !ok {
		return Command{}, false
	}
	return all[i], true
}

// Lookup resolves an id or an alias (any case, accents and an optional leading
// slash). lang is the language of the alias: "" when the word exists in both.
func Lookup(token string) (cmd Command, lang string, ok bool) {
	h, ok := byAlias[Fold(token)]
	if !ok {
		return Command{}, "", false
	}
	return all[h.idx], h.lang, true
}

// AllAliases lists every alias (folded) with the id of its command, sorted.
func AllAliases() map[string]string {
	out := map[string]string{}
	for a, h := range byAlias {
		out[a] = all[h.idx].ID
	}
	return out
}

// Names returns the sorted ids (tests).
func Names() []string {
	ids := make([]string, 0, len(all))
	for _, c := range all {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// Supports reports whether the command runs on a surface.
func (c Command) Supports(surface string) bool {
	for _, s := range c.Surfaces {
		if s == surface {
			return true
		}
	}
	return false
}

// IsWord reports whether s looks like a command token (letters, digits, '-').
func IsWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}
	return true
}
