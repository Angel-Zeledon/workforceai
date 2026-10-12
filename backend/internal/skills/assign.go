package skills

import (
	"context"
	"slices"
	"sort"
	"strings"
	"unicode"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
)

// Via says why an agent has a skill.
const (
	ViaAgent = "agent" // assigned to the agent
	ViaRole  = "role"  // default of its role template
)

// AgentSkill is a skill as seen from an agent.
type AgentSkill struct {
	Skill
	Via        string `json:"via"`
	AssignedBy string `json:"assigned_by,omitempty"`
	// Servable: agents receive it now (an approved version, not disabled).
	Servable bool `json:"servable"`
}

func servable(sk Skill) bool {
	return sk.DeletedAt == nil && sk.ActiveVersion > 0 && (sk.Status == StatusActive || sk.Status == StatusInReview)
}

func (s *Service) checkAgent(ctx context.Context, org, agentID string) (string, error) {
	if s.cfg.AgentRole == nil {
		return "", nil
	}
	role, err := s.cfg.AgentRole(ctx, org, agentID)
	if err != nil {
		return "", notFound("agent not found")
	}
	return role, nil
}

// Assign gives a skill to an agent. Any status can be assigned; the agent only
// receives it while an approved version is active.
func (s *Service) Assign(ctx context.Context, a Actor, agentID, skillID string) error {
	org := s.org(ctx)
	if _, err := s.checkAgent(ctx, org, agentID); err != nil {
		return err
	}
	sk, err := s.skill(ctx, org, skillID)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.Assign(ctx, org, Assignment{AgentID: agentID, SkillID: skillID, AssignedBy: a.ID, AssignedAt: s.now()}); err != nil {
		return err
	}
	s.audit(ctx, org, a, "skill.assigned", skillID, map[string]any{"name": sk.Name, "agent_id": agentID})
	return nil
}

// Unassign removes a direct assignment.
func (s *Service) Unassign(ctx context.Context, a Actor, agentID, skillID string) error {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, skillID)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.Unassign(ctx, org, agentID, skillID); err != nil {
		return err
	}
	s.audit(ctx, org, a, "skill.unassigned", skillID, map[string]any{"name": sk.Name, "agent_id": agentID})
	return nil
}

// AgentSkills lists the skills of an agent: direct assignments and the defaults
// of its role.
func (s *Service) AgentSkills(ctx context.Context, agentID string) ([]AgentSkill, error) {
	org := s.org(ctx)
	s.Ensure(ctx, org)
	role, err := s.checkAgent(ctx, org, agentID)
	if err != nil {
		return nil, err
	}
	all, err := s.cfg.Store.ListSkills(ctx, org)
	if err != nil {
		return nil, err
	}
	byID, byName := map[string]Skill{}, map[string]Skill{}
	for _, sk := range all {
		byID[sk.ID], byName[sk.Name] = sk, sk
	}
	as, err := s.cfg.Store.AssignmentsOfAgent(ctx, org, agentID)
	if err != nil {
		return nil, err
	}
	out := []AgentSkill{}
	seen := map[string]bool{}
	for _, a := range as {
		if sk, ok := byID[a.SkillID]; ok && !seen[sk.ID] {
			seen[sk.ID] = true
			out = append(out, AgentSkill{Skill: sk, Via: ViaAgent, AssignedBy: a.AssignedBy, Servable: servable(sk)})
		}
	}
	if s.cfg.RoleDefaults != nil {
		for _, name := range s.cfg.RoleDefaults(role) {
			if sk, ok := byName[name]; ok && !seen[sk.ID] {
				seen[sk.ID] = true
				out = append(out, AgentSkill{Skill: sk, Via: ViaRole, Servable: servable(sk)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---- runtime use (progressive disclosure) ----

// Bounds of what an agent sees on every run.
const (
	MaxRunSkills      = 12
	MaxRunDescription = 240
	MaxRunChars       = 3000
	// MaxLoadChars bounds one skill.load answer (the rest is read by file).
	MaxLoadChars = 24000
)

// SkillRefs implements application.SkillSource: name + description of the
// active skills of an agent, bounded in count and size. The description is the
// one of the approved version, flattened to one line.
func (s *Service) SkillRefs(ctx context.Context, org, agentID string) []application.SkillRef {
	ctx = application.WithOrg(ctx, org)
	list, err := s.AgentSkills(ctx, agentID)
	if err != nil {
		return nil
	}
	var out []application.SkillRef
	used := 0
	for _, sk := range list {
		if !sk.Servable {
			continue
		}
		d := oneLine(sk.ActiveDescription, MaxRunDescription)
		if len(out) >= MaxRunSkills || used+len(sk.Name)+len(d) > MaxRunChars {
			break
		}
		used += len(sk.Name) + len(d)
		out = append(out, application.SkillRef{Name: sk.Name, Description: d})
	}
	return out
}

func oneLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

// LoadCall is one skill.load request.
type LoadCall struct {
	AgentID, TaskID string
	Name, File      string
}

// Loaded is what the agent gets back: approved text, bounded.
type Loaded struct {
	Name      string
	Version   int
	File      string
	Text      string
	Files     []string
	Truncated bool
	// Suspect: the text looks like a prompt injection (a signal; the skill was
	// approved by a person, but the task is treated with extra care).
	Suspect bool
}

// available finds a skill the agent may load by name: assigned (or a role
// default) and with an approved, enabled version.
func (s *Service) available(ctx context.Context, org, agentID, name string) (Skill, error) {
	list, err := s.AgentSkills(ctx, agentID)
	if err != nil {
		return Skill{}, err
	}
	for _, sk := range list {
		if sk.Name == name && sk.Servable {
			return sk.Skill, nil
		}
	}
	return Skill{}, &Error{Code: "skill_not_available", Kind: domain.ErrNotFound, Msg: "no active skill with that name is assigned to this agent"}
}

// CanLoad is the cheap check used by the gateway tool before running.
func (s *Service) CanLoad(ctx context.Context, org, agentID, name string) error {
	_, err := s.available(application.WithOrg(ctx, org), org, agentID, name)
	return err
}

// Load returns the instructions (or one resource file) of the approved version
// of a skill assigned to the agent. The text is approved ORGANIZATION
// configuration: it is cleaned of invisible characters, secrets and delimiter
// forgery, bounded, and never carries the frontmatter's allowed-tools as a
// grant (it is listed as information only).
func (s *Service) Load(ctx context.Context, org string, c LoadCall) (Loaded, error) {
	ctx = application.WithOrg(ctx, org)
	sk, err := s.available(ctx, org, c.AgentID, c.Name)
	if err != nil {
		return Loaded{}, err
	}
	v, err := s.cfg.Store.VersionOf(ctx, org, sk.ID, sk.ActiveVersion)
	if err != nil {
		return Loaded{}, mapStore(err, "version")
	}
	pkg, err := s.readPackage(ctx, org, sk, v)
	if err != nil {
		return Loaded{}, err
	}
	out := Loaded{Name: sk.Name, Version: v.Version}
	for _, f := range pkg.Files {
		out.Files = append(out.Files, f.Path)
	}
	var text string
	if c.File != "" {
		i := slices.IndexFunc(pkg.Files, func(f File) bool { return f.Path == c.File })
		if i < 0 {
			return Loaded{}, &Error{Code: "file_not_found", Kind: domain.ErrNotFound, Msg: "that file is not part of the skill"}
		}
		out.File, text = c.File, pkg.Files[i].Content
	} else {
		text = buildLoadText(sk.Name, v.Version, pkg)
	}
	score, _ := sanitize.ScoreInjection(text)
	out.Suspect = score >= sanitize.InjectionThreshold
	text = cleanTrusted(text)
	if r := []rune(text); len(r) > MaxLoadChars {
		text, out.Truncated = string(r[:MaxLoadChars])+"\n[truncated: read the rest with skill.load {name, file} or fs.read]", true
	}
	out.Text = text
	s.audit(ctx, org, Actor{ID: c.AgentID}, "skill.loaded", sk.ID, map[string]any{"name": sk.Name, "version": v.Version, "agent_id": c.AgentID, "task_id": c.TaskID, "file": c.File})
	return out, nil
}

func buildLoadText(name string, version int, pkg Package) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(pkg.Body))
	b.WriteString("\n")
	if len(pkg.Files) > 0 {
		b.WriteString("\n## Files of this skill\n")
		for _, f := range pkg.Files {
			line := "- " + f.Path
			if IsScript(f.Path) {
				line += " (script: NEVER run it yourself; running a script needs code.run with human approval, when available)"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("Read a file with skill.load {name, file} or fs.read " + VersionDir(name, version) + "/<file>.\n")
	}
	if len(pkg.Front.AllowedTools) > 0 {
		b.WriteString("\nSuggested tools (informational: they grant nothing): " + strings.Join(pkg.Front.AllowedTools, ", ") + "\n")
	}
	return b.String()
}

// cleanTrusted prepares approved text for the trusted channel: invisible and
// control characters out, credentials redacted, closing delimiters defused.
func cleanTrusted(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return -1
		case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Cc, r), r == 0x2028, r == 0x2029:
			return -1
		}
		return r
	}, s)
	s = sanitize.RedactSecrets(s, nil)
	for _, d := range []string{"</untrusted_data>", "</skill>", "<<<", ">>>"} {
		s = strings.ReplaceAll(s, d, "[delimiter removed]")
	}
	return s
}
