package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/roles"
)

// Org commands (team, hire, permissions, shop, inventory) plus the helpers
// behind GET /commands/complete and GET /commands/suggest.

func init() {
	cmdHandlers["team"] = handler{run: cmdTeam}
	cmdHandlers["hire"] = handler{run: cmdHire}
	cmdHandlers["permissions"] = handler{run: cmdPermissions}
	cmdHandlers["shop"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) {
		return navResult("/shop", c.t("Tienda", "Shop"), ""), nil
	}}
	cmdHandlers["inventory"] = handler{run: func(c *cmdCall) (*cmdResult, *cmdFail) {
		return navResult("/shop?tab=inventory", c.t("Inventario", "Inventory"), ""), nil
	}}
}

func cmdTeam(c *cmdCall) (*cmdResult, *cmdFail) {
	rows := [][]string{}
	for _, a := range c.loadAgents() {
		rows = append(rows, []string{a.Name, a.Title, string(a.State), a.Autonomy})
	}
	return tableResult(c.t("Equipo", "Team"), []string{c.t("Nombre", "Name"), c.t("Rol", "Role"), c.t("Estado", "State"), c.t("Autonomía", "Autonomy")}, rows,
		fmt.Sprintf(c.t("%d personas en el equipo.", "%d people on the team."), len(rows)), map[string]any{"count": len(rows)}), nil
}

func cmdHire(c *cmdCall) (*cmdResult, *cmdFail) {
	ref := c.str("role")
	f := commands.Fold(ref)
	var tpl *roles.Template
	for _, t := range roles.All() {
		t := t
		if commands.Fold(t.ID) == f {
			tpl = &t
			break
		}
		for _, txt := range t.I18n {
			if commands.Fold(txt.Title) == f {
				tpl = &t
			}
		}
		if tpl != nil {
			break
		}
	}
	if tpl == nil {
		return nil, fail(http.StatusNotFound, "not_found", fmt.Sprintf("No hay una plantilla de rol «%s».", ref), fmt.Sprintf("There is no role template “%s”.", ref))
	}
	a, err := c.s.Orch.HireFromTemplate(c.ctx, application.HireInput{TemplateID: tpl.ID, Name: c.str("name"), Locale: c.lang})
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return msgResult(c.t("Nuevo integrante", "New team member"),
		fmt.Sprintf(c.t("%s se sumó al equipo como %s. Empieza con aprobación en cada acción.", "%s joined the team as %s. They start with approval on every action."), a.Name, a.Title),
		map[string]any{"agent_id": a.ID}), nil
}

func cmdPermissions(c *cmdCall) (*cmdResult, *cmdFail) {
	rows := [][]string{
		{c.t("Quién decide aprobaciones", "Who decides approvals"), c.t("Administradores y propietarios", "Administrators and owners")},
		{c.t("Quién levanta el freno de emergencia", "Who lifts the kill switch"), c.t("Solo propietarios", "Owners only")},
	}
	data := map[string]any{}
	if c.s.OrgConfig != nil {
		if v, err := c.s.OrgConfig.Rules(c.ctx); err == nil {
			rows = append(rows, []string{c.t("Monto desde el que se pide aprobación", "Amount that needs approval"), fmt.Sprintf("$%.2f", v.ApprovalAmountUSD)})
			if len(v.AlwaysApprove) > 0 {
				rows = append(rows, []string{c.t("Siempre requieren aprobación", "Always need approval"), strings.Join(v.AlwaysApprove, ", ")})
			}
			for _, b := range v.Baseline {
				rows = append(rows, []string{c.t("Regla fija", "Fixed rule"), b})
			}
			data["approval_amount_usd"], data["enforced"] = v.ApprovalAmountUSD, v.Enforced
		}
	}
	for _, a := range c.loadAgents() {
		rows = append(rows, []string{c.t("Autonomía de ", "Autonomy of ") + a.Name, a.Autonomy})
	}
	return tableResult(c.t("Quién aprueba qué", "Who approves what"), []string{c.t("Tema", "Topic"), c.t("Valor", "Value")}, rows, "", data), nil
}

// ---- GET /commands/complete ----

type completion struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

func (s *server) completeCommands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, text := q.Get("kind"), commands.Fold(strings.TrimPrefix(q.Get("q"), "@"))
	limit := 8
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, 25)
	}
	ctx := r.Context()
	out := []completion{}
	match := func(ss ...string) bool {
		if text == "" {
			return true
		}
		for _, v := range ss {
			if strings.Contains(commands.Fold(v), text) {
				return true
			}
		}
		return false
	}
	switch kind {
	case commands.KindAgent:
		agents, err := s.Store.ListAgents(ctx, s.org(r))
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, a := range agents {
			if match(a.ID, a.Name, a.Role, a.Title) {
				out = append(out, completion{a.ID, a.Name, a.Title})
			}
		}
	case commands.KindProject:
		if s.Projects != nil && s.has(r, auth.PermTasksRead) {
			if ps, err := s.Projects.List(ctx); err == nil {
				for _, p := range ps {
					if match(p.Name, p.ID) {
						out = append(out, completion{p.ID, p.Name, p.Status})
					}
				}
			}
		}
	case commands.KindTemplate:
		if s.Projects != nil && s.has(r, auth.PermTasksRead) {
			if ts, err := s.Projects.Templates(ctx); err == nil {
				for _, t := range ts {
					if match(t.Name, t.Key, t.ID) {
						out = append(out, completion{t.ID, t.Name, t.Category})
					}
				}
			}
		}
	case commands.KindSkill:
		if s.Skills != nil && s.has(r, auth.PermSkillsRead) {
			if l, err := s.Skills.List(ctx); err == nil {
				for _, sk := range l {
					if sk.Status == "active" && match(sk.Name, sk.Description) {
						out = append(out, completion{sk.Name, sk.Name, trunc(sk.Description, 80)})
					}
				}
			}
		}
	case commands.KindFile:
		if s.Drive != nil && s.has(r, auth.PermFilesRead) {
			if text == "" {
				if l, err := s.Drive.List(ctx, "/"); err == nil {
					for _, f := range l.Files {
						out = append(out, completion{f.Path, f.Name, f.Path})
					}
				}
			} else if hs, err := s.Drive.Retrieve(ctx, drive.RetrieveQuery{Query: q.Get("q"), Limit: limit}); err == nil {
				for _, h := range hs {
					out = append(out, completion{h.Path, h.Name, h.Path})
				}
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be agent, project, file, skill or template"})
		return
	}
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /commands/suggest ----

type suggestion struct {
	Command  string     `json:"command"`
	Reason   commands.L `json:"reason"`
	Severity string     `json:"severity"`
}

func (s *server) suggestCommands(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := []suggestion{}
	surface := r.URL.Query().Get("surface")
	conv := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if conv == "" {
		conv = domain.ChatOffice
	}
	if application.IsChatConversation(conv) && s.has(r, auth.PermConversationsPost) && s.Orch.HasConvContext() {
		cc := s.Orch.ConvContextOf(ctx, conv)
		if msgs, err := s.Orch.ChatMessagesSince(ctx, conv, cc.Cutoff(), 200); err == nil && len(spokenMessages(msgs)) >= suggestMinMsgs {
			out = append(out, suggestion{"compact", commands.L{
				ES: fmt.Sprintf("La conversación ya tiene %d+ mensajes: resumirla ahorra costo y mantiene el foco.", suggestMinMsgs),
				EN: fmt.Sprintf("This conversation has %d+ messages: compacting it saves cost and keeps focus.", suggestMinMsgs)}, "info"})
		}
	}
	if st, err := s.Orch.Budget().Status(ctx); err == nil && st.Org.BudgetUSD > 0 && s.has(r, auth.PermMetricsRead) {
		if used := (st.Org.UsedUSD + st.Org.ReservedUSD) / st.Org.BudgetUSD; used >= 0.8 {
			sev := "warning"
			if used >= 1 {
				sev = "critical"
			}
			out = append(out, suggestion{"usage", commands.L{
				ES: fmt.Sprintf("Ya usaste el %.0f%% del presupuesto.", used*100), EN: fmt.Sprintf("You have used %.0f%% of the budget.", used*100)}, sev})
		}
	}
	if s.has(r, auth.PermApprovalsRead) {
		if ap, err := s.Store.ListApprovals(ctx, s.org(r), string(domain.ApprovalPending)); err == nil && len(ap) > 0 {
			out = append(out, suggestion{"pending", commands.L{
				ES: fmt.Sprintf("Hay %d aprobaciones esperando.", len(ap)), EN: fmt.Sprintf("%d approvals are waiting.", len(ap))}, "info"})
		}
	}
	_ = surface
	sort.SliceStable(out, func(i, j int) bool { return sevRank(out[i].Severity) > sevRank(out[j].Severity) })
	if len(out) > 3 {
		out = out[:3]
	}
	_ = time.Now
	writeJSON(w, http.StatusOK, out)
}

func sevRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	}
	return 1
}
