package rewards

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed catalog.json
var catalogJSON []byte

// Item kinds.
const (
	KindCharacter = "character"
	KindOffice    = "office"
)

// Office slots used through the layout only (docs/architecture/office-editor.md): Place/Remove do not apply to them.
const (
	SlotPiece  = "piece" // free placement
	SlotDesk   = "desk"  // workstation desk style
	SlotChair  = "chair" // workstation chair style
	slotWindow = "window_1"
)

// layoutSlot tells whether an office slot belongs to the layout editor rather than to a legacy fixed spot.
func layoutSlot(slot string) bool { return slot == SlotPiece || slot == SlotDesk || slot == SlotChair }

// Text is a localized string (es default, en).
type Text struct {
	ES string `json:"es"`
	EN string `json:"en"`
}

// Item is one article of the shop. Rarity is cosmetic flavor only: it changes
// nothing about what an agent can do.
type Item struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`     // character|office
	Category    string `json:"category"` // outfit, hat, glasses, ... plant, lamp, rug, ...
	Slot        string `json:"slot"`     // character slot (hat, face, ...) or office slot (plant_1, ...)
	Price       int64  `json:"price"`
	Rarity      string `json:"rarity"` // common|rare|epic
	Name        Text   `json:"name"`
	Description Text   `json:"description"`
	Color       string `json:"color,omitempty"`
	Color2      string `json:"color2,omitempty"`
}

// Catalog is the shop catalog (data: catalog.json).
type Catalog struct {
	Version int    `json:"version"`
	Items   []Item `json:"items"`
	byID    map[string]Item
}

// LoadCatalog parses and validates the embedded catalog.
func LoadCatalog() (*Catalog, error) { return ParseCatalog(catalogJSON) }

// ParseCatalog validates a catalog document.
func ParseCatalog(b []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.byID = make(map[string]Item, len(c.Items))
	for _, it := range c.Items {
		switch {
		case it.ID == "" || it.Slot == "" || it.Category == "":
			return nil, fmt.Errorf("catalog: item %q is incomplete", it.ID)
		case it.Kind != KindCharacter && it.Kind != KindOffice:
			return nil, fmt.Errorf("catalog: item %q has kind %q", it.ID, it.Kind)
		case it.Price <= 0:
			return nil, fmt.Errorf("catalog: item %q has no price", it.ID)
		case it.Rarity != "common" && it.Rarity != "rare" && it.Rarity != "epic":
			return nil, fmt.Errorf("catalog: item %q has rarity %q", it.ID, it.Rarity)
		case it.Name.ES == "" || it.Name.EN == "":
			return nil, fmt.Errorf("catalog: item %q lacks a name in es/en", it.ID)
		}
		if err := checkSlot(it); err != nil {
			return nil, err
		}
		if _, dup := c.byID[it.ID]; dup {
			return nil, fmt.Errorf("catalog: duplicate item %q", it.ID)
		}
		c.byID[it.ID] = it
	}
	return &c, nil
}

// checkSlot keeps the layout slots consistent: they exist for office items only, and desk/chair slots hold
// exactly the desk/chair categories.
func checkSlot(it Item) error {
	if it.Kind == KindCharacter && layoutSlot(it.Slot) {
		return fmt.Errorf("catalog: character item %q uses the office slot %q", it.ID, it.Slot)
	}
	if it.Kind != KindOffice {
		return nil
	}
	switch {
	case it.Slot == SlotDesk && it.Category != "desk", it.Slot == SlotChair && it.Category != "chair":
		return fmt.Errorf("catalog: item %q in slot %q must be of category %q", it.ID, it.Slot, it.Slot)
	case it.Slot == SlotPiece && (it.Category == "desk" || it.Category == "chair"):
		return fmt.Errorf("catalog: item %q of category %q cannot be a piece", it.ID, it.Category)
	}
	return nil
}

// Item returns an item by id.
func (c *Catalog) Item(id string) (Item, bool) { it, ok := c.byID[id]; return it, ok }

// Slots returns the sorted slot names of a kind.
func (c *Catalog) Slots(kind string) []string {
	seen := map[string]bool{}
	for _, it := range c.Items {
		if it.Kind == kind {
			seen[it.Slot] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
