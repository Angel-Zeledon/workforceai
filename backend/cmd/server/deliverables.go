package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/drive"
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
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// deliverableName is "<node>-<slug>.md": stable per node, readable, safe.
func deliverableName(node, title string) string {
	slug := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if r := []rune(slug); len(r) > 60 {
		slug = strings.Trim(string(r[:60]), "-")
	}
	n := strings.Trim(unsafeName.ReplaceAllString(node, "-"), "-")
	if slug == "" {
		return n + ".md"
	}
	return n + "-" + slug + ".md"
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
