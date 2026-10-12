package drive

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/textfold"
)

// MemStore is the in-memory Store (demo mode without a database and tests).
type MemStore struct {
	mu       sync.Mutex
	folders  map[string]Folder    // org|id
	files    map[string]File      // org|id
	versions map[string][]Version // org|file id
	grants   map[string]Grant     // org|id
}

func NewMemStore() *MemStore {
	return &MemStore{folders: map[string]Folder{}, files: map[string]File{}, versions: map[string][]Version{}, grants: map[string]Grant{}}
}

var _ Store = (*MemStore)(nil)

func k(org, id string) string { return org + "|" + id }

func lc(s string) string { return strings.ToLower(s) }

func (m *MemStore) activeFolderPath(org, path string) (Folder, bool) {
	for key, f := range m.folders {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt == nil && lc(f.Path) == lc(path) {
			return f, true
		}
	}
	return Folder{}, false
}

func (m *MemStore) fill(org string, f File) File {
	if fo, ok := m.folders[k(org, f.FolderID)]; ok {
		f.Path = fo.Path + "/" + f.Name
	}
	f.Tags = slices.Clone(f.Tags)
	return f
}

func (m *MemStore) FolderByPath(_ context.Context, org, path string) (Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.activeFolderPath(org, path); ok {
		return f, nil
	}
	return Folder{}, domain.ErrNotFound
}

func (m *MemStore) FolderByID(_ context.Context, org, id string) (Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.folders[k(org, id)]; ok {
		return f, nil
	}
	return Folder{}, domain.ErrNotFound
}

func (m *MemStore) CreateFolder(_ context.Context, org string, f Folder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.activeFolderPath(org, f.Path); ok {
		return domain.ErrConflict
	}
	m.folders[k(org, f.ID)] = f
	return nil
}

func (m *MemStore) ChildFolders(_ context.Context, org, parentID string) ([]Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Folder
	for key, f := range m.folders {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt == nil && f.ParentID == parentID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lc(out[i].Name) < lc(out[j].Name) })
	return out, nil
}

func (m *MemStore) MoveFolder(_ context.Context, org, id, newParentID, newName, newPath string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[k(org, id)]
	if !ok || f.DeletedAt != nil {
		return domain.ErrNotFound
	}
	if other, ok := m.activeFolderPath(org, newPath); ok && other.ID != id {
		return domain.ErrConflict
	}
	old := f.Path
	for key, x := range m.folders {
		if strings.HasPrefix(key, org+"|") && (x.Path == old || strings.HasPrefix(x.Path, old+"/")) {
			x.Path = newPath + strings.TrimPrefix(x.Path, old)
			if x.ID == id {
				x.ParentID, x.Name = newParentID, newName
			}
			x.UpdatedAt = at
			m.folders[key] = x
		}
	}
	return nil
}

func (m *MemStore) subtree(org string, f Folder) map[string]bool {
	ids := map[string]bool{}
	for key, x := range m.folders {
		if strings.HasPrefix(key, org+"|") && (x.ID == f.ID || strings.HasPrefix(x.Path, f.Path+"/")) {
			ids[x.ID] = true
		}
	}
	return ids
}

func (m *MemStore) DeleteFolder(_ context.Context, org, id string, by Principal, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[k(org, id)]
	if !ok || f.DeletedAt != nil {
		return domain.ErrNotFound
	}
	ids := m.subtree(org, f)
	for fid := range ids {
		x := m.folders[k(org, fid)]
		if x.DeletedAt == nil {
			x.DeletedAt, x.DeletedBy, x.DeletedBatch = &at, &by, id
			m.folders[k(org, fid)] = x
		}
	}
	for key, x := range m.files {
		if strings.HasPrefix(key, org+"|") && ids[x.FolderID] && x.DeletedAt == nil {
			x.DeletedAt, x.DeletedBy, x.DeletedBatch = &at, &by, id
			m.files[key] = x
		}
	}
	return nil
}

func (m *MemStore) RestoreFolder(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.folders[k(org, id)]
	if !ok || f.DeletedAt == nil {
		return domain.ErrNotFound
	}
	if f.ParentID != "" {
		if p, ok := m.folders[k(org, f.ParentID)]; !ok || p.DeletedAt != nil {
			return domain.ErrConflict
		}
	}
	var restoring []Folder
	for key, x := range m.folders {
		if strings.HasPrefix(key, org+"|") && x.DeletedBatch == id && x.DeletedAt != nil {
			if other, ok := m.activeFolderPath(org, x.Path); ok && other.ID != x.ID {
				return domain.ErrConflict
			}
			restoring = append(restoring, x)
		}
	}
	// files restore only if their names are free
	for key, x := range m.files {
		if !strings.HasPrefix(key, org+"|") || x.DeletedBatch != id || x.DeletedAt == nil {
			continue
		}
		for k2, y := range m.files {
			if strings.HasPrefix(k2, org+"|") && y.DeletedAt == nil && y.FolderID == x.FolderID && lc(y.Name) == lc(x.Name) {
				return domain.ErrConflict
			}
		}
	}
	for _, x := range restoring {
		x.DeletedAt, x.DeletedBy, x.DeletedBatch, x.UpdatedAt = nil, nil, "", at
		m.folders[k(org, x.ID)] = x
	}
	for key, x := range m.files {
		if strings.HasPrefix(key, org+"|") && x.DeletedBatch == id && x.DeletedAt != nil {
			x.DeletedAt, x.DeletedBy, x.DeletedBatch = nil, nil, ""
			m.files[key] = x
		}
	}
	return nil
}

func (m *MemStore) nameTaken(org, folderID, name, exceptID string) bool {
	for key, y := range m.files {
		if strings.HasPrefix(key, org+"|") && y.DeletedAt == nil && y.FolderID == folderID && lc(y.Name) == lc(name) && y.ID != exceptID {
			return true
		}
	}
	return false
}

func (m *MemStore) CreateFile(_ context.Context, org string, f File, v Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nameTaken(org, f.FolderID, f.Name, "") {
		return domain.ErrConflict
	}
	m.files[k(org, f.ID)] = f
	m.versions[k(org, f.ID)] = []Version{v}
	return nil
}

func (m *MemStore) FileByID(_ context.Context, org, id string) (File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[k(org, id)]
	if !ok {
		return File{}, domain.ErrNotFound
	}
	return m.fill(org, f), nil
}

func (m *MemStore) FileByName(_ context.Context, org, folderID, name string) (File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, f := range m.files {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt == nil && f.FolderID == folderID && lc(f.Name) == lc(name) {
			return m.fill(org, f), nil
		}
	}
	return File{}, domain.ErrNotFound
}

func (m *MemStore) FilesIn(_ context.Context, org, folderID string) ([]File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []File
	for key, f := range m.files {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt == nil && f.FolderID == folderID {
			out = append(out, m.fill(org, f))
		}
	}
	sort.Slice(out, func(i, j int) bool { return lc(out[i].Name) < lc(out[j].Name) })
	return out, nil
}

func (m *MemStore) AddVersion(_ context.Context, org string, expected int, f File, v Version) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.files[k(org, f.ID)]
	if !ok || cur.DeletedAt != nil {
		return domain.ErrNotFound
	}
	if cur.CurrentVersion != expected || v.Version != expected+1 {
		return domain.ErrConflict
	}
	f.FolderID, f.Name, f.CreatedAt, f.CreatedBy = cur.FolderID, cur.Name, cur.CreatedAt, cur.CreatedBy
	m.files[k(org, f.ID)] = f
	m.versions[k(org, f.ID)] = append(m.versions[k(org, f.ID)], v)
	return nil
}

func (m *MemStore) UpdateFile(_ context.Context, org string, f File) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.files[k(org, f.ID)]
	if !ok || cur.DeletedAt != nil {
		return domain.ErrNotFound
	}
	if m.nameTaken(org, f.FolderID, f.Name, f.ID) {
		return domain.ErrConflict
	}
	cur.FolderID, cur.Name, cur.Tags, cur.UpdatedBy, cur.UpdatedAt = f.FolderID, f.Name, f.Tags, f.UpdatedBy, f.UpdatedAt
	m.files[k(org, f.ID)] = cur
	return nil
}

func (m *MemStore) DeleteFile(_ context.Context, org, id string, by Principal, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[k(org, id)]
	if !ok || f.DeletedAt != nil {
		return domain.ErrNotFound
	}
	f.DeletedAt, f.DeletedBy, f.DeletedBatch = &at, &by, id
	m.files[k(org, id)] = f
	return nil
}

func (m *MemStore) RestoreFile(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[k(org, id)]
	if !ok || f.DeletedAt == nil {
		return domain.ErrNotFound
	}
	if fo, ok := m.folders[k(org, f.FolderID)]; !ok || fo.DeletedAt != nil || m.nameTaken(org, f.FolderID, f.Name, id) {
		return domain.ErrConflict
	}
	f.DeletedAt, f.DeletedBy, f.DeletedBatch, f.UpdatedAt = nil, nil, "", at
	m.files[k(org, id)] = f
	return nil
}

func (m *MemStore) Versions(_ context.Context, org, fileID string) ([]Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[k(org, fileID)]; !ok {
		return nil, domain.ErrNotFound
	}
	out := slices.Clone(m.versions[k(org, fileID)])
	slices.Reverse(out) // newest first
	return out, nil
}

func (m *MemStore) VersionOf(_ context.Context, org, fileID string, n int) (Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[k(org, fileID)] {
		if v.Version == n {
			return v, nil
		}
	}
	return Version{}, domain.ErrNotFound
}

func (m *MemStore) Trash(_ context.Context, org string) (Trash, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := Trash{Folders: []Folder{}, Files: []File{}}
	for key, f := range m.folders {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt != nil && f.DeletedBatch == f.ID {
			t.Folders = append(t.Folders, f)
		}
	}
	for key, f := range m.files {
		if strings.HasPrefix(key, org+"|") && f.DeletedAt != nil && f.DeletedBatch == f.ID {
			t.Files = append(t.Files, m.fill(org, f))
		}
	}
	sort.Slice(t.Folders, func(i, j int) bool { return t.Folders[i].DeletedAt.After(*t.Folders[j].DeletedAt) })
	sort.Slice(t.Files, func(i, j int) bool { return t.Files[i].DeletedAt.After(*t.Files[j].DeletedAt) })
	return t, nil
}

func (m *MemStore) UsedBytes(_ context.Context, org string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for key, vs := range m.versions {
		if strings.HasPrefix(key, org+"|") {
			for _, v := range vs {
				n += v.Size
			}
		}
	}
	return n, nil
}

func tokens(s string) []string {
	return strings.FieldsFunc(lc(s), func(r rune) bool { return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127) })
}

func (m *MemStore) Search(_ context.Context, org string, q SearchQuery) ([]Hit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(q.Terms) > 0 {
		return m.searchTerms(org, q), nil
	}
	qt := tokens(q.Query)
	if len(qt) == 0 {
		return []Hit{}, nil
	}
	var hits []Hit
	for key, f := range m.files {
		if !strings.HasPrefix(key, org+"|") || f.DeletedAt != nil {
			continue
		}
		f = m.fill(org, f)
		if len(q.Under) > 0 && !slices.ContainsFunc(q.Under, func(p string) bool { return IsUnder(f.Path, p) }) {
			continue
		}
		name, text := lc(f.Name), lc(f.SearchText)
		score, all := 0.0, true
		for _, t := range qt {
			n := strings.Count(name, t)*5 + strings.Count(text, t)
			if n == 0 {
				all = false
				break
			}
			score += float64(n)
		}
		if !all {
			continue
		}
		hits = append(hits, Hit{FileID: f.ID, Path: f.Path, Name: f.Name, Mime: f.Mime, Version: f.CurrentVersion, Score: score,
			Snippet: snippetAround(f.SearchText, qt[0]), Size: f.Size, UpdatedBy: f.UpdatedBy})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits, nil
}

// searchTerms is the "any of these words" search used by the long-term memory:
// accent-insensitive, the score is the share of terms found (in [0,1)).
func (m *MemStore) searchTerms(org string, q SearchQuery) []Hit {
	hits := []Hit{}
	for key, f := range m.files {
		if !strings.HasPrefix(key, org+"|") || f.DeletedAt != nil {
			continue
		}
		f = m.fill(org, f)
		if q.Under != nil && !slices.ContainsFunc(q.Under, func(p string) bool { return IsUnder(f.Path, p) }) {
			continue
		}
		name, text := textfold.Fold(f.Name), textfold.Fold(f.SearchText)
		found := 0
		for _, t := range q.Terms {
			if strings.Contains(name, t) || strings.Contains(text, t) {
				found++
			}
		}
		if found == 0 {
			continue
		}
		r, _, _ := cut(f.SearchText, 6000)
		hits = append(hits, Hit{FileID: f.ID, Path: f.Path, Name: f.Name, Mime: f.Mime, Version: f.CurrentVersion,
			Score: float64(found) / float64(found+len(q.Terms)), Snippet: r, Size: f.Size, UpdatedBy: f.UpdatedBy})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits
}

func snippetAround(text, tok string) string {
	i := strings.Index(lc(text), tok)
	if i < 0 {
		r, _, _ := cut(text, 160)
		return strings.Join(strings.Fields(r), " ")
	}
	start := max(0, i-60)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	end := min(len(text), i+len(tok)+100)
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	return strings.Join(strings.Fields(text[start:end]), " ")
}

func (m *MemStore) AddGrant(_ context.Context, org string, g Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grants[k(org, g.ID)] = g
	return nil
}

func (m *MemStore) Grants(_ context.Context, org, agentID string) ([]Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Grant{}
	for key, g := range m.grants {
		if strings.HasPrefix(key, org+"|") && g.RevokedAt == nil && (agentID == "" || g.AgentID == agentID) {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) RevokeGrant(_ context.Context, org, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grants[k(org, id)]
	if !ok || g.RevokedAt != nil {
		return domain.ErrNotFound
	}
	g.RevokedAt = &at
	m.grants[k(org, id)] = g
	return nil
}
