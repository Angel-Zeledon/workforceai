package application_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

type fileResolverFake map[string]domain.FileRef

func (f fileResolverFake) ResolveFile(_ context.Context, _, p string) (domain.FileRef, bool) {
	r, ok := f[p]
	return r, ok
}

func TestAttachmentsBecomeFileRefsAndReachTheAgents(t *testing.T) {
	rt := newChatRuntime(fixedRoute("question", "finance", primary("legal")))
	h := newChat(t, rt, nil)
	h.orch.SetFileResolver(fileResolverFake{"/Shared/Chat/saldos.csv": {FileID: "fil_1", Path: "/Shared/Chat/saldos.csv", Name: "saldos.csv", Mime: "text/csv", Size: 40, Version: 1}})
	ctx := application.WithAttachments(context.Background(), []string{"/Shared/Chat/saldos.csv", "/Shared/Chat/missing.csv"})
	if _, err := h.orch.PostChat(ctx, "agent:legal", "revisa esto"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	ms := h.messages("agent:legal")
	if ms[0].Text != "revisa esto" || len(ms[0].Files) != 1 || ms[0].Files[0].Action != "attach" || ms[0].Files[0].FileID != "fil_1" {
		t.Fatalf("user message keeps its text and carries one resolved ref: %+v", ms[0])
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.replies) == 0 || !strings.Contains(rt.replies[0].Text, "/Shared/Chat/saldos.csv") || strings.Contains(rt.replies[0].Text, "missing") {
		t.Fatalf("the agent sees the delimited attachment paths: %+v", rt.replies)
	}
}
