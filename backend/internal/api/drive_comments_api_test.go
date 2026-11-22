package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/filecomments"
)

const commentsDoc = "# Contrato\n\nEl proveedor entregará el servicio en 30 días.\n\nEl pago se hará a 60 días de la factura.\n\nLa garantía dura un año.\n"

const payQuote = "El pago se hará a 60 días de la factura."

// commentsRig is the office drive of D2/D3 (Legal private, Ventas open) with the anchored comments of F7 served.
type commentsRig struct {
	*officeDrive
	dr *drive.Service
}

func newCommentsRig(t *testing.T) *commentsRig {
	t.Helper()
	r := &commentsRig{}
	r.officeDrive = newOfficeDriveRT(t, nil, func(d *api.Deps) {
		r.dr = d.Drive
		d.FileComments = filecomments.New(filecomments.Config{Store: filecomments.NewMemStore(), Drive: d.Drive})
		d.Drive.SetVersionHook(d.FileComments.OnVersion)
	})
	return r
}

func (r *commentsRig) url(fileID, sub string) string { return api1 + "/drive/files/" + fileID + "/comments" + sub }

func (r *commentsRig) list(token, fileID string, q string) []map[string]any {
	r.e.t.Helper()
	got := r.e.json("GET", r.url(fileID, "")+q, token, nil, 200)
	var out []map[string]any
	for _, c := range got["comments"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func (r *commentsRig) add(token, fileID string, body map[string]any) map[string]any {
	r.e.t.Helper()
	return r.e.json("POST", r.url(fileID, ""), token, body, 201)
}

func (r *commentsRig) agentEdit(agent, fileID, text string) {
	r.e.t.Helper()
	ctx := application.WithOrg(context.Background(), r.owner.OrgID)
	if _, err := r.dr.NewVersion(ctx, drive.AgentP(agent), fileID, []byte(text), "edit by agent", nil, "test"); err != nil {
		r.e.t.Fatal(err)
	}
}

// F7: the whole road. A person comments on a passage, asks an agent for the change (a chat message with the file
// attached and the exact passage as data), the agent delivers a new version and the comment resolves with a link to it.
func TestAnchoredCommentRoundTrip(t *testing.T) {
	r := newCommentsRig(t)
	id := r.upload(r.ana, "/Offices/Legal/contrato.md", commentsDoc)

	c := r.add(r.ana, id, map[string]any{"body": "Que sea a 30 días.", "quote": payQuote})
	cid := c["id"].(string)
	anchor := c["anchor"].(map[string]any)
	if anchor["quote"] != payQuote || anchor["version"].(float64) != 1 || !strings.Contains(anchor["prefix"].(string), "30 días.") {
		t.Fatalf("anchor = %v", anchor)
	}
	if got := r.list(r.ana, id, ""); len(got) != 1 || got[0]["location"].(map[string]any)["status"] != "intact" {
		t.Fatalf("list = %v", got)
	}
	// a reply and a general note
	r.add(r.ana, id, map[string]any{"body": "De acuerdo", "parent_id": cid})
	r.add(r.ana, id, map[string]any{"body": "Nota general"})
	if c := r.e.status("POST", r.url(id, ""), r.ana, map[string]any{"body": "x", "quote": "texto que no existe"}); c != 400 {
		t.Fatalf("invented quote = %d", c)
	}

	// ask the legal agent to make the change
	asked := r.e.json("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{"agent_id": "legal", "lang": "es"}, 200)
	ch := asked["change"].(map[string]any)
	if ch["agent_id"] != "legal" || ch["base_version"].(float64) != 1 || ch["turn_id"] == "" {
		t.Fatalf("change = %v", ch)
	}
	_, raw := r.e.do("GET", api1+"/conversations/agent:legal/messages", r.ana, nil, nil)
	var ms []domain.Message
	if err := json.Unmarshal(raw, &ms); err != nil {
		t.Fatalf("messages: %v %s", err, raw)
	}
	var sent *domain.Message
	for i := range ms {
		if strings.Contains(ms[i].Text, "fs.edit") && strings.Contains(ms[i].Text, payQuote) {
			sent = &ms[i]
		}
	}
	if sent == nil {
		t.Fatalf("the request is not in the chat: %s", raw)
	}
	if !strings.Contains(sent.Text, "Que sea a 30 días.") || !strings.Contains(sent.Text, "untrusted_data") || len(sent.Files) != 1 || sent.Files[0].Path != "/Offices/Legal/contrato.md" {
		t.Fatalf("request = %+v", sent)
	}
	// once asked, it cannot be asked twice
	if c := r.e.status("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{"agent_id": "legal"}); c != 409 {
		t.Fatalf("second request = %d", c)
	}

	// another agent saving does not resolve it; the asked agent delivering does, with the version
	r.agentEdit("sales", id, commentsDoc+"\nOtra cosa.\n")
	if got := r.list(r.ana, id, ""); got[0]["resolved"] != false {
		t.Fatalf("resolved by another agent: %v", got[0])
	}
	r.agentEdit("legal", id, strings.Replace(commentsDoc, "a 60 días", "a 30 días", 1))
	got := r.list(r.ana, id, "")
	if got[0]["resolved"] != true || got[0]["resolved_by"] != "agent:legal" || got[0]["resolved_version"].(float64) != 3 {
		t.Fatalf("after delivery = %v", got[0])
	}
	// the passage was rewritten: the comment stays, now reading "the text changed"; against v1 it is intact
	if got[0]["location"].(map[string]any)["status"] != "lost" {
		t.Fatalf("location after the edit = %v", got[0]["location"])
	}
	if old := r.list(r.ana, id, "?version=1"); old[0]["location"].(map[string]any)["status"] != "intact" {
		t.Fatalf("v1 location = %v", old[0]["location"])
	}
	// a person can reopen it
	if re := r.e.json("PATCH", r.url(id, "/"+cid), r.ana, map[string]any{"resolved": false}, 200); re["resolved"] != false || re["resolved_version"] != nil {
		t.Fatalf("reopened = %v", re)
	}
}

// D3: a private department's files, and so their comments and change requests, belong to its members. The
// administrator who is not a member gets the same 404 as for a file that does not exist, on every route.
func TestAnchoredCommentsSealedByPrivateDepartment(t *testing.T) {
	r := newCommentsRig(t)
	id := r.upload(r.ana, "/Offices/Legal/contrato.md", commentsDoc)
	cid := r.add(r.ana, id, map[string]any{"body": "Que sea a 30 días.", "quote": payQuote})["id"].(string)

	for _, tc := range []struct{ method, path string }{
		{"GET", r.url(id, "")},
		{"POST", r.url(id, "")},
		{"PATCH", r.url(id, "/"+cid)},
		{"POST", r.url(id, "/"+cid+"/request-change")},
	} {
		body := map[string]any{"body": "intruso", "quote": payQuote, "resolved": true, "agent_id": "legal"}
		if c := r.e.status(tc.method, tc.path, r.beto, body); c != 404 {
			t.Fatalf("beto %s %s = %d, want 404", tc.method, tc.path, c)
		}
	}
	// same answer as for a file that does not exist
	if c := r.e.status("GET", r.url("no-such-file", ""), r.beto, nil); c != 404 {
		t.Fatalf("missing file = %d", c)
	}
	// nothing changed under ana's eyes
	got := r.list(r.ana, id, "")
	if len(got) != 1 || got[0]["resolved"] != false || got[0]["change"] != nil || got[0]["body"] != "Que sea a 30 días." {
		t.Fatalf("after beto's attempts = %v", got)
	}
	// ana still can, and the agent chat did not get a request from beto
	r.e.json("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{"agent_id": "legal"}, 200)
}

// Permissions: a viewer reads the comments but does not write them; someone who may read a folder (an open
// department) comments but cannot ask an agent to change what they cannot edit.
func TestAnchoredCommentsPermissions(t *testing.T) {
	r := newCommentsRig(t)
	viewer := r.e.member(r.owner, "viewer-fc@example.com", auth.RoleViewer)
	id := r.upload(r.ana, "/Offices/Ventas/plan.md", commentsDoc)
	cid := r.add(r.ana, id, map[string]any{"body": "Revisar", "quote": payQuote})["id"].(string)

	// viewer: reads, cannot comment, resolve or ask
	if got := r.list(viewer, id, ""); len(got) != 1 {
		t.Fatalf("viewer list = %v", got)
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", r.url(id, "")}, {"PATCH", r.url(id, "/" + cid)}, {"POST", r.url(id, "/"+cid+"/request-change")},
	} {
		if c := r.e.status(tc.method, tc.path, viewer, map[string]any{"body": "x", "quote": payQuote, "resolved": true, "agent_id": "legal"}); c != 403 {
			t.Fatalf("viewer %s %s = %d, want 403", tc.method, tc.path, c)
		}
	}
	// beto is not in Ventas (open): he reads and comments, but may not ask an agent to edit its files
	r.add(r.beto, id, map[string]any{"body": "Un comentario desde fuera", "quote": "La garantía dura un año."})
	if c := r.e.status("POST", r.url(id, "/"+cid+"/request-change"), r.beto, map[string]any{"agent_id": "legal"}); c != 403 {
		t.Fatalf("read-only person asks for a change = %d, want 403", c)
	}
	// a missing agent id
	if c := r.e.status("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{}); c != 400 {
		t.Fatalf("no agent = %d", c)
	}
	// ana (a member) can
	r.e.json("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{"agent_id": "sales"}, 200)
}

// Another organization sees nothing: not the file, not its comments.
func TestAnchoredCommentsAcrossOrganizations(t *testing.T) {
	r := newCommentsRig(t)
	id := r.upload(r.ana, "/Offices/Ventas/plan.md", commentsDoc)
	cid := r.add(r.ana, id, map[string]any{"body": "Revisar", "quote": payQuote})["id"].(string)
	stranger := r.e.register("stranger-fc@example.com", "Stranger").AccessToken
	for _, tc := range []struct{ method, path string }{
		{"GET", r.url(id, "")}, {"POST", r.url(id, "")}, {"PATCH", r.url(id, "/" + cid)}, {"POST", r.url(id, "/"+cid+"/request-change")},
	} {
		if c := r.e.status(tc.method, tc.path, stranger, map[string]any{"body": "x", "quote": payQuote, "resolved": true, "agent_id": "legal"}); c != 404 {
			t.Fatalf("stranger %s %s = %d, want 404", tc.method, tc.path, c)
		}
	}
	if got := r.list(r.ana, id, ""); len(got) != 1 || got[0]["resolved"] != false {
		t.Fatalf("list = %v", got)
	}
}

// A lost anchor cannot be turned into a change request: the passage the agent would change no longer exists.
func TestAnchoredCommentLostCannotAskForChange(t *testing.T) {
	r := newCommentsRig(t)
	id := r.upload(r.ana, "/Offices/Legal/contrato.md", commentsDoc)
	cid := r.add(r.ana, id, map[string]any{"body": "Que sea a 30 días.", "quote": payQuote})["id"].(string)
	// a person rewrites the sentence
	body := strings.Replace(commentsDoc, "a 60 días de la factura", "al contado", 1)
	if c := r.status("PUT", api1+"/drive/files/"+id+"/content?expected_version=1", r.ana, []byte(body)); c != 200 && c != 201 {
		t.Fatalf("save = %d", c)
	}
	if got := r.list(r.ana, id, ""); got[0]["location"].(map[string]any)["status"] != "lost" || got[0]["body"] != "Que sea a 30 días." {
		t.Fatalf("lost comment = %v", got[0])
	}
	resp, raw := r.e.do("POST", r.url(id, "/"+cid+"/request-change"), r.ana, map[string]any{"agent_id": "legal"}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "anchor_lost") {
		t.Fatalf("request on a lost anchor = %d %s", resp.StatusCode, raw)
	}
}
