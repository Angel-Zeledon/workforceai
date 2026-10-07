package application

import (
	"hash/fnv"
	"regexp"
	"slices"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Local routing rules and canned lines. They are the safety net of the chat
// layer: used when the runtime is down, does not know /v1/route or answers
// something invalid. The runtime (agent-runtime/app/routing.py) has the richer
// version of the same rules; keep the intent and topic cues in sync.

var accentReplacer = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

func normText(s string) string {
	return strings.Join(strings.Fields(accentReplacer.Replace(strings.ToLower(s))), " ")
}

var tokenRe = regexp.MustCompile(`[a-z0-9&$]+`)

// topicRules maps a role to its cues over the normalized text (es + en).
var topicRules = []struct {
	role  string
	topic string
	re    *regexp.Regexp
}{
	{"accounting", "finance", regexp.MustCompile(`\b(financ|balance|margen|margin|costo|costs?\b|factur|invoice|presupuest|budget|flujo de caja|cash ?flow|impuest|tax|contab|accounting|utilidad|profit|gasto|expense|ingreso|revenue|rentab|deuda|debt|prestamo|loan|iva\b|estado de resultados)`)},
	{"legal", "legal", regexp.MustCompile(`\b(contratos?\b|contract|legal|clausula|clause|demanda|lawsuit|cumplimiento|compliance|abogad|lawyer|attorney|ley\b|leyes|law\b|privacidad|privacy|nda\b|propiedad intelectual|intellectual property|litigio|regulat|licencia|license|penalidad|penalty)`)},
	{"hr", "hr", regexp.MustCompile(`\b(contratar|contrataci|contratamos|vacante|empleado|rrhh|recursos humanos|personal\b|reclut|onboarding|nomina|salario|salary|vacaciones|hire\b|hiring|recruit|payroll|employee|candidat|entrevista|interview|talento|despido)`)},
	{"sales", "sales", regexp.MustCompile(`\b(ventas?\b|vender|sales\b|sell\b|selling|propuesta|proposal|cliente|client|customer|cotizacion|quote\b|pipeline|prospecto|lead\b|leads\b|comercial|negociacion|negotiation|descuento|discount|oferta)`)},
	{"analyst", "data", regexp.MustCompile(`\b(datos|data\b|analisis|analysis|analiz|analyz|metric|kpi|tendencia|trend|estadistic|statistic|dashboard|grafic|chart|insight|forecast|pronostico|proyeccion)`)},
	{"operations", "operations", regexp.MustCompile(`\b(operacion|operaciones|operations|capacidad|capacity|logistic|entrega|delivery|proveedor|supplier|inventario|inventory|plazo|produccion|production|almacen|warehouse|envio|shipping)`)},
}

var (
	greetRe        = regexp.MustCompile(`^(hola+|holi+|buen(as|os)( (dias|tardes|noches))?|hey+|ey|hi|hello|hiya|good (morning|afternoon|evening)|saludos|que tal|que onda|que hubo|ola)\b`)
	thanksRe       = regexp.MustCompile(`\b(gracias|thanks|thank you|thx|genial|perfecto|excelente|buen trabajo|great|awesome|perfect)\b`)
	helpRe         = regexp.MustCompile(`\b(ayuda|ayudame|help|que puedes hacer|que haces|quien eres|who are you|what can you do|como funciona)\b`)
	howareRe       = regexp.MustCompile(`\b(como estas|como esta|como andas|como va todo|como les va|how are you|how is it going|hows it going|que cuentas)\b`)
	interrogRe     = regexp.MustCompile(`^(que|como|cuanto|cuantos|cuanta|cuantas|cual|cuales|por que|porque|cuando|donde|quien|quienes|what|how|why|when|where|who|which|is|are|do|does|did|will|would|should)\b`)
	politeRe       = regexp.MustCompile(`\b(puedes|podrias|podes|podria|necesito|quiero|requiero|necesitamos|queremos|quisiera|can you|could you|would you|please|por favor|i need|i want|we need|id like|i d like)\b`)
	wantDetRe      = regexp.MustCompile(`\b(necesito|quiero|requiero|necesitamos|queremos|quisiera|i need|i want|we need|id like|i d like) (que |un |una |el |la |los |las |a |an |the |some )`)
	notTaskWantRe  = regexp.MustCompile(`\b(saber|entender|hablar|preguntar|conocer|ver|know|understand|talk|ask|see)\b`)
	taskStemsRe    = regexp.MustCompile(`\b(prepar|calcul|revis|redact|gener|elabor|armar|analiz|envi|mandar|agendar|investig|escrib|resum|compar|estim|planific|organiz|buscar|cotiz|crear|hacer|contrat|draft|write|create|build|review|prepare|send|schedule|research|summar|compare|estimate|plan\b|find|check)`)
	impESRe        = regexp.MustCompile(`^(prepara|haz|calcula|revisa|redacta|genera|crea|elabora|arma|analiza|envia|manda|investiga|escribe|resume|compara|estima|planifica|organiza|busca|contrata|cotiza|vende|proponme|agendame)(me|lo|la|le|nos|melo|mela|selo)?$|^dame$`)
	agendaRe       = regexp.MustCompile(`\bagenda (una|un|el|la)\b`)
	nonLetterStart = regexp.MustCompile(`^[^a-z0-9]+`)
	nonAlnum       = regexp.MustCompile(`[^a-z0-9 ]`)
)

var impEN = map[string]bool{"prepare": true, "make": true, "calculate": true, "review": true, "draft": true, "write": true, "create": true,
	"build": true, "generate": true, "analyze": true, "analyse": true, "send": true, "schedule": true, "research": true,
	"summarize": true, "summarise": true, "compare": true, "estimate": true, "plan": true, "find": true, "give": true,
	"put": true, "check": true, "run": true, "set": true, "list": true, "show": true}

var enFiller = map[string]bool{"please": true, "just": true, "can": true, "you": true, "could": true, "would": true, "go": true,
	"ahead": true, "kindly": true, "ok": true, "so": true, "now": true, "hey": true, "hi": true, "hello": true, "hola": true,
	"team": true, "buenas": true, "buenos": true, "dias": true, "tardes": true}

var greetFluff = map[string]bool{"a": true, "todos": true, "todas": true, "equipo": true, "team": true, "everyone": true, "all": true,
	"there": true, "amigos": true, "chicos": true, "chicas": true, "companeros": true, "como": true, "estan": true, "estas": true,
	"esta": true, "va": true, "y": true, "you": true, "how": true, "are": true, "is": true, "it": true, "going": true, "que": true,
	"tal": true, "muy": true, "dias": true, "tardes": true, "noches": true, "de": true, "nuevo": true, "again": true,
	"morning": true, "afternoon": true, "evening": true, "les": true, "te": true, "bien": true, "yo": true, "aqui": true,
	"todo": true, "hows": true, "oficina": true, "office": true}

var ackWords = map[string]bool{"ok": true, "okay": true, "vale": true, "listo": true, "entendido": true, "dale": true, "bien": true,
	"cool": true, "ya": true, "claro": true, "si": true, "sip": true, "yes": true, "yep": true, "got": true, "it": true}

func topicScores(n string) map[string]int {
	out := map[string]int{}
	for _, r := range topicRules {
		hits := map[string]bool{}
		for _, m := range r.re.FindAllString(n, -1) {
			hits[m] = true
		}
		if len(hits) > 0 {
			out[r.role] = len(hits)
		}
	}
	return out
}

func localSmalltalkKind(n string, names map[string]bool) string {
	n = nonLetterStart.ReplaceAllString(n, "")
	if n == "" {
		return ""
	}
	isFluff := func(t string) bool { return greetFluff[t] || names[t] }
	loc := greetRe.FindStringIndex(n)
	rest := n
	if loc != nil {
		rest = n[loc[1]:]
	}
	rest = strings.TrimSpace(nonAlnum.ReplaceAllString(rest, " "))
	var restTokens []string
	for _, t := range strings.Fields(rest) {
		if !isFluff(t) {
			restTokens = append(restTokens, t)
		}
	}
	noTopic := len(topicScores(n)) == 0
	if loc != nil && len(restTokens) == 0 {
		return "greeting"
	}
	if howareRe.MatchString(rest) && len(restTokens) <= 6 && noTopic {
		if loc != nil {
			return "greeting"
		}
		return "howare"
	}
	toks := tokenRe.FindAllString(n, -1)
	if helpRe.MatchString(n) && len(toks) <= 8 && noTopic {
		return "help"
	}
	if thanksRe.MatchString(n) && len(toks) <= 6 && noTopic {
		return "thanks"
	}
	if loc == nil && len(toks) > 0 && len(toks) <= 3 {
		all := true
		for _, t := range toks {
			if !ackWords[t] && !isFluff(t) {
				all = false
			}
		}
		if all {
			return "thanks"
		}
	}
	return ""
}

func localIsTask(n string) bool {
	n = nonLetterStart.ReplaceAllString(n, "")
	toks := tokenRe.FindAllString(n, -1)
	if len(toks) == 0 {
		return false
	}
	polite := politeRe.MatchString(n)
	if interrogRe.MatchString(n) && !polite {
		return false
	}
	for i, t := range toks {
		if i < 3 && !polite && len(t) > 6 && (strings.HasSuffix(t, "emos") || strings.HasSuffix(t, "amos")) &&
			taskStemsRe.MatchString(t) && !impESRe.MatchString(t) {
			return false // "revisemos...": an invitation to talk, not an order
		}
	}
	for _, t := range toks {
		if impESRe.MatchString(t) {
			return true
		}
	}
	if agendaRe.MatchString(n) {
		return true
	}
	for _, t := range toks {
		if enFiller[t] {
			continue
		}
		if impEN[t] {
			return true
		}
		break
	}
	if wantDetRe.MatchString(n) && !notTaskWantRe.MatchString(n) {
		return true
	}
	return polite && taskStemsRe.MatchString(n) && !notTaskWantRe.MatchString(n)
}

var localRelated = map[string][]string{
	"accounting": {"legal", "analyst"}, "legal": {"accounting", "hr", "sales"}, "hr": {"legal", "accounting"},
	"sales": {"accounting", "legal", "operations"}, "analyst": {"accounting", "sales"}, "operations": {"hr", "accounting"},
}

var topicOfRole = map[string]string{"accounting": "finance", "legal": "legal", "hr": "hr", "sales": "sales", "analyst": "data",
	"operations": "operations", "assistant": "general"}

var topicLabels = map[string]map[string]string{
	"es": {"finance": "finanzas", "legal": "temas legales", "hr": "personas y contratación", "sales": "ventas",
		"data": "datos y análisis", "operations": "operaciones", "general": "lo general"},
	"en": {"finance": "finance", "legal": "legal matters", "hr": "people and hiring", "sales": "sales",
		"data": "data and analysis", "operations": "operations", "general": "general topics"},
}

// localRoute is the deterministic router of the backend.
func localRoute(in RouteRequest) RouteResponse {
	loc := "es"
	if strings.HasPrefix(strings.ToLower(in.Locale), "en") {
		loc = "en"
	}
	byRole := map[string]RouteAgent{}
	byID := map[string]RouteAgent{}
	names := map[string]bool{}
	for _, a := range in.Agents {
		byID[a.ID] = a
		if _, ok := byRole[a.Role]; !ok {
			byRole[a.Role] = a
		}
		for _, t := range tokenRe.FindAllString(normText(a.Name), -1) {
			names[t] = true
		}
	}
	assistant, ok := byRole["assistant"]
	if !ok && len(in.Agents) > 0 {
		assistant = in.Agents[0]
	}
	n := normText(in.Text)
	kind := localSmalltalkKind(n, names)
	intent := domain.IntentQuestion
	switch {
	case kind != "":
		intent = domain.IntentSmalltalk
	case localIsTask(n):
		intent = domain.IntentTask
	}
	scores := topicScores(n)
	primaryRole := ""
	best := 0
	first := map[string]int{}
	for _, r := range topicRules {
		if at := r.re.FindStringIndex(n); at != nil {
			first[r.role] = at[0]
		}
	}
	for role, s := range scores {
		if s > best || (s == best && first[role] < first[primaryRole]) {
			primaryRole, best = role, s
		}
	}
	topic := "general"
	if primaryRole != "" {
		topic = topicOfRole[primaryRole]
	}
	if intent == domain.IntentSmalltalk {
		topic = kind
		if kind == "howare" {
			topic = "greeting"
		}
	}
	label := topicLabels[loc][topic]
	if label == "" {
		label = topicLabels[loc]["general"]
	}
	reason := func(key string) string { return chatText(loc, "reason_"+key) }

	if id, ok := strings.CutPrefix(in.Conversation, domain.ChatAgentPrefix); ok {
		if direct, known := byID[id]; known {
			resp := RouteResponse{Intent: intent, Topic: topic, Source: "local",
				Responders: []RouteResponder{{AgentID: direct.ID, Role: domain.RolePrimary, Reason: reason("direct")}}}
			if owner, has := byRole[primaryRole]; has && (intent == domain.IntentQuestion || intent == domain.IntentTask) &&
				primaryRole != "assistant" && direct.Role != "assistant" && owner.ID != direct.ID {
				resp.Consult = &RouteConsult{AgentID: owner.ID, Reason: strings.NewReplacer("{topic}", label, "{name}", owner.Name).Replace(reason("consult"))}
			}
			return resp
		}
	}
	// office, but the user spoke to one colleague by name: only that person answers
	if intent != domain.IntentSmalltalk {
		if a, ok := addressedAgent(n, in.Agents); ok {
			resp := RouteResponse{Intent: intent, Topic: topic, Source: "local",
				Responders: []RouteResponder{{AgentID: a.ID, Role: domain.RolePrimary, Reason: reason("direct")}}}
			if owner, has := byRole[primaryRole]; has && primaryRole != "assistant" && a.Role != "assistant" && owner.ID != a.ID {
				resp.Consult = &RouteConsult{AgentID: owner.ID, Reason: strings.NewReplacer("{topic}", label, "{name}", owner.Name).Replace(reason("consult"))}
			}
			return resp
		}
	}
	switch intent {
	case domain.IntentSmalltalk:
		out := []RouteResponder{{AgentID: assistant.ID, Role: domain.RolePrimary, Reason: reason("greeting")}}
		k := map[string]int{"greeting": 2, "howare": 1}[kind]
		var peers []RouteAgent
		for _, a := range in.Agents {
			if a.ID != assistant.ID {
				peers = append(peers, a)
			}
		}
		slices.SortFunc(peers, func(a, b RouteAgent) int { return strings.Compare(a.ID, b.ID) })
		if k > len(peers) {
			k = len(peers)
		}
		if k > 0 {
			h := fnv.New32a()
			_, _ = h.Write([]byte(n))
			start := int(h.Sum32() % uint32(len(peers)))
			for i := 0; i < k; i++ {
				out = append(out, RouteResponder{AgentID: peers[(start+i)%len(peers)].ID, Role: domain.RoleContributor, Reason: reason("peer_greeting")})
			}
		}
		return RouteResponse{Intent: intent, Topic: topic, Responders: out, Source: "local"}
	case domain.IntentTask:
		return RouteResponse{Intent: intent, Topic: topic, Source: "local",
			Responders: []RouteResponder{{AgentID: assistant.ID, Role: domain.RolePrimary, Reason: reason("task")}}}
	}
	owner, has := byRole[primaryRole]
	if !has || primaryRole == "assistant" || primaryRole == "" {
		return RouteResponse{Intent: intent, Topic: "general", Source: "local",
			Responders: []RouteResponder{{AgentID: assistant.ID, Role: domain.RolePrimary, Reason: reason("coordinates")}}}
	}
	out := []RouteResponder{{AgentID: owner.ID, Role: domain.RolePrimary, Reason: strings.ReplaceAll(reason("owner"), "{topic}", label)}}
	// one related colleague, only if the text really names their area
	var rel []string
	for _, r := range localRelated[primaryRole] {
		if scores[r] > 0 {
			rel = append(rel, r)
		}
	}
	slices.SortFunc(rel, func(a, b string) int {
		if scores[a] != scores[b] {
			return scores[b] - scores[a]
		}
		return first[a] - first[b]
	})
	if len(rel) > 0 {
		if c, ok := byRole[rel[0]]; ok {
			out = append(out, RouteResponder{AgentID: c.ID, Role: domain.RoleContributor,
				Reason: strings.ReplaceAll(reason("related"), "{topic}", topicLabels[loc][topicOfRole[rel[0]]])})
		}
	}
	return RouteResponse{Intent: intent, Topic: topic, Responders: out, Source: "local"}
}

// addressedAgent finds the one agent called by first name at the start of the message.
func addressedAgent(n string, agents []RouteAgent) (RouteAgent, bool) {
	var toks []string
	for _, t := range tokenRe.FindAllString(n, -1) {
		if enFiller[t] || t == "oye" || t == "ey" || t == "disculpa" || t == "perdona" || t == "sorry" {
			continue
		}
		toks = append(toks, t)
		if len(toks) == 2 {
			break
		}
	}
	var hit RouteAgent
	count := 0
	for _, a := range agents {
		first := tokenRe.FindAllString(normText(a.Name), -1)
		if len(first) == 0 || len(first[0]) < 3 {
			continue
		}
		if slices.Contains(toks, first[0]) {
			hit = a
			count++
		}
	}
	return hit, count == 1
}

var acceptRe = regexp.MustCompile(`^(si|sip|claro|dale|ok|okay|vale|va|perfecto|de acuerdo|adelante|por favor|pasaselo|pasalo|pasamelo|conectame|hazlo|yes|yep|sure|go ahead|please|do it|pass it|pass me)\b|\b(de todos modos|de todas formas|igual|insisto|aun asi|hazlo igual|anyway|i insist|still)\b`)

// isHandoffAccepted: a short "yes, pass it on" (or an insistence) after a redirect.
func isHandoffAccepted(text string) bool {
	n := normText(text)
	return n != "" && len(tokenRe.FindAllString(n, -1)) <= 8 && acceptRe.MatchString(n)
}

// ---- canned texts (es default, en) ----

var chatTexts = map[string]map[string]string{
	"es": {
		"office_title":                "Oficina",
		"direct_title":                "Chat con %s",
		"task_failed":                 "No pude crear la solicitud: %v",
		"blocked_kill_switch_active":  "El equipo está en pausa (interruptor de emergencia activo). No puedo atenderte hasta que se reanude.",
		"blocked_controls_unavailable": "No puedo comprobar el estado del equipo ahora mismo, así que por seguridad no respondo. Inténtalo en un momento.",
		"blocked_agent_paused":        "%s está en pausa ahora mismo y no puede responder.",
		"blocked_other":               "%s no está disponible en este momento.",
		"budget_agent":                "No puedo responder ahora: este agente llegó a su tope mensual de presupuesto ($%.2f de $%.2f). Sube el tope para seguir conversando.",
		"budget_org":                  "No puedo responder ahora: se agotó el presupuesto de la organización ($%.2f de $%.2f).",
		"budget_request":              "No puedo responder ahora: se alcanzó el tope de presupuesto ($%.2f de $%.2f).",
		"plan_intro":                  "Listo, armé un plan de %d tareas. Esto es lo que hará cada quien:",
		"why":                         "Por qué:",
		"plan_outro":                  "Antes de cualquier acción sensible te pediré aprobación. Puedes seguir el avance en Solicitudes.",
		"report_ready":                "El informe está listo: %s. Lo encuentras en Informes.",
		"reason_direct":               "Le escribiste directamente",
		"reason_greeting":             "Responde primero al saludo",
		"reason_peer_greeting":        "Saluda brevemente",
		"reason_task":                 "Coordina la solicitud y reparte el trabajo",
		"reason_coordinates":          "Coordina al equipo y atiende lo general",
		"reason_owner":                "Es quien lleva {topic}",
		"reason_related":              "Puede aportar algo desde {topic}",
		"reason_consult":              "El tema es de {topic}; {name} lo puede aclarar",
		"reason_handoff":              "Aceptaste que te lo pase",
		"reassigned":                  "Reasignada: %s no lleva este tema, así que la toma %s, que %s.",
		"fb_greet_assistant":          "¡Hola! Soy %s. ¿En qué te ayudo?",
		"fb_greet_peer":               "Hola.",
		"fb_greet_self":               "Hola, soy %s. ¿Qué necesitas?",
		"fb_thanks":                   "¡De nada!",
		"fb_task":                     "Entendido, lo coordino con el equipo.",
		"fb_question":                 "Ahora mismo no puedo darte una buena respuesta: el servicio del equipo no responde. Inténtalo de nuevo en un momento.",
	},
	"en": {
		"office_title":                "Office",
		"direct_title":                "Chat with %s",
		"task_failed":                 "I couldn't create the request: %v",
		"blocked_kill_switch_active":  "The team is paused (emergency switch is on). I can't help until it is resumed.",
		"blocked_controls_unavailable": "I can't check the team's status right now, so for safety I won't answer. Please try again in a moment.",
		"blocked_agent_paused":        "%s is paused right now and can't reply.",
		"blocked_other":               "%s is not available at the moment.",
		"budget_agent":                "I can't reply right now: this agent reached its monthly budget cap ($%.2f of $%.2f). Raise the cap to keep chatting.",
		"budget_org":                  "I can't reply right now: the organization's budget is exhausted ($%.2f of $%.2f).",
		"budget_request":              "I can't reply right now: the budget cap was reached ($%.2f of $%.2f).",
		"plan_intro":                  "Done, I put together a plan of %d tasks. Here is what each person will do:",
		"why":                         "Why:",
		"plan_outro":                  "I'll ask for your approval before any sensitive action. You can follow progress under Requests.",
		"report_ready":                "The report is ready: %s. You'll find it under Reports.",
		"reason_direct":               "You wrote to them directly",
		"reason_greeting":             "Answers the greeting first",
		"reason_peer_greeting":        "Says a quick hello",
		"reason_task":                 "Coordinates the request and splits the work",
		"reason_coordinates":          "Coordinates the team and handles general topics",
		"reason_owner":                "Owns {topic}",
		"reason_related":              "Can add something from {topic}",
		"reason_consult":              "This is about {topic}; {name} can clarify it",
		"reason_handoff":              "You accepted the handoff",
		"reassigned":                  "Reassigned: %s doesn't cover this, so %s takes it, who %s.",
		"fb_greet_assistant":          "Hi! I'm %s. How can I help?",
		"fb_greet_peer":               "Hi.",
		"fb_greet_self":               "Hi, I'm %s. What do you need?",
		"fb_thanks":                   "You're welcome!",
		"fb_task":                     "Understood, I'll coordinate it with the team.",
		"fb_question":                 "I can't give you a good answer right now: the team's service isn't responding. Please try again in a moment.",
	},
}

func chatText(loc, key string) string {
	if m, ok := chatTexts[loc]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	return chatTexts["es"][key]
}

// fallbackReply is the canned line used when the runtime cannot answer. It is
// deliberately modest: it never pretends to know anything. Empty = say nothing.
func fallbackReply(loc string, req ChatReplyRequest, agent domain.Agent) string {
	if req.Consult != nil {
		return ""
	}
	switch req.Intent {
	case domain.IntentSmalltalk:
		n := normText(req.Text)
		switch {
		case thanksRe.MatchString(n) && !greetRe.MatchString(n):
			if req.ResponderRole == domain.RoleContributor {
				return ""
			}
			return chatText(loc, "fb_thanks")
		case req.ResponderRole == domain.RoleContributor:
			return chatText(loc, "fb_greet_peer")
		case agent.ID == assistantID:
			return strings.Replace(chatText(loc, "fb_greet_assistant"), "%s", agent.Name, 1)
		default:
			return strings.Replace(chatText(loc, "fb_greet_self"), "%s", agent.Name, 1)
		}
	case domain.IntentTask:
		return chatText(loc, "fb_task")
	}
	if req.ResponderRole == domain.RoleContributor {
		return ""
	}
	return chatText(loc, "fb_question")
}

var roleReasons = map[string]map[string]string{
	"es": {
		"sales": "lleva la relación con el cliente y las propuestas comerciales", "hr": "se ocupa de contratación y normativa laboral",
		"legal": "revisa contratos, cumplimiento y riesgos legales", "accounting": "controla márgenes, costos y facturación",
		"analyst": "analiza datos y rentabilidad con evidencia", "operations": "conoce la capacidad operativa y los plazos de entrega",
		"assistant": "coordina y estructura la solicitud",
	},
	"en": {
		"sales": "owns the client relationship and commercial proposals", "hr": "handles hiring and labor rules",
		"legal": "reviews contracts, compliance and legal risks", "accounting": "controls margins, costs and invoicing",
		"analyst": "analyzes data and profitability with evidence", "operations": "knows operational capacity and delivery deadlines",
		"assistant": "coordinates and structures the request",
	},
}

// defaultAssignReason explains an assignment when the planner gave no reason.
func defaultAssignReason(locale string, a domain.Agent) string {
	loc := chatLocale(RunStyle{Locale: locale})
	if r, ok := roleReasons[loc][a.Role]; ok {
		return r
	}
	if loc == "en" {
		return "is the best fit for this work"
	}
	return "es quien mejor encaja con este trabajo"
}
