package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/officedoc"
)

// Control commands: clear, compact, context, usage, model, memory, pause,
// resume, stop, status, trace, export.

func init() {
	cmdHandlers["clear"] = handler{run: cmdClear, available: func(s *server) bool { return s.Orch.HasConvContext() }}
	cmdHandlers["compact"] = handler{run: cmdCompact, available: func(s *server) bool { return s.Orch.HasConvContext() }}
	cmdHandlers["context"] = handler{run: cmdContext}
	cmdHandlers["usage"] = handler{run: cmdUsage}
	cmdHandlers["model"] = handler{run: cmdModel, available: func(s *server) bool { return s.ModelPolicy != nil }}
	cmdHandlers["memory"] = handler{run: cmdMemory, confirm: confirmMemory}
	cmdHandlers["pause"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdPauseResume(c, "pause") }, available: func(s *server) bool { return s.Controls != nil }}
	cmdHandlers["resume"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdPauseResume(c, "resume") }, available: func(s *server) bool { return s.Controls != nil }}
	cmdHandlers["stop"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdPauseResume(c, "stop") }, confirm: confirmStop, available: func(s *server) bool { return s.Controls != nil }}
	cmdHandlers["status"] = handler{run: cmdStatus}
	cmdHandlers["trace"] = handler{run: cmdTrace, available: func(s *server) bool { return s.V2 != nil }}
	cmdHandlers["export"] = handler{run: cmdExport, available: func(s *server) bool { return s.Drive != nil }}
}

// ---- clear ----

func cmdClear(c *cmdCall) (*cmdResult, *cmdFail) {
	cc := c.s.Orch.ConvContextOf(c.ctx, c.conv)
	cc.ConversationID = c.conv
	cc.Epoch++
	cc.ClearedAt = time.Now().UTC()
	cc.Summary, cc.SummaryUpto, cc.SummaryTokens = "", time.Time{}, 0
	if err := c.s.Orch.SaveConvContext(c.ctx, cc); err != nil {
		return nil, c.serviceFail(err)
	}
	notice := c.t("— Conversación nueva: el modelo ya no ve lo anterior (sigue en el historial) —", "— New conversation: the model no longer sees what came before (it stays in the history) —")
	if err := c.s.Orch.PostSystemNotice(c.ctx, c.conv, notice); err != nil {
		c.s.Log.Warn("clear notice", "err", err)
	}
	return msgResult(c.t("Conversación nueva", "New conversation"),
		c.t("Listo. Empezamos de cero; lo anterior sigue en el historial.", "Done. We start fresh; what came before stays in the history."),
		map[string]any{"conversation_id": c.conv, "epoch": cc.Epoch}), nil
}

// ---- compact ----

func spokenMessages(msgs []domain.Message) []domain.Message {
	out := msgs[:0:0]
	for _, m := range msgs {
		if m.From == "system" || (m.Kind != domain.MsgChat && m.Kind != domain.MsgAnswer) || strings.TrimSpace(m.Text) == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func cmdCompact(c *cmdCall) (*cmdResult, *cmdFail) {
	cc := c.s.Orch.ConvContextOf(c.ctx, c.conv)
	msgs, err := c.s.Orch.ChatMessagesSince(c.ctx, c.conv, cc.Cutoff(), 80)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	msgs = spokenMessages(msgs)
	if len(msgs) < 2 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Todavía no hay suficiente conversación para resumir.", "There is not enough conversation to summarize yet.")
	}
	items := make([]application.ChatHistoryItem, 0, len(msgs))
	before := application.ApproxTokens(cc.Summary)
	for _, m := range msgs {
		before += application.ApproxTokens(m.Text)
		items = append(items, application.ChatHistoryItem{From: m.From, Text: trunc(m.Text, 800)})
	}
	agent := c.agentID
	res, err := c.s.Orch.Summarize(c.ctx, agent, application.SummarizeRequest{Mode: "compact", Messages: items, PreviousSummary: cc.Summary, Locale: c.lang})
	if err != nil {
		return nil, c.serviceFail(err)
	}
	after := application.ApproxTokens(res.Text)
	cc.ConversationID, cc.Summary, cc.SummaryUpto, cc.SummaryTokens = c.conv, res.Text, msgs[len(msgs)-1].TS, after
	if err := c.s.Orch.SaveConvContext(c.ctx, cc); err != nil {
		return nil, c.serviceFail(err)
	}
	notice := fmt.Sprintf(c.t("— Conversación resumida: %d → %d tokens aprox. Desde ahora el modelo recibe el resumen —", "— Conversation compacted: %d → %d tokens approx. From now on the model receives the summary —"), before, after)
	if err := c.s.Orch.PostSystemNotice(c.ctx, c.conv, notice); err != nil {
		c.s.Log.Warn("compact notice", "err", err)
	}
	return msgResult(c.t("Conversación resumida", "Conversation compacted"), res.Text,
		map[string]any{"tokens_before": before, "tokens_after": after, "messages": len(msgs), "source": res.Source, "conversation_id": c.conv}), nil
}

// ---- context ----

// modelWindow is the context window (tokens) assumed for a provider or model name.
func modelWindow(name string) int {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "claude") || strings.Contains(n, "anthropic"):
		return 200000
	case strings.Contains(n, "deepseek"):
		return 128000
	case strings.Contains(n, "kimi") || strings.Contains(n, "moonshot"):
		return 128000
	}
	return 128000
}

func cmdContext(c *cmdCall) (*cmdResult, *cmdFail) {
	a, f := c.targetAgent("assistant")
	if f != nil {
		return nil, f
	}
	in, err := c.s.Orch.PreviewRunRequest(c.ctx, a.ID, "")
	if err != nil {
		return nil, c.serviceFail(err)
	}
	segs := []application.ContextSegment{}
	total := 0
	if sz, ok := c.s.Orch.ContextSizes(c.ctx, in); ok {
		segs, total = sz.Segments, sz.TotalTokens
	} else { // the runtime cannot say: approximate from the payload we would send
		segs = approxSegments(in)
		for _, s := range segs {
			total += s.Tokens
		}
	}
	cc := c.s.Orch.ConvContextOf(c.ctx, domain.ChatAgentConv(a.ID))
	hist := application.ApproxTokens(cc.Summary)
	if msgs, err := c.s.Orch.ChatMessagesSince(c.ctx, domain.ChatAgentConv(a.ID), cc.Cutoff(), 8); err == nil {
		for _, m := range spokenMessages(msgs) {
			hist += application.ApproxTokens(trunc(m.Text, 400))
		}
	}
	segs = append(segs, application.ContextSegment{Key: "conversation", Label: "Conversación", Tokens: hist})
	total += hist
	model := c.modelOf(*a)
	window := modelWindow(model)
	rows := make([][]string, 0, len(segs)+1)
	for _, s := range segs {
		rows = append(rows, []string{segLabel(c.lang, s), fmt.Sprint(s.Tokens), fmt.Sprintf("%.1f%%", pct(s.Tokens, window))})
	}
	fill := pct(total, window)
	rows = append(rows, []string{c.t("Total", "Total"), fmt.Sprint(total), fmt.Sprintf("%.1f%%", fill)})
	return tableResult(fmt.Sprintf(c.t("Contexto de %s", "Context of %s"), a.Name),
		[]string{c.t("Parte", "Part"), c.t("Tokens aprox.", "Approx. tokens"), c.t("% de la ventana", "% of window")}, rows,
		fmt.Sprintf(c.t("Ventana asumida: %d tokens (%s).", "Assumed window: %d tokens (%s)."), window, model),
		map[string]any{"agent_id": a.ID, "total_tokens": total, "window_tokens": window, "fill_pct": fill, "model": model}), nil
}

func pct(n, d int) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) * 100 / float64(d)
}

var segLabels = map[string]commands.L{
	"profile":      {ES: "Perfil y reglas del agente", EN: "Agent profile and rules"},
	"skills":       {ES: "Skills", EN: "Skills"},
	"memory":       {ES: "Memoria", EN: "Memory"},
	"files":        {ES: "Archivos y carpetas", EN: "Files and folders"},
	"dependencies": {ES: "Dependencias", EN: "Dependencies"},
	"project":      {ES: "Contexto del proyecto", EN: "Project context"},
	"task":         {ES: "Tarea y reglas", EN: "Task and rules"},
	"conversation": {ES: "Conversación (resumen + recientes)", EN: "Conversation (summary + recent)"},
}

func segLabel(lang string, s application.ContextSegment) string {
	if l, ok := segLabels[s.Key]; ok {
		return l.Pick(lang)
	}
	return s.Label
}

func approxSegments(in application.RunTaskRequest) []application.ContextSegment {
	size := func(v any) int { return application.ApproxTokens(fmt.Sprint(v)) }
	var mem strings.Builder
	for _, m := range in.Context.Memory {
		mem.WriteString(m.Key + " " + m.Value + "\n")
	}
	profile := in.Agent.Persona + strings.Join(in.Agent.Responsibilities, " ") + in.Agent.Instructions
	return []application.ContextSegment{
		{Key: "profile", Tokens: application.ApproxTokens(profile) + 250},
		{Key: "skills", Tokens: size(in.Skills)},
		{Key: "memory", Tokens: application.ApproxTokens(mem.String())},
		{Key: "files", Tokens: size(in.Workspace)},
		{Key: "dependencies", Tokens: size(in.Context.DependencyOutputs)},
		{Key: "project", Tokens: size(in.Context.ProjectContext)},
		{Key: "task", Tokens: 400},
	}
}

// modelOf is the model/provider that would answer this agent (role override, else the organization's order).
func (c *cmdCall) modelOf(a domain.Agent) string {
	if c.s.ModelPolicy == nil {
		return "default"
	}
	v, err := c.s.ModelPolicy.Get(c.ctx)
	if err != nil {
		return "default"
	}
	if m := v.Policy.RoleModels[a.Role]; m != "" {
		return m
	}
	if p := v.Policy.RoleProviders[a.Role]; len(p) > 0 {
		return p[0]
	}
	if len(v.Policy.PreferredProviders) > 0 {
		return v.Policy.PreferredProviders[0]
	}
	if v.DefaultProvider != "" {
		return v.DefaultProvider
	}
	return "default"
}

// ---- usage ----

func cmdUsage(c *cmdCall) (*cmdResult, *cmdFail) {
	usage, err := c.s.Store.ListUsage(c.ctx, c.org)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := application.PeriodStart(now)
	var today, thisMonth, total, conv float64
	byAgent := map[string]float64{}
	for _, u := range usage {
		total += u.CostUSD
		if !u.TS.Before(day) {
			today += u.CostUSD
		}
		if !u.TS.Before(month) {
			thisMonth += u.CostUSD
			byAgent[u.AgentID] += u.CostUSD
		}
		if u.Kind == domain.UsageChat && (c.conv == domain.ChatOffice || u.AgentID == c.agentID) {
			conv += u.CostUSD
		}
	}
	usd := func(v float64) string { return fmt.Sprintf("$%.4f", v) }
	rows := [][]string{
		{c.t("Hoy", "Today"), usd(today), ""},
		{c.t("Este mes", "This month"), usd(thisMonth), ""},
		{c.t("Esta conversación (chat)", "This conversation (chat)"), usd(conv), ""},
		{c.t("Total acumulado", "All time"), usd(total), ""},
	}
	data := map[string]any{"today_usd": today, "month_usd": thisMonth, "conversation_usd": conv, "total_usd": total}
	if st, err := c.s.Orch.Budget().Status(c.ctx); err == nil && st.Org.BudgetUSD > 0 {
		left := st.Org.BudgetUSD - st.Org.UsedUSD - st.Org.ReservedUSD
		rows = append(rows, []string{c.t("Presupuesto restante", "Budget left"), usd(left), fmt.Sprintf(c.t("de $%.2f (%.0f%% usado)", "of $%.2f (%.0f%% used)"), st.Org.BudgetUSD, st.Org.UsedUSD*100/st.Org.BudgetUSD)})
		data["budget_usd"], data["budget_left_usd"] = st.Org.BudgetUSD, left
	}
	ids := make([]string, 0, len(byAgent))
	for id := range byAgent {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if byAgent[a] == byAgent[b] {
			return strings.Compare(a, b)
		}
		if byAgent[a] > byAgent[b] {
			return -1
		}
		return 1
	})
	for _, id := range ids {
		name := id
		if a := c.agentRef(id); a != nil {
			name = a.Name
		}
		rows = append(rows, []string{c.t("Agente: ", "Agent: ") + name, usd(byAgent[id]), c.t("este mes", "this month")})
	}
	if c.s.Projects != nil {
		if ps, err := c.s.Projects.List(c.ctx); err == nil {
			for _, p := range ps {
				if p.SpentUSD > 0 || p.BudgetUSD > 0 {
					rows = append(rows, []string{c.t("Proyecto: ", "Project: ") + p.Name, usd(p.SpentUSD), fmt.Sprintf(c.t("de $%.2f", "of $%.2f"), p.BudgetUSD)})
				}
			}
		}
	}
	if c.s.Rewards != nil {
		if st, err := c.s.Rewards.State(c.ctx, c.org); err == nil {
			rows = append(rows, []string{c.t("Monedas ganadas", "Coins earned"), fmt.Sprint(st.Wallet.EarnedTotal), fmt.Sprintf(c.t("saldo %d", "balance %d"), st.Wallet.Balance)})
			data["coins_earned"], data["coins_balance"] = st.Wallet.EarnedTotal, st.Wallet.Balance
		}
	}
	return tableResult(c.t("Uso y costo", "Usage and cost"), []string{c.t("Concepto", "Item"), "USD", c.t("Detalle", "Detail")}, rows, "", data), nil
}

// ---- model ----

var providerWords = map[string]string{
	"deepseek": "deepseek", "anthropic": "anthropic", "claude": "anthropic",
	"custom": "custom", "kimi": "custom", "moonshot": "custom", "openai": "custom", "gpt": "custom",
}

func cmdModel(c *cmdCall) (*cmdResult, *cmdFail) {
	view, err := c.s.ModelPolicy.Get(c.ctx)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	val := c.str("value")
	if val == "" {
		rows := [][]string{
			{c.t("Proveedor por defecto", "Default provider"), view.DefaultProvider},
			{c.t("Configurados", "Configured"), strings.Join(view.ConfiguredProviders, ", ")},
			{c.t("Orden preferido", "Preferred order"), strings.Join(view.Policy.PreferredProviders, ", ")},
			{c.t("Permitidos", "Allowed"), strings.Join(view.Policy.AllowedProviders, ", ")},
		}
		for role, p := range view.Policy.RoleProviders {
			rows = append(rows, []string{c.t("Rol ", "Role ") + role, strings.Join(p, ", ")})
		}
		for role, m := range view.Policy.RoleModels {
			rows = append(rows, []string{c.t("Modelo del rol ", "Role model ") + role, m})
		}
		return tableResult(c.t("Modelo", "Model"), []string{c.t("Ajuste", "Setting"), c.t("Valor", "Value")}, rows,
			c.t("Cambia con /modelo <proveedor> [@agente].", "Change with /model <provider> [@agent]."),
			map[string]any{"default_provider": view.DefaultProvider, "preferred": view.Policy.PreferredProviders}), nil
	}
	if f := c.need("org:manage"); f != nil {
		return nil, f
	}
	prov, ok := providerWords[commands.Fold(val)]
	if !ok && c.s.cmds.Models != nil {
		prov, ok = c.s.cmds.Models.Resolve(c.ctx, c.org, val)
	}
	if !ok {
		if c.s.cmds.Models == nil {
			return nil, fail(http.StatusNotImplemented, "not_available",
				fmt.Sprintf("«%s» no es un proveedor conocido (deepseek, anthropic, custom). Los modelos propios aún no están disponibles.", val),
				fmt.Sprintf("“%s” is not a known provider (deepseek, anthropic, custom). Bring-your-own models are not available yet.", val))
		}
		return nil, fail(http.StatusBadRequest, "invalid_args", fmt.Sprintf("No conozco el modelo «%s».", val), fmt.Sprintf("I do not know the model “%s”.", val))
	}
	pol := view.Policy
	target := c.t("la organización", "the organization")
	if ref := c.str("agent"); ref != "" || c.agentID != "" {
		a, f := c.targetAgent("")
		if f != nil {
			return nil, f
		}
		if pol.RoleProviders == nil {
			pol.RoleProviders = map[string][]string{}
		}
		pol.RoleProviders[a.Role] = []string{prov}
		target = fmt.Sprintf(c.t("el rol de %s", "the role of %s"), a.Name)
	} else {
		rest := []string{prov}
		for _, p := range pol.PreferredProviders {
			if p != prov {
				rest = append(rest, p)
			}
		}
		pol.PreferredProviders = rest
	}
	if _, err := c.s.ModelPolicy.Put(c.ctx, pol); err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(c.t("Modelo cambiado", "Model changed"),
		fmt.Sprintf(c.t("Ahora %s usa primero «%s».", "Now %s uses “%s” first."), target, prov), map[string]any{"provider": prov}), nil
}

// ---- memory ----

type memEntry struct{ scope, key, value string }

func (c *cmdCall) memories(agentID string) ([]memEntry, error) {
	if lm := c.s.cmds.LongMemory; lm != nil {
		items, err := lm.List(c.ctx, c.org, agentID)
		out := make([]memEntry, 0, len(items))
		for _, i := range items {
			out = append(out, memEntry{i.Scope, i.Key, i.Value})
		}
		return out, err
	}
	ms, err := c.s.Store.ListMemory(c.ctx, c.org, agentID)
	out := make([]memEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, memEntry{m.Scope, m.Key, m.Value})
	}
	return out, err
}

func matchMem(list []memEntry, action, text string) []memEntry {
	if action == "delete" {
		return list
	}
	f := commands.Fold(text)
	var out []memEntry
	for _, m := range list {
		if strings.Contains(commands.Fold(m.key), f) || strings.Contains(commands.Fold(m.value), f) {
			out = append(out, m)
		}
	}
	return out
}

func confirmMemory(c *cmdCall) (commands.L, *cmdFail) {
	a, f := c.targetAgent("assistant")
	if f != nil {
		return commands.L{}, f
	}
	if f := c.need("memories:write"); f != nil {
		return commands.L{}, f
	}
	list, err := c.memories(a.ID)
	if err != nil {
		return commands.L{}, c.serviceFail(err)
	}
	n := len(matchMem(list, c.str("action"), c.str("text")))
	return commands.L{
		ES: fmt.Sprintf("Se borrarán %d recuerdos de %s. No se puede deshacer. ¿Confirmas?", n, a.Name),
		EN: fmt.Sprintf("%d memories of %s will be deleted. This cannot be undone. Do you confirm?", n, a.Name)}, nil
}

func cmdMemory(c *cmdCall) (*cmdResult, *cmdFail) {
	a, f := c.targetAgent("assistant")
	if f != nil {
		return nil, f
	}
	action := c.str("action")
	if action == "" {
		action = "view"
	}
	list, err := c.memories(a.ID)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	if action == "view" {
		rows := [][]string{}
		for _, m := range list {
			rows = append(rows, []string{m.scope, m.key, trunc(m.value, 160)})
		}
		return tableResult(fmt.Sprintf(c.t("Memoria de %s", "Memory of %s"), a.Name), []string{c.t("Ámbito", "Scope"), c.t("Clave", "Key"), c.t("Valor", "Value")}, rows,
			fmt.Sprintf(c.t("%d recuerdos.", "%d memories."), len(rows)), map[string]any{"agent_id": a.ID, "count": len(rows)}), nil
	}
	if f := c.need("memories:write"); f != nil {
		return nil, f
	}
	if action == "forget" && c.str("text") == "" {
		return nil, fail(http.StatusBadRequest, "missing_arg", "¿Qué debo olvidar? Ejemplo: /memoria olvidar precio", "What should I forget? Example: /memory forget price")
	}
	hits := matchMem(list, action, c.str("text"))
	if len(hits) == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "No hay recuerdos que coincidan.", "No memories match.")
	}
	n := len(hits)
	if lm := c.s.cmds.LongMemory; lm != nil && action == "forget" {
		var err error
		if n, err = lm.Forget(c.ctx, c.org, a.ID, c.str("text")); err != nil {
			return nil, c.serviceFail(err)
		}
	} else {
		for _, m := range hits {
			if err := c.s.Orch.DeleteMemory(c.ctx, a.ID, m.scope, m.key); err != nil {
				return nil, c.serviceFail(err)
			}
		}
	}
	return msgResult(c.t("Memoria actualizada", "Memory updated"),
		fmt.Sprintf(c.t("%s olvidó %d recuerdos.", "%s forgot %d memories."), a.Name, n), map[string]any{"agent_id": a.ID, "deleted": n}), nil
}

// ---- pause / resume / stop ----

// resolveTarget finds the agent or the project a control command acts on.
func (c *cmdCall) resolveTarget() (agent *domain.Agent, projectID, projectName string, f *cmdFail) {
	ref := strings.TrimPrefix(c.str("agent"), "@")
	if ref == "" && c.agentID != "" {
		ref = c.agentID
	}
	if ref == "" && c.project != "" {
		ref = c.project
	}
	if ref == "" {
		return nil, "", "", fail(http.StatusBadRequest, "missing_arg", "Indica el agente o el proyecto, por ejemplo @ventas.", "Name the agent or the project, for example @sales.")
	}
	if a := c.agentRef(ref); a != nil {
		return a, "", "", nil
	}
	if c.s.Projects != nil {
		if ps, err := c.s.Projects.List(c.ctx); err == nil {
			for _, p := range ps {
				if p.ID == ref || commands.Fold(p.Name) == commands.Fold(ref) {
					return nil, p.ID, p.Name, nil
				}
			}
		}
	}
	return nil, "", "", fail(http.StatusBadRequest, "unknown_agent", fmt.Sprintf("No encuentro un agente ni un proyecto «%s».", ref), fmt.Sprintf("I cannot find an agent or project “%s”.", ref))
}

func confirmStop(c *cmdCall) (commands.L, *cmdFail) {
	a, pid, pname, f := c.resolveTarget()
	if f != nil {
		return commands.L{}, f
	}
	if a != nil {
		return commands.L{ES: fmt.Sprintf("Se detendrá a %s de inmediato y su trabajo en curso se corta. ¿Confirmas?", a.Name),
			EN: fmt.Sprintf("%s will be stopped immediately and its work in progress is cut. Do you confirm?", a.Name)}, nil
	}
	_ = pid
	return commands.L{ES: fmt.Sprintf("Se cancelará el proyecto «%s». ¿Confirmas?", pname), EN: fmt.Sprintf("The project “%s” will be cancelled. Do you confirm?", pname)}, nil
}

func cmdPauseResume(c *cmdCall, what string) (*cmdResult, *cmdFail) {
	a, pid, pname, f := c.resolveTarget()
	if f != nil {
		return nil, f
	}
	verbs := map[string]commands.L{
		"pause": {ES: "pausado", EN: "paused"}, "resume": {ES: "reanudado", EN: "resumed"}, "stop": {ES: "detenido", EN: "stopped"},
	}
	if a != nil {
		var err error
		switch what {
		case "pause":
			_, err = c.s.Controls.PauseAgent(c.ctx, c.org, a.ID, "graceful", "slash command", c.actor)
		case "stop":
			_, err = c.s.Controls.PauseAgent(c.ctx, c.org, a.ID, "immediate", "slash command /stop", c.actor)
		default:
			_, err = c.s.Controls.ResumeAgent(c.ctx, c.org, a.ID, c.actor)
		}
		if err != nil {
			return nil, c.serviceFail(err)
		}
		return msgResult(a.Name, fmt.Sprintf(c.t("%s quedó %s.", "%s was %s."), a.Name, verbs[what].Pick(c.lang)), map[string]any{"agent_id": a.ID}), nil
	}
	if c.s.Projects == nil {
		return nil, unavailable(c.t("Los proyectos", "Projects"))
	}
	if f := c.need("tasks:manage"); f != nil {
		return nil, f
	}
	action := map[string]string{"pause": "pause", "resume": "resume", "stop": "cancel"}[what]
	if _, err := c.s.Projects.Control(c.ctx, pid, action); err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(pname, fmt.Sprintf(c.t("El proyecto «%s» quedó %s.", "The project “%s” was %s."), pname, verbs[what].Pick(c.lang)), map[string]any{"project_id": pid}), nil
}

// ---- status ----

func cmdStatus(c *cmdCall) (*cmdResult, *cmdFail) {
	agents := c.loadAgents()
	tasks, err := c.s.Store.ListTasks(c.ctx, c.org, "", "")
	if err != nil {
		return nil, c.serviceFail(err)
	}
	running, waiting := map[string]int{}, 0
	for _, t := range tasks {
		switch t.Status {
		case domain.TaskRunning:
			running[t.AgentID]++
		case domain.TaskAwaitingApproval:
			waiting++
		}
	}
	appr, _ := c.s.Store.ListApprovals(c.ctx, c.org, string(domain.ApprovalPending))
	rows := [][]string{}
	nRun := 0
	for _, a := range agents {
		nRun += running[a.ID]
		rows = append(rows, []string{a.Name, string(a.State), trunc(a.Activity, 80), fmt.Sprint(running[a.ID])})
	}
	text := fmt.Sprintf(c.t("%d tareas en curso, %d aprobaciones pendientes.", "%d tasks running, %d approvals pending."), nRun, len(appr))
	return tableResult(c.t("Estado del equipo", "Team status"), []string{c.t("Agente", "Agent"), c.t("Estado", "State"), c.t("Haciendo", "Doing"), c.t("Tareas", "Tasks")}, rows, text,
		map[string]any{"running_tasks": nRun, "pending_approvals": len(appr), "tasks_awaiting_approval": waiting}), nil
}

// ---- trace ----

func cmdTrace(c *cmdCall) (*cmdResult, *cmdFail) {
	agentID := c.str("agent")
	if a := c.agentRef(agentID); a != nil {
		agentID = a.ID
	} else {
		agentID = c.agentID
	}
	if agentID == "" { // no agent named: the agent of the latest task
		tasks, err := c.s.Store.ListTasks(c.ctx, c.org, "", "")
		if err != nil {
			return nil, c.serviceFail(err)
		}
		var latest time.Time
		for _, t := range tasks {
			if t.CreatedAt.After(latest) {
				latest, agentID = t.CreatedAt, t.AgentID
			}
		}
	}
	if agentID == "" {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Todavía no hay tareas con traza.", "There are no tasks with a trace yet.")
	}
	trs, err := c.s.V2.ListTraces(c.ctx, c.org, agentID, 1, time.Time{})
	if err != nil {
		return nil, c.serviceFail(err)
	}
	if len(trs) == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Ese agente todavía no tiene trazas.", "That agent has no traces yet.")
	}
	return navResult(fmt.Sprintf("/office?agent=%s&trace=%s", agentID, trs[0].ID), c.t("Última traza", "Last trace"),
		fmt.Sprintf(c.t("Abriendo la traza de la tarea %s.", "Opening the trace of task %s."), trs[0].TaskID)), nil
}

// ---- export ----

func cmdExport(c *cmdCall) (*cmdResult, *cmdFail) {
	format := c.str("format")
	if format == "" {
		format = "md"
	}
	if !c.s.has(c.r, "files:write") {
		return nil, fail(http.StatusForbidden, "forbidden", "Exportar guarda un archivo en el drive: tu rol no puede escribir archivos.", "Exporting saves a file in the drive: your role cannot write files.")
	}
	msgs, err := c.s.Orch.ChatMessagesSince(c.ctx, c.conv, time.Time{}, 500)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	if len(msgs) == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "No hay nada que exportar todavía.", "There is nothing to export yet.")
	}
	names := map[string]string{"user": c.t("Tú", "You"), "system": c.t("Sistema", "System")}
	for _, a := range c.loadAgents() {
		names[a.ID] = a.Name
	}
	title := c.t("Conversación: ", "Conversation: ") + c.conv
	if a := c.agentRef(strings.TrimPrefix(c.conv, domain.ChatAgentPrefix)); a != nil && c.conv != domain.ChatOffice {
		title = c.t("Conversación con ", "Conversation with ") + a.Name
	} else if c.conv == domain.ChatOffice {
		title = c.t("Conversación de la oficina", "Office conversation")
	}
	doc := officedoc.Doc{Title: title, Blocks: []officedoc.Block{{Type: "paragraph", Text: c.t("Exportada el ", "Exported on ") + time.Now().UTC().Format("2006-01-02 15:04 UTC")}}}
	for _, m := range msgs {
		who := names[m.From]
		if who == "" {
			who = m.From
		}
		doc.Blocks = append(doc.Blocks, officedoc.Block{Type: "paragraph", Text: fmt.Sprintf("[%s] %s: %s", m.TS.UTC().Format("2006-01-02 15:04"), who, m.Text)})
	}
	pd, err := officedoc.Prepare(doc)
	if err != nil {
		return nil, c.serviceFail(fmt.Errorf("%w: %v", domain.ErrInvalid, err))
	}
	data, err := officedoc.Build(format, pd)
	if err != nil {
		return nil, c.serviceFail(fmt.Errorf("%w: %v", domain.ErrInvalid, err))
	}
	slug := strings.NewReplacer(":", "-", "/", "-", " ", "-").Replace(c.conv)
	path := fmt.Sprintf("/Shared/Exports/chat-%s-%s.%s", slug, time.Now().UTC().Format("20060102-150405"), format)
	res, err := c.s.Drive.Write(c.ctx, drive.Human(c.actor), drive.WriteInput{Path: path, Data: data, Message: "slash command /export", Source: "command"})
	if err != nil {
		return nil, c.driveErr(err)
	}
	return &cmdResult{Kind: "download", Title: title, Text: c.t("Guardado en el drive: ", "Saved in the drive: ") + res.File.Path,
		URL: "/api/v1/drive/files/" + res.File.ID + "/content", Filename: res.File.Name,
		Data: map[string]any{"path": res.File.Path, "file_id": res.File.ID, "messages": len(msgs), "format": format}}, nil
}

func (c *cmdCall) driveErr(err error) *cmdFail {
	if de, ok := err.(*drive.Error); ok {
		switch de.Code {
		case "read_only":
			return fail(http.StatusConflict, "read_only", "La organización está en modo solo lectura.", "The organization is in read-only mode.")
		}
	}
	return c.serviceFail(err)
}
