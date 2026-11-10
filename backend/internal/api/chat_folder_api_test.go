package api_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
)

// routeSpy is the default fake runtime that also records the text of every chat turn it is asked to route (and
// then fails, so the turn falls back to the local rules).
type routeSpy struct {
	fakeRuntime
	mu    sync.Mutex
	texts []string
}

func (r *routeSpy) Route(_ context.Context, in application.RouteRequest) (application.RouteResponse, error) {
	r.mu.Lock()
	r.texts = append(r.texts, in.Text)
	r.mu.Unlock()
	return application.RouteResponse{}, errors.New("route spy")
}

func (r *routeSpy) ChatReply(context.Context, application.ChatReplyRequest) (application.ChatReplyResponse, error) {
	return application.ChatReplyResponse{}, errors.New("route spy")
}

// turnText waits for the turn whose text starts with prefix and returns it.
func (r *routeSpy) turnText(t *testing.T, prefix string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, s := range r.texts {
			if strings.HasPrefix(s, prefix) {
				r.mu.Unlock()
				return s
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no turn %q was routed", prefix)
	return ""
}

func TestChatFolderMustBeReadableOfficeFolder(t *testing.T) {
	rt := &routeSpy{}
	o := newOfficeDriveRT(t, rt)
	o.upload(o.ana, "/Offices/Legal/Casos/demanda.md", "demanda")
	post := func(token, text, folder string) (int, string) {
		t.Helper()
		r, b := o.e.do("POST", api1+"/messages", token, map[string]any{"conversation": "office", "text": text, "folder": folder}, nil)
		return r.StatusCode, string(b)
	}
	const block = "[work folder]\n/Offices/Legal/Casos\n[/work folder]"

	// a member: accepted, and the turn works inside the folder (its canonical spelling)
	if c, b := post(o.ana, "uno ana", "/Offices/Legal/Casos"); c >= 300 {
		t.Fatalf("member = %d %s", c, b)
	}
	if got := rt.turnText(t, "uno ana"); !strings.Contains(got, block) {
		t.Fatalf("the turn must carry the folder: %q", got)
	}
	if c, b := post(o.ana, "dos ana", "/offices/legal/casos/"); c >= 300 {
		t.Fatalf("member, other case = %d %s", c, b)
	}
	if got := rt.turnText(t, "dos ana"); !strings.Contains(got, block) {
		t.Fatalf("the folder is named as the drive spells it: %q", got)
	}

	// what a missing folder answers: everything that may not be used answers exactly the same
	missC, missB := post(o.ana, "tres ana", "/Offices/Legal/NoExiste")
	if missC != 404 {
		t.Fatalf("missing folder = %d %s", missC, missB)
	}
	same := func(who, token, folder string) {
		t.Helper()
		if c, b := post(token, "x "+who, folder); c != missC || b != missB {
			t.Fatalf("%s %s = %d %s, want %d %s", who, folder, c, b, missC, missB)
		}
	}
	same("beto", o.beto, "/Offices/Legal/Casos")                  // not a member of a private office
	same("ana file", o.ana, "/Offices/Legal/Casos/demanda.md")    // a file, not a folder
	same("beto dotdot", o.beto, "/Offices/Ventas/../Legal/Casos") // checked against the final path
	same("beto lower", o.beto, "/offices/legal/casos")

	// outside the department folders: a clear 400
	for _, f := range []string{"/Shared", "/Offices", "/Agents/sales", "/Offices/../Shared"} {
		c, b := post(o.ana, "fuera", f)
		if c != 400 || !strings.Contains(b, "folder must be a department folder") {
			t.Fatalf("%s = %d %s", f, c, b)
		}
	}

	// ".." is resolved before the check: beto reads the open office Ventas
	if c, b := post(o.beto, "cuatro beto", "/Offices/Legal/../Ventas"); c >= 300 {
		t.Fatalf("beto into Ventas = %d %s", c, b)
	}
	if got := rt.turnText(t, "cuatro beto"); !strings.Contains(got, "[work folder]\n/Offices/Ventas\n[/work folder]") || strings.Contains(got, "..") {
		t.Fatalf("normalized folder: %q", got)
	}

	// no folder: no block
	if c, b := post(o.ana, "cinco ana", ""); c >= 300 {
		t.Fatalf("no folder = %d %s", c, b)
	}
	if got := rt.turnText(t, "cinco ana"); strings.Contains(got, "[work folder]") {
		t.Fatalf("no folder, no block: %q", got)
	}
}
