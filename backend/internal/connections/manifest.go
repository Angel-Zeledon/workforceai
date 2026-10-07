package connections

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed providers/*.json
var providersFS embed.FS

// AuthMethod describes how a provider authenticates.
type AuthMethod struct {
	Method         string            `json:"method"` // oauth2_pkce | api_key
	AuthorizeURL   string            `json:"authorize_url,omitempty"`
	TokenURL       string            `json:"token_url,omitempty"`
	RevokeURL      string            `json:"revoke_url,omitempty"`
	IdentityScopes []string          `json:"identity_scopes,omitempty"`
	ExtraParams    map[string]string `json:"extra_params,omitempty"`
	SecretRegex    string            `json:"secret_regex,omitempty"`
}

// CapabilitySpec translates a business capability to scopes, tools and risk.
type CapabilitySpec struct {
	Class              string   `json:"class"` // read | write
	Scopes             []string `json:"scopes"`
	Tools              []string `json:"tools"` // "tool.action"
	Risk               string   `json:"risk"`
	SideEffects        bool     `json:"side_effects"`
	Reversibility      string   `json:"reversibility,omitempty"` // full | compensating | none
	Default            bool     `json:"default"`
	AlwaysApproval     bool     `json:"always_approval,omitempty"`
	HoldSecondsDefault int      `json:"hold_seconds_default,omitempty"`
	LabelKey           string   `json:"label_key"`
	ScopeOverreach     string   `json:"scope_overreach,omitempty"`
}

// Manifest is the data describing a provider (embedded JSON, not code).
type Manifest struct {
	ID               string                    `json:"id"`
	Version          int                       `json:"version"`
	LabelKey         string                    `json:"label_key"`
	Phase            string                    `json:"phase"`
	Auth             []AuthMethod              `json:"auth"`
	Capabilities     map[string]CapabilitySpec `json:"capabilities"`
	NeverOffered     []string                  `json:"never_offered"`
	Identity         map[string]string         `json:"identity,omitempty"`
	ResourceFilters  []string                  `json:"resource_filters"`
	DefaultLimits    Limits                    `json:"default_limits"`
	UntrustedSources []string                  `json:"untrusted_sources"`
}

// Capability returns one capability spec.
func (m Manifest) Capability(name string) (CapabilitySpec, bool) {
	c, ok := m.Capabilities[name]
	return c, ok
}

// CapabilityNames lists capabilities, sorted.
func (m Manifest) CapabilityNames() []string {
	out := make([]string, 0, len(m.Capabilities))
	for k := range m.Capabilities {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AuthMethodFor returns the first auth method of the given family.
func (m Manifest) AuthMethodFor(method string) (AuthMethod, bool) {
	for _, a := range m.Auth {
		if a.Method == method {
			return a, true
		}
	}
	return AuthMethod{}, false
}

// CapabilityForTool maps "tool.action" to a capability of this provider.
func (m Manifest) CapabilityForTool(tool, action string) (string, bool) {
	key := strings.ToLower(tool + "." + action)
	for name, c := range m.Capabilities {
		for _, t := range c.Tools {
			if t == key {
				return name, true
			}
		}
	}
	return "", false
}

// ScopesFor returns the OAuth scopes needed by caps (deduplicated, sorted),
// plus the identity scopes of the auth method.
func (m Manifest) ScopesFor(caps []string, method AuthMethod) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, c := range caps {
		for _, s := range m.Capabilities[c].Scopes {
			add(s)
		}
	}
	for _, s := range method.IdentityScopes {
		add(s)
	}
	sort.Strings(out)
	return out
}

// CapabilitiesGrantedBy returns which of `requested` are covered by the scopes
// the provider really granted (the user may untick scopes on the consent screen).
func (m Manifest) CapabilitiesGrantedBy(requested, granted []string) []string {
	have := map[string]bool{}
	for _, s := range granted {
		have[s] = true
	}
	var out []string
	for _, c := range requested {
		spec := m.Capabilities[c]
		ok := len(spec.Scopes) > 0
		for _, s := range spec.Scopes {
			// gmail.compose covers drafts; a scope that implies another is
			// handled by listing the exact scope per capability.
			if !have[s] {
				ok = false
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// ClassOf returns "read", "write" or "" for an empty set; mixed sets return "mixed".
func (m Manifest) ClassOf(caps []string) string {
	class := ""
	for _, c := range caps {
		k := m.Capabilities[c].Class
		if k == "" {
			k = "read"
			if m.Capabilities[c].SideEffects {
				k = "write"
			}
		}
		switch {
		case class == "":
			class = k
		case class != k:
			return "mixed"
		}
	}
	return class
}

// LoadManifests parses every embedded provider manifest.
func LoadManifests() (map[string]Manifest, error) {
	entries, err := providersFS.ReadDir("providers")
	if err != nil {
		return nil, err
	}
	out := map[string]Manifest{}
	for _, e := range entries {
		raw, err := providersFS.ReadFile("providers/" + e.Name())
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", e.Name(), err)
		}
		if m.ID == "" {
			return nil, fmt.Errorf("manifest %s: missing id", e.Name())
		}
		out[m.ID] = m
	}
	return out, nil
}

// RiskRank orders risk labels (unknown = high, fail closed).
func RiskRank(r string) int {
	switch r {
	case "low":
		return 1
	case "medium":
		return 2
	}
	return 3
}
