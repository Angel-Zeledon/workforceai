package sanitize

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Redaction profiles (connection_grants.redaction_profile).
const (
	ProfileStrict    = "strict" // default: secrets + tax ids, accounts, phones
	ProfileStandard  = "standard"
	ProfileNoneAdmin = "none_admin_only" // secrets are STILL redacted
)

// DefaultProfile is the profile used when none is configured.
const DefaultProfile = ProfileStrict

// TrustExternal marks content written by third parties.
const TrustExternal = "external_untrusted"

// Options tune Sanitize.
type Options struct {
	Profile  string
	MaxChars int // per item (default 4000)
	Suspects *Suspects
}

// Output is a sanitized item.
type Output struct {
	Text             string   `json:"text"`
	Truncated        bool     `json:"truncated,omitempty"`
	InjectionScore   float64  `json:"injection_score"`
	InjectionSuspect bool     `json:"injection_suspected"`
	Signals          []string `json:"signals,omitempty"`
}

// InjectionThreshold is the score at/above which an item is flagged.
const InjectionThreshold = 0.5

var (
	reScriptOpen = regexp.MustCompile(`(?i)<(script|style|head|template|noscript)\b[^>]*>`)
	reComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHiddenOpen = regexp.MustCompile(`(?is)<([a-z0-9]+)\b[^>]*(?:display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0|opacity\s*:\s*0|\bhidden\b|aria-hidden\s*=\s*["']true)[^>]*>`)
	reBlock      = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|table|blockquote|ul|ol)\b[^>]*>`)
	reTag        = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces     = regexp.MustCompile(`[ \t\f\v]+`)
	reNewlines   = regexp.MustCompile(`\n{3,}`)
	reMDImage    = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]*)[^)]*\)`)
	reMDLink     = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]*)[^)]*\)`)
	reCloseTag   = regexp.MustCompile(`(?i)<\s*/?\s*untrusted[_\s-]*data[^>]*>?`)
	reQuoted     = regexp.MustCompile(`(?m)^>.*$`)
	reOnWrote    = regexp.MustCompile(`(?is)\n(?:On .{5,200}wrote:|El .{5,200}escribió:).*$`)
)

// HTMLToText strips markup, comments, scripts and visually hidden elements.
func HTMLToText(s string) string {
	if !strings.ContainsAny(s, "<>") {
		return html.UnescapeString(s)
	}
	s = reComment.ReplaceAllString(s, "")
	s = removeElements(s, reScriptOpen)
	s = removeElements(s, reHiddenOpen)
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	return html.UnescapeString(s)
}

// removeElements deletes every element whose opening tag matches open (group 1
// is the tag name) up to its closing tag (RE2 has no backreferences). An
// element without a closing tag is removed to the end of the document: failing
// closed is the safe direction here.
func removeElements(s string, open *regexp.Regexp) string {
	for i := 0; i < 200; i++ {
		loc := open.FindStringSubmatchIndex(s)
		if loc == nil {
			return s
		}
		name := strings.ToLower(s[loc[2]:loc[3]])
		rest := strings.ToLower(s[loc[1]:])
		end := len(s)
		if j := strings.Index(rest, "</"+name); j >= 0 {
			end = loc[1] + j
			if k := strings.Index(s[end:], ">"); k >= 0 {
				end += k + 1
			} else {
				end = len(s)
			}
		}
		s = s[:loc[0]] + s[end:]
	}
	return s
}

// Normalize applies NFKC, removes zero-width/bidi/control characters and
// collapses whitespace.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
		case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Cc, r), r == 0x2028, r == 0x2029, r == 0xFEFF:
			// zero-width, bidi overrides, control characters
		default:
			b.WriteRune(r)
		}
	}
	s = reSpaces.ReplaceAllString(b.String(), " ")
	return strings.TrimSpace(reNewlines.ReplaceAllString(s, "\n\n"))
}

// StripQuotedThread drops quoted reply history (cuts volume and injection surface).
func StripQuotedThread(s string) string {
	s = reOnWrote.ReplaceAllString(s, "")
	return strings.TrimSpace(reQuoted.ReplaceAllString(s, ""))
}

// Neutralize defuses delimiter forgery and render-time exfiltration: markdown
// images and links become "[link: domain]".
func Neutralize(s string) string {
	s = reCloseTag.ReplaceAllString(s, "[delimiter removed]")
	s = reMDImage.ReplaceAllStringFunc(s, func(m string) string {
		return "[image: " + domainOf(reMDImage.FindStringSubmatch(m)[1]) + "]"
	})
	s = reMDLink.ReplaceAllStringFunc(s, func(m string) string {
		g := reMDLink.FindStringSubmatch(m)
		return g[1] + " [link: " + domainOf(g[2]) + "]"
	})
	return s
}

func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return u.Hostname()
}

var injectionSignals = []struct {
	name   string
	weight float64
	re     *regexp.Regexp
}{
	{"ignore_instructions", 0.6, regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b[^.\n]{0,40}\b(previous|prior|above|earlier|all|your)\b[^.\n]{0,30}\b(instructions?|rules?|prompts?|guidelines?)\b`)},
	{"ignore_instructions_es", 0.6, regexp.MustCompile(`(?i)\b(ignora|olvida|descarta|omite)\b[^.\n]{0,40}\b(anteriores|previas|todas|tus)\b[^.\n]{0,30}\b(instrucciones|reglas|indicaciones)\b`)},
	{"role_spoof", 0.4, regexp.MustCompile(`(?im)^\s*(system|assistant|developer|sistema)\s*:|\byou are now\b|\bahora eres\b|\bnew instructions?\b|\bnuevas instrucciones\b`)},
	{"exfiltration", 0.5, regexp.MustCompile(`(?i)\b(forward|send|email|reenv[ií]a|env[ií]a|manda)\b[^.\n]{0,60}\b(all|every|todos?|contracts?|contratos?|emails?|correos?|passwords?|credentials?)\b[^.\n]{0,60}\b(to|a)\b\s*[\w.+-]+@[\w-]+\.[\w.-]+`)},
	{"skip_approval", 0.6, regexp.MustCompile(`(?i)(do not|don't|never|sin|no)\s+(ask|request|require|pidas|solicites)[^.\n]{0,30}(approval|confirmation|permission|aprobaci[oó]n|confirmaci[oó]n)|\byou are authorized\b|\bestás autorizad[oa]\b`)},
	{"payment", 0.3, regexp.MustCompile(`(?i)\b(pay|transfer|wire|paga|transfiere)\b[^.\n]{0,30}[$€]\s?\d`)},
	{"tool_call_text", 0.4, regexp.MustCompile(`(?i)\b(email\.send|send_email|tool_requests?|"tool"\s*:)`)},
}

// ScoreInjection returns a heuristic score in [0,1] and the signals matched.
// It is a signal, not a barrier: the real defenses are grants, approvals,
// taint tracking and limits.
func ScoreInjection(s string) (float64, []string) {
	score := 0.0
	var sig []string
	for _, p := range injectionSignals {
		if p.re.MatchString(s) {
			score += p.weight
			sig = append(sig, p.name)
		}
	}
	if score > 1 {
		score = 1
	}
	return score, sig
}

func truncateRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]) + " [truncated]", true
}

// Sanitize runs the whole pipeline on one text item: normalize, redact,
// neutralize, truncate, score. The text is NOT yet wrapped; see Wrap.
func Sanitize(raw string, o Options) Output {
	if o.MaxChars <= 0 {
		o.MaxChars = 4000
	}
	t := HTMLToText(raw)
	t = Normalize(t)
	// Score on the normalized text BEFORE truncation/redaction so that an
	// attacker cannot hide the payload behind them.
	score, sig := ScoreInjection(t)
	t = StripQuotedThread(t)
	t = RedactSecrets(t, o.Suspects)
	if o.Profile == "" || o.Profile == ProfileStrict {
		t = RedactPII(t)
	}
	t = Neutralize(t)
	t, trunc := truncateRunes(t, o.MaxChars)
	return Output{Text: t, Truncated: trunc, InjectionScore: score, InjectionSuspect: score >= InjectionThreshold, Signals: sig}
}

// SanitizeInline is Sanitize for short single-line fields (subject, sender).
func SanitizeInline(raw string, max int, o Options) string {
	o.MaxChars = max
	out := Sanitize(raw, o)
	return strings.ReplaceAll(out.Text, "\n", " ")
}

// Wrap delimits sanitized text as untrusted data. Attributes are escaped so
// they cannot close the tag, and the body cannot contain the closing tag
// (Neutralize already removed it).
func Wrap(id, source, from, text string) string {
	return fmt.Sprintf(`<untrusted_data id=%q source=%q from=%q trust=%q>`+"\n%s\n"+`</untrusted_data>`,
		attr(id), attr(source), attr(from), TrustExternal, reCloseTag.ReplaceAllString(text, "[delimiter removed]"))
}

func attr(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '<' || r == '>' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
