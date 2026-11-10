package api

import (
	"errors"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// D3 (Tarea 6, ronda 2): controlling a batch acts on all its projects. When every project is only the work of
// departments the person does not see the batch answers like a missing one; when any project has some of that work
// the action is refused (it would cancel or retry what they do not see); otherwise it goes ahead.
func TestBatchRule(t *testing.T) {
	visible := projectView{}
	mixed := projectView{hidden: map[string]bool{"n2": true}}
	hidden := projectView{hidden: map[string]bool{"n1": true}, all: true}
	cases := []struct {
		name  string
		views []projectView
		want  error
	}{
		{"no projects yet", nil, nil},
		{"all visible", []projectView{visible, visible}, nil},
		{"one mixed", []projectView{visible, mixed}, domain.ErrForbidden},
		{"one hidden among visible", []projectView{visible, hidden}, domain.ErrForbidden},
		{"all hidden", []projectView{hidden, hidden}, domain.ErrNotFound},
	}
	for _, c := range cases {
		err := batchRule("b1", c.views)
		if (c.want == nil) != (err == nil) || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}
