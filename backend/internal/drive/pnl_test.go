package drive_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"aiworkforce/backend/internal/drive"
)

func invoiceArgs(path string) map[string]any {
	return map[string]any{"path": path, "company": map[string]any{"name": "Acme SA", "tax_id": "ACM-1"}, "currency": "MXN", "locale": "es",
		"invoices": []any{
			map[string]any{"file": "/Shared/Facturas/Oct/a1.pdf", "page": 1.0, "date": "2026-10-01", "number": "A1", "issuer": "Acme", "issuer_tax_id": "ACM1",
				"receiver": "Cliente", "concept": "Servicio", "subtotal": 1000.0, "taxes": 160.0, "total": 1160.0},
			map[string]any{"file": "/Shared/Facturas/Oct/p1.pdf", "page": 1.0, "date": "2026-10-02", "number": "P1", "issuer": "Proveedor",
				"receiver": "Acme S.A.", "receiver_tax_id": "acm-1", "concept": "Renta", "category": "Renta", "subtotal": "400,00", "taxes": "64,00", "total": "464,00"},
			map[string]any{"file": "/Shared/Facturas/Oct/scan.pdf", "status": "scanned"},
		}}
}

func TestIncomeStatementToolWritesTheWorkbook(t *testing.T) {
	e := newAgentEnv(t)
	e.fs.LinkBase = "https://app.example.com"
	args := invoiceArgs("/Agents/sales/Estado de resultados octubre.xlsx")
	if pl := e.plan("sales", drive.ActIncomeStatement, args); pl.Deny != "" || pl.Approval {
		t.Fatalf("own desk is free: %+v", pl)
	}
	r := e.fs.Run(e.ctx, "sales", drive.ActIncomeStatement, args, false)
	if !r.OK || !strings.Contains(r.Summary, "3 invoice(s) read, 2 in the result, 1 to review") || !strings.Contains(r.Summary, "profit before taxes 600.00") {
		t.Fatalf("run: %+v", r)
	}
	if r.Data["profit_before_taxes"] != 600.0 || r.Data["to_review"] != 1 {
		t.Fatalf("data: %+v", r.Data)
	}
	f, err := e.svc.FileByPath(e.ctx, "/Agents/sales/Estado de resultados octubre.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	rc, _, err := e.svc.Open(context.Background(), f, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rc)
	x, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(x.GetSheetList(), ","); got != "Detalle,Estado de resultados,Revisar" {
		t.Fatalf("sheets: %s", got)
	}
	if ok, link, _ := x.GetCellHyperLink("Detalle", "M2"); !ok || !strings.Contains(link, "/files?path=%2FShared%2FFacturas%2FOct") {
		t.Fatalf("file link: %v %q", ok, link)
	}
	rv, _ := x.GetRows("Revisar")
	if len(rv) != 2 || !strings.Contains(strings.Join(rv[1], "|"), "escaneada: no se pudo leer el texto") {
		t.Fatalf("review: %v", rv)
	}
	// outside the writable zones it asks; bad input never writes
	if pl := e.plan("sales", drive.ActIncomeStatement, invoiceArgs("/Shared/estado.xlsx")); !pl.Approval {
		t.Fatalf("shared asks: %+v", pl)
	}
	if pl := e.plan("sales", drive.ActIncomeStatement, invoiceArgs("/Agents/sales/estado.pdf")); pl.Deny != "unsupported_type" {
		t.Fatalf("xlsx only: %+v", pl)
	}
	bad := invoiceArgs("/Agents/sales/x.xlsx")
	delete(bad, "company")
	if r := e.fs.Run(e.ctx, "sales", drive.ActIncomeStatement, bad, false); r.OK || r.Code != "invalid_args" {
		t.Fatalf("no company: %+v", r)
	}
	if !drive.IsWriteAction(drive.ActIncomeStatement) {
		t.Fatal("it writes")
	}
}
