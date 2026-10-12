package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/officedoc"
	"aiworkforce/backend/internal/projects"
)

// deliverableSink forwards node changes to the next sink (the artifacts
// workspace) and, when a project task finishes with an output, saves that
// output as a Markdown file in the project's drive folder, attributed to the
// agent. Rewriting the same content is a no-op (no new version), so replays
// and restarts are harmless. A failure here never affects the project: it is
// logged and the next sink still runs.
type deliverableSink struct {
	next  projects.Sink
	drive *drive.Service
	log   *slog.Logger
}

func (d deliverableSink) NodeChanged(ctx context.Context, ev projects.NodeEvent) {
	if d.next != nil {
		d.next.NodeChanged(ctx, ev)
	}
	if d.drive == nil || ev.State != projects.StateDone || ev.Kind != projects.KindTask || ev.Output == nil || ev.AgentID == "" {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	folder, err := d.drive.EnsureOwnedFolder(wctx, drive.RootProjects, ev.ProjectID, ev.ProjectName)
	if err != nil {
		d.log.Warn("deliverable: project folder", "project", ev.ProjectID, "err", err)
		return
	}
	// node ids carry the project id ("prj-xxx:board"); the folder already names the project
	node := strings.TrimPrefix(strings.TrimPrefix(ev.NodeID, ev.ProjectID+":"), ev.ProjectID+"-")
	path := folder.Path + "/" + deliverableName(node, ev.Title)
	_, err = d.drive.Write(wctx, drive.AgentP(ev.AgentID), drive.WriteInput{Path: path, Data: []byte(renderDeliverable(ev)),
		Message: "deliverable of " + ev.NodeID, Source: "deliverable"})
	if err != nil {
		d.log.Warn("deliverable: write", "project", ev.ProjectID, "node", ev.NodeID, "err", err)
	}
	// The request named a format (Excel, Word, PDF): the deliverable is also
	// delivered as that real file, built from the same structured output.
	if format := officedoc.FormatFromText(ev.ProjectName + " " + ev.Title); format != "" {
		data, err := officedoc.Build(format, officeDeliverable(ev))
		if err != nil {
			d.log.Warn("deliverable: build", "project", ev.ProjectID, "node", ev.NodeID, "format", format, "err", err)
			return
		}
		opath := folder.Path + "/" + deliverableNameExt(node, ev.Title, officedoc.Ext(format))
		if _, err := d.drive.Write(wctx, drive.AgentP(ev.AgentID), drive.WriteInput{Path: opath, Data: data,
			Message: "deliverable of " + ev.NodeID, Source: "deliverable"}); err != nil {
			d.log.Warn("deliverable: write", "project", ev.ProjectID, "node", ev.NodeID, "format", format, "err", err)
		}
	}
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// deliverableName is "<node>-<slug>.md": stable per node, readable, safe.
func deliverableName(node, title string) string { return deliverableNameExt(node, title, ".md") }

func deliverableNameExt(node, title, ext string) string {
	slug := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if r := []rune(slug); len(r) > 60 {
		slug = strings.Trim(string(r[:60]), "-")
	}
	n := strings.Trim(unsafeName.ReplaceAllString(node, "-"), "-")
	if slug == "" {
		return n + ext
	}
	return n + "-" + slug + ext
}

func renderDeliverable(ev projects.NodeEvent) string {
	o := ev.Output
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(ev.Title))
	fmt.Fprintf(&b, "_Project: %s · Task: %s · Agent: %s_\n\n", ev.ProjectName, ev.NodeID, ev.AgentID)
	if s := strings.TrimSpace(o.Summary); s != "" {
		b.WriteString("## Summary\n\n" + s + "\n\n")
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("## " + title + "\n\n")
		for _, it := range items {
			b.WriteString("- " + strings.TrimSpace(it) + "\n")
		}
		b.WriteString("\n")
	}
	section("Findings", o.Findings)
	section("Recommendations", o.Recommendations)
	section("Evidence", o.Evidence)
	section("Hypotheses", o.Hypotheses)
	if len(o.Metrics) > 0 {
		keys := make([]string, 0, len(o.Metrics))
		for k := range o.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("## Metrics\n\n| Metric | Value |\n|---|---|\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s | %v |\n", k, o.Metrics[k])
		}
		b.WriteString("\n")
	}
	if o.Confidence > 0 {
		fmt.Fprintf(&b, "_Confidence reported by the agent: %.2f_\n", o.Confidence)
	}
	return b.String()
}

// officeDeliverable is the structured output as an office document: a text
// document (docx, pdf) with sections and a metrics table; a workbook (xlsx)
// with one sheet per section.
func officeDeliverable(ev projects.NodeEvent) officedoc.Doc {
	o := ev.Output
	title := strings.TrimSpace(ev.Title)
	list := func(h string, items []string) []officedoc.Block {
		if len(items) == 0 {
			return nil
		}
		return []officedoc.Block{{Type: "heading", Level: 2, Text: h}, {Type: "bullets", Items: items}}
	}
	d := officedoc.Doc{Title: title}
	if s := strings.TrimSpace(o.Summary); s != "" {
		d.Blocks = append(d.Blocks, officedoc.Block{Type: "heading", Level: 2, Text: "Summary"}, officedoc.Block{Type: "paragraph", Text: s})
	}
	d.Blocks = append(d.Blocks, list("Findings", o.Findings)...)
	d.Blocks = append(d.Blocks, list("Recommendations", o.Recommendations)...)
	d.Blocks = append(d.Blocks, list("Evidence", o.Evidence)...)
	d.Blocks = append(d.Blocks, list("Hypotheses", o.Hypotheses)...)
	if len(o.Metrics) > 0 {
		keys := make([]string, 0, len(o.Metrics))
		for k := range o.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var rows [][]any
		for _, k := range keys {
			switch v := o.Metrics[k].(type) {
			case float64, string, bool, nil:
				rows = append(rows, []any{k, v})
			case int:
				rows = append(rows, []any{k, float64(v)})
			case int64:
				rows = append(rows, []any{k, float64(v)})
			default:
				rows = append(rows, []any{k, fmt.Sprint(v)})
			}
		}
		d.Blocks = append(d.Blocks, officedoc.Block{Type: "heading", Level: 2, Text: "Metrics"},
			officedoc.Block{Type: "table", Columns: []officedoc.Column{{Header: "Metric"}, {Header: "Value"}}, Rows: rows})
	}
	if len(d.Blocks) == 0 {
		d.Blocks = []officedoc.Block{{Type: "paragraph", Text: title}}
	}
	// a workbook has one sheet per section instead of blocks
	d.Sheets = officeSheets(o, ev)
	if out, err := officedoc.Prepare(d); err == nil {
		return out
	}
	return officedoc.Doc{Title: title, Blocks: []officedoc.Block{{Type: "paragraph", Text: renderDeliverable(ev)}}}
}

func officeSheets(o *domain.StructuredOutput, ev projects.NodeEvent) []officedoc.Sheet {
	var out []officedoc.Sheet
	var head [][]any
	if s := strings.TrimSpace(o.Summary); s != "" {
		head = append(head, []any{"Summary", s})
	}
	if o.Confidence > 0 {
		head = append(head, []any{"Confidence", fmt.Sprintf("%.2f", o.Confidence)})
	}
	head = append(head, []any{"Task", strings.TrimSpace(ev.Title)}, []any{"Agent", ev.AgentID})
	out = append(out, officedoc.Sheet{Name: "Summary", Columns: []officedoc.Column{{Header: "Field", Type: "text"}, {Header: "Value", Type: "text"}}, Rows: head})
	list := func(name string, items []string) {
		if len(items) == 0 {
			return
		}
		rows := make([][]any, 0, len(items))
		for i, it := range items {
			rows = append(rows, []any{float64(i + 1), strings.TrimSpace(it)})
		}
		out = append(out, officedoc.Sheet{Name: name, Columns: []officedoc.Column{{Header: "#", Type: "number"}, {Header: name, Type: "text"}}, Rows: rows})
	}
	list("Findings", o.Findings)
	list("Recommendations", o.Recommendations)
	list("Evidence", o.Evidence)
	list("Hypotheses", o.Hypotheses)
	if len(o.Metrics) > 0 {
		keys := make([]string, 0, len(o.Metrics))
		for k := range o.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rows := make([][]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []any{k, fmt.Sprint(o.Metrics[k])})
		}
		out = append(out, officedoc.Sheet{Name: "Metrics", Columns: []officedoc.Column{{Header: "Metric", Type: "text"}, {Header: "Value", Type: "text"}}, Rows: rows})
	}
	return out
}
