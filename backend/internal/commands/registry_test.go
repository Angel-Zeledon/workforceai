package commands_test

import (
	"reflect"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/commands"
)

func TestAliasesAreUniqueAcrossLanguagesAndCommands(t *testing.T) {
	owner := map[string]string{}
	for _, c := range commands.All() {
		if len(c.Aliases.ES) == 0 || len(c.Aliases.EN) == 0 {
			t.Errorf("%s needs an alias in both languages", c.ID)
		}
		seen := map[string]bool{}
		for _, a := range append(append([]string{c.ID}, c.Aliases.ES...), c.Aliases.EN...) {
			f := commands.Fold(a)
			if f == "" || !commands.IsWord(f) {
				t.Errorf("%s: alias %q is not a plain word", c.ID, a)
			}
			if o, ok := owner[f]; ok && o != c.ID {
				t.Errorf("alias %q belongs to both %s and %s", f, o, c.ID)
			}
			owner[f] = c.ID
			seen[f] = true
		}
	}
	for alias, id := range commands.AllAliases() {
		if owner[alias] != id {
			t.Errorf("lookup table disagrees on %q", alias)
		}
	}
}

func TestEveryCommandIsComplete(t *testing.T) {
	known := map[string]bool{}
	for _, r := range []auth.Role{auth.RoleViewer, auth.RoleMember, auth.RoleAdmin, auth.RoleOwner} {
		for _, p := range r.Permissions() {
			known[string(p)] = true
		}
	}
	groups := map[string]bool{commands.GroupControl: true, commands.GroupOffice: true, commands.GroupOrg: true}
	kinds := map[string]bool{"agent": true, "project": true, "file": true, "skill": true, "template": true, "text": true, "enum": true, "number": true}
	ids := map[string]bool{}
	for _, c := range commands.All() {
		if ids[c.ID] {
			t.Errorf("duplicate id %s", c.ID)
		}
		ids[c.ID] = true
		if !known[c.Permission] {
			t.Errorf("%s: permission %q is not a real permission", c.ID, c.Permission)
		}
		if c.WritePermission != "" && !known[c.WritePermission] {
			t.Errorf("%s: write permission %q is not a real permission", c.ID, c.WritePermission)
		}
		if !groups[c.Group] || c.Icon == "" || len(c.Surfaces) == 0 {
			t.Errorf("%s: group/icon/surfaces missing", c.ID)
		}
		for _, l := range []commands.L{c.Title, c.Description, c.Example} {
			if l.ES == "" || l.EN == "" {
				t.Errorf("%s: a text lacks a language", c.ID)
			}
		}
		if !strings.HasPrefix(c.Example.ES, "/") || !strings.HasPrefix(c.Example.EN, "/") {
			t.Errorf("%s: examples must start with /", c.ID)
		}
		// the example of each language must use an alias of that language
		for lang, ex := range map[string]string{"es": c.Example.ES, "en": c.Example.EN} {
			word, _, ok := commands.Split(ex)
			got, aliasLang, found := commands.Lookup(word)
			if !ok || !found || got.ID != c.ID || (aliasLang != "" && aliasLang != lang) {
				t.Errorf("%s: %s example %q does not use a %s alias of the command", c.ID, lang, ex, lang)
			}
		}
		for _, a := range c.Args {
			if !kinds[a.Kind] {
				t.Errorf("%s: arg %s has kind %q", c.ID, a.Name, a.Kind)
			}
			if a.Kind == "enum" && len(a.Enum) == 0 {
				t.Errorf("%s: enum arg %s has no values", c.ID, a.Name)
			}
		}
	}
	// the commands the owner asked for are all there
	for _, want := range []string{"clear", "compact", "context", "usage", "model", "memory", "pause", "resume", "stop", "status", "trace", "export",
		"excel", "word", "pdf", "presentation", "meeting", "assign", "project", "daily-summary", "pending", "search", "file", "email", "linkedin",
		"remember", "tone", "language", "save-template", "skill", "learn", "undo", "team", "hire", "permissions", "shop", "inventory"} {
		if !ids[want] {
			t.Errorf("command %s is missing", want)
		}
	}
}

func TestDestructiveCommands(t *testing.T) {
	var got []string
	for _, c := range commands.All() {
		if c.Destructive {
			got = append(got, c.ID)
		}
	}
	if want := []string{"memory", "stop", "undo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("destructive = %v, want %v", got, want)
	}
}

func TestBothLanguagesAlwaysResolve(t *testing.T) {
	for _, tc := range []struct{ word, id, lang string }{
		{"resumir", "compact", "es"}, {"/compact", "compact", "en"}, {"Resumir", "compact", "es"}, {"summarize", "compact", "en"},
		{"reunión", "meeting", "es"}, {"MEETING", "meeting", "en"}, {"pdf", "pdf", ""}, {"excel", "excel", ""}, {"limpiar", "clear", "es"},
		{"uso", "usage", "es"}, {"cost", "usage", "en"}, {"abrir", "file", "es"}, {"open", "file", "en"},
	} {
		c, lang, ok := commands.Lookup(tc.word)
		if !ok || c.ID != tc.id || lang != tc.lang {
			t.Errorf("Lookup(%q) = %s %q %v, want %s %q", tc.word, c.ID, lang, ok, tc.id, tc.lang)
		}
	}
	if _, _, ok := commands.Lookup("nope"); ok {
		t.Fatal("unknown word resolved")
	}
}

func TestSplit(t *testing.T) {
	for raw, want := range map[string][2]string{"/resumir": {"resumir", ""}, "  /uso  hoy ": {"uso", "hoy"}, "/reunion a b: c": {"reunion", "a b: c"}} {
		w, r, ok := commands.Split(raw)
		if !ok || w != want[0] || r != want[1] {
			t.Errorf("Split(%q) = %q %q %v", raw, w, r, ok)
		}
	}
	for _, raw := range []string{"hola", "/", "/ x", "//x", "/a/b"} {
		if _, _, ok := commands.Split(raw); ok {
			t.Errorf("Split(%q) should not be a command", raw)
		}
	}
}

func cmd(t *testing.T, id string) commands.Command {
	c, ok := commands.Get(id)
	if !ok {
		t.Fatal(id)
	}
	return c
}

func TestParseArgs(t *testing.T) {
	isAgent := func(w string) bool { return w == "ventas" || w == "legal" || w == "contabilidad" }
	o := commands.ParseOpts{IsAgent: isAgent}
	got := commands.ParseArgs(cmd(t, "meeting"), "ventas legal contabilidad: precios 2027", o)
	if !reflect.DeepEqual(got, map[string]any{"agents": []any{"ventas", "legal", "contabilidad"}, "topic": "precios 2027"}) {
		t.Errorf("meeting = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "meeting"), "@ventas, @legal precios 2027", o)
	if !reflect.DeepEqual(got, map[string]any{"agents": []any{"ventas", "legal"}, "topic": "precios 2027"}) {
		t.Errorf("meeting without colon = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "assign"), "@ventas prepara la   cotización", o)
	if got["agent"] != "ventas" || got["task"] != "prepara la   cotización" {
		t.Errorf("assign = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "remember"), "el cliente prefiere lunes @ventas", o)
	if got["agent"] != "ventas" || got["text"] != "el cliente prefiere lunes" {
		t.Errorf("remember = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "memory"), "olvidar el precio", o)
	if got["action"] != "forget" || got["text"] != "el precio" {
		t.Errorf("memory = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "memory"), "borrar", o)
	if got["action"] != "delete" {
		t.Errorf("memory delete = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "export"), "pdf", o)
	if got["format"] != "pdf" {
		t.Errorf("export = %#v", got)
	}
	got = commands.ParseArgs(cmd(t, "tone"), "mx @ventas", o)
	if got["value"] != "mx" || got["agent"] != "ventas" {
		t.Errorf("tone = %#v", got)
	}
	if got := commands.ParseArgs(cmd(t, "usage"), "", o); len(got) != 0 {
		t.Errorf("no args = %#v", got)
	}
}

func TestDetectLang(t *testing.T) {
	for text, want := range map[string]string{"resumen del día para el cliente": "es", "summary of the meeting with the client": "en", "": "", "xyz": ""} {
		if got := commands.DetectLang(text); got != want {
			t.Errorf("DetectLang(%q) = %q, want %q", text, got, want)
		}
	}
}
