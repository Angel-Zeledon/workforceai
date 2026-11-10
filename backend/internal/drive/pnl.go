package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/xuri/excelize/v2"

	"aiworkforce/backend/internal/invoicepnl"
	"aiworkforce/backend/internal/officedoc"
)

// fs.income_statement (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 2): the agent
// extracts one structured row per invoice; the backend classifies, checks and adds up in code (internal/invoicepnl) and
// writes an Excel with Detail / Income statement / To review. Same zones and approvals as write_document.

// ActIncomeStatement is the fs action that builds an income statement from invoice rows.
const ActIncomeStatement = "income_statement"

func pnlInput(args map[string]any) (invoicepnl.Input, error) {
	var in invoicepnl.Input
	switch c := args["company"].(type) {
	case string:
		in.Company.Name = c
	case map[string]any:
		in.Company.Name, _ = c["name"].(string)
		in.Company.TaxID, _ = c["tax_id"].(string)
	}
	if t := argStr(args, "company_tax_id"); t != "" {
		in.Company.TaxID = t
	}
	in.Currency, in.Locale = argStr(args, "currency"), argStr(args, "locale")
	raw, err := json.Marshal(map[string]any{"invoices": args["invoices"], "rates": args["rates"]})
	if err != nil {
		return in, fmt.Errorf("invoices must be a list of invoice rows")
	}
	var x struct {
		Invoices []invoicepnl.Invoice `json:"invoices"`
		Rates    map[string]float64   `json:"rates"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return in, fmt.Errorf("invoices must be a list of rows {file, page, date, number, issuer, issuer_tax_id, receiver, receiver_tax_id, concept, category, currency, subtotal, taxes, total, type, status, reason}; rates a map of currency to rate")
	}
	in.Invoices, in.Rates = x.Invoices, x.Rates
	return in, nil
}

func (a *AgentFS) planIncomeStatement(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	p := argStr(args, "path")
	folder, name, err := SplitFile(p)
	if err != nil {
		return deny("path_invalid", err.(*Error).Msg)
	}
	if _, err := CleanName(name); err != nil {
		return deny("name_invalid", err.(*Error).Msg)
	}
	if officedoc.FormatOfName(name) != "xlsx" {
		return deny("unsupported_type", "income_statement writes an Excel file: the path must end in .xlsx")
	}
	c := folder + "/" + name
	pl := Plan{Path: c, Reversibility: "versioned", Risk: "medium"}
	in, err := pnlInput(args)
	var res invoicepnl.Result
	if err == nil {
		res, err = invoicepnl.Compute(in)
	}
	if err != nil {
		pl.docErr = err.Error()
		return pl
	}
	doc, fileCol := invoicepnl.Workbook(res, in.Locale)
	data, err := officedoc.Build("xlsx", doc)
	if err != nil {
		pl.docErr = err.Error()
		return pl
	}
	if a.LinkBase != "" {
		data = withFileLinks(data, doc.Sheets[0].Name, fileCol, res, a.LinkBase)
	}
	if int64(len(data)) > a.Svc.cfg.Limits.MaxFileBytes {
		return deny("file_too_large", fmt.Sprintf("the file exceeds the limit of %d MB", a.Svc.cfg.Limits.MaxFileBytes>>20))
	}
	pl.data, pl.Bytes = data, len(data)
	st := res.Statement
	pl.note = fmt.Sprintf("%d invoice(s) read, %d in the result, %d to review; currency %s: revenue %s, cost of sales %s, gross profit %s, operating expenses %s, profit before taxes %s",
		res.Read, res.Included, len(res.Review), res.Currency, invoicepnl.Money(st.Revenue), invoicepnl.Money(st.Cost), invoicepnl.Money(st.Gross),
		invoicepnl.Money(st.Expenses), invoicepnl.Money(st.Operating))
	pl.noteData = map[string]any{"read": res.Read, "included": res.Included, "to_review": len(res.Review), "currency": res.Currency,
		"revenue": cents(st.Revenue), "cost": cents(st.Cost), "gross_profit": cents(st.Gross), "expenses": cents(st.Expenses), "profit_before_taxes": cents(st.Operating),
		"period": res.Period}
	if z := a.zoneAll(ctx, org, sc, c); z == ZonePersonDenied {
		return personDeny()
	} else if sc.otherOfficeRefused(z) {
		return otherOfficeDeny()
	} else if sc.writeRefused(z) {
		return deny(denyCustomerIsolation, "this task works for one customer: write only in that customer's folders or your desk for it")
	} else if !autoWrite(z) {
		pl.Approval, pl.Flags = true, []string{"outside_scope"}
		if z == ZoneOtherDesk {
			pl.Flags = append(pl.Flags, "other_agent_desk")
		}
	}
	return pl
}

func cents(c int64) float64 { return float64(c) / 100 }

func (a *AgentFS) runIncomeStatement(ctx context.Context, me Principal, pl Plan, args map[string]any) RunResult {
	if pl.docErr != "" {
		return RunResult{Code: "invalid_args", Summary: "invalid_args: " + pl.docErr}
	}
	res, err := a.Svc.Write(ctx, me, WriteInput{Path: pl.Path, Data: pl.data, Message: argStr(args, "message"), Tags: argTags(args), Source: "agent_tool"})
	if err != nil {
		return failure(err)
	}
	data := map[string]any{"file_id": res.File.ID, "path": res.File.Path, "version": res.File.CurrentVersion, "created": res.Created}
	for k, v := range pl.noteData {
		data[k] = v
	}
	return RunResult{OK: true, Summary: fmt.Sprintf("wrote %s (version %d): %s. Tell the person these numbers as computed here; never recompute them",
		res.File.Path, res.File.CurrentVersion, pl.note), Data: data}
}

// withFileLinks makes the file column of the detail sheet open the folder of each invoice in Files. On any error the
// workbook is returned unchanged (the path stays as text).
func withFileLinks(data []byte, sheet string, col int, res invoicepnl.Result, base string) []byte {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer f.Close()
	base = strings.TrimRight(base, "/")
	for i, r := range res.Rows {
		if !strings.HasPrefix(r.File, "/") {
			continue
		}
		cell, err := excelize.CoordinatesToCellName(col, i+2)
		if err != nil {
			return data
		}
		link := base + "/files?path=" + url.QueryEscape(parentPath(r.File))
		if err := f.SetCellHyperLink(sheet, cell, link, "External"); err != nil {
			return data
		}
	}
	var out bytes.Buffer
	if err := f.Write(&out); err != nil {
		return data
	}
	return out.Bytes()
}
