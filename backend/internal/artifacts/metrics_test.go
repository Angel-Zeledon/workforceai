package artifacts_test

import (
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
)

// The metric_change signal reads the last two numbers of a margin / sales / cash column: totals, SUM formulas,
// cost columns, text that looks like a number and old or non-tabular artifacts are left out.
func TestMetricSeriesFromSheetsAndTables(t *testing.T) {
	e := newEnv(t, nil)
	e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindSheet, Title: "Margen Acme", Content: raw(sheet(`{
		"A1":{"v":"Mes"},"B1":{"v":"Ventas"},"C1":{"v":"Costo de ventas"},"D1":{"v":"Margen %"},"E1":{"v":"Caja"},
		"A2":{"v":"Julio"},"B2":{"v":1000},"C2":{"v":700},"D2":{"v":0.30},"E2":{"v":"500"},
		"A3":{"v":"Agosto"},"B3":{"v":1100},"C3":{"v":760},"D3":{"v":0.31},"E3":{"v":"450"},
		"A4":{"v":"Septiembre"},"B4":{"v":900},"C4":{"v":690},"D4":{"v":0.24},
		"A5":{"v":"Total"},"B5":{"v":3000},"C5":{"v":2150},
		"B6":{"f":"=SUM(B2:B4)","v":3000}}`))})
	e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindDoc, Title: "Notas de ventas",
		Content: raw(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[]}}`)})

	got, err := e.svc.MetricSeries(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]application.MetricSeries{
		application.MetricSales:  {Column: "Ventas", Previous: 1100, Current: 900, Period: "Septiembre"},
		application.MetricMargin: {Column: "Margen %", Previous: 0.31, Current: 0.24, Period: "Septiembre"},
	}
	if len(got) != len(want) {
		t.Fatalf("series = %+v", got)
	}
	for _, s := range got {
		w, ok := want[s.Metric]
		if !ok || s.Column != w.Column || s.Previous != w.Previous || s.Current != w.Current || s.Period != w.Period ||
			s.ArtifactTitle != "Margen Acme" || s.Sheet != "Margen" || s.CreatorAgent != "" || s.Ref == "" {
			t.Errorf("series %+v, want %+v", s, w)
		}
	}
}

func TestExtractMetricSeriesTableAndAgentCreator(t *testing.T) {
	m := artifacts.Meta{ID: "art-t", Kind: artifacts.KindTable, Title: "Flujo semanal", CreatedBy: artifacts.ActorRef{Kind: artifacts.ActorAgent, ID: "accounting"},
		UpdatedAt: time.Now()}
	content := raw(`{"schema":"aiw.table/1",
		"columns":[{"key":"week","label":"Semana","type":"text"},{"key":"cash","label":"Caja final","type":"money"},{"key":"rev","label":"Revenue","type":"number"}],
		"rows":[{"id":"r1","cells":{"week":"S1","cash":2000,"rev":10}},
		        {"id":"r2","cells":{"week":"S2","cash":1500}},
		        {"id":"r3","cells":{"week":"Total","cash":3500,"rev":10}}]}`)
	got := artifacts.ExtractMetricSeries(m, content)
	if len(got) != 1 {
		t.Fatalf("series = %+v", got)
	}
	s := got[0]
	if s.Metric != application.MetricCash || s.Previous != 2000 || s.Current != 1500 || s.Ref != "r2:cash" || s.Period != "S2" ||
		s.CreatorAgent != "accounting" || s.ArtifactID != "art-t" || s.Column != "Caja final" {
		t.Fatalf("table series = %+v", s)
	}
	if got := artifacts.ExtractMetricSeries(m, raw(`not json`)); len(got) != 0 {
		t.Errorf("broken content = %+v", got)
	}
}
