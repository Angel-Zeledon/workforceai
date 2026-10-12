package application

import "testing"

// Which native holds can become a standing approval: versioned internal changes
// only, even when the task read other agents' files (tainted); never content
// from outside the organization, nothing irreversible, no extra flags.
func TestNativeStandingRule(t *testing.T) {
	cases := []struct {
		name string
		p    GatewayPending
		want string
	}{
		{"versioned write, autonomy hold", GatewayPending{Reversibility: "versioned", Flags: []string{"autonomy"}}, "native.autonomy"},
		{"tainted by other agents' files", GatewayPending{Reversibility: "versioned", Tainted: true, Flags: []string{"autonomy"}}, "native.autonomy"},
		{"content from outside", GatewayPending{Reversibility: "versioned", ExternalOrigin: true}, "native.restricted"},
		{"delete to trash", GatewayPending{Reversibility: "trash", Flags: []string{"delete"}}, "native.restricted"},
		{"injection suspected", GatewayPending{Reversibility: "versioned", Flags: []string{"autonomy", "injection_suspected"}}, "native.restricted"},
	}
	for _, c := range cases {
		if got := nativeStandingRule(c.p); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
