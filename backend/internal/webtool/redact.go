package webtool

import (
	"regexp"

	"aiworkforce/backend/internal/sanitize"
)

// Extra patterns on top of sanitize.RedactSecrets: provider keys with hyphens inside (sk-ant-api03-...),
// Slack/Stripe-style tokens and Google API keys.
var extraSecrets = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
}

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)

func redactAll(s string, sus *sanitize.Suspects) string {
	s = sanitize.RedactSecrets(s, sus)
	for _, re := range extraSecrets {
		s = re.ReplaceAllString(s, "[REDACTED:secret]")
	}
	return s
}

func hasPersonal(s string) bool {
	return emailRe.MatchString(s) || sanitize.RedactPII(s) != s
}
