package notify

import (
	"strings"
	"testing"
)

// Needs your help: the channel notice names what the agent needs, or how many tasks wait when several were gathered.
func TestHelpNoticeTexts(t *testing.T) {
	for _, loc := range []string{"es", "en", "pt"} {
		for _, k := range []string{"helpSubj", "helpSubjN", "helpBody", "helpBodyN"} {
			if tr(loc, k) == "" || tr(loc, k) == k {
				t.Fatalf("%s: missing %s", loc, k)
			}
		}
	}
	m := Message{Locale: "es", Notice: "x", NoticeSubject: "Un agente necesita tu ayuda"}
	if m.Subject() != "Un agente necesita tu ayuda" || !strings.Contains(m.PlainText(), "x") {
		t.Fatalf("subject %q", m.Subject())
	}
	if (Message{Locale: "es", Notice: "x"}).Subject() != tr("es", "subjCap") {
		t.Fatal("the plan-cap notice keeps its subject")
	}
}
