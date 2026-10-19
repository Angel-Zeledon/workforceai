package notify

import (
	"context"
	"log/slog"
)

// Sealer encrypts short secrets under the organization's data key. *vault.Vault implements it.
type Sealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

// Seal purposes (bound as AAD, so a sealed Slack URL cannot be replayed as a Teams URL or as an outbox payload).
const (
	sealSlack  = "notify-webhook-slack"
	sealTeams  = "notify-webhook-teams"
	sealOutbox = "notify-outbox"
)

func sealPurpose(kind string) string {
	if kind == KindTeams {
		return sealTeams
	}
	return sealSlack
}

func sealOn(s Sealer) bool { return s != nil && s.Enabled() }

// sealHook returns what to store for a webhook URL: the sealed bytes (nil when empty or when the deployment has no
// vault key) and the plaintext column ("" whenever the URL was sealed). With no vault key the URL stays in the
// plaintext column, exactly as before migration 783.
func sealHook(ctx context.Context, s Sealer, org, kind, hook string) (enc []byte, plain string, err error) {
	if hook == "" {
		return nil, "", nil
	}
	if !sealOn(s) {
		return nil, hook, nil
	}
	enc, err = s.Seal(ctx, org, sealPurpose(kind), []byte(hook))
	if err != nil {
		return nil, "", err
	}
	return enc, "", nil
}

// openHook returns the webhook URL from the two columns. reseal is true when the URL is still in the plaintext column
// and the deployment can seal it now (rows written before 783). A sealed value that cannot be opened (key removed or
// rotated away) yields "": the channel is off until an administrator saves the URL again, and the failure is logged
// without the secret.
func openHook(ctx context.Context, s Sealer, log *slog.Logger, org, kind string, enc []byte, plain string) (hook string, reseal bool) {
	if len(enc) > 0 {
		if !sealOn(s) {
			log.Warn("notify: sealed webhook but no vault key", "org", org, "kind", kind)
			return "", false
		}
		b, err := s.Unseal(ctx, org, sealPurpose(kind), enc)
		if err != nil {
			log.Warn("notify: sealed webhook cannot be opened", "org", org, "kind", kind)
			return "", false
		}
		return string(b), false
	}
	return plain, plain != "" && sealOn(s)
}
