package application

import "context"

// SkillRef is what an agent sees of a skill before loading it: name and
// description only (progressive disclosure).
type SkillRef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SkillSource resolves the active skills of an agent (installed by the server
// when the skill library is configured). It returns at most a bounded list.
type SkillSource interface {
	SkillRefs(ctx context.Context, org, agentID string) []SkillRef
}

// SetSkillSource installs the skill resolver (nil removes it).
func (o *Orchestrator) SetSkillSource(s SkillSource) { o.skillSrc = s }

// skillsFor is the progressive-disclosure list of an agent for a run.
func (o *Orchestrator) skillsFor(ctx context.Context, agentID string) []SkillRef {
	if o.skillSrc == nil {
		return nil
	}
	return o.skillSrc.SkillRefs(ctx, o.org(ctx), agentID)
}

// addLoadedSkill remembers a skill the agent loaded so the next turn carries
// its text; one entry per skill file, at most maxLoadedSkills per task.
func addLoadedSkill(in *RunTaskRequest, b TrustedBlock) {
	const maxLoadedSkills = 4
	for _, l := range in.LoadedSkills {
		if l.Name == b.Name && l.File == b.File {
			return
		}
	}
	if len(in.LoadedSkills) >= maxLoadedSkills {
		return
	}
	in.LoadedSkills = append(in.LoadedSkills, b)
}
