package filecomments

import (
	"strings"
	"testing"
)

const doc1 = "# Contrato\n\nEl proveedor entregará el servicio en 30 días.\n\nEl pago se hará a 60 días de la factura.\n\nLa garantía dura un año.\n"

func TestMakeAnchorCapturesQuoteContextAndOffsets(t *testing.T) {
	a, err := MakeAnchor(doc1, "El pago se hará a 60 días de la factura.", 3)
	if err != nil {
		t.Fatal(err)
	}
	if a.Quote != "El pago se hará a 60 días de la factura." || a.Version != 3 {
		t.Fatalf("anchor = %+v", a)
	}
	if !strings.HasSuffix(a.Prefix, "30 días.") || !strings.HasPrefix(a.Suffix, "La garantía") {
		t.Fatalf("context = %q | %q", a.Prefix, a.Suffix)
	}
	if got := string([]rune(doc1)[a.Start:a.End]); got != a.Quote {
		t.Fatalf("offsets give %q", got)
	}
}

func TestMakeAnchorRejectsAQuoteThatIsNotInTheText(t *testing.T) {
	if _, err := MakeAnchor(doc1, "algo que no está", 1); err == nil {
		t.Fatal("want an error")
	}
	if _, err := MakeAnchor(doc1, "   ", 1); err == nil {
		t.Fatal("empty quote must fail")
	}
}

func TestLocateIntactWhenTheTextDoesNotChange(t *testing.T) {
	a, _ := MakeAnchor(doc1, "La garantía dura un año.", 1)
	l := Locate(doc1, a)
	if l.Status != StatusIntact || l.Start != a.Start || l.End != a.End {
		t.Fatalf("location = %+v", l)
	}
}

func TestLocateFollowsTheQuoteAfterAnEditAbove(t *testing.T) {
	a, _ := MakeAnchor(doc1, "El pago se hará a 60 días de la factura.", 1)
	edited := strings.Replace(doc1, "entregará el servicio en 30 días", "entregará el servicio completo en un plazo máximo de 15 días", 1)
	l := Locate(edited, a)
	if l.Status != StatusMoved {
		t.Fatalf("status = %q", l.Status)
	}
	if got := string([]rune(edited)[l.Start:l.End]); got != a.Quote {
		t.Fatalf("relocated to %q", got)
	}
}

func TestLocateUsesContextToPickTheRightRepeat(t *testing.T) {
	text := "Primero: Pagar a 30 días.\n\nSegundo: Pagar a 30 días.\n\nTercero: otra cosa."
	a, err := MakeAnchor(text, "Pagar a 30 días.", 1)
	if err != nil {
		t.Fatal(err)
	}
	// the anchor was made on the first one; the text now has a new paragraph in front
	edited := "Nuevo párrafo al principio del documento.\n\n" + text
	l := Locate(edited, a)
	if l.Status != StatusMoved {
		t.Fatalf("status = %q", l.Status)
	}
	if got := string([]rune(edited)[l.Start-9 : l.Start]); got != "Primero: " {
		t.Fatalf("picked the wrong repeat: start %d (%q before)", l.Start, got)
	}
	// and the second one, anchored there, is found there
	b, _ := MakeAnchor(text, "Pagar a 30 días.", 1)
	b.Start, b.End = strings.Index(text, "Segundo")+9, strings.Index(text, "Segundo")+9+len([]rune("Pagar a 30 días."))
	b.Prefix, b.Suffix = "Segundo:", "Tercero: otra cosa."
	l2 := Locate(edited, b)
	if got := string([]rune(edited)[l2.Start-9 : l2.Start]); got != "Segundo: " {
		t.Fatalf("second repeat: %q", got)
	}
}

func TestLocateLostWhenThePassageWasRewritten(t *testing.T) {
	a, _ := MakeAnchor(doc1, "El pago se hará a 60 días de la factura.", 1)
	edited := strings.Replace(doc1, "a 60 días de la factura", "al contado", 1)
	l := Locate(edited, a)
	if l.Status != StatusLost || l.Start != -1 || l.End != -1 {
		t.Fatalf("location = %+v", l)
	}
}

func TestLocateMatchesRenderedTextAgainstMarkdownSource(t *testing.T) {
	src := "## Plazos\n\n- El **proveedor** entregará en `30` días.\n- Pago a [60 días](https://ejemplo.com/x) de la factura.\n"
	// what a person selects in the rendered page: no markup, one line break between items
	quote := "El proveedor entregará en 30 días.\nPago a 60 días de la factura."
	a, err := MakeAnchor(src, quote, 2)
	if err != nil {
		t.Fatal(err)
	}
	l := Locate(src, a)
	if l.Status != StatusIntact {
		t.Fatalf("location = %+v", l)
	}
	raw := string([]rune(src)[l.Start:l.End])
	if !strings.HasPrefix(raw, "El **proveedor**") || !strings.HasSuffix(raw, "de la factura.") {
		t.Fatalf("raw span = %q", raw)
	}
}

func TestMakeAnchorBoundsTheContext(t *testing.T) {
	long := strings.Repeat("palabra ", 200)
	a, err := MakeAnchor(long+"CLAVE"+long, "CLAVE", 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(a.Prefix)); n > ContextRunes || len([]rune(a.Suffix)) > ContextRunes {
		t.Fatalf("context too long: %d", n)
	}
}
