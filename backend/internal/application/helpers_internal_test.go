package application

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// gwStub classifies tool calls like the Tool Gateway: reads, writes and unknown tools.
type gwStub struct{ ToolGateway }

func (gwStub) Route(_ context.Context, _, _, tool, action string) GatewayRoute {
	switch tool + "." + action {
	case "fs.read", "fs.list", "fs.search", "crm.search":
		return RouteRead
	case "fs.write", "fs.delete", "email.send":
		return RouteWrite
	}
	return RouteNone
}

func TestHelperToolWhitelistFailsClosed(t *testing.T) {
	o := &Orchestrator{cfg: DefaultConfig()}
	ctx := context.Background()
	agent := domain.Agent{ID: "a", Tools: []string{"fs", "web", "email"}}
	read := func(tool, action string) ToolRequest { return ToolRequest{Tool: tool, Action: action, Risk: "low"} }

	// No gateway: nothing but the web (served by its own flow) is allowed.
	if o.helperToolAllowed(ctx, agent, read("fs", "read")) || o.helperToolAllowed(ctx, agent, read("crm", "search")) {
		t.Fatal("without a gateway the reads cannot be classified: refuse")
	}
	if !o.helperToolAllowed(ctx, agent, read("web", "search")) || !o.helperToolAllowed(ctx, agent, read("web", "fetch")) {
		t.Fatal("web search and fetch are reads")
	}
	if o.helperToolAllowed(ctx, agent, read("web", "post")) || o.helperToolAllowed(ctx, domain.Agent{ID: "b", Tools: []string{"fs"}}, read("web", "search")) {
		t.Fatal("only the web actions of a responsible that holds the web")
	}

	o.conn.gw = gwStub{}
	for _, tr := range []ToolRequest{read("fs", "read"), read("fs", "list"), read("fs", "search"), read("crm", "search")} {
		if !o.helperToolAllowed(ctx, agent, tr) {
			t.Errorf("%s.%s is a read the responsible has: allow", tr.Tool, tr.Action)
		}
	}
	for _, tr := range []ToolRequest{read("fs", "write"), read("fs", "delete"), read("email", "send"), read("code", "run"), read("crm", "update"),
		read("", ""), read("fs", ""), {Tool: "email", Action: "send", Risk: "high"}} {
		if o.helperToolAllowed(ctx, agent, tr) {
			t.Errorf("%s.%s must be refused", tr.Tool, tr.Action)
		}
	}
	// The drive read needs the drive tool on the responsible itself.
	if o.helperToolAllowed(ctx, domain.Agent{ID: "c", Tools: []string{"email"}}, read("fs", "read")) {
		t.Fatal("a responsible without the drive cannot lend it")
	}
	// Read-only label of the drive is enough for reads.
	if !o.helperToolAllowed(ctx, domain.Agent{ID: "d", Tools: []string{"fs_read"}}, read("fs", "read")) {
		t.Fatal("fs_read allows drive reads")
	}
	if got := helperToolLabels(agent); len(got) != 2 || got[0] != "fs_read" || got[1] != "web" {
		t.Fatalf("labels %v", got)
	}
}

func TestMergingPartsKeepsEveryTextAndFile(t *testing.T) {
	parts := []domain.HelperPart{
		{Index: 1, Label: "A", Text: "uno", Files: []domain.FileRef{{Path: "/a"}}},
		{Index: 2, Label: "B", Text: "dos"},
		{Index: 3, Label: "C", Text: "tres", Files: []domain.FileRef{{Path: "/c"}}},
		{Index: 4, Label: "D", Text: "cuatro"},
		{Index: 5, Label: "E", Text: "cinco"},
	}
	out := normalizeHelperParts(parts, 2)
	if len(out) != 2 || out[0].Index != 1 || out[1].Index != 2 {
		t.Fatalf("%+v", out)
	}
	if out[0].Label != "A … C" || out[1].Label != "D + E" {
		t.Fatalf("labels %q / %q", out[0].Label, out[1].Label)
	}
	if out[0].Text != "uno\n\ndos\n\ntres" || len(out[0].Files) != 2 || out[1].Text != "cuatro\n\ncinco" {
		t.Fatalf("material lost: %+v", out)
	}
	if same := normalizeHelperParts(parts, 0); len(same) != 5 || same[4].Label != "E" {
		t.Fatalf("count 0 keeps every part: %+v", same)
	}
}
