package application

import (
	"context"
	"regexp"

	"aiworkforce/backend/internal/domain"
)

// ContextSource lets the backend read the text of artifacts a task explicitly
// references (W3). Implementations must check that the agent may read each one
// and bound the total to budgetTokens; the result is injected as delimited
// data, the runtime never reads anything itself.
type ContextSource interface {
	ContextArtifacts(ctx context.Context, org, agentID, requestID string, ids []string, budgetTokens int) []ContextArtifact
}

// SetContextSource installs the artifact reader (nil removes it).
func (o *Orchestrator) SetContextSource(s ContextSource) { o.ctxSrc = s }

var artifactRefRe = regexp.MustCompile(`artifact:([A-Za-z0-9][A-Za-z0-9_-]{5,63})`)

// artifactRefs lists the artifact ids a task description references
// ("artifact:<id>"), unique and capped.
func artifactRefs(text string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range artifactRefRe.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] && len(ids) < maxContextArtifacts {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// projectContext builds the bounded index of the other finished tasks of a
// project request (and the artifacts the task references). It is nil for
// requests that are not projects and when there is nothing to show, so the
// payload of ordinary requests does not change.
func (o *Orchestrator) projectContext(ctx context.Context, rs *run, t domain.Task, others []domain.Task) *ProjectContext {
	refs := artifactRefs(t.Description)
	if len(others) == 0 && len(refs) == 0 {
		return nil
	}
	if ow := o.durable.owner; ow == nil || !ow.OwnsRequest(ctx, o.org(ctx), rs.req.ID) {
		return nil
	}
	pc := &ProjectContext{Index: []ProjectTaskRef{}}
	pc.Index, pc.IndexOmitted = buildProjectIndex(others, o.cfg.projectContextBudget())
	if len(refs) > 0 && o.ctxSrc != nil {
		pc.Artifacts = o.ctxSrc.ContextArtifacts(ctx, o.org(ctx), t.AgentID, rs.req.ID, refs, defaultContextArtifactBudget)
	}
	if len(pc.Index) == 0 && len(pc.Artifacts) == 0 {
		return nil
	}
	return pc
}
