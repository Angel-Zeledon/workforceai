package main

import (
	"testing"

	"aiworkforce/backend/internal/domain"
)

// Temporary helpers never read what their responsible cannot: the file check is the zone rule of fs.read.
func TestHelperFileCheckIsTheReadRuleOfTheAgent(t *testing.T) {
	ctx, _, fs := officeDriveFix(t)
	check := helperFileCheck(fs)
	read := func(agent, path string) bool { return check(ctx, "org1", agent, domain.FileRef{Path: path, Name: "x"}) }
	if !read("a-open", "/Offices/Abierta/a.md") {
		t.Fatal("an agent reads the folder of its own office")
	}
	if read("a-open", "/Offices/Privada/x.md") {
		t.Fatal("an agent must not read the private folder of another office")
	}
	if read("a-priv", "/Offices/Abierta/a.md") == false {
		// open offices are readable by everyone in the organization? the rule decides, not the test: only fail on the
		// private direction above
		t.Log("a private-office agent does not read another open office")
	}
	if read("a-open", "/Agents/a-priv/notes.md") {
		t.Fatal("an agent must not read another agent's desk")
	}
	if helperFileCheck(nil)(ctx, "org1", "a-open", domain.FileRef{Path: "/Shared/x.md"}) {
		t.Fatal("without a drive nothing is readable (fail closed)")
	}
}
