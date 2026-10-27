package integrations

import (
	"reflect"
	"testing"
)

func TestParseDefaultsOff(t *testing.T) {
	for _, raw := range []string{"", "off", "FALSE", " 0 ", "none"} {
		p, unk := Parse(raw)
		if p.Any() || p.Enabled("google_gmail") || p.Enabled("slack") || len(unk) != 0 {
			t.Fatalf("%q: vendor integrations must be off", raw)
		}
		if !p.Enabled("custom_api") {
			t.Fatalf("%q: the generic API source is never gated", raw)
		}
	}
	var zero Policy
	if zero.Enabled("github") || !zero.Enabled("custom_api") {
		t.Fatal("the zero policy is the v1 default (vendors off, generic on)")
	}
}

func TestParseOnAndList(t *testing.T) {
	for _, raw := range []string{"on", "true", "1", "ALL"} {
		p, _ := Parse(raw)
		for _, id := range Vendors {
			if !p.Enabled(id) {
				t.Fatalf("%q: %s should be on", raw, id)
			}
		}
	}
	p, unk := Parse("github, slack ,nope,")
	if !p.Enabled("github") || !p.Enabled("slack") || p.Enabled("google_gmail") || !p.Any() {
		t.Fatal("a list turns on only the listed ids")
	}
	if !reflect.DeepEqual(unk, []string{"nope"}) {
		t.Fatalf("unknown = %v", unk)
	}
	st := p.State()
	if !st.Enabled || st.All || !reflect.DeepEqual(st.Providers, []string{"github", "slack"}) {
		t.Fatalf("state = %+v", st)
	}
	if s := (Policy{}).State(); s.Enabled || s.Providers == nil || len(s.Providers) != 0 {
		t.Fatalf("off state = %+v", s)
	}
}
