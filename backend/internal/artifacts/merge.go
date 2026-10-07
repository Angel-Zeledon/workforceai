package artifacts

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
)

// Three-way merge (docs/architecture/agent-workspaces.md sec. 5.3). `base` is
// the version the writer started from, `mine` what they saved and `head` what
// is stored now. The unit of conflict is the cell (sheet), the block (doc), the
// row+column (table), the card (board), the event/item (agenda/inbox) or the
// top-level field (the rest). Units changed on one side only are combined;
// units changed differently on both sides are returned as conflicts and nothing
// is written.

type merger struct {
	conflicts []UnitConflict
	author    *ActorRef // who made the head version (shown to the loser)
}

func raw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func equal(a, b any) bool { return reflect.DeepEqual(norm(a), norm(b)) }

// norm turns json.Number into float64 so values decoded with UseNumber compare with the others.
func norm(v any) any {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return f
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = norm(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = norm(x)
		}
		return out
	}
	return v
}

func (m *merger) conflict(unit string, mine, theirs any) {
	m.conflicts = append(m.conflicts, UnitConflict{Unit: unit, Mine: raw(mine), Theirs: raw(theirs), TheirsAuthor: m.author})
}

// unit merges one value: base, mine, head. ok=false means a conflict (recorded).
func (m *merger) unit(name string, base, mine, head any) (any, bool) {
	switch {
	case equal(mine, base):
		return head, true
	case equal(head, base), equal(mine, head):
		return mine, true
	}
	m.conflict(name, mine, head)
	return head, false
}

// mergeMap merges two-level maps (cells of a sheet, cells of a row): every key is a unit.
func (m *merger) mergeMap(prefix string, base, mine, head map[string]any) map[string]any {
	out := map[string]any{}
	keys := map[string]bool{}
	for _, mm := range []map[string]any{base, mine, head} {
		for k := range mm {
			keys[k] = true
		}
	}
	for k := range keys {
		v, _ := m.unit(prefix+k, base[k], mine[k], head[k])
		if v != nil {
			out[k] = v
		}
	}
	return out
}

// mergeList merges lists of objects identified by idKey. cell, when not nil,
// merges two versions of the same item (rows merge per column); otherwise the
// item is one unit.
func (m *merger) mergeList(label, idKey string, base, mine, head []any, cell func(id string, b, mi, h map[string]any) any) []any {
	index := func(l []any) (map[string]any, []string, bool) {
		byID, order := map[string]any{}, []string{}
		for _, it := range l {
			id := asStr(asMap(it)[idKey])
			if id == "" {
				return nil, nil, false
			}
			byID[id] = it
			order = append(order, id)
		}
		return byID, order, true
	}
	bm, _, ok1 := index(base)
	mm, mOrder, ok2 := index(mine)
	hm, hOrder, ok3 := index(head)
	if !ok1 || !ok2 || !ok3 { // items without id: the whole list is one unit
		v, _ := m.unit(label, base, mine, head)
		l, _ := v.([]any)
		return l
	}
	merged := map[string]any{}
	var order []string
	for _, id := range hOrder {
		b, inBase := bm[id]
		mi, inMine := mm[id]
		h := hm[id]
		switch {
		case inMine && inBase:
			if cell != nil && !equal(mi, b) && !equal(h, b) && !equal(mi, h) {
				merged[id] = cell(id, asMap(b), asMap(mi), asMap(h))
			} else {
				v, _ := m.unit(id, b, mi, h)
				merged[id] = v
			}
		case inMine && !inBase:
			v, _ := m.unit(id, nil, mi, h)
			merged[id] = v
		case !inMine && inBase: // I deleted it
			if equal(h, b) {
				continue
			}
			m.conflict(id, nil, h)
			merged[id] = h
		default: // added by them
			merged[id] = h
		}
		order = append(order, id)
	}
	// Items that only my version has: new ones are inserted after their predecessor in my order.
	for i, id := range mOrder {
		if _, inHead := hm[id]; inHead {
			continue
		}
		if b, inBase := bm[id]; inBase { // they deleted it
			if !equal(mm[id], b) {
				m.conflict(id, mm[id], nil)
			}
			continue
		}
		merged[id] = mm[id]
		pos := 0
		for j := i - 1; j >= 0; j-- {
			if k := slices.Index(order, mOrder[j]); k >= 0 {
				pos = k + 1
				break
			}
		}
		order = slices.Insert(order, pos, id)
	}
	out := make([]any, 0, len(order))
	for _, id := range order {
		out = append(out, merged[id])
	}
	return out
}

func mergeRow(m *merger, id string, base, mine, head map[string]any) any {
	out := map[string]any{}
	for k, v := range head {
		out[k] = v
	}
	out["cells"] = m.mergeMap(id+".", asMap(base["cells"]), asMap(mine["cells"]), asMap(head["cells"]))
	return out
}

// mergeSheets merges the sheets by id; inside a sheet every cell is a unit and
// the rest of the sheet (widths, names, frozen panes...) is one unit.
func (m *merger) mergeSheets(base, mine, head []any) []any {
	return m.mergeList("sheets", "id", base, mine, head, func(id string, b, mi, h map[string]any) any {
		name := asStr(h["name"])
		cells := m.mergeMap(name+"!", asMap(b["cells"]), asMap(mi["cells"]), asMap(h["cells"]))
		strip := func(s map[string]any) map[string]any {
			o := map[string]any{}
			for k, v := range s {
				if k != "cells" {
					o[k] = v
				}
			}
			return o
		}
		rest, _ := m.unit(name+" (layout)", strip(b), strip(mi), strip(h))
		out := map[string]any{}
		for k, v := range asMap(rest) {
			out[k] = v
		}
		out["cells"] = cells
		return out
	})
}

// mergeDoc merges the top-level blocks of a document by their bid.
func (m *merger) mergeDoc(base, mine, head map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range head {
		out[k] = v
	}
	out["content"] = m.mergeList("document", "bid", blocks(base), blocks(mine), blocks(head), nil)
	return out
}

// blocks returns the top-level nodes with their bid lifted to a plain field.
func blocks(doc map[string]any) []any {
	var out []any
	for _, n := range asList(doc["content"]) {
		nm := asMap(n)
		cp := map[string]any{}
		for k, v := range nm {
			cp[k] = v
		}
		cp["bid"] = asStr(asMap(nm["attrs"])["bid"])
		out = append(out, cp)
	}
	return out
}

// unblock removes the lifted field again.
func unblock(l []any) []any {
	out := make([]any, 0, len(l))
	for _, n := range l {
		nm := asMap(n)
		cp := map[string]any{}
		for k, v := range nm {
			if k != "bid" {
				cp[k] = v
			}
		}
		out = append(out, cp)
	}
	return out
}

// mergeContent merges the three decoded contents of one artifact.
func mergeContent(kind Kind, base, mine, head map[string]any, author *ActorRef) (map[string]any, []UnitConflict) {
	m := &merger{author: author}
	out := map[string]any{}
	special := map[string]bool{}
	switch kind {
	case KindSheet:
		special["sheets"] = true
		out["sheets"] = m.mergeSheets(asList(base["sheets"]), asList(mine["sheets"]), asList(head["sheets"]))
	case KindDoc:
		special["doc"] = true
		merged := m.mergeDoc(asMap(base["doc"]), asMap(mine["doc"]), asMap(head["doc"]))
		merged["content"] = unblock(merged["content"].([]any))
		out["doc"] = merged
	case KindTable:
		special["rows"] = true
		out["rows"] = m.mergeList("rows", "id", asList(base["rows"]), asList(mine["rows"]), asList(head["rows"]), func(id string, b, mi, h map[string]any) any { return mergeRow(m, id, b, mi, h) })
	case KindBoard:
		special["cards"] = true
		out["cards"] = m.mergeList("cards", "id", asList(base["cards"]), asList(mine["cards"]), asList(head["cards"]), nil)
	case KindAgenda:
		special["events"] = true
		out["events"] = m.mergeList("events", "id", asList(base["events"]), asList(mine["events"]), asList(head["events"]), nil)
	case KindInbox:
		special["items"] = true
		out["items"] = m.mergeList("items", "id", asList(base["items"]), asList(mine["items"]), asList(head["items"]), nil)
	case KindPDF:
		special["annotations"] = true
		out["annotations"] = m.mergeList("annotations", "id", asList(base["annotations"]), asList(mine["annotations"]), asList(head["annotations"]), nil)
	}
	keys := map[string]bool{}
	for _, mm := range []map[string]any{base, mine, head} {
		for k := range mm {
			keys[k] = true
		}
	}
	for k := range keys {
		if special[k] {
			continue
		}
		if v, _ := m.unit(k, base[k], mine[k], head[k]); v != nil {
			out[k] = v
		}
	}
	slices.SortFunc(m.conflicts, func(a, b UnitConflict) int { return bytes.Compare([]byte(a.Unit), []byte(b.Unit)) })
	return out, m.conflicts
}
