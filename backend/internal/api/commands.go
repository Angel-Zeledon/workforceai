package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/domain"
)

// Slash commands (docs/architecture/commands.md). The registry and the parser
// live in internal/commands; the executors (commands_*.go) call the same
// services as the REST handlers, so RBAC, budget, audit and the Tool Gateway
// apply exactly as they do everywhere else. The model is never asked which
// command to run: only a person types one.

// CommandsDeps configures the slash commands. The zero value is usable.
type CommandsDeps struct {
	// Limiter rate-limits runs per person (nil: in-process fixed window).
	Limiter auth.Limiter
	// RunLimit / LLMRunLimit: runs per window; commands that call the model have their own, lower limit.
	RunLimit, LLMRunLimit auth.Limit
	// Models is the seam for the bring-your-own-model service (branch g3). nil: /model only knows the built-in providers.
	Models ModelCatalog
	// LongMemory is the seam for the long-term memory service (branch m1). nil: the agent memory store is used.
	LongMemory LongMemory
}

// ModelCatalog resolves a model name the built-in providers do not know (bring-your-own models).
type ModelCatalog interface {
	// Resolve returns the provider id to route to, or ok=false when the name is unknown.
	Resolve(ctx context.Context, org, name string) (provider string, ok bool)
}

// MemoryItem is one remembered fact.
type MemoryItem struct {
	Scope, Key, Value string
}

// LongMemory is the long-term memory service seam.
type LongMemory interface {
	List(ctx context.Context, org, agentID string) ([]MemoryItem, error)
	Remember(ctx context.Context, org, agentID, text string) error
	Forget(ctx context.Context, org, agentID, text string) (int, error)
}

type commandsState struct {
	CommandsDeps
	idem sync.Map // org|actor|key -> idemEntry
}

type idemEntry struct {
	at     time.Time
	status int
	body   runResponse
}

const (
	cmdIdemTTL     = 10 * time.Minute
	suggestMinMsgs = 40
)

// cmdHandlers is filled by the init functions of commands_*.go.
var cmdHandlers = map[string]handler{}

// ---- wire types ----

type cmdCtxIn struct {
	Surface        string  `json:"surface"`
	AgentID        string  `json:"agent_id,omitempty"`
	ConversationID string  `json:"conversation_id,omitempty"`
	ProjectID      *string `json:"project_id,omitempty"`
}

type runRequest struct {
	Command        string         `json:"command"`
	Args           map[string]any `json:"args"`
	Raw            string         `json:"raw"`
	Lang           string         `json:"lang"`
	Context        cmdCtxIn       `json:"context"`
	Confirm        bool           `json:"confirm"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type cmdResult struct {
	Kind           string         `json:"kind"`
	Title          string         `json:"title,omitempty"`
	Text           string         `json:"text,omitempty"`
	Columns        []string       `json:"columns,omitempty"`
	Rows           [][]string     `json:"rows,omitempty"`
	Href           string         `json:"href,omitempty"`
	URL            string         `json:"url,omitempty"`
	Filename       string         `json:"filename,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
}

type cmdError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type confirmBody struct {
	Message commands.L `json:"message"`
}

type runResponse struct {
	Status  string       `json:"status"`
	Lang    string       `json:"lang"`
	Command string       `json:"command,omitempty"`
	Confirm *confirmBody `json:"confirm,omitempty"`
	Result  *cmdResult   `json:"result,omitempty"`
	Error   *cmdError    `json:"error,omitempty"`
}

// cmdFail is an executor's refusal: HTTP status, stable code and a message in both languages.
type cmdFail struct {
	status int
	code   string
	msg    commands.L
}

// handler is the executor of one command. confirm (optional) returns the message to show
// before a destructive run, after validating the target; it must not change anything.
type handler struct {
	run     func(c *cmdCall) (*cmdResult, *cmdFail)
	confirm func(c *cmdCall) (commands.L, *cmdFail)
	// available says whether the feature behind the command is configured on this server.
	available func(s *server) bool
}

// cmdCall is one run of a command.
type cmdCall struct {
	s       *server
	r       *http.Request
	ctx     context.Context
	cmd     commands.Command
	args    map[string]any
	lang    string
	surface string
	agentID string // context agent (1:1 chat)
	conv    string
	project string
	raw     string
	org     string
	actor   string

	agents []domain.Agent
	loaded bool
}

func (c *cmdCall) t(es, en string) string {
	if c.lang == "en" {
		return en
	}
	return es
}

func fail(status int, code, es, en string) *cmdFail {
	return &cmdFail{status: status, code: code, msg: commands.L{ES: es, EN: en}}
}

// ---- mounting ----

func (s *server) mountCommands(r chi.Router) {
	if s.Commands == nil {
		return
	}
	if s.cmds == nil {
		s.cmds = &commandsState{CommandsDeps: *s.Commands}
		if s.cmds.Limiter == nil {
			s.cmds.Limiter = auth.NewMemoryLimiter()
		}
		if s.cmds.RunLimit.Max == 0 {
			s.cmds.RunLimit = auth.Limit{Max: 120, Window: time.Minute}
		}
		if s.cmds.LLMRunLimit.Max == 0 {
			s.cmds.LLMRunLimit = auth.Limit{Max: 20, Window: time.Minute}
		}
	}
	// Any authenticated role may open the catalog; every run is authorized per command.
	r.Get("/commands", s.listCommands)
	r.Get("/commands/complete", s.completeCommands)
	r.Get("/commands/suggest", s.suggestCommands)
	r.Post("/commands/run", s.runCommand)
}

// ---- GET /commands ----

type commandView struct {
	commands.Command
	Allowed   bool `json:"allowed"`
	Available bool `json:"available"`
}

func (s *server) listCommands(w http.ResponseWriter, r *http.Request) {
	out := make([]commandView, 0, 48)
	for _, c := range commands.All() {
		// Lists are always JSON arrays, never null (the composer iterates them).
		if c.Args == nil {
			c.Args = []commands.Arg{}
		}
		out = append(out, commandView{Command: c, Allowed: s.has(r, auth.Permission(c.Permission)), Available: s.cmdAvailable(c.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) cmdAvailable(id string) bool {
	h, ok := cmdHandlers[id]
	return ok && (h.available == nil || h.available(s))
}

// ---- POST /commands/run ----

func (s *server) runCommand(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := s.decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if len(key) > 128 {
		writeJSON(w, http.StatusBadRequest, runResponse{Status: "error", Lang: "es", Error: &cmdError{Code: "invalid_args", Message: "idempotency_key is too long (max 128)"}})
		return
	}
	full := s.org(r) + "|" + s.actor(r) + "|" + key
	if key != "" {
		if v, ok := s.cmds.idem.Load(full); ok {
			if e := v.(idemEntry); time.Since(e.at) < cmdIdemTTL {
				w.Header().Set("Idempotent-Replay", "true")
				writeJSON(w, e.status, e.body)
				return
			}
		}
	}
	status, body := s.execCommand(r, req)
	if key != "" && body.Status != "needs_confirmation" && status < 500 && status != http.StatusTooManyRequests {
		s.cmds.idem.Store(full, idemEntry{at: time.Now(), status: status, body: body})
	}
	writeJSON(w, status, body)
}

func (s *server) execCommand(r *http.Request, req runRequest) (int, runResponse) {
	ctx := r.Context()
	word, rest := strings.TrimPrefix(strings.TrimSpace(req.Command), "/"), ""
	if word == "" && strings.TrimSpace(req.Raw) != "" {
		if w, rs, ok := commands.Split(req.Raw); ok {
			word, rest = w, rs
		}
	} else if strings.TrimSpace(req.Raw) != "" {
		if _, rs, ok := commands.Split(req.Raw); ok {
			rest = rs
		}
	}
	lang := strings.ToLower(strings.TrimSpace(req.Lang))
	if lang != "es" && lang != "en" {
		lang = ""
	}
	cmd, aliasLang, found := commands.Lookup(word)
	if lang == "" {
		lang = aliasLang
	}
	if lang == "" {
		lang = commands.DetectLang(req.Raw + " " + rest)
	}
	if lang == "" {
		lang = s.orgLang(ctx)
	}
	errResp := func(status int, code string, m commands.L) (int, runResponse) {
		return status, runResponse{Status: "error", Lang: lang, Command: cmd.ID, Error: &cmdError{Code: code, Message: m.Pick(lang)}}
	}
	if !found {
		s.cmdAudit(ctx, r, "command.denied", word, nil, map[string]any{"reason": "unknown_command", "lang": lang})
		return errResp(http.StatusNotFound, "unknown_command", commands.L{ES: "Ese comando no existe: /" + word, EN: "That command does not exist: /" + word})
	}
	h := cmdHandlers[cmd.ID]
	// RBAC first: nothing about the arguments is looked at before the role is checked.
	if !s.has(r, auth.Permission(cmd.Permission)) {
		s.cmdAudit(ctx, r, "command.denied", cmd.ID, req.Args, map[string]any{"reason": "forbidden", "lang": lang, "surface": req.Context.Surface})
		return errResp(http.StatusForbidden, "forbidden", commands.L{ES: "Tu rol no puede ejecutar /" + cmd.ID, EN: "Your role cannot run /" + cmd.ID})
	}
	limit := s.cmds.RunLimit
	if cmd.UsesLLM {
		limit = s.cmds.LLMRunLimit
	}
	bucket := "cmd:" + s.org(r) + ":" + s.actor(r)
	if cmd.UsesLLM {
		bucket += ":llm"
	}
	if res, err := s.cmds.Limiter.Allow(ctx, bucket, limit); err == nil && !res.Allowed {
		s.cmdAudit(ctx, r, "command.denied", cmd.ID, req.Args, map[string]any{"reason": "rate_limited", "lang": lang})
		return errResp(http.StatusTooManyRequests, "rate_limited", commands.L{
			ES: fmt.Sprintf("Demasiados comandos seguidos. Espera %d s.", int(res.RetryAfter.Seconds())+1),
			EN: fmt.Sprintf("Too many commands in a row. Wait %d s.", int(res.RetryAfter.Seconds())+1)})
	}
	surface := strings.ToLower(strings.TrimSpace(req.Context.Surface))
	if surface == "" {
		surface = commands.SurfaceOffice
	}
	if !cmd.Supports(surface) {
		return errResp(http.StatusBadRequest, "surface_not_supported", commands.L{ES: "Este comando no está disponible aquí.", EN: "This command is not available here."})
	}
	if h.run == nil || (h.available != nil && !h.available(s)) {
		return errResp(http.StatusNotImplemented, "not_available", commands.L{ES: "Este servidor no tiene configurado lo que necesita /" + cmd.ID, EN: "This server does not have what /" + cmd.ID + " needs"})
	}
	c := &cmdCall{s: s, r: r, ctx: ctx, cmd: cmd, lang: lang, surface: surface, raw: req.Raw, org: s.org(r), actor: s.actor(r)}
	c.agentID = strings.TrimSpace(req.Context.AgentID)
	if req.Context.ProjectID != nil {
		c.project = strings.TrimSpace(*req.Context.ProjectID)
	}
	if rf := c.agentRef(c.agentID); c.agentID != "" && rf == nil {
		return errResp(http.StatusBadRequest, "unknown_agent", commands.L{ES: "El agente del contexto no existe.", EN: "The context agent does not exist."})
	}
	if rf := c.agentRef(c.agentID); rf != nil {
		c.agentID = rf.ID
	}
	c.conv = c.convID(req.Context.ConversationID)
	// Arguments: explicit ones win; otherwise parse the text after the command.
	args := map[string]any{}
	if len(req.Args) == 0 && strings.TrimSpace(rest) != "" {
		args = commands.ParseArgs(cmd, rest, commands.ParseOpts{IsAgent: func(w string) bool { return c.agentRef(w) != nil }})
	} else {
		for k, v := range req.Args {
			args[k] = v
		}
	}
	c.args = args
	if f := c.validateArgs(); f != nil {
		s.cmdAudit(ctx, r, "command.run", cmd.ID, args, map[string]any{"status": "error", "code": f.code, "lang": lang, "surface": surface})
		return errResp(f.status, f.code, f.msg)
	}
	if destructiveRun(cmd, c) && !req.Confirm {
		msg := commands.L{ES: "/" + cmd.ID + " no se puede deshacer. ¿Confirmas?", EN: "/" + cmd.ID + " cannot be undone. Do you confirm?"}
		if h.confirm != nil {
			m, f := h.confirm(c)
			if f != nil {
				return errResp(f.status, f.code, f.msg)
			}
			msg = m
		}
		return http.StatusOK, runResponse{Status: "needs_confirmation", Lang: lang, Command: cmd.ID, Confirm: &confirmBody{Message: msg}}
	}
	res, f := h.run(c)
	if f != nil {
		s.cmdAudit(ctx, r, "command.run", cmd.ID, args, map[string]any{"status": "error", "code": f.code, "lang": lang, "surface": surface})
		return errResp(f.status, f.code, f.msg)
	}
	status := "ok"
	if res.Kind == "request" {
		status = "started"
	}
	d := map[string]any{"status": status, "lang": lang, "surface": surface, "confirmed": req.Confirm}
	if c.agentID != "" {
		d["agent_id"] = c.agentID
	}
	if res.RequestID != "" {
		d["request_id"] = res.RequestID
	}
	s.cmdAudit(ctx, r, "command.run", cmd.ID, args, d)
	return http.StatusOK, runResponse{Status: status, Lang: lang, Command: cmd.ID, Result: res}
}

// destructiveRun: /memory only asks for confirmation when it deletes.
func destructiveRun(cmd commands.Command, c *cmdCall) bool {
	if !cmd.Destructive {
		return false
	}
	if cmd.ID == "memory" {
		a := c.str("action")
		return a == "delete" || a == "forget"
	}
	return true
}

// cmdAudit records a run: the command id and the NAMES of the arguments, never their values.
func (s *server) cmdAudit(ctx context.Context, r *http.Request, action, id string, args map[string]any, details map[string]any) {
	if s.Rec == nil {
		return
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	d := map[string]any{"command": id, "arg_keys": keys}
	for k, v := range details {
		d[k] = v
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: s.actor(r), Action: action, Entity: "command", EntityID: id, Details: d})
}

// orgLang is the organization language ("es" when nothing is configured).
func (s *server) orgLang(ctx context.Context) string {
	if s.OrgConfig != nil {
		if st, err := s.OrgConfig.Style(ctx); err == nil && strings.HasPrefix(strings.ToLower(st.Locale), "en") {
			return "en"
		}
	}
	return "es"
}

// ---- helpers shared by the executors ----

func (c *cmdCall) loadAgents() []domain.Agent {
	if !c.loaded {
		c.agents, _ = c.s.Store.ListAgents(c.ctx, c.org)
		c.loaded = true
	}
	return c.agents
}

// agentRef finds an agent by id, name or role (any case, accents ignored). nil when none.
func (c *cmdCall) agentRef(ref string) *domain.Agent {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "@")
	if ref == "" {
		return nil
	}
	f := commands.Fold(ref)
	agents := c.loadAgents()
	for i := range agents {
		if commands.Fold(agents[i].ID) == f {
			return &agents[i]
		}
	}
	for i := range agents {
		if commands.Fold(agents[i].Name) == f || commands.Fold(agents[i].Role) == f {
			return &agents[i]
		}
	}
	for i := range agents {
		if commands.Fold(agents[i].Title) == f || strings.HasPrefix(commands.Fold(agents[i].Name), f) {
			return &agents[i]
		}
	}
	// the word of the area ("ventas", "contabilidad", "rrhh"...) in either language
	if role, ok := roleWords[f]; ok {
		for i := range agents {
			if agents[i].Role == role {
				return &agents[i]
			}
		}
	}
	var hit *domain.Agent
	for i := range agents {
		if strings.Contains(commands.Fold(agents[i].Title), f) && len(f) >= 4 {
			if hit != nil {
				return nil // ambiguous
			}
			hit = &agents[i]
		}
	}
	return hit
}

// roleWords maps the words people use for an area to the role id of the agent that owns it.
var roleWords = map[string]string{
	"ventas": "sales", "venta": "sales", "comercial": "sales", "sales": "sales",
	"contabilidad": "accounting", "contador": "accounting", "contadora": "accounting", "finanzas": "accounting", "accountant": "accounting", "finance": "accounting",
	"legal": "legal", "abogado": "legal", "abogada": "legal", "lawyer": "legal",
	"rrhh": "hr", "hr": "hr", "recursos": "hr", "personas": "hr",
	"datos": "analyst", "analista": "analyst", "analisis": "analyst", "data": "analyst",
	"operaciones": "operations", "operations": "operations", "logistica": "operations",
	"asistente": "assistant", "assistant": "assistant",
}

// convID is the stored conversation of the context: "office" for the office and General, "agent:<id>" for a 1:1 chat.
func (c *cmdCall) convID(given string) string {
	given = strings.TrimSpace(given)
	if given != "" && application.IsChatConversation(given) && given != domain.ChatAgentPrefix {
		return given
	}
	if c.surface == commands.SurfaceAgent && c.agentID != "" {
		return domain.ChatAgentConv(c.agentID)
	}
	return domain.ChatOffice
}

func (c *cmdCall) str(name string) string {
	switch v := c.args[name].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprint(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func (c *cmdCall) list(name string) []string {
	switch v := c.args[name].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s := strings.TrimPrefix(strings.TrimSpace(fmt.Sprint(x)), "@"); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	}
	return nil
}

// validateArgs checks required arguments and enum values (agents and files are resolved by the executors).
func (c *cmdCall) validateArgs() *cmdFail {
	for _, a := range c.cmd.Args {
		raw, present := c.args[a.Name]
		empty := !present || c.str(a.Name) == "" && len(c.list(a.Name)) == 0
		if empty && a.Kind == commands.KindAgent && a.Name == "agent" && c.agentID != "" {
			empty = false // the 1:1 chat names the agent
			continue
		}
		if a.Required && empty {
			return fail(http.StatusBadRequest, "missing_arg",
				fmt.Sprintf("Falta el argumento «%s». Ejemplo: %s", a.Name, c.cmd.Example.ES),
				fmt.Sprintf("Missing argument “%s”. Example: %s", a.Name, c.cmd.Example.EN))
		}
		if a.Kind == commands.KindEnum && !empty {
			v, ok := commands.NormalizeEnum(a, fmt.Sprint(raw))
			if !ok {
				return fail(http.StatusBadRequest, "invalid_args",
					fmt.Sprintf("«%s» no es válido para %s. Opciones: %s", fmt.Sprint(raw), a.Name, strings.Join(a.Enum, ", ")),
					fmt.Sprintf("“%s” is not valid for %s. Options: %s", fmt.Sprint(raw), a.Name, strings.Join(a.Enum, ", ")))
			}
			c.args[a.Name] = v
		}
		if a.Kind == commands.KindAgent && !empty {
			for _, ref := range append(c.list(a.Name), c.strIf(a)) {
				if ref == "" {
					continue
				}
				if c.agentRef(ref) == nil && !(c.cmd.ID == "pause" || c.cmd.ID == "resume" || c.cmd.ID == "stop") { // those also take a project
					return fail(http.StatusBadRequest, "unknown_agent",
						fmt.Sprintf("No encuentro al agente «%s».", ref), fmt.Sprintf("I cannot find the agent “%s”.", ref))
				}
			}
		}
	}
	return nil
}

func (c *cmdCall) strIf(a commands.Arg) string {
	if _, ok := c.args[a.Name].(string); ok {
		return strings.TrimPrefix(c.str(a.Name), "@")
	}
	return ""
}

// need checks an extra permission (the "write" half of a command) and refuses with 403.
func (c *cmdCall) need(perm string) *cmdFail {
	if perm == "" || c.s.has(c.r, auth.Permission(perm)) {
		return nil
	}
	return fail(http.StatusForbidden, "forbidden", "Tu rol no puede hacer ese cambio.", "Your role cannot make that change.")
}

// targetAgent is the agent argument, else the 1:1 chat's agent, else the fallback id ("" = none).
func (c *cmdCall) targetAgent(fallback string) (*domain.Agent, *cmdFail) {
	ref := c.str("agent")
	if ref == "" {
		ref = c.agentID
	}
	if ref == "" {
		ref = fallback
	}
	if ref == "" {
		return nil, fail(http.StatusBadRequest, "missing_arg", "Indica el agente, por ejemplo @ventas.", "Name the agent, for example @sales.")
	}
	a := c.agentRef(ref)
	if a == nil {
		return nil, fail(http.StatusBadRequest, "unknown_agent", fmt.Sprintf("No encuentro al agente «%s».", ref), fmt.Sprintf("I cannot find the agent “%s”.", ref))
	}
	return a, nil
}

func msgResult(title, text string, data map[string]any) *cmdResult {
	return &cmdResult{Kind: "message", Title: title, Text: text, Data: data}
}

func tableResult(title string, cols []string, rows [][]string, text string, data map[string]any) *cmdResult {
	if rows == nil {
		rows = [][]string{}
	}
	return &cmdResult{Kind: "table", Title: title, Columns: cols, Rows: rows, Text: text, Data: data}
}

func navResult(href, title, text string) *cmdResult {
	return &cmdResult{Kind: "navigate", Href: href, Title: title, Text: text}
}

// serviceFail maps a service error to the command envelope.
func (c *cmdCall) serviceFail(err error) *cmdFail {
	var ex *application.Exceeded
	switch {
	case errors.As(err, &ex):
		return fail(http.StatusConflict, "budget_exceeded", "No hay presupuesto para esto: se alcanzó el tope.", "There is no budget for this: the cap was reached.")
	case errors.Is(err, domain.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "No se encontró lo que pides.", "I could not find what you asked for.")
	case errors.Is(err, domain.ErrInvalid):
		return fail(http.StatusBadRequest, "invalid_args", err.Error(), err.Error())
	case errors.Is(err, domain.ErrForbidden):
		return fail(http.StatusForbidden, "forbidden", "No tienes permiso para eso.", "You do not have permission for that.")
	case errors.Is(err, domain.ErrConflict):
		return fail(http.StatusConflict, "conflict", err.Error(), err.Error())
	}
	c.s.Log.Error("command failed", "command", c.cmd.ID, "err", err)
	return fail(http.StatusInternalServerError, "failed", "Algo salió mal al ejecutar el comando.", "Something went wrong running the command.")
}

func unavailable(what string) *cmdFail {
	return fail(http.StatusNotImplemented, "not_available", what+" no está disponible en este servidor.", what+" is not available on this server.")
}

func trunc(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
