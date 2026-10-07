package audit

import (
	"regexp"
	"strings"

	"aiworkforce/backend/internal/sanitize"
)

// The audit trail stores metadata, never content or secrets. Scrub is applied
// to every entry before it is chained (see application.Recorder.Audit), so a
// careless caller cannot put a message body, tool arguments or a credential
// into the trail.

const (
	redacted     = "[redacted]"
	maxString    = 300
	maxDepth     = 6
	maxArrayLen  = 50
	maxMapFields = 100
)

// contentKeys hold user, model or third-party content: replaced wholesale.
var contentKeys = map[string]bool{
	"args": true, "arguments": true, "body": true, "content": true, "text": true, "html": true, "snippet": true,
	"preview": true, "subject": true, "output": true, "summary": true, "findings": true, "hypotheses": true,
	"evidence": true, "recommendations": true, "sections": true, "persona": true, "question": true, "answer": true,
	"prompt": true, "description": true, "details": true, "context": true, "memory": true, "value": true,
}

// isSecretKey reports credential-like keys. Counters such as "input_tokens"
// are metadata and are kept.
func isSecretKey(k string) bool {
	switch {
	case k == "token" || strings.HasSuffix(k, "_token") || k == "authorization" || k == "cookie" || k == "set_cookie":
		return true
	}
	for _, frag := range []string{"secret", "password", "passwd", "api_key", "apikey", "credential", "private_key"} {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// Scrub returns a copy of details that is safe to store: content keys and
// credential-like keys are replaced by "[redacted]", secrets embedded in
// strings are masked and long strings are truncated. It works on the value
// obtained after a JSON round trip, so structs and maps are handled alike.
func Scrub(details any) any {
	if details == nil {
		return nil
	}
	return scrub(NormalizeDetails(details), 0)
}

func scrub(v any, depth int) any {
	if depth > maxDepth {
		return "[truncated]"
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		n := 0
		for k, vv := range t {
			if n++; n > maxMapFields {
				out["_truncated"] = true
				break
			}
			lk := strings.ToLower(k)
			if contentKeys[lk] || isSecretKey(lk) {
				// Keep the shape (that the field existed) without its value.
				if vv != nil {
					out[k] = redacted
				}
				continue
			}
			out[k] = scrub(vv, depth+1)
		}
		return out
	case []any:
		if len(t) > maxArrayLen {
			t = t[:maxArrayLen]
		}
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = scrub(vv, depth+1)
		}
		return out
	case string:
		return clean(t)
	}
	return v
}

// uuidRe: identifiers are not secrets. The card-number masker would otherwise
// mangle all-digit UUIDs such as the demo organization id.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func clean(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if uuidRe.MatchString(s) {
		return s
	}
	s = sanitize.RedactSecrets(s, nil)
	if r := []rune(s); len(r) > maxString {
		s = string(r[:maxString]) + "…"
	}
	return s
}

// CleanField scrubs a plain string column (actor, entity id...).
func CleanField(s string) string { return clean(s) }
