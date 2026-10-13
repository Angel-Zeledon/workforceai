package docindex

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/textfold"
)

// MemStore is the in-memory Store (tests and deployments without Postgres).
// It reads the live state of files from the drive, like the SQL joins do.
type MemStore struct {
	Drive *drive.Service

	mu       sync.Mutex
	settings map[string]Settings
	states   map[string]map[string]*memState
	chunks   map[string]map[string][]StoredChunk
	usage    map[string]map[string]int
}

type memState struct {
	version, attempts, chunks, embedded int
	status, reason, model               string
	lease                               time.Time
}

// NewMemStore builds an empty store over a drive.
func NewMemStore(d *drive.Service) *MemStore {
	return &MemStore{Drive: d, settings: map[string]Settings{}, states: map[string]map[string]*memState{},
		chunks: map[string]map[string][]StoredChunk{}, usage: map[string]map[string]int{}}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) st(org string) map[string]*memState {
	if m.states[org] == nil {
		m.states[org] = map[string]*memState{}
	}
	return m.states[org]
}

func (m *MemStore) ch(org string) map[string][]StoredChunk {
	if m.chunks[org] == nil {
		m.chunks[org] = map[string][]StoredChunk{}
	}
	return m.chunks[org]
}

func (m *MemStore) Settings(_ context.Context, org string) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.settings[org]; ok {
		return s, nil
	}
	return Settings{Enabled: true}, nil
}

func (m *MemStore) SetSettings(_ context.Context, org string, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[org] = s
	return nil
}

func (m *MemStore) Enqueue(_ context.Context, org, fileID string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st(org)[fileID]
	if s == nil {
		s = &memState{}
		m.st(org)[fileID] = s
	}
	if s.version != version || s.status == StatusFailed || s.status == "" {
		s.version, s.status, s.reason, s.attempts, s.lease = version, StatusPending, "", 0, time.Time{}
	}
	return nil
}

// live returns the file when it is searchable (not in the trash, folder live).
func (m *MemStore) live(ctx context.Context, org, id string) (drive.File, bool) {
	ctx = application.WithOrg(ctx, org)
	f, err := m.Drive.GetFile(ctx, id)
	if err != nil || f.DeletedAt != nil {
		return drive.File{}, false
	}
	if _, err := m.Drive.FolderByPath(ctx, drive.ParentPath(f.Path)); err != nil {
		return drive.File{}, false
	}
	return f, true
}

func (m *MemStore) allFiles(ctx context.Context, org string) []drive.File {
	ctx = application.WithOrg(ctx, org)
	var out []drive.File
	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		if depth > 40 {
			return
		}
		l, err := m.Drive.List(ctx, p)
		if err != nil {
			return
		}
		out = append(out, l.Files...)
		for _, f := range l.Folders {
			if !f.Virtual {
				walk(f.Path, depth+1)
			}
		}
	}
	walk("/", 0)
	return out
}

func (m *MemStore) Reconcile(ctx context.Context, org string, limit int) (int, error) {
	files := m.allFiles(ctx, org)
	alive := map[string]bool{}
	n := 0
	for _, f := range files {
		alive[f.ID] = true
		m.mu.Lock()
		s := m.st(org)[f.ID]
		stale := s == nil || s.version != f.CurrentVersion
		m.mu.Unlock()
		if stale && n < limit {
			_ = m.Enqueue(ctx, org, f.ID, f.CurrentVersion)
			n++
		}
	}
	m.mu.Lock()
	for id := range m.st(org) {
		if !alive[id] {
			delete(m.st(org), id)
			delete(m.ch(org), id)
		}
	}
	m.mu.Unlock()
	return n, nil
}

func (m *MemStore) Claim(_ context.Context, org string, n int, lease time.Duration) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	ids := make([]string, 0)
	for id, s := range m.st(org) {
		if s.status == StatusPending || s.status == StatusIndexing && now.After(s.lease) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []Job
	for _, id := range ids {
		if len(out) >= n {
			break
		}
		s := m.st(org)[id]
		s.status, s.lease = StatusIndexing, now.Add(lease)
		s.attempts++
		out = append(out, Job{FileID: id, Version: s.version, Attempts: s.attempts})
	}
	return out, nil
}

func (m *MemStore) Finish(_ context.Context, org string, j Job, cs []StoredChunk, out Outcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st(org)[j.FileID]
	if s == nil || s.version != j.Version || s.status != StatusIndexing {
		return nil // re-queued meanwhile: drop this result
	}
	s.status, s.reason, s.chunks, s.embedded, s.model = out.Status, out.Reason, out.Chunks, out.Embedded, out.Model
	s.lease = time.Time{}
	if out.Status == StatusIndexed {
		cp := make([]StoredChunk, len(cs))
		copy(cp, cs)
		m.ch(org)[j.FileID] = cp
	} else {
		delete(m.ch(org), j.FileID)
	}
	return nil
}

func (m *MemStore) Drop(_ context.Context, org, fileID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.st(org), fileID)
	delete(m.ch(org), fileID)
	return nil
}

func (m *MemStore) DropAll(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, org)
	delete(m.chunks, org)
	return nil
}

func (m *MemStore) PreviousVectors(_ context.Context, org, fileID, model string) (map[string][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]float32{}
	for _, c := range m.ch(org)[fileID] {
		if c.Vec != nil && c.Model == model {
			out[c.Hash] = c.Vec
		}
	}
	return out, nil
}

func (m *MemStore) RequeueUnembedded(_ context.Context, org, model string, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, s := range m.st(org) {
		if n >= limit {
			break
		}
		if s.status == StatusIndexed && s.chunks > 0 && (s.embedded < s.chunks || s.model != model) {
			_ = id
			s.status = StatusPending
			n++
		}
	}
	return n, nil
}

func (m *MemStore) ChunkCount(_ context.Context, org string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, cs := range m.ch(org) {
		n += len(cs)
	}
	return n, nil
}

func inScope(p string, under []string) bool {
	if under == nil {
		return true
	}
	lp := strings.ToLower(p)
	for _, u := range under {
		if drive.IsUnder(lp, strings.ToLower(u)) {
			return true
		}
	}
	return false
}

// visible walks the chunks of current versions of live files in scope.
func (m *MemStore) visible(ctx context.Context, org string, under []string, fn func(f drive.File, c StoredChunk)) {
	if under != nil && len(under) == 0 {
		return
	}
	m.mu.Lock()
	snap := map[string][]StoredChunk{}
	vers := map[string]int{}
	for id, cs := range m.ch(org) {
		snap[id] = cs
		if s := m.st(org)[id]; s != nil {
			vers[id] = s.version
		}
	}
	m.mu.Unlock()
	ids := make([]string, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f, ok := m.live(ctx, org, id)
		if !ok || !inScope(f.Path, under) || vers[id] != f.CurrentVersion {
			continue
		}
		for _, c := range snap[id] {
			fn(f, c)
		}
	}
}

func passageOf(f drive.File, c StoredChunk, ver int) Passage {
	return Passage{Key: Key{FileID: f.ID, Version: ver, Idx: c.Idx}, Path: f.Path, Name: f.Name, Page: c.Page, Line: c.Line, Start: c.Start,
		End: c.End, Heading: c.Heading, Text: c.Text, UpdatedBy: f.UpdatedBy}
}

func (m *MemStore) Keyword(ctx context.Context, org string, q KeywordQuery) ([]Passage, error) {
	terms := queryTerms(q.Query)
	if len(terms) == 0 {
		return []Passage{}, nil
	}
	var out []Passage
	m.visible(ctx, org, q.Under, func(f drive.File, c StoredChunk) {
		words := strings.FieldsFunc(textfold.Fold(c.Heading+" "+c.Text), func(r rune) bool { return !isWordRune(r) })
		hits := 0
		for _, t := range terms {
			st := stemOf(t)
			found := 0
			for _, w := range words {
				if strings.HasPrefix(w, st) {
					found++
				}
			}
			if found == 0 {
				return // AND semantics, like websearch_to_tsquery
			}
			hits += found
		}
		p := passageOf(f, c, f.CurrentVersion)
		p.Rank = float64(hits) / float64(len(words)+1)
		out = append(out, p)
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank > out[j].Rank })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *MemStore) Candidates(ctx context.Context, org string, q CandidateQuery) ([]Candidate, error) {
	var first, rest []Candidate
	m.visible(ctx, org, q.Under, func(f drive.File, c StoredChunk) {
		if c.Vec == nil || c.Model != q.Model {
			return
		}
		cd := Candidate{Key: Key{FileID: f.ID, Version: f.CurrentVersion, Idx: c.Idx}, Vec: c.Vec}
		low := textfold.Fold(c.Text)
		for _, t := range q.OrTerms {
			if strings.Contains(low, t) {
				first = append(first, cd)
				return
			}
		}
		rest = append(rest, cd)
	})
	out := append(first, rest...)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *MemStore) Passages(ctx context.Context, org string, keys []Key, under []string) ([]Passage, error) {
	want := map[Key]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []Passage
	m.visible(ctx, org, under, func(f drive.File, c StoredChunk) {
		k := Key{FileID: f.ID, Version: f.CurrentVersion, Idx: c.Idx}
		if want[k] {
			out = append(out, passageOf(f, c, f.CurrentVersion))
		}
	})
	return out, nil
}

func (m *MemStore) Status(ctx context.Context, org string, under []string) (StatusCounts, error) {
	var sc StatusCounts
	for _, f := range m.allFiles(ctx, org) {
		if !inScope(f.Path, under) {
			continue
		}
		m.mu.Lock()
		s := m.st(org)[f.ID]
		var st memState
		if s != nil {
			st = *s
		}
		m.mu.Unlock()
		if s == nil || st.version != f.CurrentVersion && st.status == StatusIndexed {
			sc.Unindexed++
			continue
		}
		switch st.status {
		case StatusPending:
			sc.Pending++
		case StatusIndexing:
			sc.Indexing++
		case StatusIndexed:
			sc.Indexed++
			sc.Chunks += st.chunks
			sc.Embedded += st.embedded
		case StatusSkipped:
			sc.Skipped++
			if st.reason == ReasonTooLarge {
				sc.TooLarge++
			}
		case StatusFailed:
			sc.Failed++
		}
	}
	return sc, nil
}

func dayKey(t time.Time) string { return t.UTC().Format(time.DateOnly) }

func (m *MemStore) EmbedUsage(_ context.Context, org string, day time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage[org][dayKey(day)], nil
}

func (m *MemStore) AddEmbedUsage(_ context.Context, org string, day time.Time, tokens int, _ float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usage[org] == nil {
		m.usage[org] = map[string]int{}
	}
	m.usage[org][dayKey(day)] += tokens
	return nil
}
