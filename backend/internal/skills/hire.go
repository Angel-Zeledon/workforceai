package skills

import (
	"context"
	"sort"

	"aiworkforce/backend/internal/offices"
)

// Hiring with an office (design 2026-11-23 §3: "Contratar"). The skills a new agent may get are the company's and
// those of the office it is hired for; the position and the role template suggest some.

// CanAssignTo checks, before anything is created, that the actor sees the skill and that it works in the office the
// agent is hired for (a skill of another department does not).
func (s *Service) CanAssignTo(ctx context.Context, a Actor, officeID, skillID string) error {
	org := s.org(ctx)
	sk, err := s.visible(ctx, org, a, skillID)
	if err != nil {
		return err
	}
	if sk.DeletedAt != nil {
		return notFound("skill not found")
	}
	if sk.OfficeID != "" && sk.OfficeID != officeID {
		return invalid("other_department", "skill belongs to another department")
	}
	return nil
}

// Suggest lists what the role template and the position (its skill ids) suggest for an agent hired for an office:
// the position's skills first, then the role's defaults, without repeats. Only skills that work in that office and
// that the actor sees are listed.
func (s *Service) Suggest(ctx context.Context, a Actor, officeID, role, positionName string, positionSkills []string) ([]AgentSkill, error) {
	org := s.org(ctx)
	s.Ensure(ctx, org)
	all, err := s.cfg.Store.ListSkills(ctx, org)
	if err != nil {
		return nil, err
	}
	byID, byName := map[string]Skill{}, map[string]Skill{}
	for _, sk := range all {
		byID[sk.ID], byName[sk.Name] = sk, sk
	}
	m := s.model(ctx, org)
	ok := func(sk Skill) bool {
		return sk.DeletedAt == nil && (sk.OfficeID == "" || sk.OfficeID == officeID && officeID != offices.GeneralID) && canSee(m, a, sk)
	}
	out, seen := []AgentSkill{}, map[string]bool{}
	for _, id := range positionSkills {
		if sk, found := byID[id]; found && !seen[sk.ID] && ok(sk) {
			seen[sk.ID] = true
			out = append(out, AgentSkill{Skill: withOfficeName(m, sk), Via: ViaPosition, PositionName: positionName, Servable: servable(sk)})
		}
	}
	if s.cfg.RoleDefaults != nil && role != "" {
		for _, name := range s.cfg.RoleDefaults(role) {
			if sk, found := byName[name]; found && !seen[sk.ID] && ok(sk) {
				seen[sk.ID] = true
				out = append(out, AgentSkill{Skill: withOfficeName(m, sk), Via: ViaRole, Servable: servable(sk)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return viaRank(out[i].Via) < viaRank(out[j].Via) })
	return out, nil
}
