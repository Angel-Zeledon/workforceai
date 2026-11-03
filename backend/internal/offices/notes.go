package offices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/textfold"
)

// NoteMaxChars caps a note (the same order as the notes a person gives an agent with /recordar).
const NoteMaxChars = 1000

// NoteList is what a viewer reads of one memory.
type NoteList struct {
	Notes      []Note `json:"notes"`
	CanWrite   bool   `json:"can_write"`
	CanPromote bool   `json:"can_promote"`
}

func newNoteID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "tn_" + hex.EncodeToString(b)
}

func (s *Service) cleanNote(text string) (string, error) {
	t := strings.TrimSpace(text)
	if s.cfg.CleanNote != nil && t != "" {
		t = s.cfg.CleanNote(t)
	}
	if t == "" {
		return "", invalid("the note is empty or unsafe (it looks like a secret or an instruction)")
	}
	if utf8.RuneCountInString(t) > NoteMaxChars {
		return "", invalid("a note has at most %d characters", NoteMaxChars)
	}
	return t, nil
}

// noteRights says whether the viewer reads and writes a memory (scope + owner).
func noteRights(m *Model, v Viewer, scope, ref string) (read, write bool, err error) {
	switch scope {
	case ScopeOffice:
		if _, ok := m.Office(ref); !ok {
			return false, false, fmt.Errorf("%w: office %s", domain.ErrNotFound, ref)
		}
		return m.CanEnter(v, ref), v.Admin || m.IsMember(v.UserID, ref), nil
	case ScopePerson:
		return ref == v.UserID, ref == v.UserID && v.UserID != "", nil
	case ScopeCompany:
		return true, v.Admin, nil
	}
	return false, false, invalid("scope must be office, person or company")
}

// AddNote writes a note in the memory of an office (its members), of the viewer (scope person: the ref is always
// the viewer) or of the company (admins; the others promote).
func (s *Service) AddNote(ctx context.Context, org string, v Viewer, scope, ref, text string) (Note, error) {
	m, err := s.Model(ctx, org)
	if err != nil {
		return Note{}, err
	}
	switch scope {
	case ScopePerson:
		ref = v.UserID
	case ScopeCompany:
		ref = ""
	}
	_, write, err := noteRights(m, v, scope, ref)
	if err != nil {
		return Note{}, err
	}
	if !write {
		return Note{}, forbidden("you cannot write in this memory")
	}
	t, err := s.cleanNote(text)
	if err != nil {
		return Note{}, err
	}
	n := Note{ID: newNoteID(), Scope: scope, OwnerRef: ref, Text: t, CreatedBy: v.UserID, CreatedAt: s.now()}
	if err := s.cfg.Store.PutNote(ctx, org, n); err != nil {
		return Note{}, err
	}
	if scope != ScopePerson { // a personal memory is private: not even its size goes to the audit
		s.audit(ctx, org, v, "memory.note_added", n.ID, map[string]any{"scope": scope, "office": ref})
	}
	return n, nil
}

// Notes returns a memory with what the viewer may do with it.
func (s *Service) Notes(ctx context.Context, org string, v Viewer, scope, ref string) (NoteList, error) {
	m, err := s.Model(ctx, org)
	if err != nil {
		return NoteList{}, err
	}
	switch scope {
	case ScopePerson:
		ref = v.UserID
	case ScopeCompany:
		ref = ""
	}
	read, write, err := noteRights(m, v, scope, ref)
	if err != nil {
		return NoteList{}, err
	}
	if !read {
		return NoteList{}, forbidden("this memory belongs to a private office")
	}
	var ns []Note
	if scope != ScopePerson || ref != "" {
		if ns, err = s.cfg.Store.ListNotes(ctx, org, scope, ref); err != nil {
			return NoteList{}, err
		}
		if scope == ScopeCompany { // ref "" lists every owner: keep the company ones only
			ns = filterNotes(ns, func(n Note) bool { return n.OwnerRef == "" })
		}
	}
	if ns == nil {
		ns = []Note{}
	}
	return NoteList{Notes: ns, CanWrite: write, CanPromote: scope == ScopeOffice && ref != GeneralID && m.CanManage(v, ref) ||
		scope == ScopeOffice && ref == GeneralID && v.Admin}, nil
}

func filterNotes(ns []Note, keep func(Note) bool) []Note {
	out := ns[:0:0]
	for _, n := range ns {
		if keep(n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Service) findNote(ctx context.Context, org, id string) (Note, error) {
	ns, err := s.cfg.Store.ListNotes(ctx, org, "", "")
	if err != nil {
		return Note{}, err
	}
	for _, n := range ns {
		if n.ID == id {
			return n, nil
		}
	}
	return Note{}, fmt.Errorf("%w: note %s", domain.ErrNotFound, id)
}

// CanDeleteNote reports whether the viewer may remove a note: its author, the office's responsible, an admin for
// office and company notes; a personal note only its owner.
func CanDeleteNote(m *Model, v Viewer, n Note) bool {
	switch n.Scope {
	case ScopePerson:
		return n.OwnerRef == v.UserID
	case ScopeOffice:
		return n.CreatedBy == v.UserID && m.IsMember(v.UserID, n.OwnerRef) || m.CanManage(v, n.OwnerRef)
	}
	return v.Admin
}

// DeleteNote removes a note from every list and recall (soft, audited).
func (s *Service) DeleteNote(ctx context.Context, org string, v Viewer, id string) error {
	m, err := s.Model(ctx, org)
	if err != nil {
		return err
	}
	n, err := s.findNote(ctx, org, id)
	if err != nil {
		return err
	}
	if n.Scope == ScopePerson && n.OwnerRef != v.UserID {
		return fmt.Errorf("%w: note %s", domain.ErrNotFound, id) // somebody else's personal memory does not exist for you
	}
	if !CanDeleteNote(m, v, n) {
		return forbidden("only its author, the office's responsible or an admin removes a note")
	}
	if err := s.cfg.Store.DeleteNote(ctx, org, id, v.UserID); err != nil {
		return err
	}
	if n.Scope != ScopePerson {
		s.audit(ctx, org, v, "memory.note_deleted", id, map[string]any{"scope": n.Scope, "office": n.OwnerRef})
	}
	return nil
}

// Promote copies an office note to company knowledge (equipos-colaboracion.md §2.9): its responsible or an admin,
// once, recorded in the audit. Nothing is promoted by itself.
func (s *Service) Promote(ctx context.Context, org string, v Viewer, id string) (Note, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.Model(ctx, org)
	if err != nil {
		return Note{}, err
	}
	n, err := s.findNote(ctx, org, id)
	if err != nil {
		return Note{}, err
	}
	if n.Scope != ScopeOffice {
		if n.Scope == ScopePerson && n.OwnerRef != v.UserID {
			return Note{}, fmt.Errorf("%w: note %s", domain.ErrNotFound, id)
		}
		return Note{}, invalid("only an office's memory is promoted to the company")
	}
	if !m.CanManage(v, n.OwnerRef) {
		return Note{}, forbidden("only the office's responsible or an admin promotes its memory")
	}
	company, err := s.cfg.Store.ListNotes(ctx, org, ScopeCompany, "")
	if err != nil {
		return Note{}, err
	}
	for _, c := range company {
		if c.PromotedFrom == n.ID {
			return Note{}, fmt.Errorf("%w: this note is already company knowledge", domain.ErrConflict)
		}
	}
	c := Note{ID: newNoteID(), Scope: ScopeCompany, Text: n.Text, PromotedFrom: n.ID, CreatedBy: v.UserID, CreatedAt: s.now()}
	if err := s.cfg.Store.PutNote(ctx, org, c); err != nil {
		return Note{}, err
	}
	s.audit(ctx, org, v, "memory.promoted", c.ID, map[string]any{"from": n.ID, "office": n.OwnerRef, "author": n.CreatedBy})
	return c, nil
}

// RecallNotes returns the notes an agent keeps in mind for a task: its office's memory and the company's knowledge
// when they share words with the task, and the memory of the person it works for (preferences apply to any task).
// personID "" (a scheduled task) recalls no personal memory.
func (s *Service) RecallNotes(ctx context.Context, org, agentID, personID, text string, limit int) []Note {
	if limit <= 0 {
		limit = 3
	}
	m, err := s.Model(ctx, org)
	if err != nil {
		return nil
	}
	office := m.Agent(agentID).OfficeID
	all, err := s.cfg.Store.ListNotes(ctx, org, "", "")
	if err != nil {
		return nil
	}
	terms := textfold.Terms(text, 14)
	type scored struct {
		n     Note
		score float64
	}
	var cand []scored
	for _, n := range all {
		var base float64
		switch {
		case n.Scope == ScopePerson && personID != "" && n.OwnerRef == personID:
			base = 0.5 // a preference of the person applies even without shared words
		case n.Scope == ScopeOffice && n.OwnerRef == office, n.Scope == ScopeCompany && n.OwnerRef == "":
		default:
			continue
		}
		hits := 0
		folded := textfold.Fold(n.Text)
		for _, t := range terms {
			if strings.Contains(folded, t) {
				hits++
			}
		}
		if hits == 0 && base == 0 {
			continue
		}
		cand = append(cand, scored{n, base + float64(hits)})
	}
	sort.SliceStable(cand, func(i, j int) bool {
		if cand[i].score != cand[j].score {
			return cand[i].score > cand[j].score
		}
		return cand[i].n.CreatedAt.After(cand[j].n.CreatedAt)
	})
	out := make([]Note, 0, limit)
	for _, c := range cand {
		if len(out) >= limit {
			break
		}
		out = append(out, c.n)
	}
	return out
}

// NoteTitle names where a recalled note comes from (the agent reads it next to the text).
func NoteTitle(n Note) string {
	switch n.Scope {
	case ScopePerson:
		return "Memory of the person you work for"
	case ScopeCompany:
		return "Company knowledge"
	}
	return "Memory of your office"
}
