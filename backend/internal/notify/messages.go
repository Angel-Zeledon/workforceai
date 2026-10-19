package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

// Message is one notification, independent of the channel.
type Message struct {
	Locale   string
	Title    string // what the agent wants to do
	Agent    string
	Risk     string
	Details  string // clipped
	Approve  string // signed links ("" = this approval must be decided in the app)
	Reject   string
	Open     string // the app
	NeedsApp bool   // links are not allowed for this approval (high risk, double approval...)
	// Notice is set instead of an approval: the plan cap was reached.
	Notice string
}

// text holds the words of the messages in the three languages of the product.
var text = map[string]map[string]string{
	"es": {
		"subject":  "Aprobación pendiente: %s",
		"subjCap":  "Tu trabajo está en pausa: se alcanzó el tope del plan gratis",
		"intro":    "Un agente necesita tu decisión.",
		"risk":     "Riesgo",
		"agent":    "Agente",
		"approve":  "Aprobar",
		"reject":   "Rechazar",
		"open":     "Abrir en la app",
		"needsApp": "Por su riesgo, esta aprobación solo se decide dentro de la app, con tu sesión.",
		"expires":  "Los enlaces funcionan una vez y caducan. Abrirlos no decide nada: te pedirán confirmar.",
		"test":     "Prueba de notificaciones: todo funciona. No hay nada que aprobar.",
		"capBody":  "El plan gratis tiene un tope duro de gasto. Se alcanzó: el trabajo queda en pausa y no se cobra nada de más. Para seguir, pasa a un plan de pago.",
		"cLandT":   "Confirma tu decisión",
		"cLandOK":  "Listo: la aprobación quedó %s.",
		"cLandAp":  "aprobada",
		"cLandRj":  "rechazada",
		"cBad":     "Este enlace no es válido.",
		"cExp":     "Este enlace caducó. Abre la app para decidir.",
		"cUsed":    "Este enlace ya se usó.",
		"cDone":    "Esta aprobación ya fue decidida.",
		"cSess":    "Esta aprobación necesita tu sesión en la app.",
		"cFail":    "No se pudo registrar la decisión. Abre la app.",
		"cOff":     "Las aprobaciones por enlace están desactivadas.",
		"confirm":  "Confirmar",
	},
	"en": {
		"subject":  "Approval pending: %s",
		"subjCap":  "Your work is paused: the free plan cap was reached",
		"intro":    "An agent needs your decision.",
		"risk":     "Risk",
		"agent":    "Agent",
		"approve":  "Approve",
		"reject":   "Reject",
		"open":     "Open in the app",
		"needsApp": "Because of its risk, this approval is only decided inside the app, with your session.",
		"expires":  "Links work once and expire. Opening them decides nothing: you will be asked to confirm.",
		"test":     "Notification test: everything works. There is nothing to approve.",
		"capBody":  "The free plan has a hard spending cap. It was reached: the work is paused and nothing extra is charged. To continue, move to a paid plan.",
		"cLandT":   "Confirm your decision",
		"cLandOK":  "Done: the approval was %s.",
		"cLandAp":  "approved",
		"cLandRj":  "rejected",
		"cBad":     "This link is not valid.",
		"cExp":     "This link has expired. Open the app to decide.",
		"cUsed":    "This link was already used.",
		"cDone":    "This approval was already decided.",
		"cSess":    "This approval needs your session in the app.",
		"cFail":    "The decision could not be recorded. Open the app.",
		"cOff":     "Approvals by link are turned off.",
		"confirm":  "Confirm",
	},
	"pt": {
		"subject":  "Aprovação pendente: %s",
		"subjCap":  "Seu trabalho está em pausa: o limite do plano grátis foi atingido",
		"intro":    "Um agente precisa da sua decisão.",
		"risk":     "Risco",
		"agent":    "Agente",
		"approve":  "Aprovar",
		"reject":   "Rejeitar",
		"open":     "Abrir no app",
		"needsApp": "Pelo risco, esta aprovação só é decidida dentro do app, com a sua sessão.",
		"expires":  "Os links funcionam uma vez e expiram. Abri-los não decide nada: você será pedido a confirmar.",
		"test":     "Teste de notificações: tudo funciona. Não há nada para aprovar.",
		"capBody":  "O plano grátis tem um limite rígido de gasto. Ele foi atingido: o trabalho fica em pausa e nada a mais é cobrado. Para continuar, passe para um plano pago.",
		"cLandT":   "Confirme a sua decisão",
		"cLandOK":  "Pronto: a aprovação foi %s.",
		"cLandAp":  "aprovada",
		"cLandRj":  "rejeitada",
		"cBad":     "Este link não é válido.",
		"cExp":     "Este link expirou. Abra o app para decidir.",
		"cUsed":    "Este link já foi usado.",
		"cDone":    "Esta aprovação já foi decidida.",
		"cSess":    "Esta aprovação precisa da sua sessão no app.",
		"cFail":    "Não foi possível registrar a decisão. Abra o app.",
		"cOff":     "As aprovações por link estão desativadas.",
		"confirm":  "Confirmar",
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

// PickLocale chooses es/en/pt from an Accept-Language header.
func PickLocale(accept string) string {
	for _, part := range strings.Split(strings.ToLower(accept), ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch {
		case strings.HasPrefix(tag, "es"):
			return "es"
		case strings.HasPrefix(tag, "pt"):
			return "pt"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "es"
}

// Clip shortens s to n runes on a rune boundary.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// oneLine removes line breaks so a header or a chat title cannot be forged.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

// Subject is the e-mail subject.
func (m Message) Subject() string {
	if m.Notice != "" {
		return tr(m.Locale, "subjCap")
	}
	return fmt.Sprintf(tr(m.Locale, "subject"), Clip(oneLine(m.Title), 90))
}

// PlainText renders the e-mail body.
func (m Message) PlainText() string {
	var b strings.Builder
	if m.Notice != "" {
		b.WriteString(m.Notice + "\n")
		if m.Open != "" {
			b.WriteString("\n" + tr(m.Locale, "open") + ": " + m.Open + "\n")
		}
		return b.String()
	}
	b.WriteString(tr(m.Locale, "intro") + "\n\n" + m.Title + "\n")
	if m.Agent != "" {
		b.WriteString(tr(m.Locale, "agent") + ": " + m.Agent + "\n")
	}
	b.WriteString(tr(m.Locale, "risk") + ": " + m.Risk + "\n")
	if m.Details != "" {
		b.WriteString("\n" + m.Details + "\n")
	}
	b.WriteString("\n")
	if m.Approve != "" {
		b.WriteString(tr(m.Locale, "approve") + ": " + m.Approve + "\n" + tr(m.Locale, "reject") + ": " + m.Reject + "\n")
		b.WriteString("\n" + tr(m.Locale, "expires") + "\n")
	} else if m.NeedsApp {
		b.WriteString(tr(m.Locale, "needsApp") + "\n")
	}
	if m.Open != "" {
		b.WriteString("\n" + tr(m.Locale, "open") + ": " + m.Open + "\n")
	}
	return b.String()
}

// HTMLBody renders the e-mail HTML (everything user-visible is escaped).
func (m Message) HTMLBody() string {
	e := html.EscapeString
	btn := func(label, href, bg string) string {
		return `<a href="` + e(href) + `" style="display:inline-block;padding:10px 18px;margin:0 8px 8px 0;border-radius:6px;background:` + bg + `;color:#fff;text-decoration:none;font-weight:600">` + e(label) + `</a>`
	}
	var b strings.Builder
	b.WriteString(`<div style="font-family:system-ui,sans-serif;max-width:560px;color:#111">`)
	if m.Notice != "" {
		b.WriteString(`<p>` + e(m.Notice) + `</p>`)
		if m.Open != "" {
			b.WriteString(btn(tr(m.Locale, "open"), m.Open, "#374151"))
		}
		return b.String() + `</div>`
	}
	b.WriteString(`<p>` + e(tr(m.Locale, "intro")) + `</p><h3 style="margin:8px 0">` + e(m.Title) + `</h3>`)
	if m.Agent != "" {
		b.WriteString(`<p style="margin:2px 0">` + e(tr(m.Locale, "agent")) + `: ` + e(m.Agent) + `</p>`)
	}
	b.WriteString(`<p style="margin:2px 0">` + e(tr(m.Locale, "risk")) + `: <b>` + e(m.Risk) + `</b></p>`)
	if m.Details != "" {
		b.WriteString(`<p style="color:#444;white-space:pre-wrap">` + e(m.Details) + `</p>`)
	}
	if m.Approve != "" {
		b.WriteString(`<p>` + btn(tr(m.Locale, "approve"), m.Approve, "#047857") + btn(tr(m.Locale, "reject"), m.Reject, "#b91c1c") + `</p>`)
		b.WriteString(`<p style="color:#666;font-size:12px">` + e(tr(m.Locale, "expires")) + `</p>`)
	} else if m.NeedsApp {
		b.WriteString(`<p style="color:#92400e">` + e(tr(m.Locale, "needsApp")) + `</p>`)
	}
	if m.Open != "" {
		b.WriteString(`<p>` + btn(tr(m.Locale, "open"), m.Open, "#374151") + `</p>`)
	}
	return b.String() + `</div>`
}

// SlackPayload is the incoming-webhook body: blocks with link buttons. The
// buttons only open the confirmation page; nothing is decided by a click here.
func (m Message) SlackPayload() ([]byte, error) {
	var blocks []map[string]any
	head := oneLine(m.Title)
	if m.Notice != "" {
		head = m.Notice
	}
	blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "*" + slackEscape(Clip(m.Subject(), 150)) + "*\n" + slackEscape(Clip(head, 600))}})
	if m.Notice == "" {
		meta := tr(m.Locale, "risk") + ": " + m.Risk
		if m.Agent != "" {
			meta = tr(m.Locale, "agent") + ": " + m.Agent + " · " + meta
		}
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": slackEscape(meta)}}})
		if m.Details != "" {
			blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": slackEscape(Clip(m.Details, 500))}})
		}
	}
	var els []map[string]any
	if m.Approve != "" {
		els = append(els,
			map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": tr(m.Locale, "approve")}, "style": "primary", "url": m.Approve},
			map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": tr(m.Locale, "reject")}, "style": "danger", "url": m.Reject})
	}
	if m.Open != "" {
		els = append(els, map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": tr(m.Locale, "open")}, "url": m.Open})
	}
	if len(els) > 0 {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": els})
	}
	if m.Notice == "" && m.Approve == "" && m.NeedsApp {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": slackEscape(tr(m.Locale, "needsApp"))}}})
	}
	return json.Marshal(map[string]any{"text": Clip(m.Subject(), 150), "blocks": blocks})
}

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// TeamsPayload is an Adaptive Card message (Workflows / incoming webhook) with
// Action.OpenUrl buttons.
func (m Message) TeamsPayload() ([]byte, error) {
	body := []map[string]any{{"type": "TextBlock", "text": Clip(m.Subject(), 150), "weight": "Bolder", "size": "Medium", "wrap": true}}
	if m.Notice != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Notice, "wrap": true})
	} else {
		body = append(body, map[string]any{"type": "TextBlock", "text": Clip(oneLine(m.Title), 600), "wrap": true})
		facts := []map[string]any{{"title": tr(m.Locale, "risk"), "value": m.Risk}}
		if m.Agent != "" {
			facts = append([]map[string]any{{"title": tr(m.Locale, "agent"), "value": m.Agent}}, facts...)
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
		if m.Details != "" {
			body = append(body, map[string]any{"type": "TextBlock", "text": Clip(m.Details, 500), "wrap": true, "isSubtle": true})
		}
		if m.Approve == "" && m.NeedsApp {
			body = append(body, map[string]any{"type": "TextBlock", "text": tr(m.Locale, "needsApp"), "wrap": true, "color": "Warning"})
		}
	}
	var actions []map[string]any
	if m.Approve != "" {
		actions = append(actions,
			map[string]any{"type": "Action.OpenUrl", "title": tr(m.Locale, "approve"), "url": m.Approve, "style": "positive"},
			map[string]any{"type": "Action.OpenUrl", "title": tr(m.Locale, "reject"), "url": m.Reject, "style": "destructive"})
	}
	if m.Open != "" {
		actions = append(actions, map[string]any{"type": "Action.OpenUrl", "title": tr(m.Locale, "open"), "url": m.Open})
	}
	card := map[string]any{"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4", "body": body}
	if len(actions) > 0 {
		card["actions"] = actions
	}
	return json.Marshal(map[string]any{"type": "message", "attachments": []map[string]any{{"contentType": "application/vnd.microsoft.card.adaptive", "content": card}}})
}
