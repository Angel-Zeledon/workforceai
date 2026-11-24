package api_test

import (
	"strings"
	"testing"
)

// POST /messages and /requests accept office_id: a person who cannot enter that office (a private one they are not in,
// or one that does not exist) gets 404 like a missing office; the history is listed by office and never holds the
// messages of a private office for who is not in it, asked for or not.
func TestMessagesOfficeAPI(t *testing.T) {
	e, owner, ana, marta, luis, ventas, soporte := officesWorld(t, true)
	// Soporte becomes private (Marta leads it); Luis is in neither office.
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)

	e.json("POST", api1+"/messages", luis, map[string]any{"conversation": "office", "text": "hola", "office_id": soporte}, 404)
	e.json("POST", api1+"/messages", luis, map[string]any{"conversation": "office", "text": "hola", "office_id": "nowhere"}, 404)
	e.json("POST", api1+"/requests", luis, map[string]any{"text": "hola", "office_id": soporte}, 404)
	for _, p := range []string{"/requests?office_id=", "/conversations/office/messages?office_id=", "/chats/threads/general/messages?office_id="} {
		if c := e.status("GET", api1+p+soporte, luis, nil); c != 404 {
			t.Fatalf("GET %s for a non-member of the private office = %d", p, c)
		}
	}

	e.json("POST", api1+"/messages", marta, map[string]any{"conversation": "office", "text": "secreto de soporte", "office_id": soporte}, 202)
	e.json("POST", api1+"/messages", ana, map[string]any{"conversation": "office", "text": "texto de ventas", "office_id": ventas}, 202)
	e.json("POST", api1+"/messages", ana, map[string]any{"conversation": "office", "text": "sin oficina"}, 202)
	e.waitMessages(marta, "office", 3)

	texts := func(token, query string) string {
		var b strings.Builder
		ms, _ := e.messages(token, "office/messages"+query)
		for _, m := range ms {
			b.WriteString(m.Text + "\n")
		}
		return b.String()
	}
	// whoever is not in Soporte never reads its messages, with or without asking for an office
	for _, q := range []string{"", "?office_id=" + ventas} {
		for _, tok := range []string{luis, ana, owner} {
			if body := texts(tok, q); strings.Contains(body, "secreto de soporte") {
				t.Fatalf("a non-member read the private office's message (%q): %s", q, body)
			}
		}
	}
	if body := texts(marta, "?office_id="+soporte); !strings.Contains(body, "secreto de soporte") || strings.Contains(body, "texto de ventas") || strings.Contains(body, "sin oficina") {
		t.Fatalf("Soporte's own history = %s", body)
	}
	if body := texts(ana, "?office_id="+ventas); !strings.Contains(body, "texto de ventas") || strings.Contains(body, "secreto de soporte") {
		t.Fatalf("Ventas history = %s", body)
	}
	// the General thread (the other history view) holds nothing of the private office for a non-member either
	_, raw := e.do("GET", api1+"/chats/threads/general/messages", luis, nil, nil)
	if strings.Contains(string(raw), "secreto de soporte") {
		t.Fatalf("thread leaks the private office: %s", raw)
	}
}
