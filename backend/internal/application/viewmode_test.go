package application_test

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// UX tanda 1, decision 3 of the owner: new accounts start in simple mode, existing ones and the demo see everything.
func TestViewModeFor(t *testing.T) {
	since := application.SimpleModeSince
	cases := []struct {
		name, org string
		at        time.Time
		want      string
	}{
		{"created before the release keeps everything open", "org-old", since.Add(-time.Hour), "full"},
		{"created at the release starts simple", "org-new", since, "simple"},
		{"created after the release starts simple", "org-new", since.Add(72 * time.Hour), "simple"},
		{"the demo always shows everything", domain.DemoOrgID, since.Add(time.Hour), "full"},
		{"unknown creation date shows everything", "org-x", time.Time{}, "full"},
	}
	for _, c := range cases {
		if got := application.ViewModeFor(c.org, c.at); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

type viewModeStore struct {
	application.ConfigStore
	at time.Time
}

func (s viewModeStore) GetOrgSettings(context.Context, string) (application.OrgSettings, error) {
	return application.DefaultOrgSettings(), nil
}
func (s viewModeStore) ListAgentTones(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (s viewModeStore) OrgCreatedAt(context.Context, string) (time.Time, error) { return s.at, nil }

func TestSettingsCarriesTheViewModeDefault(t *testing.T) {
	ctx := context.Background()
	got, err := (&application.OrgConfig{Cfg: application.Config{OrgID: "org-new"}, Store: viewModeStore{at: application.SimpleModeSince.Add(time.Hour)}}).Settings(ctx)
	if err != nil || got.ViewModeDefault != "simple" {
		t.Fatalf("new org: %q %v", got.ViewModeDefault, err)
	}
	got, _ = (&application.OrgConfig{Cfg: application.Config{OrgID: "org-old"}, Store: viewModeStore{at: application.SimpleModeSince.Add(-time.Hour)}}).Settings(ctx)
	if got.ViewModeDefault != "full" {
		t.Fatalf("old org: %q", got.ViewModeDefault)
	}
}
