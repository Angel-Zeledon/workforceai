package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/sanitize"
)

// ProfileService is the work profile of an agent: extra instructions, a
// creativity level, a tone and skill tags. Humans with agents:write edit it;
// every change is a new immutable version, audited, and any version can be
// restored. The profile is trusted organization configuration, but it never
// grants anything: tools, permissions, autonomy and approvals come from the role,
// the organization rules and RBAC only (the runtime repeats that to the model).
type ProfileService struct {
	Store  V2Store
	Core   Store
	Tones  ConfigStore // per-agent tone (optional: without it the tone is not managed here)
	Rec    *Recorder
	Config Config
	Locale func(ctx context.Context) string // organization locale for the role skills (optional)
}

// ProfileInput is a requested change. A nil Tone leaves the tone untouched ("" clears it).
type ProfileInput struct {
	Instructions string   `json:"instructions"`
	Creativity   string   `json:"creativity"`
	Skills       []string `json:"skills"`
	Tone         *string  `json:"tone"`
}

// ProfileLimits tells the UI what is accepted.
type ProfileLimits struct {
	InstructionsMax int `json:"instructions_max"`
	SkillsMax       int `json:"skills_max"`
	SkillMaxLen     int `json:"skill_max_len"`
}

// ProfileView is what GET /agents/{id}/profile returns.
type ProfileView struct {
	AgentProfile
	RoleSkills []string      `json:"role_skills"`
	Limits     ProfileLimits `json:"limits"`
}

var skillRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _.+#-]*$`)

func (s *ProfileService) org(ctx context.Context) string { return OrgFrom(ctx, s.Config.OrgID) }

func defaultProfile(agentID string) AgentProfile {
	return AgentProfile{AgentID: agentID, ProfileSnapshot: ProfileSnapshot{Creativity: CreativityMedium, Skills: []string{}}}
}

// current returns the stored profile or the defaults (version 0).
func (s *ProfileService) current(ctx context.Context, agentID string) (AgentProfile, error) {
	p, ok, err := s.Store.GetAgentProfile(ctx, s.org(ctx), agentID)
	if err != nil {
		return p, err
	}
	if !ok {
		p = defaultProfile(agentID)
	}
	if p.Skills == nil {
		p.Skills = []string{}
	}
	if p.Creativity == "" {
		p.Creativity = CreativityMedium
	}
	return p, nil
}

// tone is the per-agent tone override currently stored ("" = organization tone).
func (s *ProfileService) tone(ctx context.Context, agentID string) string {
	if s.Tones == nil {
		return ""
	}
	m, err := s.Tones.ListAgentTones(ctx, s.org(ctx))
	if err != nil {
		return ""
	}
	return m[agentID]
}

// Get returns the profile of an agent (defaults when never edited).
func (s *ProfileService) Get(ctx context.Context, agentID string) (ProfileView, error) {
	a, err := s.Core.GetAgent(ctx, s.org(ctx), agentID)
	if err != nil {
		return ProfileView{}, err
	}
	p, err := s.current(ctx, agentID)
	if err != nil {
		return ProfileView{}, err
	}
	p.Tone = s.tone(ctx, agentID)
	loc := ""
	if s.Locale != nil {
		loc = s.Locale(ctx)
	}
	return ProfileView{AgentProfile: p, RoleSkills: roleSkills(a.Role, loc), Limits: profileLimits()}, nil
}

func profileLimits() ProfileLimits {
	return ProfileLimits{InstructionsMax: MaxInstructionsRunes, SkillsMax: MaxSkills, SkillMaxLen: MaxSkillRunes}
}

// roleSkills are the suggested tags of the role template (its routing keywords), shown read-only.
func roleSkills(role, loc string) []string {
	p, ok := roles.ProfileOf(role, loc)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, k := range p.Keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && len([]rune(k)) <= MaxSkillRunes && !slices.Contains(out, k) {
			out = append(out, k)
		}
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// Versions returns the history, newest first.
func (s *ProfileService) Versions(ctx context.Context, agentID string) ([]ProfileVersion, error) {
	if _, err := s.Core.GetAgent(ctx, s.org(ctx), agentID); err != nil {
		return nil, err
	}
	v, err := s.Store.ListProfileVersions(ctx, s.org(ctx), agentID)
	if v == nil {
		v = []ProfileVersion{}
	}
	return v, err
}

// actor is the human making the change. An agent (or anything that looks like
// one) can never edit a profile, not even its own.
func (s *ProfileService) actor(ctx context.Context) (string, error) {
	a := ActorFrom(ctx, "")
	if a == "" {
		return "user", nil // open demo without auth: same convention as the rest of the API
	}
	if strings.HasPrefix(a, "agent:") {
		return "", fmt.Errorf("%w: agents cannot edit work profiles", domain.ErrForbidden)
	}
	if _, err := s.Core.GetAgent(ctx, s.org(ctx), a); err == nil {
		return "", fmt.Errorf("%w: agents cannot edit work profiles", domain.ErrForbidden)
	}
	return a, nil
}

// CleanInstructions validates extra instructions: bounded, printable and free of
// anything with the shape of a credential (they are sent to the model provider).
func CleanInstructions(raw string) (string, error) {
	var b strings.Builder
	for _, r := range raw {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	t := strings.TrimSpace(b.String())
	if len([]rune(t)) > MaxInstructionsRunes {
		return "", fmt.Errorf("%w: instructions are too long (max %d characters)", domain.ErrInvalid, MaxInstructionsRunes)
	}
	if sanitize.RedactSecrets(t, nil) != t {
		return "", fmt.Errorf("%w: instructions seem to contain a secret or credential; they are sent to the model provider, remove it", domain.ErrInvalid)
	}
	return t, nil
}

// CleanSkills normalizes the custom skill tags (lowercase, unique, bounded).
func CleanSkills(in []string) ([]string, error) {
	out := []string{}
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if len([]rune(t)) > MaxSkillRunes || !skillRe.MatchString(t) {
			return nil, fmt.Errorf("%w: invalid skill %q", domain.ErrInvalid, raw)
		}
		if sanitize.RedactSecrets(t, nil) != t {
			return nil, fmt.Errorf("%w: invalid skill %q", domain.ErrInvalid, raw)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) > MaxSkills {
		return nil, fmt.Errorf("%w: at most %d skills", domain.ErrInvalid, MaxSkills)
	}
	return out, nil
}

func cleanCreativity(v string) (string, error) {
	switch c := strings.ToLower(strings.TrimSpace(v)); c {
	case "":
		return CreativityMedium, nil
	case CreativityLow, CreativityMedium, CreativityHigh:
		return c, nil
	}
	return "", fmt.Errorf("%w: creativity must be low, medium or high", domain.ErrInvalid)
}

// Update validates and stores a new version. Nothing is written when nothing changed.
func (s *ProfileService) Update(ctx context.Context, agentID string, in ProfileInput) (ProfileView, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return ProfileView{}, err
	}
	if _, err := s.Core.GetAgent(ctx, s.org(ctx), agentID); err != nil {
		return ProfileView{}, err
	}
	cur, err := s.current(ctx, agentID)
	if err != nil {
		return ProfileView{}, err
	}
	cur.Tone = s.tone(ctx, agentID)
	next := ProfileSnapshot{Tone: cur.Tone}
	if next.Instructions, err = CleanInstructions(in.Instructions); err != nil {
		return ProfileView{}, err
	}
	if next.Creativity, err = cleanCreativity(in.Creativity); err != nil {
		return ProfileView{}, err
	}
	if next.Skills, err = CleanSkills(in.Skills); err != nil {
		return ProfileView{}, err
	}
	if in.Tone != nil {
		t := strings.ToLower(strings.TrimSpace(*in.Tone))
		if t != "" {
			var ok bool
			if t, ok = catalog.NormalizeTone(t); !ok {
				return ProfileView{}, fmt.Errorf("%w: unknown tone %q", domain.ErrInvalid, *in.Tone)
			}
		}
		next.Tone = t
	}
	return s.apply(ctx, actor, agentID, cur, next, nil)
}

// Restore makes a past version the current one, as a NEW version (history is never rewritten).
func (s *ProfileService) Restore(ctx context.Context, agentID string, version int) (ProfileView, error) {
	actor, err := s.actor(ctx)
	if err != nil {
		return ProfileView{}, err
	}
	if _, err := s.Core.GetAgent(ctx, s.org(ctx), agentID); err != nil {
		return ProfileView{}, err
	}
	old, err := s.Store.GetProfileVersion(ctx, s.org(ctx), agentID, version)
	if err != nil {
		return ProfileView{}, err
	}
	cur, err := s.current(ctx, agentID)
	if err != nil {
		return ProfileView{}, err
	}
	cur.Tone = s.tone(ctx, agentID)
	snap := old.Snapshot
	if snap.Skills == nil {
		snap.Skills = []string{}
	}
	return s.apply(ctx, actor, agentID, cur, snap, &version)
}

func (s *ProfileService) apply(ctx context.Context, actor, agentID string, cur AgentProfile, next ProfileSnapshot, restoredFrom *int) (ProfileView, error) {
	diff := diffSnapshots(cur.ProfileSnapshot, next)
	if len(diff) == 0 {
		return s.Get(ctx, agentID)
	}
	now := time.Now().UTC()
	p := AgentProfile{AgentID: agentID, ProfileSnapshot: next, Version: cur.Version + 1, UpdatedBy: actor, UpdatedAt: &now}
	v := ProfileVersion{AgentID: agentID, Version: p.Version, Author: actor, CreatedAt: now, RestoredFrom: restoredFrom, Diff: diff, Snapshot: next}
	if err := s.Store.SaveAgentProfile(ctx, s.org(ctx), p, v); err != nil {
		return ProfileView{}, err
	}
	if _, changed := diff["tone"]; changed && s.Tones != nil { // the tone keeps its existing storage and runtime path
		if err := s.Tones.SetAgentTone(ctx, s.org(ctx), agentID, next.Tone); err != nil {
			return ProfileView{}, err
		}
	}
	fields := make([]string, 0, len(diff))
	for k := range diff {
		fields = append(fields, k)
	}
	slices.Sort(fields)
	action := "agent.profile_updated"
	details := map[string]any{"version": p.Version, "fields": fields, "creativity": next.Creativity, "skills": len(next.Skills),
		"instructions_chars": len([]rune(next.Instructions)), "instructions_sha256": shortHash(next.Instructions)}
	if restoredFrom != nil {
		action, details["restored_from"] = "agent.profile_restored", *restoredFrom
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: "agent", EntityID: agentID, Details: details})
	s.Rec.Emit(ctx, Action{Type: "agent.profile_changed", AgentID: agentID, Entity: "agent", EntityID: agentID, SkipAudit: true,
		Payload: map[string]any{"agent_id": agentID, "version": p.Version}})
	return s.Get(ctx, agentID)
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

func diffSnapshots(a, b ProfileSnapshot) map[string]ProfileChange {
	d := map[string]ProfileChange{}
	if a.Instructions != b.Instructions {
		d["instructions"] = ProfileChange{From: a.Instructions, To: b.Instructions}
	}
	if (a.Creativity != b.Creativity) && !(a.Creativity == "" && b.Creativity == CreativityMedium) {
		d["creativity"] = ProfileChange{From: orDefault(a.Creativity), To: b.Creativity}
	}
	if !slices.Equal(nonNil(a.Skills), nonNil(b.Skills)) {
		d["skills"] = ProfileChange{From: nonNil(a.Skills), To: nonNil(b.Skills)}
	}
	if a.Tone != b.Tone {
		d["tone"] = ProfileChange{From: a.Tone, To: b.Tone}
	}
	return d
}

func orDefault(c string) string {
	if c == "" {
		return CreativityMedium
	}
	return c
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- what the orchestrator sends to the runtime ----

// RunProfile is the part of the profile the runtime receives (all optional).
type RunProfile struct {
	Instructions string
	Creativity   string
	Skills       []string
}

// RunProfileOf returns the profile to send for an agent; ok=false when none was ever saved.
func (s *ProfileService) RunProfileOf(ctx context.Context, agentID string) (RunProfile, bool) {
	p, ok, err := s.Store.GetAgentProfile(ctx, s.org(ctx), agentID)
	if err != nil || !ok || p.Version == 0 {
		return RunProfile{}, false
	}
	return RunProfile{Instructions: p.Instructions, Creativity: p.Creativity, Skills: p.Skills}, true
}

// SkillsByAgent returns the custom skills of every edited agent (routing hints for the planner and the router).
func (s *ProfileService) SkillsByAgent(ctx context.Context) map[string][]string {
	ps, err := s.Store.ListAgentProfiles(ctx, s.org(ctx))
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range ps {
		if len(p.Skills) > 0 {
			out[p.AgentID] = p.Skills
		}
	}
	return out
}
