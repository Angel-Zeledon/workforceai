package meetings

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Deterministic extraction of decisions and action items from notes or a
// transcript, in Spanish, English and Portuguese. It runs on the notes AND on
// the model's answer, so minutes are useful (and testable) with no LLM at all;
// the model improves the summary and finds items phrased in free prose.
//
// Recognized shapes:
//   - Section headings ("Decisions:", "Acuerdos", "Decisões" ... "Action items", "Tareas",
//     "Próximos pasos", "Ações" ...) followed by bullets.
//   - Labels: "Decision: ...", "Acuerdo: ...", "Action: ...", "TODO ...", "[ ] ...".
//   - Phrases: "We decided ...", "Decidimos ...", "Acordamos ...".
//   - "@ana will ..." with a commitment verb.
//   - Transcript lines "Ana: I will send the quote" (first-person commitments).

const (
	maxItems    = 50
	maxItemChar = 300
)

var (
	reBullet   = regexp.MustCompile(`^\s*(?:[-*•–]|\d{1,2}[.)])\s+`)
	reCheckbox = regexp.MustCompile(`^\s*(?:[-*•]\s*)?\[( |x|X)?\]\s*`)
	reDueDate  = regexp.MustCompile(`(?i)\s*(?:\b(?:due|by|vence|para el|hasta el|antes del|até|ate|prazo)\b\s*:?\s*)?(\d{4}-\d{2}-\d{2})\b`)
	reAtOwner  = regexp.MustCompile(`@([\p{L}][\p{L}\p{N}_.-]{0,40})`)
	reOwnerKV  = regexp.MustCompile(`(?i)\(?\s*\b(?:owner|responsable|respons[aá]vel|asignado a|assigned to)\s*[:=]\s*([\p{L}][\p{L}\p{N}_.' -]{0,40}?)\s*(?:[;,)]|$)`)
	reTrailing = regexp.MustCompile(`\(([\p{L}][\p{L}\p{N}_.' -]{0,30})\)\s*[.;]?\s*$`)
	reNameHead = regexp.MustCompile(`^([\p{L}][\p{L}\p{N}_.'-]*(?: [\p{L}][\p{L}\p{N}_.'-]*){0,2}):\s+(\S.*)$`)
	reSpeaker  = regexp.MustCompile(`^\s*\[?(?:\d{1,2}:\d{2}(?::\d{2})?\]?\s*)?([\p{L}][\p{L}\p{N}_.'-]*(?: [\p{L}][\p{L}\p{N}_.'-]*){0,2}):\s+(\S.*)$`)

	decisionHead = []string{"decisions", "decision", "decisiones", "decision log", "decisoes", "decisões", "acuerdos", "acordos", "agreements", "agreed", "resolutions", "resoluciones"}
	actionHead   = []string{"action items", "action item", "actions", "next steps", "todo", "to-do", "to do", "tasks", "follow ups", "follow-ups", "acciones", "acciones a seguir", "tareas", "pendientes", "proximos pasos", "compromisos", "ações", "acoes", "tarefas", "proximos passos", "pendências", "pendencias"}
	otherHead    = []string{"summary", "resumen", "resumo", "agenda", "attendees", "asistentes", "participantes", "participants", "notes", "notas", "topics", "temas", "discussion", "discusion", "discussão", "risks", "riesgos", "riscos", "conclusiones", "conclusions"}

	// "Label: text" prefixes; they need the colon (or a dash) so ordinary prose is not captured.
	decisionLabel = []string{"decision", "decisión", "decisão", "decisao", "acuerdo", "acordo", "agreed", "resolved", "resolución", "resolucion"}
	actionLabel   = []string{"action item", "action", "acción", "accion", "ação", "acao", "todo", "to-do", "tarea", "tarefa", "pendiente", "pendência", "pendencia", "follow up", "follow-up", "next step", "próximo paso", "proximo paso", "próximo passo", "proximo passo"}
	// phrases that make the whole sentence a decision
	decisionPhrase = []string{"we decided", "we agreed", "it was agreed", "decidimos", "acordamos", "se acordó", "se acordo", "se decidió", "se decidio", "quedamos en", "ficou decidido", "combinamos"}
	// first-person commitments in a transcript
	commitStart = []string{"i will ", "i'll ", "i can take", "i am going to ", "i'm going to ", "me encargo", "yo me encargo", "yo lo hago", "lo hago yo", "voy a ", "yo voy a ", "yo envío", "yo envio", "yo preparo", "eu vou ", "eu faço", "eu faco", "eu envio", "eu preparo", "fico responsável", "fico responsavel"}
	// commitment verbs that make an "@owner ..." line an action
	commitVerb = []string{" will ", " to ", " needs to ", " should ", " must ", " debe ", " hará ", " hara ", " se encargará", " se encargara", " tiene que ", " deberá ", " vai ", " deve ", " fará ", " fara "}
)

// Extracted is the result of the deterministic pass.
type Extracted struct {
	Decisions []string
	Actions   []ActionItem
}

// Fold lowercases and strips accents (one rune in, one rune out).
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'á', 'à', 'ä', 'â', 'ã':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô', 'õ':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		case 'ñ':
			r = 'n'
		case 'ç':
			r = 'c'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func headingKind(line string) string {
	t := strings.TrimSpace(line)
	t = strings.TrimLeft(t, "#*_ ")
	t = strings.TrimRight(t, "*_ ")
	t = strings.TrimSpace(strings.TrimSuffix(t, ":"))
	if t == "" || len(t) > 40 {
		return ""
	}
	f := Fold(t)
	for _, h := range decisionHead {
		if f == Fold(h) {
			return "decision"
		}
	}
	for _, h := range actionHead {
		if f == Fold(h) {
			return "action"
		}
	}
	for _, h := range otherHead {
		if f == Fold(h) {
			return "other"
		}
	}
	return ""
}

func cleanItem(s string) string {
	s = reBullet.ReplaceAllString(s, "")
	s = reCheckbox.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	s = strings.Trim(s, " ;*_")
	r := []rune(s)
	if len(r) > maxItemChar {
		s = string(r[:maxItemChar-1]) + "…"
	}
	return s
}

// prefixRest returns the text after one of the prefixes when the line starts with it at a word
// boundary. With needSep the prefix must be followed by ':' or a dash (a label).
func prefixRest(line string, prefixes []string, needSep bool) (string, bool) {
	t := strings.TrimLeft(reBullet.ReplaceAllString(line, ""), "*_ ")
	tr, fr := []rune(t), []rune(Fold(t))
	if len(tr) != len(fr) {
		return "", false
	}
	for _, p := range prefixes {
		fp := []rune(Fold(p))
		if len(fr) < len(fp) || string(fr[:len(fp)]) != string(fp) {
			continue
		}
		rest := tr[len(fp):]
		if len(rest) > 0 && (unicode.IsLetter(rest[0]) || unicode.IsDigit(rest[0])) {
			continue // "action" must not match "actionable"
		}
		s := strings.TrimLeft(string(rest), " ")
		sep := false
		if s != "" && strings.ContainsRune(":-–—", []rune(s)[0]) {
			sep = true
			s = strings.TrimLeft(s, ":-–— ")
		}
		if needSep && !sep {
			continue
		}
		s = strings.Trim(s, " *_")
		if s == "" {
			continue
		}
		return s, true
	}
	return "", false
}

func hasPhrase(line string, phrases []string) bool {
	f := " " + Fold(line)
	for _, p := range phrases {
		if strings.Contains(f, " "+Fold(p)) {
			return true
		}
	}
	return false
}

// parseAction splits owner and due date out of an action's text.
func parseAction(text string) ActionItem {
	it := ActionItem{}
	if m := reDueDate.FindStringSubmatch(text); m != nil {
		it.Due = m[1]
		text = reDueDate.ReplaceAllString(text, "")
	}
	if m := reOwnerKV.FindStringSubmatch(text); m != nil {
		it.Owner = strings.TrimSpace(m[1])
		text = strings.Replace(text, m[0], "", 1)
	}
	if it.Owner == "" {
		if m := reAtOwner.FindStringSubmatch(text); m != nil {
			it.Owner = strings.Trim(m[1], ".-_")
			text = strings.Replace(text, m[0], "", 1)
		}
	}
	if it.Owner == "" {
		if m := reTrailing.FindStringSubmatch(text); m != nil {
			it.Owner = strings.TrimSpace(m[1])
			text = strings.Replace(text, m[0], "", 1)
		}
	}
	if it.Owner == "" {
		if m := reNameHead.FindStringSubmatch(strings.TrimSpace(text)); m != nil && len(m[1]) <= 30 {
			it.Owner = m[1]
			text = m[2]
		}
	}
	it.Text = cleanItem(text)
	return it
}

func isSpeakerNoise(name string) bool {
	switch Fold(name) {
	case "http", "https", "note", "nota", "notes", "notas", "nb", "ps", "re", "fwd", "tip", "ojo", "obs":
		return true
	}
	return false
}

// ExtractMinutes runs the deterministic pass over notes, a transcript or a model answer.
func ExtractMinutes(text string) Extracted {
	var out Extracted
	seenD, seenA := map[string]bool{}, map[string]bool{}
	addD := func(s string) {
		s = cleanItem(s)
		k := Fold(s)
		if s == "" || seenD[k] || len(out.Decisions) >= maxItems {
			return
		}
		seenD[k] = true
		out.Decisions = append(out.Decisions, s)
	}
	addA := func(it ActionItem) {
		if it.Text == "" || len(out.Actions) >= maxItems {
			return
		}
		k := Fold(it.Text) + "|" + Fold(it.Owner)
		if seenA[k] {
			return
		}
		seenA[k] = true
		out.Actions = append(out.Actions, it)
	}

	section := ""
	for _, line := range units(text) {
		if k := headingKind(line); k != "" {
			section = k
			continue
		}
		// a speaker prefix ("Ana: ...") is split off for the transcript rules
		speaker, body := "", line
		if m := reSpeaker.FindStringSubmatch(reBullet.ReplaceAllString(line, "")); m != nil && !isSpeakerNoise(m[1]) {
			if _, isLabel := prefixRest(line, append(append([]string{}, decisionLabel...), actionLabel...), true); !isLabel && headingKind(m[1]) == "" {
				speaker, body = m[1], m[2]
			}
		}
		checked := false
		if m := reCheckbox.FindStringSubmatch(line); m != nil {
			checked = m[1] == "x" || m[1] == "X"
		}

		switch {
		case checked:
			continue // already done
		case reCheckbox.MatchString(line):
			addA(parseAction(line))
			continue
		}
		if rest, ok := prefixRest(line, decisionLabel, true); ok {
			addD(rest)
			continue
		}
		if rest, ok := prefixRest(line, actionLabel, true); ok {
			addA(parseAction(rest))
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(reBullet.ReplaceAllString(line, "")), "TODO") { // "TODO send the quote"
			if rest := strings.TrimLeft(strings.TrimPrefix(strings.TrimSpace(reBullet.ReplaceAllString(line, "")), "TODO"), " :-"); rest != "" {
				addA(parseAction(rest))
				continue
			}
		}
		if hasPhrase(body, decisionPhrase) {
			addD(body)
			continue
		}
		switch section {
		case "decision":
			if reBullet.MatchString(line) { // prose after the list is not a decision
				addD(line)
				continue
			}
		case "action":
			if reBullet.MatchString(line) || reNameHead.MatchString(line) {
				addA(parseAction(reBullet.ReplaceAllString(line, "")))
				continue
			}
		}
		if speaker != "" {
			fb := Fold(body)
			for _, c := range commitStart {
				if strings.HasPrefix(fb, Fold(c)) {
					it := parseAction(body)
					if it.Owner == "" {
						it.Owner = speaker
					}
					addA(it)
					break
				}
			}
			continue
		}
		if reAtOwner.MatchString(line) {
			fl := " " + Fold(line) + " "
			for _, v := range commitVerb {
				if strings.Contains(fl, Fold(v)) {
					addA(parseAction(reBullet.ReplaceAllString(line, "")))
					break
				}
			}
		}
	}
	return out
}

// units cuts the text into lines and splits plain prose lines into sentences, so
// "We decided X. TODO: Ana sends Y" yields two units. Bullets and checkboxes stay whole.
func units(text string) []string {
	var out []string
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if reBullet.MatchString(line) || reCheckbox.MatchString(line) || len(line) < 40 {
			out = append(out, line)
			continue
		}
		out = append(out, splitSentences(line)...)
	}
	return out
}

// splitSentences splits after ". ", "! " or "? " when the next sentence starts with an upper-case
// letter, a digit, "[" or "@" (so decimals, dates and "e.g. x" stay together).
func splitSentences(line string) []string {
	r := []rune(line)
	var out []string
	start := 0
	for i := 0; i+2 < len(r); i++ {
		if (r[i] == '.' || r[i] == '!' || r[i] == '?') && r[i+1] == ' ' {
			j := i + 1
			for j < len(r) && r[j] == ' ' {
				j++
			}
			if j < len(r) && (unicode.IsUpper(r[j]) || unicode.IsDigit(r[j]) || r[j] == '[' || r[j] == '@') {
				out = append(out, strings.TrimSpace(string(r[start:i+1])))
				start = j
				i = j - 1
			}
		}
	}
	out = append(out, strings.TrimSpace(string(r[start:])))
	return out
}

// MergeExtracted adds b to a without duplicates (compared folded).
func MergeExtracted(a, b Extracted) Extracted {
	seenD := map[string]bool{}
	for _, d := range a.Decisions {
		seenD[Fold(d)] = true
	}
	for _, d := range b.Decisions {
		if !seenD[Fold(d)] && len(a.Decisions) < maxItems {
			seenD[Fold(d)] = true
			a.Decisions = append(a.Decisions, d)
		}
	}
	seenA := map[string]bool{}
	for _, it := range a.Actions {
		seenA[Fold(it.Text)] = true
	}
	for _, it := range b.Actions {
		if !seenA[Fold(it.Text)] && len(a.Actions) < maxItems {
			seenA[Fold(it.Text)] = true
			a.Actions = append(a.Actions, it)
		}
	}
	return a
}

// ResolveOwners matches the owner written in the notes with the invited attendees
// (exact name or id, then first name, then a single word-contained match).
func ResolveOwners(items []ActionItem, att []Attendee) []ActionItem {
	out := make([]ActionItem, len(items))
	copy(out, items)
	for i, it := range out {
		if it.Owner == "" {
			continue
		}
		if a, ok := MatchAttendee(it.Owner, att); ok {
			out[i].OwnerID, out[i].OwnerKind = a.ID, a.Kind
			if a.ID == "" {
				out[i].OwnerID = a.Email
			}
			out[i].Owner = a.Name
		}
	}
	return out
}

// MatchAttendee finds the attendee a name refers to.
func MatchAttendee(name string, att []Attendee) (Attendee, bool) {
	n := Fold(strings.TrimSpace(name))
	if n == "" {
		return Attendee{}, false
	}
	for _, a := range att {
		if n == Fold(a.Name) || (a.ID != "" && n == Fold(a.ID)) || (a.Email != "" && n == Fold(a.Email)) {
			return a, true
		}
	}
	var hits []Attendee
	for _, a := range att {
		for _, w := range strings.Fields(Fold(a.Name)) {
			if w == n {
				hits = append(hits, a)
				break
			}
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return Attendee{}, false
}

// SplitNotes cuts the notes into the messages the summarizer receives (speaker
// lines keep their speaker, everything else is "notes"). It reports whether the
// text looks like a transcript.
func SplitNotes(notes string) (msgs [][2]string, transcript bool) {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	nonEmpty, speakers := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if m := reSpeaker.FindStringSubmatch(l); m != nil && !isSpeakerNoise(m[1]) && headingKind(m[1]) == "" {
			speakers++
		}
	}
	transcript = nonEmpty >= 4 && speakers*10 >= nonEmpty*6
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			msgs = append(msgs, [2]string{"notes", s})
		}
		cur.Reset()
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		if transcript {
			if m := reSpeaker.FindStringSubmatch(l); m != nil && !isSpeakerNoise(m[1]) {
				msgs = append(msgs, [2]string{m[1], strings.TrimSpace(m[2])})
				continue
			}
		}
		if cur.Len()+len(l) > 600 {
			flush()
		}
		cur.WriteString(l)
		cur.WriteByte('\n')
	}
	flush()
	if len(msgs) > 300 {
		msgs = msgs[:300]
	}
	return msgs, transcript
}

// LocalSummary is the deterministic summary used when no model is available: the first
// sentences of the prose (headings and bullets of the item sections are skipped).
func LocalSummary(notes string) string {
	var parts []string
	section := ""
	total := 0
	for _, raw := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if k := headingKind(line); k != "" {
			section = k
			continue
		}
		if section == "decision" || section == "action" || reCheckbox.MatchString(line) {
			continue
		}
		if _, ok := prefixRest(line, append(append([]string{}, decisionLabel...), actionLabel...), true); ok {
			continue
		}
		line = cleanItem(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
		total += len(line)
		if len(parts) >= 4 || total > 700 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// ---- rendering ----

type words struct {
	title, date, participants, agenda, summary, decisions, actions, owner, due, none, source, room, link string
}

var wordsBy = map[string]words{
	"es": {"Acta de reunión", "Fecha", "Participantes", "Agenda", "Resumen", "Decisiones", "Acciones", "Responsable", "Fecha límite", "Ninguna.", "Fuente", "Sala", "Enlace"},
	"en": {"Meeting minutes", "Date", "Participants", "Agenda", "Summary", "Decisions", "Action items", "Owner", "Due", "None.", "Source", "Room", "Link"},
	"pt": {"Ata da reunião", "Data", "Participantes", "Pauta", "Resumo", "Decisões", "Ações", "Responsável", "Prazo", "Nenhuma.", "Fonte", "Sala", "Link"},
}

// NormLang maps any language tag to es | en | pt (default es).
func NormLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	switch {
	case strings.HasPrefix(l, "en"):
		return "en"
	case strings.HasPrefix(l, "pt"):
		return "pt"
	}
	return "es"
}

// RuntimeLocale is the locale the model runtime understands (es | en).
func RuntimeLocale(l string) string {
	if NormLang(l) == "en" {
		return "en"
	}
	return "es"
}

// mdEscape keeps third-party text from becoming markdown structure or HTML.
func mdEscape(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "<", "&lt;", ">", "&gt;").Replace(s)
	return strings.TrimSpace(s)
}

// RenderMarkdown renders the minutes document saved to the drive.
func RenderMarkdown(m Meeting, min Minutes) string {
	w := wordsBy[NormLang(min.Lang)]
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n\n", w.title, mdEscape(m.Title))
	fmt.Fprintf(&b, "- **%s:** %s\n", w.date, m.Start.UTC().Format("2006-01-02 15:04 UTC"))
	if m.Room != "" {
		fmt.Fprintf(&b, "- **%s:** %s\n", w.room, mdEscape(m.Room))
	}
	if m.JoinURL != "" {
		fmt.Fprintf(&b, "- **%s:** %s\n", w.link, mdEscape(m.JoinURL))
	}
	if len(m.Attendees) > 0 {
		names := make([]string, 0, len(m.Attendees))
		for _, a := range m.Attendees {
			n := mdEscape(a.Name)
			if a.Kind == KindAgent {
				n += " (AI)"
			}
			names = append(names, n)
		}
		fmt.Fprintf(&b, "- **%s:** %s\n", w.participants, strings.Join(names, ", "))
	}
	b.WriteString("\n")
	if len(m.Agenda) > 0 {
		fmt.Fprintf(&b, "## %s\n\n", w.agenda)
		for i, a := range m.Agenda {
			fmt.Fprintf(&b, "%d. %s", i+1, mdEscape(a.Title))
			if a.Minutes > 0 {
				fmt.Fprintf(&b, " (%d min)", a.Minutes)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## %s\n\n%s\n\n", w.summary, strings.TrimSpace(mdEscape(min.Summary)))
	fmt.Fprintf(&b, "## %s\n\n", w.decisions)
	if len(min.Decisions) == 0 {
		b.WriteString(w.none + "\n")
	}
	for _, d := range min.Decisions {
		fmt.Fprintf(&b, "- %s\n", mdEscape(d))
	}
	fmt.Fprintf(&b, "\n## %s\n\n", w.actions)
	if len(min.ActionItems) == 0 {
		b.WriteString(w.none + "\n")
	}
	for _, it := range min.ActionItems {
		fmt.Fprintf(&b, "- [ ] %s", mdEscape(it.Text))
		if it.Owner != "" {
			fmt.Fprintf(&b, " — %s: %s", w.owner, mdEscape(it.Owner))
		}
		if it.Due != "" {
			fmt.Fprintf(&b, " — %s: %s", w.due, it.Due)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n---\n%s: %s · %s\n", w.source, min.Source, min.CreatedAt.UTC().Format(time.RFC3339))
	return b.String()
}

// FileName is the drive file name of the minutes: "2026-10-20 title-slug (id8).md".
func FileName(m Meeting) string {
	id := m.ID
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return fmt.Sprintf("%s %s (%s).md", m.Start.UTC().Format("2006-01-02"), roomSlug(m.Title), id)
}
