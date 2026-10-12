package api_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/officedoc"
)

// Office files made by agents are previewed by the server as JSON: sheets with
// formatted rows, documents as blocks. Same read permission as the content.
func TestDrivePreviewOfficeFiles(t *testing.T) {
	e := newEnv(t, opts{auth: true, drive: true})
	owner := e.register("owner-prev@example.com", "OwnerPrev")
	viewer := e.member(owner, "viewer-prev@example.com", auth.RoleViewer)
	rival := e.register("rival-prev@example.com", "RivalPrev")

	build := func(format string, raw map[string]any) []byte {
		d, err := officedoc.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		b, err := officedoc.Build(format, d)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	upload := func(path string, data []byte) string {
		r, b := e.do("PUT", api1+"/drive/upload?path="+url.QueryEscape(path), owner.AccessToken, data, map[string]string{"Content-Type": "application/octet-stream"})
		if r.StatusCode != 201 {
			t.Fatalf("upload %s = %d %s", path, r.StatusCode, b)
		}
		var w struct {
			File drive.File `json:"file"`
		}
		if err := json.Unmarshal(b, &w); err != nil {
			t.Fatal(err)
		}
		return w.File.ID
	}

	xid := upload("/Shared/productos.xlsx", build("xlsx", map[string]any{"sheets": []any{
		map[string]any{"name": "Productos", "columns": []any{"Producto", map[string]any{"header": "Precio", "type": "currency"}},
			"rows": []any{[]any{"Lapiz", 1.5}, []any{"=cmd", 2.0}}, "totals": map[string]any{}},
		map[string]any{"name": "Notas", "columns": []any{"Nota"}, "rows": []any{[]any{"hola"}}},
	}}))
	got := e.json("GET", api1+"/drive/files/"+xid+"/preview", viewer, nil, 200)
	raw, _ := json.Marshal(got)
	var res struct {
		Version int `json:"version"`
		Preview struct {
			Kind   string `json:"kind"`
			Sheets []struct {
				Name string     `json:"name"`
				Rows [][]string `json:"rows"`
			} `json:"sheets"`
		} `json:"preview"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Preview.Kind != "sheets" || len(res.Preview.Sheets) != 2 || res.Preview.Sheets[0].Name != "Productos" || res.Preview.Sheets[1].Name != "Notas" {
		t.Fatalf("preview = %s", raw)
	}
	rows := res.Preview.Sheets[0].Rows
	if len(rows) != 4 || rows[0][0] != "Producto" || rows[1][1] != "$1.50" || rows[2][0] != "=cmd" || rows[3][0] != "Total" || rows[3][1] != "$3.50" {
		t.Fatalf("rows = %v", rows)
	}

	did := upload("/Shared/informe.docx", build("docx", map[string]any{"title": "Informe", "blocks": []any{
		map[string]any{"type": "heading", "level": 1, "text": "Resumen"},
		map[string]any{"type": "paragraph", "text": "Todo bien."},
		map[string]any{"type": "bullets", "items": []any{"uno", "dos"}},
		map[string]any{"type": "table", "columns": []any{"A", "B"}, "rows": []any{[]any{"1", "2"}}},
	}}))
	d := e.json("GET", api1+"/drive/files/"+did+"/preview", viewer, nil, 200)
	s := mustJSON(t, d)
	for _, want := range []string{`"kind":"doc"`, `"type":"heading"`, `"Informe"`, `"Resumen"`, `"Todo bien."`, `"items":["uno","dos"]`, `"type":"table"`} {
		if !strings.Contains(s, want) {
			t.Errorf("docx preview misses %s in %s", want, s)
		}
	}

	// other types have no preview; other organizations cannot see it
	tid := upload("/Shared/nota.md", []byte("# hola"))
	if p := mustJSON(t, e.json("GET", api1+"/drive/files/"+tid+"/preview", viewer, nil, 200)); !strings.Contains(p, `"kind":"none"`) {
		t.Fatalf("md preview: %s", p)
	}
	if r, _ := e.do("GET", api1+"/drive/files/"+xid+"/preview", rival.AccessToken, nil, nil); r.StatusCode != 404 {
		t.Fatalf("cross-tenant preview = %d, want 404", r.StatusCode)
	}
	if r, _ := e.do("GET", api1+"/drive/files/"+xid+"/preview", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("anonymous preview = %d, want 401", r.StatusCode)
	}
}
