package rewards

import (
	"context"
	"encoding/json"
	"strings"
)

// GeneralOffice is the office whose layout is the organization's one (office_layout): General, in E2.
const GeneralOffice = "general"

// OfficeLayoutStore keeps one layout per office (E2 "Oficinas", migration 815). Optional: a Store without it only has
// the organization's layout.
type OfficeLayoutStore interface {
	GetOfficeLayout(ctx context.Context, org, office string) (doc json.RawMessage, version int, err error)
	SaveOfficeLayout(ctx context.Context, org, office string, doc json.RawMessage, base int, actor string) (version int, err error)
}

func isGeneral(office string) bool {
	o := strings.TrimSpace(office)
	return o == "" || o == GeneralOffice
}

// GetLayoutFor returns the saved layout of an office (General = the organization's layout). An office that never
// saved one answers null, version 0: the UI starts from the default.
func (s *Service) GetLayoutFor(ctx context.Context, org, office string) (LayoutDoc, error) {
	if isGeneral(office) {
		return s.GetLayout(ctx, org)
	}
	ols, ok := s.Store.(OfficeLayoutStore)
	if !ok {
		return LayoutDoc{}, invalidf("this store has one office layout per organization")
	}
	doc, v, err := ols.GetOfficeLayout(ctx, org, office)
	return LayoutDoc{Layout: doc, Version: v}, err
}

// SaveLayoutFor validates and stores the layout of an office, with the same rules and concurrency as SaveLayout.
func (s *Service) SaveLayoutFor(ctx context.Context, org, office, actor string, raw []byte, base int) (LayoutDoc, error) {
	if isGeneral(office) {
		return s.SaveLayout(ctx, org, actor, raw, base)
	}
	ols, ok := s.Store.(OfficeLayoutStore)
	if !ok {
		return LayoutDoc{}, invalidf("this store has one office layout per organization")
	}
	return s.saveLayout(ctx, org, actor, raw, base, func(doc json.RawMessage) (int, error) {
		return ols.SaveOfficeLayout(ctx, org, office, doc, base, actor)
	}, office)
}

// officeKey keeps the layouts of the offices apart from the organization's in the memory store.
func officeKey(org, office string) string { return org + "\x00office:" + office }

// GetOfficeLayout implements OfficeLayoutStore.
func (m *MemStore) GetOfficeLayout(ctx context.Context, org, office string) (json.RawMessage, int, error) {
	return m.GetLayout(ctx, officeKey(org, office))
}

// SaveOfficeLayout implements OfficeLayoutStore.
func (m *MemStore) SaveOfficeLayout(ctx context.Context, org, office string, doc json.RawMessage, base int, actor string) (int, error) {
	return m.SaveLayout(ctx, officeKey(org, office), doc, base, actor)
}
