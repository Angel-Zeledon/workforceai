package drive

import (
	"bytes"
	"context"
	"fmt"
	"unicode/utf8"

	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/officedoc"
)

// fs.edit: a precise edit of an existing file (docs/architecture/agent-edit.md).
//
//	args: {path | file_id, expected_version, ops: [...], message?}
//
// It is a write like fs.write: the same zones (own desk and assigned projects
// free; anywhere else a human approves), every edit is a new version attributed
// to the agent (so "Undo" is the restore-version of the previous one), and it
// is bound to expected_version (a version_conflict when someone saved first).
// All operations apply or none do. The operations are in internal/officedoc.

// ActEdit is the precise-edit action of the fs tool.
const ActEdit = "edit"

// editPlan is what Plan keeps of an edit for Run.
type editPlan struct {
	ops      []officedoc.Op
	expected int
	err      *officedoc.EditError // the ops are malformed: reported to the agent by Run
}

// maxEditDiffChars bounds the diff returned to the agent and the chat card.
const maxEditDiffChars = 4000

func (a *AgentFS) planEdit(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return planErr(err)
	}
	if f.DeletedAt != nil {
		return deny("not_found", "no such file")
	}
	zs := a.zoneAll(ctx, org, sc, f.Path)
	if !(canRead(zs) || autoWrite(zs)) { // an edit needs to read the file first
		return deny("not_granted", "this file is outside the areas you can read")
	}
	if _, ok := TypeOf(f.Name); !ok || officedoc.EditKind(f.Name) == "" || (officedoc.EditKind(f.Name) == "text" && !IsTextName(f.Name)) {
		return deny("unsupported_type", "edit works on text files (md, txt, csv, json, code), docx and xlsx")
	}
	pl := Plan{Path: f.Path, Bytes: int(f.Size), Reversibility: "versioned", Risk: "medium"}
	ep := &editPlan{}
	pl.edit = ep
	if v, ok := args["expected_version"]; !ok || v == nil {
		ep.err = &officedoc.EditError{Code: "invalid_args", Msg: "expected_version is required: read the file and pass the version you read"}
	} else if n, ok := argInt(args, "expected_version"); !ok || n < 1 {
		ep.err = &officedoc.EditError{Code: "invalid_args", Msg: "expected_version must be a positive integer"}
	} else {
		ep.expected = n
	}
	if ep.err == nil {
		ops, oerr := officedoc.ParseOps(args["ops"])
		ep.ops, ep.err = ops, oerr
	}
	if !autoWrite(zs) {
		pl.Approval, pl.Flags = true, []string{"outside_scope"}
		if zs == ZoneOtherDesk {
			pl.Flags = append(pl.Flags, "other_agent_desk")
		}
	}
	return pl
}

func editFailure(e *officedoc.EditError) RunResult {
	return RunResult{Code: e.Code, Summary: e.Code + ": " + e.Msg, Data: map[string]any{"op": e.Op, "matches": e.Matches}}
}

func (a *AgentFS) runEdit(ctx context.Context, agentID string, pl Plan, args map[string]any) RunResult {
	if pl.edit == nil {
		return RunResult{Code: "invalid_args", Summary: "invalid_args: bad edit"}
	}
	if pl.edit.err != nil {
		return editFailure(pl.edit.err)
	}
	me := AgentP(agentID)
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return failure(err)
	}
	exp := pl.edit.expected
	if f.CurrentVersion != exp {
		e := conflict("version_conflict", "the file is at version %d, not %d: read it again before editing", f.CurrentVersion, exp)
		e.Current = f.CurrentVersion
		return failure(e)
	}
	rc, _, err := a.Svc.Open(ctx, f, f.CurrentVersion)
	if err != nil {
		return failure(err)
	}
	data, err := objstore.ReadAllBounded(rc, a.Svc.cfg.Limits.MaxFileBytes)
	rc.Close()
	if err != nil {
		return failure(err)
	}
	out, eerr := officedoc.Edit(f.Name, data, pl.edit.ops)
	if eerr != nil {
		return editFailure(eerr)
	}
	if int64(len(out)) > a.Svc.cfg.Limits.MaxFileBytes {
		return editFailure(&officedoc.EditError{Code: "file_too_large", Msg: fmt.Sprintf("the result exceeds the limit of %d MB", a.Svc.cfg.Limits.MaxFileBytes>>20)})
	}
	n := len(pl.edit.ops)
	if bytes.Equal(out, data) {
		return RunResult{OK: true, Summary: fmt.Sprintf("%s unchanged (version %d): the edit produced the same content", f.Path, f.CurrentVersion),
			Data: map[string]any{"file_id": f.ID, "path": f.Path, "version": f.CurrentVersion, "previous_version": f.CurrentVersion, "added": 0, "removed": 0, "ops": n, "unchanged": true}}
	}
	msg := argStr(args, "message")
	if msg == "" {
		msg = fmt.Sprintf("Edit by agent: %d operation(s)", n)
	}
	res, err := a.Svc.NewVersion(ctx, me, f.ID, out, msg, &exp, "agent_edit")
	if err != nil {
		return failure(err)
	}
	before, after := editText4Diff(f.Name, data), editText4Diff(f.Name, out)
	lines, added, removed, trunc := DiffText(before, after)
	uni, tr2 := unified(lines, 2)
	if r := []rune(uni); len(r) > maxEditDiffChars {
		uni, tr2 = string(r[:maxEditDiffChars]), true
	}
	sum := fmt.Sprintf("edited %s (version %d -> %d): +%d -%d lines", res.File.Path, exp, res.File.CurrentVersion, added, removed)
	return RunResult{OK: true, Summary: sum, Text: uni, SourceNote: fmt.Sprintf("fs.edit %s v%d", res.File.Path, res.File.CurrentVersion),
		AuthoredByOthers: f.UpdatedBy != me,
		Data: map[string]any{"file_id": res.File.ID, "path": res.File.Path, "version": res.File.CurrentVersion, "previous_version": exp,
			"added": added, "removed": removed, "ops": n, "diff": uni, "diff_truncated": trunc || tr2}}
}

// editText4Diff is the text compared in a diff: the file itself for text types,
// the extracted text (docx paragraphs, first sheet rows) for office files.
func editText4Diff(name string, data []byte) string {
	if officedoc.EditKind(name) == "text" {
		if utf8.Valid(data) {
			return string(data)
		}
		return string(bytes.ToValidUTF8(data, []byte("?")))
	}
	return Extract(name, data, MaxIndexChars).Text
}
