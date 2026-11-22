package skills

import (
	"context"
	"errors"

	"aiworkforce/backend/internal/domain"
)

// Office templates (F3, docs/architecture/office-templates.md): a template carries the active version of the skills
// assigned to its people, and the importing organization receives them for review, never active.

// ActivePackage returns the approved, enabled version of a skill the actor can see, for an office template. A skill
// without an active version, a disabled or deleted one, and a builtin starter (every organization has its own copy)
// answer ok=false: nothing of them travels.
func (s *Service) ActivePackage(ctx context.Context, a Actor, id string) (Package, Skill, bool, error) {
	org := s.org(ctx)
	sk, err := s.visible(ctx, org, a, id)
	if err != nil {
		return Package{}, Skill{}, false, err
	}
	if sk.Builtin || sk.DeletedAt != nil || sk.Status != StatusActive || sk.ActiveVersion <= 0 {
		return Package{}, sk, false, nil
	}
	v, err := s.cfg.Store.VersionOf(ctx, org, id, sk.ActiveVersion)
	if err != nil {
		return Package{}, sk, false, mapStore(err, "version")
	}
	pkg, err := s.readPackage(ctx, org, sk, v)
	if err != nil {
		return Package{}, sk, false, err
	}
	return pkg, sk, true, nil
}

// ByName returns the skill of the organization with that name the actor can see (ok=false when there is none or it
// is hidden or deleted).
func (s *Service) ByName(ctx context.Context, a Actor, name string) (Skill, bool, error) {
	org := s.org(ctx)
	s.Ensure(ctx, org)
	sk, err := s.cfg.Store.SkillByName(ctx, org, name)
	if errors.Is(err, domain.ErrNotFound) {
		return Skill{}, false, nil
	}
	if err != nil {
		return Skill{}, false, err
	}
	if sk.DeletedAt != nil || !canSee(s.model(ctx, org), a, sk) {
		return Skill{}, false, nil
	}
	return sk, true, nil
}

// ImportPackage creates a skill that came in an office template, in the given department ("" = the company). Like
// every import it starts in review: a person must read it and approve it before an agent receives it. The package
// was validated by Build.
func (s *Service) ImportPackage(ctx context.Context, a Actor, pkg Package, office string) (Detail, error) {
	s.Ensure(ctx, s.org(ctx))
	office, err := s.officeFor(ctx, s.org(ctx), a, office)
	if err != nil {
		return Detail{}, err
	}
	return s.createFrom(ctx, a, pkg, SourceImported, StatusInReview, "Imported with an office template", false, office)
}
