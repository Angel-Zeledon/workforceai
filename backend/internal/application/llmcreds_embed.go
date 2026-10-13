package application

import "context"

// ActiveCredential returns the organization's own active model credential
// (bring-your-own-model) and true when the organization runs on its own key;
// false when it uses the platform default. The passage index uses it to embed
// with the organization's OpenAI / OpenAI-compatible key and, because byo_only
// forbids the platform's keys, to stay keyword-only for any other provider.
// The returned value carries a clear key: never log it (RuntimeCredential is redacted when formatted).
func (s *LLMService) ActiveCredential(ctx context.Context) (RuntimeCredential, bool) {
	if s == nil {
		return RuntimeCredential{}, false
	}
	c := s.load(ctx)
	if !c.byo || len(c.creds) == 0 {
		return RuntimeCredential{}, false
	}
	return c.creds[0], true
}
