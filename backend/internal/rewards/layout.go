package rewards

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// The office layout document (docs/architecture/office-editor.md): colors, light, workstations and the freely
// placed furniture of one organization, replaced as a whole and versioned for optimistic concurrency.

// EvOfficeLayout is the realtime frame and the audit action of a saved layout.
const EvOfficeLayout = "office.layout_updated"

// Limits of a layout document.
const (
	LayoutVersion = 1
	MaxPieces     = 150
	MaxDesks      = 60
	maxCoord      = 40.0
	maxUID        = 24
	maxAgentID    = 64
)

// Layout is the saved decoration of the office. Absent parts fall back to the built-in look.
type Layout struct {
	V      int             `json:"v"`
	Walls  *LayoutWalls    `json:"walls,omitempty"`
	Floor  *LayoutFloor    `json:"floor,omitempty"`
	Light  *LayoutLight    `json:"light,omitempty"`
	Desks  map[string]Desk `json:"desks,omitempty"`
	Pieces []Piece         `json:"pieces"`
	Seeded bool            `json:"seeded,omitempty"`
	// Zones moves the three rooms (meeting, approvals, cafe); Fixtures moves or hides the original decoration.
	Zones    map[string]Spot    `json:"zones,omitempty"`
	Fixtures map[string]Fixture `json:"fixtures,omitempty"`
}

// Spot is a position on the floor plan.
type Spot struct {
	X float64 `json:"x"`
	Z float64 `json:"z"`
}

// Fixture is one of the built-in decorations (plants, rack): moved, turned or taken away.
type Fixture struct {
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Rot    float64 `json:"rot,omitempty"`
	Hidden bool    `json:"hidden,omitempty"`
}

// LayoutWalls colors the walls (hex).
type LayoutWalls struct {
	Color string `json:"color,omitempty"`
	Trim  string `json:"trim,omitempty"`
	// Windows turns the glass bands of the walls on (default) or off (solid walls).
	Windows *bool `json:"windows,omitempty"`
}

// LayoutFloor is the floor material and its tint.
type LayoutFloor struct {
	Kind string `json:"kind,omitempty"`
	Tint string `json:"tint,omitempty"`
}

// LayoutLight is the lighting preset with fine adjustment.
type LayoutLight struct {
	Mood       string   `json:"mood,omitempty"`
	Temp       *float64 `json:"temp,omitempty"`
	Brightness *float64 `json:"brightness,omitempty"`
}

// Desk is one workstation: its position and the desk/chair styles (owned catalog items). Every field is optional.
type Desk struct {
	X     *float64 `json:"x,omitempty"`
	Z     *float64 `json:"z,omitempty"`
	Desk  string   `json:"desk,omitempty"`
	Chair string   `json:"chair,omitempty"`
}

// Piece is one placed furniture item. S is the uniform scale (omitted = 1).
type Piece struct {
	UID  string  `json:"uid"`
	Item string  `json:"item"`
	X    float64 `json:"x"`
	Z    float64 `json:"z"`
	Rot  float64 `json:"rot,omitempty"`
	S    float64 `json:"s,omitempty"`
	Y    float64 `json:"y,omitempty"`
}

var (
	hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	uidRe    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

	zoneNames  = map[string]bool{"meeting": true, "approvals": true, "cafe": true}
	fixtureIDs = map[string]bool{"plant_a": true, "plant_b": true, "plant_c": true, "plant_d": true, "rack": true}
	floorKinds = map[string]bool{"concrete": true, "wood": true, "parquet": true, "carpet": true, "tile": true, "marble": true}
	moods      = map[string]bool{"natural": true, "warm": true, "cool": true, "sunset": true, "night": true}
)

// ErrLayoutConflict is returned (as *LayoutConflictError) when base_version is not the stored version.
var ErrLayoutConflict = fmt.Errorf("%w: the office layout changed, reload it", domain.ErrConflict)

// LayoutConflictError carries the document that won, so the client can rebase on it.
type LayoutConflictError struct {
	Version int
	Layout  json.RawMessage // nil when none is stored
}

func (e *LayoutConflictError) Error() string { return ErrLayoutConflict.Error() }
func (e *LayoutConflictError) Unwrap() error { return ErrLayoutConflict }

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: layout: "+format, append([]any{domain.ErrInvalid}, a...)...)
}

// ParseLayout decodes a layout document strictly (unknown fields are rejected).
func ParseLayout(raw []byte) (Layout, error) {
	var l Layout
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return l, invalidf("%v", err)
	}
	if dec.More() {
		return l, invalidf("trailing data")
	}
	return l, nil
}

func inRange(v, lo, hi float64) bool { return v >= lo && v <= hi }

// Validate checks the shape, the limits and the catalog references of a layout (not the ownership, see
// Service.SaveLayout). It also normalizes: a missing scale becomes 1 and a missing piece list becomes empty.
func Validate(c *Catalog, l *Layout) error {
	if l.V != LayoutVersion {
		return invalidf("v must be %d", LayoutVersion)
	}
	if w := l.Walls; w != nil {
		if w.Color != "" && !hexColor.MatchString(w.Color) || w.Trim != "" && !hexColor.MatchString(w.Trim) {
			return invalidf("walls colors must be #rgb or #rrggbb")
		}
	}
	if f := l.Floor; f != nil {
		if f.Kind != "" && !floorKinds[f.Kind] {
			return invalidf("unknown floor kind %q", clip(f.Kind, 20))
		}
		if f.Tint != "" && !hexColor.MatchString(f.Tint) {
			return invalidf("floor tint must be #rgb or #rrggbb")
		}
	}
	if li := l.Light; li != nil {
		if li.Mood != "" && !moods[li.Mood] {
			return invalidf("unknown light mood %q", clip(li.Mood, 20))
		}
		if li.Temp != nil && !inRange(*li.Temp, -1, 1) {
			return invalidf("light temp must be within -1..1")
		}
		if li.Brightness != nil && !inRange(*li.Brightness, 0.5, 1.5) {
			return invalidf("light brightness must be within 0.5..1.5")
		}
	}
	for name, z := range l.Zones {
		if !zoneNames[name] {
			return invalidf("unknown room %q", clip(name, 20))
		}
		if !inRange(z.X, -maxCoord, maxCoord) || !inRange(z.Z, -maxCoord, maxCoord) {
			return invalidf("room %q is out of range", name)
		}
	}
	for id, f := range l.Fixtures {
		if !fixtureIDs[id] {
			return invalidf("unknown fixture %q", clip(id, 20))
		}
		if !inRange(f.X, -maxCoord, maxCoord) || !inRange(f.Z, -maxCoord, maxCoord) || !inRange(f.Rot, -1000, 1000) {
			return invalidf("fixture %q is out of range", id)
		}
	}
	if len(l.Desks) > MaxDesks {
		return invalidf("at most %d desks", MaxDesks)
	}
	for agent, d := range l.Desks {
		if agent == "" || len(agent) > maxAgentID {
			return invalidf("invalid desk agent id")
		}
		if d.X != nil && !inRange(*d.X, -maxCoord, maxCoord) || d.Z != nil && !inRange(*d.Z, -maxCoord, maxCoord) {
			return invalidf("desk %q is out of range", clip(agent, 20))
		}
		if err := styleItem(c, d.Desk, SlotDesk); err != nil {
			return err
		}
		if err := styleItem(c, d.Chair, SlotChair); err != nil {
			return err
		}
	}
	if l.Pieces == nil {
		l.Pieces = []Piece{}
	}
	if len(l.Pieces) > MaxPieces {
		return invalidf("at most %d pieces", MaxPieces)
	}
	uids, items := map[string]bool{}, map[string]bool{}
	for i := range l.Pieces {
		p := &l.Pieces[i]
		if p.UID == "" || len(p.UID) > maxUID || !uidRe.MatchString(p.UID) {
			return invalidf("piece %d has an invalid uid", i)
		}
		if uids[p.UID] {
			return invalidf("duplicate uid %q", p.UID)
		}
		uids[p.UID] = true
		it, ok := c.Item(p.Item)
		if !ok || it.Kind != KindOffice || it.Slot == SlotDesk || it.Slot == SlotChair || it.Slot == slotWindow {
			return invalidf("%q is not a placeable office item", clip(p.Item, 60))
		}
		if items[p.Item] {
			return invalidf("item %q is placed more than once", p.Item)
		}
		items[p.Item] = true
		if !inRange(p.X, -maxCoord, maxCoord) || !inRange(p.Z, -maxCoord, maxCoord) || !inRange(p.Y, -maxCoord, maxCoord) {
			return invalidf("piece %q is out of range", p.UID)
		}
		if !inRange(p.Rot, -1000, 1000) {
			return invalidf("piece %q has an invalid rotation", p.UID)
		}
		if p.S == 0 {
			p.S = 1
		}
		if !inRange(p.S, 0.6, 1.6) {
			return invalidf("piece %q scale must be within 0.6..1.6", p.UID)
		}
	}
	return nil
}

// styleItem checks a desk/chair style reference ("" = built-in look).
func styleItem(c *Catalog, id, slot string) error {
	if id == "" {
		return nil
	}
	it, ok := c.Item(id)
	if !ok || it.Kind != KindOffice || it.Slot != slot || it.Category != slot {
		return invalidf("%q is not a %s style", clip(id, 60), slot)
	}
	return nil
}

// layoutItems lists every catalog item a layout uses (the ones that must be owned).
func layoutItems(l Layout) []string {
	seen, out := map[string]bool{}, []string{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, d := range l.Desks {
		add(d.Desk)
		add(d.Chair)
	}
	for _, p := range l.Pieces {
		add(p.Item)
	}
	return out
}

// LayoutDoc is what GET /office/layout returns: the stored document (null when none) and its version.
type LayoutDoc struct {
	Layout  json.RawMessage `json:"layout"`
	Version int             `json:"version"`
}

// GetLayout returns the saved layout of the organization (null, version 0 when it never saved one).
func (s *Service) GetLayout(ctx context.Context, org string) (LayoutDoc, error) {
	doc, v, err := s.Store.GetLayout(ctx, org)
	return LayoutDoc{Layout: doc, Version: v}, err
}

// SaveLayout validates and stores the layout if base equals the stored version (0 when none); the answer carries
// the new version. A stale base returns *LayoutConflictError with the current document.
func (s *Service) SaveLayout(ctx context.Context, org, actor string, raw []byte, base int) (LayoutDoc, error) {
	if base < 0 {
		return LayoutDoc{}, invalidf("base_version must not be negative")
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || string(t) == "null" {
		return LayoutDoc{}, invalidf("layout is required")
	}
	l, err := ParseLayout(raw)
	if err != nil {
		return LayoutDoc{}, err
	}
	if err := Validate(s.Catalog, &l); err != nil {
		return LayoutDoc{}, err
	}
	if used := layoutItems(l); len(used) > 0 {
		inv, err := s.Store.Inventory(ctx, org)
		if err != nil {
			return LayoutDoc{}, err
		}
		have := make(map[string]bool, len(inv))
		for _, o := range inv {
			have[o.ItemID] = true
		}
		for _, id := range used {
			if !have[id] {
				return LayoutDoc{}, fmt.Errorf("%w: %s", ErrNotOwned, id)
			}
		}
	}
	doc, err := json.Marshal(l)
	if err != nil {
		return LayoutDoc{}, err
	}
	v, err := s.Store.SaveLayout(ctx, org, doc, base, actor)
	if err != nil {
		return LayoutDoc{}, err
	}
	s.audit(ctx, actor, EvOfficeLayout, "office", "layout", map[string]any{"version": v, "pieces": len(l.Pieces), "desks": len(l.Desks)})
	s.emit(ctx, application.Action{Type: EvOfficeLayout, SkipAudit: true, Entity: "office", EntityID: "layout",
		Payload: map[string]any{"version": v, "actor": actor}})
	return LayoutDoc{Layout: doc, Version: v}, nil
}

// AsConflict extracts the conflict carried by an error, if any.
func AsConflict(err error) (*LayoutConflictError, bool) {
	var ce *LayoutConflictError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}
