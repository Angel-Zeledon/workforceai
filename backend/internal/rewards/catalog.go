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
		if _, dup := c.byID[it.ID]; dup {
			return nil, fmt.Errorf("catalog: duplicate item %q", it.ID)
		}
		c.byID[it.ID] = it
	}
	return &c, nil
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
