package rewards_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rewards"
)

const layoutCatalog = `{"version":1,"items":[
 {"id":"sofa_a","kind":"office","category":"sofa","slot":"piece","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"table_a","kind":"office","category":"table","slot":"piece","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"desk_walnut","kind":"office","category":"desk","slot":"desk","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"chair_teal","kind":"office","category":"chair","slot":"chair","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"plant_cactus","kind":"office","category":"plant","slot":"plant_1","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"view_city","kind":"office","category":"window","slot":"window_1","price":10,"rarity":"common","name":{"es":"a","en":"a"}},
 {"id":"hat_cap","kind":"character","category":"hat","slot":"hat","price":10,"rarity":"common","name":{"es":"a","en":"a"}}
]}`

func layoutFixture(t *testing.T, own ...string) *fixture {
	t.Helper()
	f := newFixture(t)
	cat, err := rewards.ParseCatalog([]byte(layoutCatalog))
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Catalog = cat
	if _, _, _, err := f.svc.Store.Earn(f.ctx, "org-a", rewards.Entry{Kind: rewards.KindEarn, Amount: 1000, Reason: rewards.ReasonTaskDone, IdemKey: "seed"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range own {
		if _, err := f.svc.Buy(f.ctx, "org-a", "u1", id, ""); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func mustLayout(t *testing.T, s string) rewards.Layout {
	t.Helper()
	l, err := rewards.ParseLayout([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return l
}

func TestValidateLayout(t *testing.T) {
	cat, _ := rewards.ParseCatalog([]byte(layoutCatalog))
	ok := []string{
		`{"v":1}`,
		`{"v":1,"walls":{"color":"#fff","trim":"#3a414c"},"floor":{"kind":"marble","tint":"#e7eaee"},"light":{"mood":"night","temp":-1,"brightness":1.5},"seeded":true}`,
		`{"v":1,"desks":{"analyst":{"x":-9.5,"z":-4,"desk":"desk_walnut","chair":"chair_teal"},"sales":{}},"pieces":[{"uid":"p_1","item":"sofa_a","x":40,"z":-40,"rot":1.5708,"s":0.6},{"uid":"p-2","item":"plant_cactus","x":0,"z":0,"s":1.6}]}`,
	}
	for _, s := range ok {
		l := mustLayout(t, s)
		if err := rewards.Validate(cat, &l); err != nil {
			t.Errorf("rejected %s: %v", s, err)
		}
	}
	l := mustLayout(t, `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":1,"z":1}]}`)
	if err := rewards.Validate(cat, &l); err != nil || l.Pieces[0].S != 1 {
		t.Errorf("missing scale should normalize to 1: %v %+v", err, l.Pieces)
	}

	many := func(n int) string {
		var ps []string
		for i := 0; i < n; i++ {
			ps = append(ps, fmt.Sprintf(`{"uid":"u%d","item":"sofa_a","x":0,"z":0}`, i))
		}
		return strings.Join(ps, ",")
	}
	desks := func(n int) string {
		var ds []string
		for i := 0; i < n; i++ {
			ds = append(ds, fmt.Sprintf(`"a%d":{}`, i))
		}
		return strings.Join(ds, ",")
	}
	bad := map[string]string{
		"version":          `{"v":2}`,
		"no version":       `{}`,
		"bad hex":          `{"v":1,"walls":{"color":"red"}}`,
		"bad hex len":      `{"v":1,"walls":{"trim":"#ffff"}}`,
		"floor kind":       `{"v":1,"floor":{"kind":"lava"}}`,
		"floor tint":       `{"v":1,"floor":{"tint":"#gggggg"}}`,
		"mood":             `{"v":1,"light":{"mood":"disco"}}`,
		"temp high":        `{"v":1,"light":{"temp":1.01}}`,
		"temp low":         `{"v":1,"light":{"temp":-1.01}}`,
		"brightness low":   `{"v":1,"light":{"brightness":0.49}}`,
		"brightness high":  `{"v":1,"light":{"brightness":1.51}}`,
		"too many pieces":  `{"v":1,"pieces":[` + many(rewards.MaxPieces+1) + `]}`,
		"too many desks":   `{"v":1,"desks":{` + desks(rewards.MaxDesks+1) + `}}`,
		"uid chars":        `{"v":1,"pieces":[{"uid":"a b","item":"sofa_a","x":0,"z":0}]}`,
		"uid empty":        `{"v":1,"pieces":[{"uid":"","item":"sofa_a","x":0,"z":0}]}`,
		"uid long":         `{"v":1,"pieces":[{"uid":"` + strings.Repeat("a", 25) + `","item":"sofa_a","x":0,"z":0}]}`,
		"uid duplicate":    `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":0},{"uid":"a","item":"table_a","x":0,"z":0}]}`,
		"item twice":       `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":0},{"uid":"b","item":"sofa_a","x":1,"z":0}]}`,
		"x range":          `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":40.1,"z":0}]}`,
		"z range":          `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":-41}]}`,
		"scale low":        `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":0,"s":0.59}]}`,
		"scale high":       `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":0,"s":1.61}]}`,
		"unknown item":     `{"v":1,"pieces":[{"uid":"a","item":"nope","x":0,"z":0}]}`,
		"character item":   `{"v":1,"pieces":[{"uid":"a","item":"hat_cap","x":0,"z":0}]}`,
		"desk as piece":    `{"v":1,"pieces":[{"uid":"a","item":"desk_walnut","x":0,"z":0}]}`,
		"window as piece":  `{"v":1,"pieces":[{"uid":"a","item":"view_city","x":0,"z":0}]}`,
		"desk style kind":  `{"v":1,"desks":{"x":{"desk":"sofa_a"}}}`,
		"chair style kind": `{"v":1,"desks":{"x":{"chair":"desk_walnut"}}}`,
		"desk unknown":     `{"v":1,"desks":{"x":{"desk":"desk_none"}}}`,
		"desk x range":     `{"v":1,"desks":{"x":{"x":41}}}`,
		"agent id empty":   `{"v":1,"desks":{"":{}}}`,
	}
	for name, s := range bad {
		l, err := rewards.ParseLayout([]byte(s))
		if err == nil {
			err = rewards.Validate(cat, &l)
		}
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	// Strict decoding: unknown fields and trailing data are refused.
	for _, s := range []string{`{"v":1,"extra":1}`, `{"v":1} {"v":1}`, `[]`, `{"v":"1"}`} {
		if _, err := rewards.ParseLayout([]byte(s)); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", s, err)
		}
	}
	// Exactly at the limits.
	l = mustLayout(t, `{"v":1,"desks":{`+desks(rewards.MaxDesks)+`}}`)
	if err := rewards.Validate(cat, &l); err != nil {
		t.Errorf("60 desks: %v", err)
	}
}

func TestSaveLayoutService(t *testing.T) {
	f := layoutFixture(t, "sofa_a", "desk_walnut", "chair_teal", "plant_cactus")
	svc, ctx := f.svc, f.ctx
	doc := `{"v":1,"floor":{"kind":"wood"},"desks":{"analyst":{"x":-9.5,"z":-4,"desk":"desk_walnut","chair":"chair_teal"}},"pieces":[{"uid":"p1","item":"sofa_a","x":2.5,"z":3,"rot":1.5708,"s":1},{"uid":"p2","item":"plant_cactus","x":1,"z":1}]}`

	if d, err := svc.GetLayout(ctx, "org-a"); err != nil || d.Version != 0 || d.Layout != nil {
		t.Fatalf("empty layout = %+v %v", d, err)
	}
	if st, _ := svc.State(ctx, "org-a"); st.Layout != nil || st.LayoutVersion != 0 {
		t.Fatalf("state = %+v", st)
	}
	d, err := svc.SaveLayout(ctx, "org-a", "u1", []byte(doc), 0)
	if err != nil || d.Version != 1 {
		t.Fatalf("save: %+v %v", d, err)
	}
	got, _ := svc.GetLayout(ctx, "org-a")
	var l rewards.Layout
	if err := json.Unmarshal(got.Layout, &l); err != nil || got.Version != 1 || len(l.Pieces) != 2 || l.Desks["analyst"].Desk != "desk_walnut" || l.Pieces[1].S != 1 {
		t.Fatalf("stored = %s v%d %v", got.Layout, got.Version, err)
	}
	if st, _ := svc.State(ctx, "org-a"); st.LayoutVersion != 1 || len(st.Layout) == 0 {
		t.Fatalf("state = %+v", st)
	}
	if f.cap.count(rewards.EvOfficeLayout) != 1 {
		t.Fatalf("frames = %v", f.cap.types())
	}
	for _, e := range f.cap.ev {
		if e.Type == rewards.EvOfficeLayout {
			p, _ := e.Payload.(map[string]any)
			if p["version"] != 1 || p["actor"] != "u1" || len(p) != 2 {
				t.Fatalf("payload = %#v", e.Payload)
			}
		}
	}

	// A stale base conflicts and carries the winning document; nothing changes.
	_, err = svc.SaveLayout(ctx, "org-a", "u2", []byte(`{"v":1}`), 0)
	ce, ok := rewards.AsConflict(err)
	if !ok || !errors.Is(err, rewards.ErrLayoutConflict) || !errors.Is(err, domain.ErrConflict) || ce.Version != 1 || string(ce.Layout) != string(got.Layout) {
		t.Fatalf("conflict = %v %+v", err, ce)
	}
	if _, err = svc.SaveLayout(ctx, "org-a", "u2", []byte(`{"v":1}`), 5); !errors.Is(err, rewards.ErrLayoutConflict) {
		t.Fatalf("future base = %v", err)
	}
	if d, err = svc.SaveLayout(ctx, "org-a", "u2", []byte(`{"v":1}`), 1); err != nil || d.Version != 2 {
		t.Fatalf("second save: %+v %v", d, err)
	}
	if f.cap.count(rewards.EvOfficeLayout) != 2 {
		t.Fatalf("frames = %v", f.cap.types())
	}

	// Failures save nothing and emit nothing.
	cases := []struct {
		name, doc string
		invalid   bool
	}{
		{"not owned piece", `{"v":1,"pieces":[{"uid":"a","item":"table_a","x":0,"z":0}]}`, false},
		{"unknown item", `{"v":1,"pieces":[{"uid":"a","item":"nope","x":0,"z":0}]}`, true},
		{"duplicate item", `{"v":1,"pieces":[{"uid":"a","item":"sofa_a","x":0,"z":0},{"uid":"b","item":"sofa_a","x":0,"z":0}]}`, true},
		{"null", `null`, true},
		{"", ``, true},
	}
	for _, c := range cases {
		_, err := svc.SaveLayout(ctx, "org-a", "u1", []byte(c.doc), 2)
		switch {
		case c.invalid && !errors.Is(err, domain.ErrInvalid):
			t.Errorf("%s: want invalid, got %v", c.name, err)
		case !c.invalid && !errors.Is(err, rewards.ErrNotOwned):
			t.Errorf("%s: want not owned, got %v", c.name, err)
		}
	}
	// Desk styles must be owned too, in another organization nothing is owned.
	if _, err := svc.SaveLayout(ctx, "org-b", "u1", []byte(`{"v":1,"desks":{"a":{"desk":"desk_walnut"}}}`), 0); !errors.Is(err, rewards.ErrNotOwned) {
		t.Errorf("org-b desk style = %v", err)
	}
	if _, err := svc.SaveLayout(ctx, "org-a", "u1", []byte(`{"v":1}`), -1); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("negative base = %v", err)
	}
	if d, _ := svc.GetLayout(ctx, "org-a"); d.Version != 2 {
		t.Errorf("version after failures = %d", d.Version)
	}
	if f.cap.count(rewards.EvOfficeLayout) != 2 {
		t.Errorf("failed saves emitted frames: %v", f.cap.types())
	}
	// The layout of one organization is not visible to another.
	if d, _ := svc.GetLayout(ctx, "org-b"); d.Version != 0 || d.Layout != nil {
		t.Errorf("org-b sees %+v", d)
	}
}

func TestLayoutSlotsAreNotPlaceable(t *testing.T) {
	f := layoutFixture(t, "sofa_a", "desk_walnut", "chair_teal", "plant_cactus")
	for _, id := range []string{"sofa_a", "desk_walnut", "chair_teal"} {
		if _, err := f.svc.Place(f.ctx, "org-a", "u1", id); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Place(%s) = %v, want invalid", id, err)
		}
	}
	for _, slot := range []string{"piece", "desk", "chair"} {
		if err := f.svc.Remove(f.ctx, "org-a", "u1", slot); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Remove(%s) = %v, want invalid", slot, err)
		}
	}
	// Legacy slots keep working.
	if p, err := f.svc.Place(f.ctx, "org-a", "u1", "plant_cactus"); err != nil || p.Slot != "plant_1" {
		t.Fatalf("legacy place: %+v %v", p, err)
	}
	if err := f.svc.Remove(f.ctx, "org-a", "u1", "plant_1"); err != nil {
		t.Fatalf("legacy remove: %v", err)
	}
}

func TestCatalogLayoutSlotRules(t *testing.T) {
	item := func(cat, slot string) string {
		return `{"items":[{"id":"x","kind":"office","category":"` + cat + `","slot":"` + slot + `","price":1,"rarity":"common","name":{"es":"a","en":"b"}}]}`
	}
	for _, c := range [][2]string{{"desk", "desk"}, {"chair", "chair"}, {"sofa", "piece"}} {
		if _, err := rewards.ParseCatalog([]byte(item(c[0], c[1]))); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	for _, c := range [][2]string{{"sofa", "desk"}, {"desk", "chair"}, {"desk", "piece"}, {"chair", "piece"}} {
		if _, err := rewards.ParseCatalog([]byte(item(c[0], c[1]))); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	ch := `{"items":[{"id":"x","kind":"character","category":"hat","slot":"piece","price":1,"rarity":"common","name":{"es":"a","en":"b"}}]}`
	if _, err := rewards.ParseCatalog([]byte(ch)); err == nil {
		t.Error("character item in a layout slot accepted")
	}
}

// storeLayoutContract is the behavior every rewards.Store must have for layouts (the Postgres test runs the same).
func TestMemStoreLayoutContract(t *testing.T) {
	st := rewards.NewMemStore()
	ctx := context.Background()
	if doc, v, err := st.GetLayout(ctx, "o1"); err != nil || doc != nil || v != 0 {
		t.Fatalf("empty = %s %d %v", doc, v, err)
	}
	if v, err := st.SaveLayout(ctx, "o1", json.RawMessage(`{"v":1,"a":1}`), 0, "u1"); err != nil || v != 1 {
		t.Fatalf("first save = %d %v", v, err)
	}
	if v, err := st.SaveLayout(ctx, "o1", json.RawMessage(`{"v":1,"a":2}`), 1, "u1"); err != nil || v != 2 {
		t.Fatalf("second save = %d %v", v, err)
	}
	_, err := st.SaveLayout(ctx, "o1", json.RawMessage(`{"v":1,"a":3}`), 1, "u2")
	ce, ok := rewards.AsConflict(err)
	if !ok || ce.Version != 2 || !strings.Contains(string(ce.Layout), `"a":2`) {
		t.Fatalf("stale save = %v %+v", err, ce)
	}
	if _, err := st.SaveLayout(ctx, "o1", json.RawMessage(`{}`), 0, "u2"); !errors.Is(err, rewards.ErrLayoutConflict) {
		t.Fatalf("base 0 over a stored layout = %v", err)
	}
	if doc, v, _ := st.GetLayout(ctx, "o1"); v != 2 || !strings.Contains(string(doc), `"a":2`) {
		t.Fatalf("stored = %s %d", doc, v)
	}
	if _, v, _ := st.GetLayout(ctx, "o2"); v != 0 {
		t.Fatal("o2 sees o1's layout")
	}
	// Exactly one of many concurrent saves from the same version wins.
	var wg sync.WaitGroup
	var wins atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.SaveLayout(ctx, "o1", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)), 2, "u"); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if _, v, _ := st.GetLayout(ctx, "o1"); wins.Load() != 1 || v != 3 {
		t.Fatalf("wins=%d version=%d", wins.Load(), v)
	}
}
