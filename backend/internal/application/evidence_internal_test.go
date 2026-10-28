package application

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

func TestParseNumberBothConventions(t *testing.T) {
	cases := map[string]float64{
		"50,000": 50000, "50.000": 50000, "1,200,000": 1200000, "1.200.000,50": 1200000.5, "1,200,000.50": 1200000.5,
		"24,5": 24.5, "0.24": 0.24, "7": 7, "3.14159": 3.14159,
	}
	for in, want := range cases {
		f, ok := parseNumber(in)
		if !ok || !near(f.n, want) {
			t.Errorf("parseNumber(%q) = %v %v, want %v", in, f.n, ok, want)
		}
	}
}

func TestFiguresAndMatching(t *testing.T) {
	if got := figures("Margen 24 % y costo $7,800; entrega en 3-4 semanas"); len(got) != 4 || !got[0].pct || got[1].n != 7800 || got[3].n != 4 {
		t.Fatalf("figures: %+v", got)
	}
	for _, c := range []struct {
		text, value string
		want        bool
	}{
		{"El margen real es 24%, no 31%", "24%", true},
		{"El margen real es 24%, no 31%", "24 %", true},
		{"Ticket de $50,000", "$50.000", true},
		{"margen_real: 0.24", "24%", true},
		{"El margen real es 24%", "25%", false},
		{"sin cifras", "$1", false},
	} {
		if got := containsFigure(c.text, c.value); got != c.want {
			t.Errorf("containsFigure(%q, %q) = %v", c.text, c.value, got)
		}
	}
	if !sameFigure("50000", "$50,000") || sameFigure("50000", "$5,000") || !sameFigure("Abierto", "abierto") {
		t.Fatal("sameFigure")
	}
}

type fakeCells map[string]string

func (f fakeCells) ResolveCell(_ context.Context, _, _, _, art, sheet, cell string) (string, bool) {
	v, ok := f[art+"!"+sheet+"!"+cell]
	return v, ok
}

func TestCellCitationNeedsTheArtifactInContextAndTheSameValue(t *testing.T) {
	o := &Orchestrator{citeRes: fakeCells{"art_close!Result!B12": "112000", "art_other!Result!B2": "5"}}
	in := RunTaskRequest{Context: RunContext{ProjectContext: &ProjectContext{Artifacts: []ContextArtifact{{ID: "art_close", Title: "Estado de resultados"}}}}}
	v := citationCheck{o: o, ctx: context.Background(), t: domain.Task{ID: "t1", AgentID: "accounting", RequestID: "r1"}, in: in,
		seen: taskSeen{conn: map[string]seenItem{}, recall: map[string]string{}}}
	cases := []struct {
		value, ref, status, reason string
	}{
		{"$112,000", "cell:art_close!Result!B12", domain.CitationVerified, ""},
		{"$112,000", "cell:art_close!Result!b12", domain.CitationVerified, ""},
		{"$120,000", "cell:art_close!Result!B12", domain.CitationUnverified, ReasonValueMismatch},
		{"$5", "cell:art_other!Result!B2", domain.CitationUnverified, ReasonNotSeen}, // exists, but the task never saw it
		{"$1", "cell:art_close!Result!Z9", domain.CitationUnverified, ReasonNotFound},
		{"$1", "cell:art_close!B12", domain.CitationUnverified, ReasonMalformed},
	}
	for _, c := range cases {
		got, keep := v.check(domain.Citation{Value: c.value, Ref: c.ref})
		if !keep || got.Status != c.status || got.Reason != c.reason {
			t.Errorf("%s %s: %+v", c.value, c.ref, got)
			continue
		}
		if c.status == domain.CitationVerified && (got.Source == nil || got.Source.ArtifactID != "art_close" || got.Source.Cell != "B12" || !got.ValueChecked) {
			t.Errorf("verified cell source: %+v", got.Source)
		}
		if c.status != domain.CitationVerified && got.Source != nil {
			t.Errorf("an unverified citation must carry no source: %+v", got)
		}
	}
}

func TestFileAndRecallCitationsUseWhatTheReadsReturned(t *testing.T) {
	rs := &run{}
	at := time.Now().UTC()
	rs.recordSources("t1", []ReadSource{
		{Kind: SourceFile, FileID: "f1", Path: "/Projects/close/bank.csv", Version: 2},
		{Kind: SourceRecall, Ref: "report:r9", Title: "Cierre de septiembre"},
		{Kind: SourceConn, Tool: "email", Provider: "google_gmail", ItemID: "m-17"},
	}, at)
	rs.recordSources("t2", []ReadSource{{Kind: SourceFile, FileID: "f2", Path: "/secret.csv", Version: 1}}, at)
	v := citationCheck{o: &Orchestrator{}, ctx: context.Background(), t: domain.Task{ID: "t1"}, seen: rs.seenOf("t1")}
	for ref, want := range map[string]string{
		"file:/Projects/close/bank.csv": domain.CitationVerified,
		"file:f1":                       domain.CitationVerified,
		"file:/secret.csv":              domain.CitationUnverified, // read by ANOTHER task
		"report:r9":                     domain.CitationVerified,
		"memory:m1":                     domain.CitationUnverified,
		"conn:email:m-17":               domain.CitationVerified,
		"conn:EMAIL:m-17":               domain.CitationVerified,
		"conn:calendar:m-17":            domain.CitationUnverified,
		"conn:email:m-18":               domain.CitationUnverified,
		"web:https://x.test":            domain.CitationUnverified,
	} {
		got, _ := v.check(domain.Citation{Value: "$10", Ref: ref})
		if got.Status != want {
			t.Errorf("%s: %+v", ref, got)
		}
	}
	got, _ := v.check(domain.Citation{Value: "$10", Ref: "file:/Projects/close/bank.csv"})
	if got.Source.Version != 2 || got.Source.FileID != "f1" || got.Source.ReadAt == nil {
		t.Fatalf("file source: %+v", got.Source)
	}
	got, _ = v.check(domain.Citation{Value: "$10", Ref: "conn:email:m-17"})
	if got.Source.Provider != "google_gmail" || got.Source.ItemID != "m-17" || got.ValueChecked {
		t.Fatalf("conn source: %+v", got)
	}
}
