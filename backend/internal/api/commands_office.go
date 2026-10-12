package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/skills"
)

// Office commands: the deliverables, meeting, assign, project, daily-summary,
// pending, search, file, email, linkedin, remember, tone, language,
// save-template, skill, learn and undo.

func init() {
	for _, id := range []string{"excel", "word", "pdf", "presentation"} {
		id := id
		cmdHandlers[id] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdDeliverable(c, id) }}
	}
	cmdHandlers["meeting"] = handler{run: cmdMeeting}
	cmdHandlers["assign"] = handler{run: cmdAssign}
	cmdHandlers["project"] = handler{run: cmdProject}
	cmdHandlers["daily-summary"] = handler{run: cmdDaily}
	cmdHandlers["pending"] = handler{run: cmdPending}
	cmdHandlers["search"] = handler{run: cmdSearch}
	cmdHandlers["file"] = handler{run: cmdFile, available: func(s *server) bool { return s.Drive != nil }}
	cmdHandlers["email"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdDraft(c, "email") }}
	cmdHandlers["linkedin"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) { return cmdDraft(c, "linkedin") }}
	cmdHandlers["remember"] = handler{run: cmdRemember}
	cmdHandlers["tone"] = handler{run: cmdTone}
	cmdHandlers["language"] = handler{run: cmdLanguage, available: func(s *server) bool { return s.OrgConfig != nil }}
	cmdHandlers["save-template"] = handler{run: cmdSaveTemplate, available: func(s *server) bool { return s.Projects != nil }}
	cmdHandlers["skill"] = handler{run: cmdSkill, available: func(s *server) bool { return s.Skills != nil }}
	cmdHandlers["learn"] = handler{run: cmdLearn, available: func(s *server) bool { return s.Skills != nil }}
	cmdHandlers["undo"] = handler{run: cmdUndo, confirm: confirmUndo, available: func(s *server) bool { return s.Drive != nil && s.Audit != nil }}
}

// requestResult submits request text through the normal path (planner, budget, approvals, Tool Gateway).
func (c *cmdCall) submit(text string) (*cmdResult, *cmdFail) {
	id, err := c.s.Orch.Submit(c.ctx, text)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return &cmdResult{Kind: "request", RequestID: id, Title: c.t("Solicitud creada", "Request created"),
		Text: c.t("Lo puse en marcha; verás el avance en la oficina.", "It is under way; you will see the progress in the office.")}, nil
}

func (c *cmdCall) marks(extra ...string) string {
	return strings.TrimSpace("[[lang:" + c.lang + "]] " + strings.Join(extra, " "))
}

// ---- deliverables ----

func cmdDeliverable(c *cmdCall, id string) (*cmdResult, *cmdFail) {
	text := c.str("text")
	format := map[string]string{"excel": "xlsx", "word": "docx", "pdf": "pdf", "presentation": "pdf"}[id]
	body := text
	if id == "presentation" {
		body = c.t("Presentación (una sección por diapositiva, con título breve y 3 a 5 viñetas): ", "Presentation (one section per slide, with a short title and 3 to 5 bullets): ") + text
	}
	if a := c.agentRef(c.agentID); a != nil && c.surface == commands.SurfaceAgent {
		body = fmt.Sprintf(c.t("Para %s: ", "For %s: "), a.Name) + body
	}
	return c.submit(c.marks("[[format:"+format+"]]") + " " + body)
}

// ---- meeting ----

func cmdMeeting(c *cmdCall) (*cmdResult, *cmdFail) {
	var ids, names []string
	for _, ref := range c.list("agents") {
		a := c.agentRef(ref)
		if a == nil {
			return nil, fail(http.StatusBadRequest, "unknown_agent", fmt.Sprintf("No encuentro al agente «%s».", ref), fmt.Sprintf("I cannot find the agent “%s”.", ref))
		}
		if !slices.Contains(ids, a.ID) {
			ids, names = append(ids, a.ID), append(names, a.Name)
		}
	}
	topic := c.str("topic")
	if len(ids) == 0 {
		// Nobody named: the team meets and the planner calls in the areas the topic needs.
		return c.submit(fmt.Sprintf(c.t("Reunión del equipo sobre: %s. Convoca a las áreas que el tema necesita; cada una da su postura con riesgos y propuestas, y la asistente redacta la conclusión (acuerdos, desacuerdos y próximos pasos).",
			"Team meeting about: %s. Call in the areas the topic needs; each gives its position with risks and proposals, and the assistant writes the conclusion (agreements, disagreements and next steps)."), topic))
	}
	body := fmt.Sprintf(c.t("Reunión entre %s sobre: %s. Cada uno da la postura de su área con riesgos y propuestas; la asistente redacta la conclusión (acuerdos, desacuerdos y próximos pasos).",
		"Meeting between %s about: %s. Each one gives their area's position with risks and proposals; the assistant writes the conclusion (agreements, disagreements and next steps)."),
		strings.Join(names, ", "), topic)
	return c.submit(c.marks("[[meeting:"+strings.Join(ids, ",")+"]]") + " " + body)
}

// ---- assign ----

func cmdAssign(c *cmdCall) (*cmdResult, *cmdFail) {
	a, f := c.targetAgent("")
	if f != nil {
		return nil, f
	}
	task := c.str("task")
	title := trunc(task, 80)
	desc := task + "\n\n" + c.t("Responde en español.", "Answer in English.")
	plan := application.PlanResponse{
		Objectives: []string{title},
		Tasks:      []application.PlannedTask{{Key: "t1", Title: title, Description: desc, AgentID: a.ID, Reason: c.t("Encargo directo de una persona", "Direct assignment from a person")}},
	}
	text := fmt.Sprintf(c.t("Encargo para %s: %s", "Assignment for %s: %s"), a.Name, task)
	id, err := c.s.Orch.SubmitPlan(c.ctx, text, plan)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return &cmdResult{Kind: "request", RequestID: id, Title: c.t("Tarea encargada", "Task assigned"),
		Text: fmt.Sprintf(c.t("%s ya tiene el encargo.", "%s has the assignment."), a.Name), Data: map[string]any{"agent_id": a.ID}}, nil
}

// ---- project ----

func cmdProject(c *cmdCall) (*cmdResult, *cmdFail) {
	href := "/projects/new"
	if ref := c.str("template"); ref != "" {
		id := ref
		if c.s.Projects != nil {
			if ts, err := c.s.Projects.Templates(c.ctx); err == nil {
				id = ""
				for _, t := range ts {
					if t.ID == ref || t.Key == ref || commands.Fold(t.Name) == commands.Fold(ref) {
						id = t.ID
						break
					}
				}
			}
		}
		if id == "" {
			return nil, fail(http.StatusBadRequest, "not_found", fmt.Sprintf("No encuentro la plantilla «%s».", ref), fmt.Sprintf("I cannot find the template “%s”.", ref))
		}
		href += "?template=" + url.QueryEscape(id)
	}
	return navResult(href, c.t("Nuevo proyecto", "New project"), ""), nil
}

// ---- daily summary ----

func cmdDaily(c *cmdCall) (*cmdResult, *cmdFail) {
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var facts []application.ChatHistoryItem
	tasks, _ := c.s.Store.ListTasks(c.ctx, c.org, "", "")
	done := 0
	for _, t := range tasks {
		if t.Status == domain.TaskDone && t.FinishedAt != nil && !t.FinishedAt.Before(day) {
			done++
			sum := ""
			if t.Output != nil {
				sum = t.Output.Summary
			}
			facts = append(facts, application.ChatHistoryItem{From: t.AgentID, Text: "Tarea terminada: " + t.Title + ". " + trunc(sum, 300)})
		}
	}
	reports, _ := c.s.Store.ListReports(c.ctx, c.org)
	for _, r := range reports {
		if !r.CreatedAt.Before(day) {
			facts = append(facts, application.ChatHistoryItem{From: "assistant", Text: "Informe: " + r.Title + ". " + trunc(r.Summary, 300)})
		}
	}
	appr, _ := c.s.Store.ListApprovals(c.ctx, c.org, string(domain.ApprovalPending))
	for _, a := range appr {
		facts = append(facts, application.ChatHistoryItem{From: a.AgentID, Text: "Aprobación pendiente: " + a.Title})
	}
	if usage, err := c.s.Store.ListUsage(c.ctx, c.org); err == nil {
		var spend float64
		for _, u := range usage {
			if !u.TS.Before(day) {
				spend += u.CostUSD
			}
		}
		facts = append(facts, application.ChatHistoryItem{From: "system", Text: fmt.Sprintf("Gasto de hoy: $%.4f", spend)})
	}
	if done == 0 && len(reports) == 0 && len(appr) == 0 {
		return msgResult(c.t("Resumen del día", "Summary of the day"), c.t("Hoy todavía no hubo actividad.", "There has been no activity today yet."), map[string]any{"tasks_done": 0}), nil
	}
	res, err := c.s.Orch.Summarize(c.ctx, c.agentID, application.SummarizeRequest{Mode: "daily", Messages: facts, Locale: c.lang})
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(c.t("Resumen del día", "Summary of the day"), res.Text, map[string]any{"tasks_done": done, "pending_approvals": len(appr), "source": res.Source}), nil
}

// ---- pending ----

func cmdPending(c *cmdCall) (*cmdResult, *cmdFail) {
	rows := [][]string{}
	appr, err := c.s.Store.ListApprovals(c.ctx, c.org, string(domain.ApprovalPending))
	if err != nil {
		return nil, c.serviceFail(err)
	}
	for _, a := range appr {
		rows = append(rows, []string{c.t("Aprobación", "Approval"), trunc(a.Title, 90), a.AgentID, "/approvals/" + a.ID})
	}
	tasks, _ := c.s.Store.ListTasks(c.ctx, c.org, "", "")
	open := 0
	for _, t := range tasks {
		if t.Status == domain.TaskRunning || t.Status == domain.TaskPending || t.Status == domain.TaskBlocked {
			open++
			rows = append(rows, []string{c.t("Tarea", "Task"), trunc(t.Title, 90), t.AgentID, string(t.Status)})
		}
	}
	return tableResult(c.t("Pendientes", "Pending"), []string{c.t("Tipo", "Type"), c.t("Qué", "What"), c.t("Quién", "Who"), c.t("Detalle", "Detail")}, rows,
		fmt.Sprintf(c.t("%d aprobaciones por decidir y %d tareas abiertas.", "%d approvals to decide and %d open tasks."), len(appr), open),
		map[string]any{"approvals": len(appr), "open_tasks": open}), nil
}

// ---- search ----

func cmdSearch(c *cmdCall) (*cmdResult, *cmdFail) {
	q := c.str("query")
	rows := [][]string{}
	hits := []map[string]string{}
	add := func(source, title, detail, href string) {
		rows = append(rows, []string{source, trunc(title, 100), trunc(detail, 120)})
		hits = append(hits, map[string]string{"source": source, "title": title, "href": href})
	}
	if c.s.Drive != nil && c.s.has(c.r, "files:read") {
		if hs, err := c.s.Drive.Retrieve(c.ctx, drive.RetrieveQuery{Query: q, Limit: 5}); err == nil {
			for _, h := range hs {
				add(c.t("Archivo", "File"), h.Name, h.Path, "/files?open="+h.FileID)
			}
		}
	}
	if c.s.Chats != nil {
		if cs, _, err := c.s.Chats.Conversations(c.ctx, application.ChatConvQuery{Q: q, Limit: 5}); err == nil {
			for _, cv := range cs {
				href := "/chats"
				if cv.AgentID != "" {
					href = "/chats/" + cv.AgentID
				}
				add(c.t("Chat", "Chat"), cv.Title, cv.Preview, href)
			}
		}
	}
	f := commands.Fold(q)
	if reports, err := c.s.Store.ListReports(c.ctx, c.org); err == nil && c.s.has(c.r, "reports:read") {
		n := 0
		for _, r := range reports {
			if n < 5 && (strings.Contains(commands.Fold(r.Title), f) || strings.Contains(commands.Fold(r.Summary), f)) {
				add(c.t("Informe", "Report"), r.Title, r.Summary, "/office")
				n++
			}
		}
	}
	if c.s.has(c.r, "memories:read") {
		for _, a := range c.loadAgents() {
			ms, _ := c.memories(a.ID)
			for _, m := range matchMem(ms, "forget", q) {
				add(c.t("Memoria", "Memory"), a.Name+" · "+m.key, m.value, "/office?agent="+a.ID)
			}
		}
	}
	if len(rows) == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", fmt.Sprintf("No encontré nada para «%s».", q), fmt.Sprintf("I found nothing for “%s”.", q))
	}
	return tableResult(c.t("Resultados", "Results"), []string{c.t("Fuente", "Source"), c.t("Resultado", "Result"), c.t("Detalle", "Detail")}, rows,
		fmt.Sprintf(c.t("%d resultados para «%s».", "%d results for “%s”."), len(rows), q), map[string]any{"query": q, "hits": hits}), nil
}

// ---- file / open ----

func cmdFile(c *cmdCall) (*cmdResult, *cmdFail) {
	ref := c.str("file")
	if strings.HasPrefix(ref, "/") {
		if f, err := c.s.Drive.FileByPath(c.ctx, ref); err == nil {
			return navResult("/files?open="+f.ID, f.Name, f.Path), nil
		}
		if l, err := c.s.Drive.List(c.ctx, ref); err == nil {
			return navResult("/files?path="+url.QueryEscape(l.Folder.Path), l.Folder.Path, ""), nil
		}
	} else if f, err := c.s.Drive.GetFile(c.ctx, ref); err == nil {
		return navResult("/files?open="+f.ID, f.Name, f.Path), nil
	}
	if hs, err := c.s.Drive.Retrieve(c.ctx, drive.RetrieveQuery{Query: ref, Limit: 1}); err == nil && len(hs) > 0 {
		return navResult("/files?open="+hs[0].FileID, hs[0].Name, hs[0].Path), nil
	}
	return nil, fail(http.StatusNotFound, "not_found", fmt.Sprintf("No encuentro el archivo «%s».", ref), fmt.Sprintf("I cannot find the file “%s”.", ref))
}

// ---- email / linkedin (draft + approval, never sends) ----

func cmdDraft(c *cmdCall, kind string) (*cmdResult, *cmdFail) {
	text := c.str("text")
	var body string
	if kind == "email" {
		body = c.t("Redacta un borrador de correo (NO lo envíes por tu cuenta: el envío lo aprueba una persona): ", "Write an email draft (do NOT send it on your own: a person approves the sending): ") + text
	} else {
		body = c.t("Redacta una publicación de LinkedIn (NO la publiques por tu cuenta: la publicación la aprueba una persona): ", "Write a LinkedIn post (do NOT publish it on your own: a person approves the publishing): ") + text
	}
	res, f := c.submit(c.marks("[[draft:"+kind+"]]") + " " + body)
	if f == nil {
		res.Title = c.t("Borrador en preparación", "Draft in progress")
		res.Text = c.t("Prepararé el borrador; para enviarlo te pediré aprobación.", "I will prepare the draft; I will ask for your approval before anything is sent.")
	}
	return res, f
}

// ---- remember ----

func cmdRemember(c *cmdCall) (*cmdResult, *cmdFail) {
	a, f := c.targetAgent("assistant")
	if f != nil {
		return nil, f
	}
	text := c.str("text")
	if lm := c.s.cmds.LongMemory; lm != nil {
		if err := lm.Remember(c.ctx, c.org, a.ID, text); err != nil {
			return nil, c.serviceFail(err)
		}
	} else {
		// The sequence keeps two notes written within one clock tick (coarse on Windows) from sharing a key and overwriting each other.
		key := fmt.Sprintf("nota-%d-%d", time.Now().UnixNano(), noteSeq.Add(1))
		if err := c.s.Store.SetMemory(c.ctx, c.org, a.ID, domain.Memory{Scope: "agent", Key: key, Value: trunc(text, 1000)}); err != nil {
			return nil, c.serviceFail(err)
		}
	}
	return msgResult(c.t("Anotado", "Noted"), fmt.Sprintf(c.t("%s lo recordará: «%s»", "%s will remember it: “%s”"), a.Name, trunc(text, 120)), map[string]any{"agent_id": a.ID}), nil
}

// noteSeq makes the key of a remembered note unique within the process.
var noteSeq atomic.Int64

// ---- tone / language ----

func cmdTone(c *cmdCall) (*cmdResult, *cmdFail) {
	tone := c.str("value")
	if c.str("agent") != "" || c.agentID != "" {
		a, f := c.targetAgent("")
		if f != nil {
			return nil, f
		}
		if c.s.Profiles == nil {
			return nil, unavailable(c.t("El perfil del agente", "The agent profile"))
		}
		if _, err := c.s.Profiles.Update(c.ctx, a.ID, application.ProfileInput{Tone: &tone, Instructions: c.currentInstructions(a.ID), Creativity: c.currentCreativity(a.ID), Skills: c.currentSkills(a.ID)}); err != nil {
			return nil, c.serviceFail(err)
		}
		return msgResult(c.t("Tono cambiado", "Tone changed"), fmt.Sprintf(c.t("%s ahora habla con tono «%s».", "%s now speaks with the “%s” tone."), a.Name, tone), map[string]any{"agent_id": a.ID, "tone": tone}), nil
	}
	if f := c.need("org:manage"); f != nil {
		return nil, f
	}
	if c.s.OrgConfig == nil {
		return nil, unavailable(c.t("La configuración de la organización", "The organization settings"))
	}
	if _, err := c.s.OrgConfig.UpdateSettings(c.ctx, application.SettingsUpdate{Tone: &tone}); err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(c.t("Tono cambiado", "Tone changed"), fmt.Sprintf(c.t("El tono de la organización ahora es «%s».", "The organization tone is now “%s”."), tone), map[string]any{"tone": tone}), nil
}

func (c *cmdCall) profile(agentID string) application.ProfileView {
	v, _ := c.s.Profiles.Get(c.ctx, agentID)
	return v
}
func (c *cmdCall) currentInstructions(id string) string { return c.profile(id).Instructions }
func (c *cmdCall) currentCreativity(id string) string   { return c.profile(id).Creativity }
func (c *cmdCall) currentSkills(id string) []string     { return c.profile(id).Skills }

func cmdLanguage(c *cmdCall) (*cmdResult, *cmdFail) {
	loc := c.str("value")
	if _, err := c.s.OrgConfig.UpdateSettings(c.ctx, application.SettingsUpdate{Locale: &loc}); err != nil {
		return nil, c.serviceFail(err)
	}
	// the confirmation is in the language that was just chosen
	if loc == "en" {
		return msgResult("Language changed", "The organization language is now English.", map[string]any{"locale": loc}), nil
	}
	return msgResult("Idioma cambiado", "El idioma de la organización ahora es español.", map[string]any{"locale": loc}), nil
}

// ---- save-template ----

func cmdSaveTemplate(c *cmdCall) (*cmdResult, *cmdFail) {
	name := strings.TrimSpace(c.str("name"))
	for _, p := range []string{"guardar ", "save "} {
		if strings.HasPrefix(commands.Fold(name+" "), p) {
			name = strings.TrimSpace(name[len(p)-1:])
			break
		}
	}
	if name == "guardar" || name == "save" {
		name = ""
	}
	if c.project != "" {
		t, err := c.s.Projects.SaveAsTemplateWith(c.ctx, c.project, projects.SaveOpts{Name: name})
		if err != nil {
			return nil, c.serviceFail(err)
		}
		return msgResult(c.t("Plantilla guardada", "Template saved"), fmt.Sprintf(c.t("«%s» quedó en tus plantillas.", "“%s” is now in your templates."), t.Name), map[string]any{"template_id": t.ID}), nil
	}
	reqs, err := c.s.Store.ListRequests(c.ctx, c.org)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	if len(reqs) == 0 {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Todavía no hay una solicitud para convertir en plantilla.", "There is no request yet to turn into a template.")
	}
	last := reqs[0]
	for _, r := range reqs {
		if r.CreatedAt.After(last.CreatedAt) {
			last = r
		}
	}
	if name == "" {
		name = trunc(last.Text, 60)
	}
	role := "assistant"
	if tasks, err := c.s.Store.ListTasksByRequest(c.ctx, c.org, last.ID); err == nil && len(tasks) > 0 {
		role = tasks[0].AgentID
		if a := c.agentRef(role); a != nil {
			role = a.Role
		}
	}
	node := projects.TemplateNode{Key: "n1", Title: name, Description: last.Text, Agent: role, Complexity: "M"}
	in := projects.TemplateInput{Name: name, Description: trunc(last.Text, 300), Category: "custom",
		Objectives: []projects.TemplateObjective{{Key: "main", Title: name, Workflows: []projects.TemplateWorkflow{{Key: "w1", Title: name, Nodes: []projects.TemplateNode{node}}}}}}
	t, err := c.s.Projects.CreateTemplate(c.ctx, in)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(c.t("Plantilla guardada", "Template saved"), fmt.Sprintf(c.t("«%s» quedó en tus plantillas.", "“%s” is now in your templates."), t.Name), map[string]any{"template_id": t.ID}), nil
}

// ---- skill / learn ----

func cmdSkill(c *cmdCall) (*cmdResult, *cmdFail) {
	if f := c.need("agents:write"); f != nil {
		return nil, f
	}
	a, f := c.targetAgent("")
	if f != nil {
		return nil, f
	}
	ref := c.str("skill")
	list, err := c.s.Skills.List(c.ctx)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	for _, sk := range list {
		if sk.ID == ref || commands.Fold(sk.Name) == commands.Fold(ref) {
			if err := c.s.Skills.Assign(c.ctx, c.s.skillActorOf(c), a.ID, sk.ID); err != nil {
				return nil, c.skillErr(err)
			}
			return msgResult(c.t("Skill asignada", "Skill assigned"),
				fmt.Sprintf(c.t("%s ya tiene la skill «%s» para sus próximas tareas.", "%s now has the skill “%s” for its next tasks."), a.Name, sk.Name), map[string]any{"skill_id": sk.ID, "agent_id": a.ID}), nil
		}
	}
	return nil, fail(http.StatusNotFound, "not_found", fmt.Sprintf("No encuentro la skill «%s».", ref), fmt.Sprintf("I cannot find the skill “%s”.", ref))
}

func (s *server) skillActorOf(c *cmdCall) skills.Actor { return s.skillActor(c.r) }

func (c *cmdCall) skillErr(err error) *cmdFail {
	if se, ok := err.(*skills.Error); ok {
		return fail(http.StatusBadRequest, "invalid_args", se.Msg, se.Msg)
	}
	return c.serviceFail(err)
}

func slugName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range commands.Fold(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteRune('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}

func cmdLearn(c *cmdCall) (*cmdResult, *cmdFail) {
	agentID := c.agentID
	if ref := c.str("agent"); ref != "" {
		if a := c.agentRef(ref); a != nil {
			agentID = a.ID
		}
	}
	tasks, err := c.s.Store.ListTasks(c.ctx, c.org, agentID, string(domain.TaskDone))
	if err != nil {
		return nil, c.serviceFail(err)
	}
	var last *domain.Task
	for i := range tasks {
		if tasks[i].Output != nil && (last == nil || tasks[i].CreatedAt.After(last.CreatedAt)) {
			last = &tasks[i]
		}
	}
	if last == nil {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Todavía no hay una tarea terminada de la que aprender.", "There is no finished task to learn from yet.")
	}
	name := slugName(c.str("name"))
	if name == "" {
		name = slugName(last.Title)
	}
	if name == "" {
		name = "tarea-aprendida"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", last.Title, c.t("Cómo se resolvió esta tarea (guardado desde un trabajo terminado; revísalo antes de usarlo).", "How this task was solved (saved from finished work; review it before using it)."))
	fmt.Fprintf(&b, "## %s\n\n%s\n", c.t("Resumen", "Summary"), last.Output.Summary)
	for _, sec := range []struct {
		h     string
		items []string
	}{{c.t("Pasos y hallazgos", "Steps and findings"), last.Output.Findings}, {c.t("Recomendaciones", "Recommendations"), last.Output.Recommendations}} {
		if len(sec.items) > 0 {
			fmt.Fprintf(&b, "\n## %s\n\n", sec.h)
			for _, it := range sec.items {
				fmt.Fprintf(&b, "- %s\n", it)
			}
		}
	}
	d, err := c.s.Skills.Create(c.ctx, c.s.skillActor(c.r), skills.Input{Name: name, Description: trunc(c.t("Cómo hacer: ", "How to: ")+last.Title, 300), Instructions: b.String(), Source: skills.SourceFromWork})
	if err != nil {
		return nil, c.skillErr(err)
	}
	return msgResult(c.t("Skill guardada como borrador", "Skill saved as a draft"),
		fmt.Sprintf(c.t("«%s» quedó como borrador; un administrador debe revisarla antes de que los agentes la usen.", "“%s” is a draft; an administrator must review it before agents use it."), d.Name),
		map[string]any{"skill_id": d.ID, "task_id": last.ID}), nil
}

// ---- undo ----

// lastDriveChange finds the newest file version created by the agent (or by the person) in the audit trail.
func (c *cmdCall) lastDriveChange() (fileID, path string, version int, f *cmdFail) {
	actor := c.actor
	if a := c.agentRef(c.str("agent")); a != nil {
		actor = a.ID
	} else if c.agentID != "" {
		actor = c.agentID
	}
	page, err := c.s.Audit.Query(c.ctx, domain.AuditQuery{Actor: actor, Action: "drive.file.version_created", Desc: true, Limit: 1})
	if err != nil {
		return "", "", 0, c.serviceFail(err)
	}
	if len(page.Items) == 0 {
		return "", "", 0, fail(http.StatusUnprocessableEntity, "nothing_to_do", "No hay cambios recientes que deshacer.", "There are no recent changes to undo.")
	}
	e := page.Items[0]
	cur, err := c.s.Drive.GetFile(c.ctx, e.EntityID)
	if err != nil {
		return "", "", 0, c.driveErr(err)
	}
	if cur.CurrentVersion < 2 {
		return "", "", 0, fail(http.StatusUnprocessableEntity, "nothing_to_do", "Ese archivo no tiene una versión anterior.", "That file has no earlier version.")
	}
	return cur.ID, cur.Path, cur.CurrentVersion, nil
}

func confirmUndo(c *cmdCall) (commands.L, *cmdFail) {
	_, path, v, f := c.lastDriveChange()
	if f != nil {
		return commands.L{}, f
	}
	return commands.L{ES: fmt.Sprintf("Se volverá %s a la versión %d (queda como una versión nueva; el historial se conserva). ¿Confirmas?", path, v-1),
		EN: fmt.Sprintf("%s will go back to version %d (as a new version; the history is kept). Do you confirm?", path, v-1)}, nil
}

func cmdUndo(c *cmdCall) (*cmdResult, *cmdFail) {
	id, path, v, f := c.lastDriveChange()
	if f != nil {
		return nil, f
	}
	exp := v
	res, err := c.s.Drive.RestoreVersion(c.ctx, drive.Human(c.actor), id, v-1, &exp)
	if err != nil {
		return nil, c.driveErr(err)
	}
	return msgResult(c.t("Cambio deshecho", "Change undone"),
		fmt.Sprintf(c.t("%s volvió a la versión %d (ahora es la versión %d).", "%s went back to version %d (it is now version %d)."), path, v-1, res.File.CurrentVersion),
		map[string]any{"file_id": id, "restored_version": v - 1, "new_version": res.File.CurrentVersion}), nil
}
