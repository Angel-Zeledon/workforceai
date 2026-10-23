package slackapp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
)

// Action ids of the buttons. The value of approve / reject is only the approval id: no details travel in Slack.
const (
	ActionApprove = "approval_approve"
	ActionReject  = "approval_reject"
	ActionOpen    = "approval_open"
)

// DefaultCommand is the slash command suggested in the setup (the admin may register another name in Slack;
// the replies use the name Slack sends).
const DefaultCommand = "/oficina"

// text holds the words of the Slack messages in the three languages of the product.
var text = map[string]map[string]string{
	"es": {
		"intro":       "Un agente necesita tu decisión.",
		"risk":        "Riesgo",
		"agent":       "Agente",
		"progress":    "Aprobaciones: %d de %d",
		"approve":     "Aprobar",
		"reject":      "Rechazar",
		"open":        "Abrir en la app",
		"cfTitle":     "¿Aprobar esta acción?",
		"cfText":      "Quedará en la auditoría con tu nombre.",
		"cfNo":        "Cancelar",
		"needsApp":    "Por su riesgo, esta aprobación solo se aprueba dentro de la app. Aquí puedes rechazarla.",
		"stApproved":  "Aprobada por %s.",
		"stRejected":  "Rechazada por %s.",
		"stPartial":   "Aprobación %d de %d. Falta otra persona.",
		"stClosed":    "Cerrada: %s.",
		"wApproved":   "aprobada",
		"wRejected":   "rechazada",
		"wPending":    "pendiente",
		"rUnlinked":   "Tu cuenta de Slack no está vinculada a AI Workforce OS. En la app abre Seguridad › Slack, pide un código y escribe aquí `%s vincular <código>`.",
		"rNotMember":  "Tu cuenta ya no pertenece a esta organización.",
		"rForbidden":  "Tu rol no puede decidir aprobaciones.",
		"rRole":       "Esta aprobación necesita un rol más alto que el tuyo.",
		"rSelf":       "Tú pediste esta acción: debe aprobarla otra persona.",
		"rAlready":    "Ya la aprobaste: la siguiente aprobación debe darla otra persona.",
		"rDone":       "Esta aprobación ya fue decidida.",
		"rKill":       "El interruptor de emergencia está activo: no se aprueba nada. Sí puedes rechazar.",
		"rReadOnly":   "La oficina está en modo solo lectura: no se aprueba nada. Sí puedes rechazar.",
		"rDisabled":   "El canal de Slack está desactivado para esta organización.",
		"rFail":       "No se pudo registrar la decisión. Abre la app.",
		"rNotFound":   "No encontré esa aprobación.",
		"help":        "Comandos: `%[1]s estado` (cómo va la oficina), `%[1]s pendientes` (aprobaciones por decidir), `%[1]s vincular <código>` (vincula tu cuenta) y `%[1]s desvincular`.",
		"statusTitle": "Estado de la oficina",
		"stPending":   "Aprobaciones pendientes: %d",
		"stRunning":   "Tareas en curso: %d",
		"stAwaiting":  "Tareas esperando aprobación: %d",
		"stNormal":    "Controles: normal",
		"stKill":      "Interruptor de emergencia ACTIVO (%s)",
		"stRO":        "Modo solo lectura",
		"pNone":       "No hay aprobaciones pendientes.",
		"pTitle":      "Aprobaciones pendientes (%d)",
		"pMore":       "…y %d más en la app.",
		"pViewOnly":   "Puedes verlas, pero tu rol no puede decidirlas.",
		"linkOK":      "Listo: tus decisiones desde Slack quedarán a nombre de %s.",
		"linkBad":     "Ese código no es válido o caducó. Pide uno nuevo en la app.",
		"linkUsage":   "Escribe `%s vincular <código>` con el código que te da la app.",
		"unlinkOK":    "Tu cuenta de Slack quedó desvinculada.",
		"unlinkNone":  "No había nada que desvincular.",
		"test":        "Prueba de AI Workforce OS: el canal funciona. No hay nada que aprobar.",
	},
	"en": {
		"intro":       "An agent needs your decision.",
		"risk":        "Risk",
		"agent":       "Agent",
		"progress":    "Approvals: %d of %d",
		"approve":     "Approve",
		"reject":      "Reject",
		"open":        "Open in the app",
		"cfTitle":     "Approve this action?",
		"cfText":      "It will be recorded in the audit trail under your name.",
		"cfNo":        "Cancel",
		"needsApp":    "Because of its risk this approval is approved only inside the app. You can reject it here.",
		"stApproved":  "Approved by %s.",
		"stRejected":  "Rejected by %s.",
		"stPartial":   "Approval %d of %d. Another person is needed.",
		"stClosed":    "Closed: %s.",
		"wApproved":   "approved",
		"wRejected":   "rejected",
		"wPending":    "pending",
		"rUnlinked":   "Your Slack account is not linked to AI Workforce OS. In the app open Security › Slack, get a code and type `%s link <code>` here.",
		"rNotMember":  "Your account no longer belongs to this organization.",
		"rForbidden":  "Your role cannot decide approvals.",
		"rRole":       "This approval needs a higher role than yours.",
		"rSelf":       "You requested this action: another person must approve it.",
		"rAlready":    "You already approved it: the next approval must come from another person.",
		"rDone":       "This approval was already decided.",
		"rKill":       "The kill switch is on: nothing can be approved. You can still reject.",
		"rReadOnly":   "The office is in read-only mode: nothing can be approved. You can still reject.",
		"rDisabled":   "The Slack channel is turned off for this organization.",
		"rFail":       "The decision could not be recorded. Open the app.",
		"rNotFound":   "I could not find that approval.",
		"help":        "Commands: `%[1]s status` (how the office is doing), `%[1]s pending` (approvals to decide), `%[1]s link <code>` (link your account) and `%[1]s unlink`.",
		"statusTitle": "Office status",
		"stPending":   "Pending approvals: %d",
		"stRunning":   "Tasks in progress: %d",
		"stAwaiting":  "Tasks waiting for approval: %d",
		"stNormal":    "Controls: normal",
		"stKill":      "Kill switch ON (%s)",
		"stRO":        "Read-only mode",
		"pNone":       "No pending approvals.",
		"pTitle":      "Pending approvals (%d)",
		"pMore":       "…and %d more in the app.",
		"pViewOnly":   "You can see them, but your role cannot decide them.",
		"linkOK":      "Done: your Slack decisions will be recorded as %s.",
		"linkBad":     "That code is not valid or has expired. Get a new one in the app.",
		"linkUsage":   "Type `%s link <code>` with the code the app gives you.",
		"unlinkOK":    "Your Slack account was unlinked.",
		"unlinkNone":  "There was nothing to unlink.",
		"test":        "AI Workforce OS test: the channel works. There is nothing to approve.",
	},
	"pt": {
		"intro":       "Um agente precisa da sua decisão.",
		"risk":        "Risco",
		"agent":       "Agente",
		"progress":    "Aprovações: %d de %d",
		"approve":     "Aprovar",
		"reject":      "Rejeitar",
		"open":        "Abrir no app",
		"cfTitle":     "Aprovar esta ação?",
		"cfText":      "Ficará na auditoria com o seu nome.",
		"cfNo":        "Cancelar",
		"needsApp":    "Pelo seu risco, esta aprovação só é aprovada dentro do app. Aqui você pode rejeitá-la.",
		"stApproved":  "Aprovada por %s.",
		"stRejected":  "Rejeitada por %s.",
		"stPartial":   "Aprovação %d de %d. Falta outra pessoa.",
		"stClosed":    "Encerrada: %s.",
		"wApproved":   "aprovada",
		"wRejected":   "rejeitada",
		"wPending":    "pendente",
		"rUnlinked":   "Sua conta do Slack não está vinculada ao AI Workforce OS. No app abra Segurança › Slack, peça um código e digite aqui `%s vincular <código>`.",
		"rNotMember":  "Sua conta não pertence mais a esta organização.",
		"rForbidden":  "Seu papel não pode decidir aprovações.",
		"rRole":       "Esta aprovação precisa de um papel mais alto que o seu.",
		"rSelf":       "Você pediu esta ação: outra pessoa deve aprová-la.",
		"rAlready":    "Você já aprovou: a próxima aprovação deve vir de outra pessoa.",
		"rDone":       "Esta aprovação já foi decidida.",
		"rKill":       "O botão de emergência está ativo: nada é aprovado. Você ainda pode rejeitar.",
		"rReadOnly":   "O escritório está em modo somente leitura: nada é aprovado. Você ainda pode rejeitar.",
		"rDisabled":   "O canal do Slack está desativado para esta organização.",
		"rFail":       "Não foi possível registrar a decisão. Abra o app.",
		"rNotFound":   "Não encontrei essa aprovação.",
		"help":        "Comandos: `%[1]s estado` (como vai o escritório), `%[1]s pendentes` (aprovações a decidir), `%[1]s vincular <código>` (vincula sua conta) e `%[1]s desvincular`.",
		"statusTitle": "Estado do escritório",
		"stPending":   "Aprovações pendentes: %d",
		"stRunning":   "Tarefas em andamento: %d",
		"stAwaiting":  "Tarefas aguardando aprovação: %d",
		"stNormal":    "Controles: normal",
		"stKill":      "Botão de emergência ATIVO (%s)",
		"stRO":        "Modo somente leitura",
		"pNone":       "Não há aprovações pendentes.",
		"pTitle":      "Aprovações pendentes (%d)",
		"pMore":       "…e mais %d no app.",
		"pViewOnly":   "Você pode vê-las, mas seu papel não pode decidi-las.",
		"linkOK":      "Pronto: suas decisões no Slack ficarão em nome de %s.",
		"linkBad":     "Esse código não é válido ou expirou. Peça um novo no app.",
		"linkUsage":   "Digite `%s vincular <código>` com o código que o app lhe dá.",
		"unlinkOK":    "Sua conta do Slack foi desvinculada.",
		"unlinkNone":  "Não havia nada para desvincular.",
		"test":        "Teste do AI Workforce OS: o canal funciona. Não há nada para aprovar.",
	},
}

// tr returns the text in the locale (Spanish when unknown).
func tr(locale, key string) string {
	if m, ok := text[locale]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return text["es"][key]
}

// esc escapes the three characters Slack's mrkdwn treats as control (&, <, >): a title can never become a link,
// a mention or a channel broadcast.
func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

func mrkdwn(s string) map[string]any { return map[string]any{"type": "mrkdwn", "text": s} }
func plain(s string) map[string]any {
	return map[string]any{"type": "plain_text", "text": clip(s, 75), "emoji": false}
}

// card is what an approval message shows. It never carries the details, arguments or recipients of the action.
type card struct {
	Locale   string
	Approval domain.Approval
	Agent    string
	OpenURL  string
	NeedsApp bool   // approving needs the app (only reject + open are offered)
	Status   string // "" = pending with buttons; otherwise the outcome line that replaces the buttons
	Partial  string // progress line of a double approval (buttons stay)
}

func (c card) msg() Msg {
	loc, ap := c.Locale, c.Approval
	title := esc(clip(oneLine(ap.Title), 200))
	if title == "" {
		title = esc(ap.ID)
	}
	meta := []string{tr(loc, "risk") + ": *" + esc(oneLine(ap.Risk)) + "*"}
	if c.Agent != "" {
		meta = append(meta, tr(loc, "agent")+": "+esc(clip(oneLine(c.Agent), 60)))
	}
	if n := ap.RequiredApprovals; n > 1 {
		meta = append(meta, fmt.Sprintf(tr(loc, "progress"), len(ap.Decisions), n))
	}
	blocks := []map[string]any{
		{"type": "section", "text": mrkdwn("*" + title + "*\n" + tr(loc, "intro"))},
		{"type": "context", "elements": []map[string]any{mrkdwn(strings.Join(meta, "  ·  "))}},
	}
	if c.Status != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": mrkdwn(c.Status)})
		if c.OpenURL != "" {
			blocks = append(blocks, map[string]any{"type": "actions", "block_id": "approval_done",
				"elements": []map[string]any{{"type": "button", "action_id": ActionOpen, "text": plain(tr(loc, "open")), "url": c.OpenURL}}})
		}
		return Msg{Text: clip(oneLine(ap.Title), 150) + " — " + stripMarks(c.Status), Blocks: blocks}
	}
	if c.Partial != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{mrkdwn(c.Partial)}})
	}
	if c.NeedsApp {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{mrkdwn(tr(loc, "needsApp"))}})
	}
	var buttons []map[string]any
	if !c.NeedsApp {
		buttons = append(buttons, map[string]any{"type": "button", "action_id": ActionApprove, "style": "primary", "value": ap.ID,
			"text": plain(tr(loc, "approve")),
			"confirm": map[string]any{"title": plain(tr(loc, "cfTitle")), "text": plain(tr(loc, "cfText")),
				"confirm": plain(tr(loc, "approve")), "deny": plain(tr(loc, "cfNo"))}})
	}
	buttons = append(buttons, map[string]any{"type": "button", "action_id": ActionReject, "style": "danger", "value": ap.ID, "text": plain(tr(loc, "reject"))})
	if c.OpenURL != "" {
		buttons = append(buttons, map[string]any{"type": "button", "action_id": ActionOpen, "text": plain(tr(loc, "open")), "url": c.OpenURL})
	}
	blocks = append(blocks, map[string]any{"type": "actions", "block_id": "approval:" + ap.ID, "elements": buttons})
	return Msg{Text: tr(loc, "intro") + " " + clip(oneLine(ap.Title), 150), Blocks: blocks}
}

func stripMarks(s string) string { return strings.NewReplacer("*", "", "_", "", "`", "").Replace(s) }

// mention renders a Slack user so the message shows who decided (Slack resolves the name; nothing is stored).
func mention(slackUser string) string {
	if slackUser == "" {
		return "?"
	}
	return "<@" + esc(slackUser) + ">"
}

// statusLine is the outcome shown in place of the buttons once an approval is closed.
func statusLine(loc string, ap domain.Approval, by string) string {
	switch ap.Status {
	case domain.ApprovalApproved:
		return ":white_check_mark: " + fmt.Sprintf(tr(loc, "stApproved"), by)
	case domain.ApprovalRejected:
		return ":no_entry_sign: " + fmt.Sprintf(tr(loc, "stRejected"), by)
	}
	return fmt.Sprintf(tr(loc, "stClosed"), tr(loc, "wPending"))
}

// closedLine is the outcome of an approval decided somewhere else (the app, a link, a timeout).
func closedLine(loc string, ap domain.Approval) string {
	w := tr(loc, "wPending")
	switch ap.Status {
	case domain.ApprovalApproved:
		w = tr(loc, "wApproved")
	case domain.ApprovalRejected:
		w = tr(loc, "wRejected")
	}
	return fmt.Sprintf(tr(loc, "stClosed"), w)
}

// ephemeral is a reply only the clicking / typing person sees.
func ephemeral(s string) map[string]any {
	return map[string]any{"response_type": "ephemeral", "replace_original": false, "text": s}
}
