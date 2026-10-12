package api_test

import (
	"net/url"
	"strings"
	"testing"
)

// H1 over HTTP: the "Antes de empezar" step of a draft.
func TestProjectInputsOverHTTP(t *testing.T) {
	e := newEnv(t, opts{ws: true, drive: true})
	created := e.json("POST", api1+"/projects/draft", "", map[string]any{"goal": "Cierre del grupo", "template_id": "group_consolidated_close",
		"params": map[string]string{"subsidiaries": "3"}, "locale": "es"}, 201)
	id := str(created, "project_id")
	base := api1 + "/projects/" + id + "/inputs"

	v := e.json("GET", base, "", nil, 200)
	if v["ready"] != false || len(v["missing"].([]any)) != 4 || v["demo_set"] != "group_close" {
		t.Fatalf("inputs: %v", v)
	}

	// Launch is blocked with a clear message until the required inputs exist.
	resp, raw := e.do("POST", api1+"/projects/"+id+"/launch", "", map[string]any{"approved_budget_usd": 500, "acknowledge_underbudget": true}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "inputs_required") {
		t.Fatalf("launch = %d %s", resp.StatusCode, raw)
	}

	// Upload: only the accepted extensions, into /Projects/<name>/Entradas.
	text := map[string]string{"Content-Type": "text/csv"}
	if r, _ := e.do("PUT", base+"/fx_rates/upload?name=fx.exe", "", []byte("a,b\n1,2\n"), text); r.StatusCode != 415 {
		t.Fatalf("bad extension = %d", r.StatusCode)
	}
	if r, _ := e.do("PUT", base+"/ghost/upload?name=fx.csv", "", []byte("a,b\n1,2\n"), text); r.StatusCode != 404 {
		t.Fatalf("unknown input = %d", r.StatusCode)
	}
	if r, _ := e.do("PUT", base+"/fx_rates/upload?name="+url.QueryEscape("../../x.csv"), "", []byte("a,b\n1,2\n"), text); r.StatusCode != 200 {
		t.Fatalf("a path in the name is reduced to its base name: %d", r.StatusCode)
	}
	v = e.json("PUT", base+"/fx_rates/upload?name=fx_rates.csv", "", []byte("currency,rate\nMXN,17.8\n"), 200)
	folder := str(v, "folder")
	if !strings.HasSuffix(folder, "/Entradas") || !strings.HasPrefix(folder, "/Projects/") {
		t.Fatalf("folder %q", folder)
	}
	l := e.json("GET", api1+"/drive/list?path="+url.QueryEscape(folder), "", nil, 200)
	if fs, _ := l["files"].([]any); len(fs) < 1 {
		t.Fatalf("the file must be in the drive: %v", l)
	}

	// Link a drive file: it is COPIED into Entradas.
	e.do("PUT", api1+"/drive/upload?path="+url.QueryEscape("/Shared/matriz.csv"), "", []byte("creditor,debtor\nS01,S02\n"), text)
	v = e.json("POST", base+"/intercompany/link", "", map[string]any{"paths": []string{"/Shared/matriz.csv"}}, 200)
	if !strings.Contains(mustJSON(t, v), folder+"/matriz.csv") {
		t.Fatalf("link: %v", v)
	}
	if c := e.status("POST", base+"/intercompany/link", "", map[string]any{"paths": []string{"/Shared/nope.csv"}}); c != 404 {
		t.Fatalf("missing drive file = %d", c)
	}

	// Sample data fills what is still missing (and only that).
	v = e.json("POST", base+"/demo", "", nil, 200)
	if v["ready"] != true || v["demo_used"] != true || len(v["missing"].([]any)) != 0 {
		t.Fatalf("demo: %v", v)
	}
	tb := 0
	for _, it := range v["items"].([]any) {
		m := it.(map[string]any)
		if m["key"] == "tb" {
			tb = len(m["files"].([]any))
		}
		if m["key"] == "intercompany" && len(m["files"].([]any)) != 1 {
			t.Fatalf("the user's intercompany file must not be replaced by the sample: %v", m)
		}
	}
	if tb != 3 {
		t.Fatalf("a trial balance per subsidiary: %d", tb)
	}
	l = e.json("GET", api1+"/drive/list?path="+url.QueryEscape(folder), "", nil, 200)
	if fs, _ := l["files"].([]any); len(fs) < 8 {
		t.Fatalf("files in Entradas: %d", len(fs))
	}

	// A file deleted by hand stops counting before launch.
	var fid string
	for _, f := range l["files"].([]any) {
		if m := f.(map[string]any); m["name"] == "fx_rates.csv" {
			fid = str(m, "id")
		}
	}
	if fid == "" {
		t.Fatal("fx_rates.csv not listed")
	}
	e.do("DELETE", api1+"/drive/files/"+fid, "", nil, nil)
	resp, raw = e.do("POST", api1+"/projects/"+id+"/launch", "", map[string]any{"approved_budget_usd": 500, "acknowledge_underbudget": true}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "fx_rates") {
		t.Fatalf("launch after deleting a required file = %d %s", resp.StatusCode, raw)
	}

	// Removing a file detaches it; launching without the input is an explicit, recorded choice.
	v = e.json("DELETE", base+"/tb/files?path="+url.QueryEscape(folder+"/tb_S01_MX.csv"), "", nil, 200)
	if v["ready"] != false {
		t.Fatalf("remove: %v", v)
	}
	launched := e.json("POST", api1+"/projects/"+id+"/launch", "", map[string]any{"approved_budget_usd": 500, "acknowledge_underbudget": true, "proceed_without_inputs": true}, 202)
	if launched["ok"] != true {
		t.Fatalf("launch: %v", launched)
	}
	v = e.json("GET", base, "", nil, 200)
	w, _ := v["waived"].(map[string]any)
	if w == nil || len(w["missing"].([]any)) == 0 {
		t.Fatalf("the waiver must be recorded: %v", v)
	}
	// Launched: the inputs are frozen.
	if r, _ := e.do("PUT", base+"/materiality/upload?name=m.md", "", []byte("# m"), map[string]string{"Content-Type": "text/markdown"}); r.StatusCode != 409 {
		t.Fatalf("upload after launch = %d", r.StatusCode)
	}
	snap := e.json("GET", api1+"/projects/"+id+"/snapshot", "", nil, 200)
	if snap["inputs"] == nil {
		t.Fatalf("the snapshot carries the inputs: %v", snap)
	}
}

func TestProjectInputsValuesAndNoDemo(t *testing.T) {
	e := newEnv(t, opts{ws: true, drive: true})
	id := str(e.json("POST", api1+"/projects/draft", "", map[string]any{"template_id": "erp_migration", "locale": "en"}, 201), "project_id")
	base := api1 + "/projects/" + id + "/inputs"
	if c := e.status("PUT", base+"/program_budget/value", "", map[string]any{"value": "lots"}); c != 400 {
		t.Fatalf("invalid number = %d", c)
	}
	v := e.json("PUT", base+"/program_budget/value", "", map[string]any{"value": "1500000"}, 200)
	if !strings.Contains(mustJSON(t, v), "1500000") {
		t.Fatalf("%v", v)
	}
	if !strings.Contains(mustJSON(t, v), "Systems inventory") {
		t.Fatalf("English labels expected: %v", v)
	}
	// A project without inputs has no sample data.
	g := str(e.json("POST", api1+"/projects/draft", "", map[string]any{"goal": "algo libre", "template_id": "tpl-generic"}, 201), "project_id")
	if c := e.status("POST", api1+"/projects/"+g+"/inputs/demo", "", nil); c != 404 {
		t.Fatalf("demo without a dataset = %d", c)
	}
}
