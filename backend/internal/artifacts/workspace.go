package artifacts

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// Project workspaces: every agent node of a launched project gets a deliverable
// (an artifact owned by that agent). Its progress and build state follow the
// node, and when the node finishes the task output is written into it as a new
// version authored by the agent.

var _ projects.Sink = (*Service)(nil)

// Workspace is GET /projects/{id}/workspace.
func (s *Service) Workspace(ctx context.Context, projectID string) (ProjectWorkspace, error) {
	org := s.org(ctx)
	w := ProjectWorkspace{ProjectID: projectID, Name: projectID, Status: "running", Deliverables: []Deliverable{}, Links: []Link{}}
	if s.cfg.Projects != nil {
		name, status, err := s.cfg.Projects.Info(ctx, projectID)
		if err != nil {
			return w, err
		}
		w.Name, w.Status = name, status
	}
	metas, err := s.List(ctx, Filter{ProjectID: projectID})
	if err != nil {
		return w, err
	}
	rank := map[string]int{BuildBlocked: 0, BuildQueued: 1, BuildBuilding: 2, BuildReady: 3, BuildDone: 4}
	groups := map[string][]Meta{}
	var order []string
	for _, m := range metas {
		k := m.ID
		if m.DeliverableID != nil && *m.DeliverableID != "" {
			k = *m.DeliverableID
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}
	sort.Strings(order)
	for _, k := range order {
		list := groups[k]
		slices.SortFunc(list, func(a, b Meta) int { return a.CreatedAt.Compare(b.CreatedAt) })
		sum, state := 0, list[0].BuildState
		for _, m := range list {
			sum += m.Progress
			if rank[m.BuildState] < rank[state] {
				state = m.BuildState
			}
		}
		d := Deliverable{DeliverableID: k, Title: list[0].Title, AgentID: list[0].CreatedBy.ID, Progress: sum / len(list), BuildState: state, Artifacts: list, Status: "running"}
		if state == BuildDone {
			d.Status = "done"
		}
		w.Deliverables = append(w.Deliverables, d)
	}
	in := map[string]bool{}
	for _, m := range metas {
		in[m.ID] = true
	}
	all, err := s.cfg.Store.ListAllLinks(ctx, org)
	if err != nil {
		return w, err
	}
	for _, l := range all {
		if in[l.From] || in[l.To] {
			w.Links = append(w.Links, s.markStale(ctx, org, l))
		}
	}
	return w, nil
}

func buildOf(state string, progress int) (int, string) {
	switch state {
	case projects.StateDone:
		return 100, BuildReady
	case projects.StateRunning:
		return max(progress, 10), BuildBuilding
	case projects.StateAwaitingApproval:
		return max(progress, 80), BuildReady
	case projects.StateFailed, projects.StateBlocked, projects.StateCancelled:
		return progress, BuildBlocked
	}
	return 0, BuildQueued
}

// NodeChanged keeps the deliverable of a project node in sync with it.
func (s *Service) NodeChanged(ctx context.Context, ev projects.NodeEvent) {
	org := s.org(ctx)
	key := org + "|" + ev.ProjectID + "|" + ev.NodeID
	s.mu.Lock()
	id := s.deliv[key]
	s.mu.Unlock()
	if id == "" {
		if metas, err := s.List(ctx, Filter{ProjectID: ev.ProjectID}); err == nil {
			for _, m := range metas {
				if m.DeliverableID != nil && *m.DeliverableID == ev.NodeID {
					id = m.ID
				}
			}
		}
	}
	progress, build := buildOf(ev.State, ev.Progress)
	if id == "" {
		agent, err := s.cfg.Core.GetAgent(ctx, org, ev.AgentID)
		if err != nil {
			return
		}
		kind := KindDoc
		if sug := SuggestedKinds[agent.Role]; len(sug) > 0 {
			kind = sug[0]
		}
		locale := "es"
		in := CreateInput{Kind: kind, Title: ev.Title, TaskID: ev.TaskID, Locale: locale}
		if t, ok := findTemplate(defaultTemplate[agent.Role]); ok && t.kind == kind {
			in.Content = rawOf(t.build(locale))
		}
		a, err := s.create(ctx, Actor{Kind: ActorAgent, ID: agent.ID}, in,
			createOpts{progress: progress, build: build, projectID: ev.ProjectID, deliverableID: ev.NodeID, reqID: ev.RequestID})
		if err != nil {
			s.cfg.Log.Warn("create deliverable", "project", ev.ProjectID, "node", ev.NodeID, "err", err)
			return
		}
		s.mu.Lock()
		s.deliv[key] = a.ID
		s.mu.Unlock()
		id = a.ID
	} else {
		s.mu.Lock()
		s.deliv[key] = id
		s.mu.Unlock()
		s.setBuild(ctx, org, id, progress, build, ev.TaskID)
	}
	if ev.State == projects.StateDone && ev.Output != nil {
		s.writeOutput(ctx, org, id, ev)
	}
}

func (s *Service) setBuild(ctx context.Context, org, id string, progress int, build, taskID string) {
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return
	}
	m := cur.Meta
	if m.Progress == progress && m.BuildState == build {
		return
	}
	m.Progress, m.BuildState = progress, build
	if taskID != "" {
		m.TaskID = ptr(taskID)
	}
	if err := s.cfg.Store.UpdateMeta(ctx, org, m); err != nil {
		return
	}
	s.emit(ctx, "artifact.progress_changed", m.CreatedBy.ID, id, map[string]any{"artifact_id": id, "project_id": m.ProjectID, "deliverable_id": m.DeliverableID,
		"task_id": m.TaskID, "progress": progress, "build_state": build}, "")
}

// writeOutput stores the task output in the deliverable as a version by the agent.
func (s *Service) writeOutput(ctx context.Context, org, id string, ev projects.NodeEvent) {
	unlock := s.lock(org, id)
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil || cur.Meta.Locked {
		unlock()
		return
	}
	content := contentFromOutput(cur.Meta.Kind, cur.Meta.Locale, cur.Meta.Title, ev.Output)
	if content == nil {
		unlock()
		return
	}
	raw, root, err := parseContent(cur.Meta.Kind, rawOf(content))
	if err != nil {
		s.cfg.Log.Warn("deliverable output rejected", "artifact", id, "err", err)
		unlock()
		return
	}
	b := cur.Meta.HeadVersion
	author := ActorRef{Kind: ActorAgent, ID: ev.AgentID}
	v, err := s.commit(ctx, org, cur, raw, root, author, "agent_task", truncate(ev.Output.Summary, 160), &b)
	unlock()
	if err == nil {
		s.propagate(ctx, org, id, v, 0)
	}
}

func contentFromOutput(kind Kind, loc, title string, out *domain.StructuredOutput) map[string]any {
	switch kind {
	case KindDoc:
		nodes := []any{heading("b1", 1, title)}
		n := 2
		add := func(node map[string]any) { nodes = append(nodes, node); n++ }
		if out.Summary != "" {
			add(para(fmt.Sprintf("b%d", n), out.Summary))
		}
		for _, sec := range []struct {
			h     string
			items []string
		}{{L(loc, "Hallazgos", "Findings"), out.Findings}, {L(loc, "Recomendaciones", "Recommendations"), out.Recommendations}, {L(loc, "Evidencia", "Evidence"), out.Evidence}} {
			if len(sec.items) == 0 {
				continue
			}
			add(heading(fmt.Sprintf("b%d", n), 2, sec.h))
			for _, it := range sec.items {
				add(para(fmt.Sprintf("b%d", n), it))
			}
		}
		return docOf(nodes...)
	case KindSheet:
		cells := map[string]any{"A1": map[string]any{"v": L(loc, "Concepto", "Item")}, "B1": map[string]any{"v": L(loc, "Valor", "Value")},
			"A2": map[string]any{"v": L(loc, "Resumen", "Summary")}, "B2": map[string]any{"v": truncate(out.Summary, 500)}}
		keys := make([]string, 0, len(out.Metrics))
		for k := range out.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			r := i + 3
			cells[fmt.Sprintf("A%d", r)] = map[string]any{"v": k}
			if f, ok := toNum(out.Metrics[k]); ok {
				cells[fmt.Sprintf("B%d", r)] = map[string]any{"v": f}
			} else {
				cells[fmt.Sprintf("B%d", r)] = map[string]any{"v": strings.TrimSpace(fmt.Sprint(out.Metrics[k]))}
			}
		}
		return map[string]any{"schema": "aiw.sheet/1", "sheets": []any{map[string]any{"id": "s1", "name": L(loc, "Resultado", "Result"), "rows": max(40, len(keys)+10), "cols": 8,
			"cells": cells, "colw": map[string]any{"A": 220, "B": 220}}}}
	case KindTable:
		var rows []map[string]any
		for _, f := range out.Findings {
			rows = append(rows, map[string]any{"item": f, "type": L(loc, "Hallazgo", "Finding")})
		}
		for _, r := range out.Recommendations {
			rows = append(rows, map[string]any{"item": r, "type": L(loc, "Recomendación", "Recommendation")})
		}
		if len(rows) == 0 {
			rows = []map[string]any{{"item": out.Summary, "type": L(loc, "Resumen", "Summary")}}
		}
		return tableOf([][]any{{"item", L(loc, "Detalle", "Detail")}, {"type", L(loc, "Tipo", "Type")}}, rows)
	}
	return nil
}
