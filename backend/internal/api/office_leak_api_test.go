package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The history of a private office never reaches who is not in it: not the text, not the count, not the unread, not the
// preview, not the search, and a page of messages holds what the person may read (the store leaves the rest out in the
// query, so "has more" is true to what they can read). A member and an admin who is not a member are both tested.
func TestOfficeLeakChatSurfacesAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte := officesWorld(t, true)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	e.json("POST", api1+"/messages", ana, map[string]any{"conversation": "office", "text": "visible uno"}, 202)
	e.waitMessages(ana, "office", 1)
	for i := 0; i < 3; i++ {
		e.json("POST", api1+"/messages", marta, map[string]any{"conversation": "office", "text": "SECRETO-soporte " + string(rune('a'+i)), "office_id": soporte}, 202)
	}
	e.waitMessages(marta, "office", 4)
	e.json("POST", api1+"/messages", ana, map[string]any{"conversation": "office", "text": "visible dos"}, 202)
	e.waitMessages(marta, "office", 5)
	time.Sleep(500 * time.Millisecond) // the agents' replies settle: the counts below compare two reads

	const secret = "SECRETO-soporte"
	for _, who := range []struct{ name, token string }{{"admin not in the office", owner}, {"member of another office", ana}, {"nobody's", luis}} {
		for _, p := range []string{"/chats/threads", "/conversations", "/conversations?q=" + secret, "/conversations?kind=office",
			"/chats/threads/general/messages", "/chats/threads/general/messages?limit=2", "/conversations/office/messages", "/conversations/office/messages?limit=2"} {
			if _, raw := e.do("GET", api1+p, who.token, nil, nil); strings.Contains(string(raw), secret) {
				t.Fatalf("%s: GET %s leaks the private office: %s", who.name, p, raw)
			}
		}
		// the count of the General thread does not include what was said in the private office
		var threads []struct {
			Key          string `json:"key"`
			MessageCount int    `json:"message_count"`
		}
		_, raw := e.do("GET", api1+"/chats/threads", who.token, nil, nil)
		if err := json.Unmarshal(raw, &threads); err != nil || len(threads) == 0 {
			t.Fatalf("threads = %s (%v)", raw, err)
		}
		_, raw = e.do("GET", api1+"/chats/threads/general/messages", who.token, nil, nil)
		var all []map[string]any
		_ = json.Unmarshal(raw, &all)
		for _, th := range threads {
			if th.Key == "general" && th.MessageCount != len(all) {
				t.Fatalf("%s: the General thread counts %d messages, the thread holds %d", who.name, th.MessageCount, len(all))
			}
		}
		// a page of 1 holds 1 message, and has more exactly when the person reads more (not when only hidden rows are left)
		resp, raw := e.do("GET", api1+"/chats/threads/general/messages?limit=1", who.token, nil, nil)
		var page []map[string]any
		_ = json.Unmarshal(raw, &page)
		if resp.StatusCode != http.StatusOK || len(page) != 1 || (len(all) > 1) != (resp.Header.Get("X-Has-More") == "true") {
			t.Fatalf("%s: page = %d messages, has more %q of %d (%d)", who.name, len(page), resp.Header.Get("X-Has-More"), len(all), resp.StatusCode)
		}
	}

	// a member of the office reads it all, and finds it by search
	_, raw := e.do("GET", api1+"/conversations?q="+secret, marta, nil, nil)
	if !strings.Contains(string(raw), `"id":"office"`) {
		t.Fatalf("a member does not find the office chat: %s", raw)
	}
	_, raw = e.do("GET", api1+"/chats/threads/general/messages?office_id="+soporte, marta, nil, nil)
	if strings.Count(string(raw), secret) != 3 {
		t.Fatalf("a member's own office thread = %s", raw)
	}
	// ...with its pages: the office asked is filtered in the query, so 2 per page leaves a second page
	resp, raw := e.do("GET", api1+"/chats/threads/general/messages?office_id="+soporte+"&limit=2", marta, nil, nil)
	var page []map[string]any
	_ = json.Unmarshal(raw, &page)
	if len(page) != 2 || resp.Header.Get("X-Has-More") != "true" {
		t.Fatalf("first page of the office = %d (has more %q): %s", len(page), resp.Header.Get("X-Has-More"), raw)
	}
}
