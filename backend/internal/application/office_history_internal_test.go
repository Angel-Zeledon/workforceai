package application

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// The history an agent reads is the one of the office the turn is asked from: General reads what was written without
// an office; an unknown office too, never another office's conversation.
func TestIsolation_ChatHistory(t *testing.T) {
	msgs := []domain.Message{{ID: "g"}, {ID: "l", OfficeID: "o-legal"}, {ID: "v", OfficeID: "o-ven"}, {ID: "gen", OfficeID: "general"}}
	bg := context.Background()
	for _, c := range []struct {
		ctx  context.Context
		want string
	}{
		{WithRequestOffice(bg, "o-legal"), "l,"}, {WithRequestOffice(bg, "o-ven"), "v,"},
		{WithRequestOffice(bg, "general"), "g,gen,"}, {bg, "g,gen,"},
	} {
		var got string
		for _, m := range officeHistory(c.ctx, append([]domain.Message(nil), msgs...)) {
			got += m.ID + ","
		}
		if got != c.want {
			t.Errorf("office %q reads %q, want %q", RequestOfficeFrom(c.ctx), got, c.want)
		}
	}
	m := domain.Message{}
	stampOffice(WithRequestOffice(bg, "general"), &m)
	if m.OfficeID != "" {
		t.Errorf("General stamps no office, got %q", m.OfficeID)
	}
}
