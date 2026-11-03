package api

import "testing"

func TestPluralUsesTheSingularForOne(t *testing.T) {
	if got := plural(1, "Hay 1 aprobación esperando.", "Hay %d aprobaciones esperando."); got != "Hay 1 aprobación esperando." {
		t.Fatalf("one: %q", got)
	}
	if got := plural(3, "Hay 1 aprobación esperando.", "Hay %d aprobaciones esperando."); got != "Hay 3 aprobaciones esperando." {
		t.Fatalf("many: %q", got)
	}
}
