// Package integrations holds the v1 stance on vendor-specific integrations (docs/architecture/integrations-credentials.md,
// section "v1: generic imports only"). Version 1 ships only generic ways to bring data in (the generic API source
// custom_api, link imports, uploads); connectors tied to one product (Gmail, Google Calendar/Drive, GitHub, LinkedIn,
// Microsoft 365 calendar, Slack) and the two-way Slack channel stay in the code but are switched off unless the
// operator turns them on with VENDOR_INTEGRATIONS.
//
//	VENDOR_INTEGRATIONS=""|off|false|0   default: every vendor integration is off
//	VENDOR_INTEGRATIONS=on|true|1|all    every vendor integration is on (pre-v1 behavior)
//	VENDOR_INTEGRATIONS=github,slack     only the listed provider ids are on ("slack" also covers the Slack channel)
//
// Generic providers (custom_api) are never affected.
package integrations

import (
	"sort"
	"strings"
)

// EnvVar is the environment variable read by config.Load.
const EnvVar = "VENDOR_INTEGRATIONS"

// CodeDisabled is the stable error code returned when a vendor integration is used while it is off.
const CodeDisabled = "vendor_integrations_disabled"

// SlackChannel is the id that gates the two-way Slack channel (the same id as the Slack connector on purpose:
// turning "slack" on turns both on).
const SlackChannel = "slack"

// generic providers are not tied to any product: they are always available.
var generic = map[string]bool{"custom_api": true}

// Vendors lists the vendor-specific provider ids known to this build (manifests under
// internal/connections/providers plus the Slack channel). Unknown ids in a list are ignored (and reported by Parse).
var Vendors = []string{"github", "google_calendar", "google_calendar_sync", "google_drive", "google_gmail", "linkedin", "microsoft_calendar", "slack"}

// IsGeneric reports whether a provider is generic (never gated).
func IsGeneric(id string) bool { return generic[id] }

// Policy says which vendor integrations are on. The zero value turns every vendor integration OFF (the v1 default).
type Policy struct {
	all bool
	ids map[string]bool
}

// All turns every vendor integration on (pre-v1 behavior; tests).
func All() Policy { return Policy{all: true} }

// Only turns on the listed provider ids.
func Only(ids ...string) Policy {
	p := Policy{ids: map[string]bool{}}
	for _, id := range ids {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			p.ids[id] = true
		}
	}
	return p
}

// Parse reads the VENDOR_INTEGRATIONS value. unknown lists ids that are not known vendor providers (they are
// ignored; the caller logs them).
func Parse(raw string) (p Policy, unknown []string) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "off", "false", "0", "no", "none":
		return Policy{}, nil
	case "on", "true", "1", "yes", "all":
		return All(), nil
	}
	known := map[string]bool{}
	for _, id := range Vendors {
		known[id] = true
	}
	var ids []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !known[part] {
			unknown = append(unknown, part)
			continue
		}
		ids = append(ids, part)
	}
	return Only(ids...), unknown
}

// Enabled reports whether a provider (or the Slack channel) may be used. Generic providers always may.
func (p Policy) Enabled(id string) bool {
	if IsGeneric(id) {
		return true
	}
	return p.all || p.ids[id]
}

// Any reports whether at least one vendor integration is on.
func (p Policy) Any() bool { return p.all || len(p.ids) > 0 }

// State is what the frontend reads (GET /org/settings -> vendor_integrations).
type State struct {
	// Enabled is true when at least one vendor integration is on.
	Enabled bool `json:"enabled"`
	// All is true when every vendor integration is on.
	All bool `json:"all"`
	// Providers lists the vendor provider ids that are on (every known one when All).
	Providers []string `json:"providers"`
}

// State describes the policy for the UI.
func (p Policy) State() State {
	s := State{Enabled: p.Any(), All: p.all, Providers: []string{}}
	for _, id := range Vendors {
		if p.Enabled(id) {
			s.Providers = append(s.Providers, id)
		}
	}
	sort.Strings(s.Providers)
	return s
}
