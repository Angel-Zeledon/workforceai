package officedoc

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// XLSX edits open the workbook with excelize, change the cells asked for and
// write it back. Values, number formats, column widths, merged cells, frozen
// panes and the cell styles survive; what excelize does not model (some chart
// and drawing details, macros, pivot caches, external links) may be lost.
// Formulas are limited like everywhere else: SUM or AVERAGE over a range of the
// same sheet; text is always stored as text, never evaluated.

const maxXlsxEditBytes = 8 << 20

var reEditFormula = regexp.MustCompile(`^(SUM|AVERAGE)\(\$?[A-Z]{1,3}\$?[1-9][0-9]{0,6}:\$?[A-Z]{1,3}\$?[1-9][0-9]{0,6}\)$`)

func editXLSX(data []byte, ops []Op) ([]byte, *EditError) {
	if len(data) > maxXlsxEditBytes {
		return nil, &EditError{Code: CodeTooLarge, Msg: "the workbook is too large to edit"}
	}
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 32 << 20})
	if err != nil {
		return nil, &EditError{Code: CodeUnsupported, Msg: "the workbook could not be read safely"}
	}
	defer f.Close()
	for n, op := range ops {
		i := n + 1
		kind, _ := op.str("op")
		var e *EditError
		switch kind {
		case "set_cell":
			e = xlsxSetCell(f, op, i)
		case "add_row":
			e = xlsxAddRow(f, op, i)
		case "rename_sheet":
			e = xlsxRename(f, op, i)
		default:
			return nil, editBad(i, "unknown op %q for an xlsx file (set_cell, add_row, rename_sheet)", kind)
		}
		if e != nil {
			return nil, e
		}
	}
	// the cached values of formulas that depend on the edited cells are stale:
	// ask Excel (and any calculating viewer) to recalculate on open
	yes := true
	_ = f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &yes})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, &EditError{Code: CodeInvalidResult, Msg: "could not write the workbook"}
	}
	out, err := fixFormulaTypes(buf.Bytes())
	if err != nil {
		return nil, &EditError{Code: CodeInvalidResult, Msg: "could not write the workbook"}
	}
	return out, nil
}

// sheetOf resolves the "sheet" argument (default: the first sheet).
func sheetOf(f *excelize.File, op Op, i int) (string, *EditError) {
	names := f.GetSheetList()
	if len(names) == 0 {
		return "", &EditError{Code: CodeUnsupported, Op: i, Msg: "the workbook has no sheets"}
	}
	s, _ := op.str("sheet")
	if s == "" {
		return names[0], nil
	}
	for _, n := range names {
		if n == s {
			return n, nil
		}
	}
	return "", &EditError{Code: CodeNotFound, Op: i, Msg: fmt.Sprintf("op %d: no sheet with that name (sheets: %s)", i, strings.Join(names, ", "))}
}

func cellValue(f *excelize.File, sh, ref string, v any, i int) *EditError {
	switch x := v.(type) {
	case nil:
		return setStr(f, sh, ref, "")
	case string:
		x = cleanText(x)
		if utf8.RuneCountInString(x) > MaxCellRunes {
			return editBad(i, "a cell holds at most %d characters", MaxCellRunes)
		}
		return setStr(f, sh, ref, x)
	case float64:
		if err := f.SetCellFloat(sh, ref, x, -1, 64); err != nil {
			return editBad(i, "cannot write %s", ref)
		}
	case int:
		if err := f.SetCellInt(sh, ref, int64(x)); err != nil {
			return editBad(i, "cannot write %s", ref)
		}
	case bool:
		if err := f.SetCellBool(sh, ref, x); err != nil {
			return editBad(i, "cannot write %s", ref)
		}
	default:
		return editBad(i, "a cell value must be text, a number, true/false or null")
	}
	return nil
}

func setStr(f *excelize.File, sh, ref, s string) *EditError {
	if err := f.SetCellStr(sh, ref, s); err != nil {
		return editBad(0, "cannot write %s", ref)
	}
	return nil
}

func xlsxSetCell(f *excelize.File, op Op, i int) *EditError {
	sh, e := sheetOf(f, op, i)
	if e != nil {
		return e
	}
	ref, _ := op.str("cell")
	col, row, err := excelize.CellNameToCoordinates(strings.ToUpper(ref))
	if err != nil || row > MaxRows+10 || col > MaxColumns+10 {
		return editBad(i, "cell must be a reference like B3 (within %d rows and %d columns)", MaxRows+10, MaxColumns+10)
	}
	ref, _ = excelize.CoordinatesToCellName(col, row)
	if fm, ok := op.str("formula"); ok && fm != "" {
		fm = strings.ToUpper(strings.TrimPrefix(strings.ReplaceAll(fm, " ", ""), "="))
		if !reEditFormula.MatchString(fm) {
			return editBad(i, "only SUM(A1:A9) or AVERAGE(A1:A9) over a range of the same sheet are allowed as formulas")
		}
		// write the computed value first so viewers that do not calculate show it
		if err := f.SetCellFormula(sh, ref, fm); err != nil {
			return editBad(i, "invalid formula")
		}
		if v, err := f.CalcCellValue(sh, ref); err == nil {
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				_ = f.SetCellFloat(sh, ref, n, -1, 64)
				_ = f.SetCellFormula(sh, ref, fm)
			}
		}
		return nil
	}
	v, has := op["value"]
	if !has {
		return editBad(i, "set_cell needs value (or formula)")
	}
	// a plain value replaces a formula the cell may have had
	_ = f.SetCellFormula(sh, ref, "")
	return cellValue(f, sh, ref, v, i)
}

func xlsxAddRow(f *excelize.File, op Op, i int) *EditError {
	sh, e := sheetOf(f, op, i)
	if e != nil {
		return e
	}
	vals, ok := op["values"].([]any)
	if !ok || len(vals) == 0 {
		return editBad(i, "add_row needs values: a list with one entry per column")
	}
	if len(vals) > MaxColumns {
		return editBad(i, "a row has at most %d columns", MaxColumns)
	}
	rows, err := f.GetRows(sh)
	if err != nil {
		return &EditError{Code: CodeUnsupported, Op: i, Msg: "the sheet could not be read"}
	}
	next := len(rows) + 1
	if next > MaxRows+1 {
		return editBad(i, "the sheet already has the maximum of %d rows", MaxRows)
	}
	for c, v := range vals {
		ref, _ := excelize.CoordinatesToCellName(c+1, next)
		if e := cellValue(f, sh, ref, v, i); e != nil {
			return e
		}
		if next > 1 { // same look as the row above (number format, alignment)
			up, _ := excelize.CoordinatesToCellName(c+1, next-1)
			if st, err := f.GetCellStyle(sh, up); err == nil && st != 0 {
				_ = f.SetCellStyle(sh, ref, ref, st)
			}
		}
	}
	return nil
}

func xlsxRename(f *excelize.File, op Op, i int) *EditError {
	sh, e := sheetOf(f, op, i)
	if e != nil {
		return e
	}
	nw, _ := op.str("name")
	nw = strings.TrimSpace(cleanText(nw))
	if nw == "" || utf8.RuneCountInString(nw) > MaxSheetNameLen || strings.ContainsAny(nw, `[]:*?/\`) {
		return editBad(i, "name must be 1-%d characters without [ ] : * ? / \\", MaxSheetNameLen)
	}
	if err := f.SetSheetName(sh, nw); err != nil {
		return editBad(i, "cannot rename the sheet (the name may already exist)")
	}
	return nil
}
