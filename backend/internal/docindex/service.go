package docindex

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// Limits bound the index (env overrides in LimitsFromEnv).
type Limits struct {
	MaxChunksPerFile int     // DOCINDEX_MAX_CHUNKS_PER_FILE (2000): above it the file is "skipped: too large"
	MaxChunksPerOrg  int     // DOCINDEX_MAX_CHUNKS_PER_ORG (200000)
	MaxTextChars     int     // characters extracted per file (1,000,000, the drive's read cap)
	DailyEmbedTokens int     // EMBEDDINGS_DAILY_TOKENS per organization (2,000,000)
	EmbedPricePerM   float64 // EMBEDDINGS_PRICE_PER_M USD per million tokens (0.02)
	CandidateCap     int     // DOCINDEX_SEMANTIC_CANDIDATES chunks scored by cosine per query (3000)
	Workers          int     // DOCINDEX_WORKERS concurrent files per replica (2)
	QueueSize        int     // pending notifications kept in memory (256)
	Tick             time.Duration
	ReconcileEvery   time.Duration
	Lease            time.Duration
	SearchTimeout    time.Duration
	EmbedTimeout     time.Duration
}

// LimitsFromEnv reads the DOCINDEX_* and EMBEDDINGS_* limits.
func LimitsFromEnv() Limits {
	return Limits{MaxChunksPerFile: envInt("DOCINDEX_MAX_CHUNKS_PER_FILE", 0), MaxChunksPerOrg: envInt("DOCINDEX_MAX_CHUNKS_PER_ORG", 0),
		DailyEmbedTokens: envInt("EMBEDDINGS_DAILY_TOKENS", 0), EmbedPricePerM: envFloat("EMBEDDINGS_PRICE_PER_M", -1),
		CandidateCap: envInt("DOCINDEX_SEMANTIC_CANDIDATES", 0), Workers: envInt("DOCINDEX_WORKERS", 0)}
}

func (l Limits) norm() Limits {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&l.MaxChunksPerFile, 2000)
	def(&l.MaxChunksPerOrg, 200_000)
	def(&l.MaxTextChars, 1_000_000)
	def(&l.DailyEmbedTokens, 2_000_000)
	def(&l.CandidateCap, 3000)
	l.CandidateCap = min(l.CandidateCap, 20_000) // hard cap: memory and latency of the cosine pass
	def(&l.Workers, 2)
	def(&l.QueueSize, 256)
	if l.EmbedPricePerM < 0 {
		l.EmbedPricePerM = 0.02
	}
	dur := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	dur(&l.Tick, 20*time.Second)
	dur(&l.ReconcileEvery, 5*time.Minute)
	dur(&l.Lease, 3*time.Minute)
	dur(&l.SearchTimeout, 8*time.Second)
	dur(&l.EmbedTimeout, 45*time.Second)
	return l
}

// Spend lets embedding calls count against the organization's budget.
type Spend interface {
	// Allow reports whether usd more may be spent now (the org budget cap).
	Allow(ctx context.Context, org string, usd float64) bool
	// Record books a finished embeddings call in the usage ledger.
	Record(ctx context.Context, org string, tokens int, usd float64, model string)
}

// Config wires the service.
type Config struct {
	Store Store
	Drive *drive.Service
	// Orgs lists the organizations for the background sweep (nil = only OrgID).
	Orgs  func(ctx context.Context) ([]string, error)
	OrgID string // default organization (single-tenant mode)
	// Embeddings picks the embedder per organization (nil = keyword only).
	Embeddings EmbedResolver
	Spend      Spend
	// Locale returns the organization language ("es", "en", "pt") for stemming.
	Locale func(ctx context.Context, org string) string
	Limits Limits
	Chunk  ChunkOptions
	Log    *slog.Logger
	Now    func() time.Time
	// Audit records setting changes (metadata only).
	Audit func(ctx context.Context, org, actor, action string, details map[string]any)
}

// Service is the indexer (background worker) and the searcher.
type Service struct {
	cfg   Config
	notes chan note

	mu         sync.Mutex
	reconciled map[string]time.Time
	started    bool
}

type note struct{ org, fileID string }

// New builds the service; Start launches the worker.
func New(cfg Config) *Service {
	cfg.Limits = cfg.Limits.norm()
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg, notes: make(chan note, cfg.Limits.QueueSize), reconciled: map[string]time.Time{}}
}

var _ drive.ChunkSearcher = (*Service)(nil)

// Binding are the collaborators that exist only after the drive is built
// (model policy, bring-your-own keys, budget, organization language).
type Binding struct {
	Embeddings EmbedResolver
	Spend      Spend
	Locale     func(ctx context.Context, org string) string
	Audit      func(ctx context.Context, org, actor, action string, details map[string]any)
	Orgs       func(ctx context.Context) ([]string, error)
}

// Bind sets the late collaborators. Call it before Start and before serving.
func (s *Service) Bind(b Binding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.Embeddings != nil {
		s.cfg.Embeddings = b.Embeddings
	}
	if b.Spend != nil {
		s.cfg.Spend = b.Spend
	}
	if b.Locale != nil {
		s.cfg.Locale = b.Locale
	}
	if b.Audit != nil {
		s.cfg.Audit = b.Audit
	}
	if b.Orgs != nil {
		s.cfg.Orgs = b.Orgs
	}
}

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

// ---- triggers ----

// Notify tells the worker that a file changed (new version, restore, trash).
// It never blocks a write: when the queue is full the periodic reconcile
// finds the change anyway.
func (s *Service) Notify(org, fileID string) {
	select {
	case s.notes <- note{org, fileID}:
	default:
	}
}

// OnDriveAudit maps the drive's audit actions to index triggers.
func (s *Service) OnDriveAudit(org, action, entity, id string) {
	switch action {
	case "drive.file.created", "drive.file.version_created", "drive.version.restored", "drive.file.restored", "drive.file.deleted":
		s.Notify(org, id)
	case "drive.folder.deleted", "drive.folder.restored":
		s.Notify(org, "") // a whole subtree: reconcile the organization
	}
}

// Start runs the worker until ctx ends. Safe with several replicas: work is
// claimed in the database with a lease.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go s.loop(ctx)
}

func (s *Service) loop(ctx context.Context) {
	t := time.NewTicker(s.cfg.Limits.Tick)
	defer t.Stop()
	s.sweep(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-s.notes:
			dirty := map[string]bool{}
			s.handle(ctx, n, dirty)
			for more := true; more; { // coalesce a burst of writes
				select {
				case n2 := <-s.notes:
					s.handle(ctx, n2, dirty)
				default:
					more = false
				}
			}
			for org := range dirty {
				s.Drain(ctx, org)
			}
		case <-t.C:
			s.sweep(ctx, false)
		}
	}
}

func (s *Service) handle(ctx context.Context, n note, dirty map[string]bool) {
	if !s.enabled(ctx, n.org) {
		return
	}
	dirty[n.org] = true
	if n.fileID == "" {
		s.reconcile(ctx, n.org, true)
		return
	}
	octx := application.WithOrg(ctx, n.org)
	f, err := s.cfg.Drive.GetFile(octx, n.fileID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		_ = s.cfg.Store.Drop(octx, n.org, n.fileID)
	case err != nil:
		s.cfg.Log.Warn("docindex: notify", "org", n.org, "err", err)
	case f.DeletedAt != nil:
		_ = s.cfg.Store.Drop(octx, n.org, n.fileID) // trash: never searchable
	default:
		if err := s.cfg.Store.Enqueue(octx, n.org, f.ID, f.CurrentVersion); err != nil {
			s.cfg.Log.Warn("docindex: enqueue", "org", n.org, "err", err)
		}
	}
}

func (s *Service) orgs(ctx context.Context) []string {
	if s.cfg.Orgs == nil {
		if s.cfg.OrgID == "" {
			return nil
		}
		return []string{s.cfg.OrgID}
	}
	ids, err := s.cfg.Orgs(ctx)
	if err != nil {
		s.cfg.Log.Warn("docindex: list organizations", "err", err)
	}
	return ids
}

// sweep reconciles (every ReconcileEvery, or always at start) and drains every organization.
func (s *Service) sweep(ctx context.Context, force bool) {
	for _, org := range s.orgs(ctx) {
		if ctx.Err() != nil {
			return
		}
		if !s.enabled(ctx, org) {
			continue
		}
		s.reconcile(ctx, org, force)
		s.Drain(ctx, org)
	}
}

func (s *Service) reconcile(ctx context.Context, org string, force bool) {
	s.mu.Lock()
	last := s.reconciled[org]
	if !force && s.now().Sub(last) < s.cfg.Limits.ReconcileEvery {
		s.mu.Unlock()
		return
	}
	s.reconciled[org] = s.now()
	s.mu.Unlock()
	octx := application.WithOrg(ctx, org)
	if n, err := s.cfg.Store.Reconcile(octx, org, 5000); err != nil {
		s.cfg.Log.Warn("docindex: reconcile", "org", org, "err", err)
	} else if n > 0 {
		s.cfg.Log.Info("docindex: files queued", "org", org, "count", n)
	}
	// embeddings turned on later: re-embed what was indexed keyword-only (within the daily cap)
	if s.cfg.Embeddings != nil {
		if ch := s.cfg.Embeddings(octx, org); ch.Embedder != nil {
			if used, err := s.cfg.Store.EmbedUsage(octx, org, s.now()); err == nil && used < s.cfg.Limits.DailyEmbedTokens {
				_, _ = s.cfg.Store.RequeueUnembedded(octx, org, ch.Embedder.Model(), 200)
			}
		}
	}
}

// Sync reconciles an organization with the drive (backfill, new versions,
// trash) and indexes what is pending, synchronously. The worker does the same
// in the background; Sync is for tests, tools and the first enable.
func (s *Service) Sync(ctx context.Context, org string) int {
	if !s.enabled(ctx, org) {
		return 0
	}
	s.reconcile(ctx, org, true)
	return s.Drain(ctx, org)
}

// Drain indexes the pending files of an organization (bounded per call so one
// organization cannot starve the others). Exported for tests and backfills.
func (s *Service) Drain(ctx context.Context, org string) int {
	octx := application.WithOrg(ctx, org)
	done := 0
	for round := 0; round < 50 && ctx.Err() == nil; round++ {
		jobs, err := s.cfg.Store.Claim(octx, org, s.cfg.Limits.Workers*2, s.cfg.Limits.Lease)
		if err != nil {
			s.cfg.Log.Warn("docindex: claim", "org", org, "err", err)
			return done
		}
		if len(jobs) == 0 {
			return done
		}
		sem := make(chan struct{}, s.cfg.Limits.Workers)
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func(j Job) {
				defer func() { <-sem; wg.Done() }()
				s.index(octx, org, j)
			}(j)
		}
		wg.Wait()
		done += len(jobs)
	}
	return done
}

func (s *Service) enabled(ctx context.Context, org string) bool {
	if org == "" {
		return false
	}
	st, err := s.cfg.Store.Settings(application.WithOrg(ctx, org), org)
	return err == nil && st.Enabled
}

// index builds the chunks of one file version.
func (s *Service) index(ctx context.Context, org string, j Job) {
	finish := func(chunks []StoredChunk, out Outcome) {
		if err := s.cfg.Store.Finish(ctx, org, j, chunks, out); err != nil {
			s.cfg.Log.Warn("docindex: store chunks", "org", org, "file", j.FileID, "err", err)
		}
	}
	f, err := s.cfg.Drive.GetFile(ctx, j.FileID)
	if errors.Is(err, domain.ErrNotFound) || err == nil && f.DeletedAt != nil {
		_ = s.cfg.Store.Drop(ctx, org, j.FileID)
		return
	}
	if err != nil {
		finish(nil, Outcome{Status: StatusFailed, Reason: ReasonError})
		return
	}
	if f.CurrentVersion != j.Version { // a newer version exists: index that one instead
		_ = s.cfg.Store.Enqueue(ctx, org, f.ID, f.CurrentVersion)
		return
	}
	if _, ok := drive.TypeOf(f.Name); !ok {
		finish(nil, Outcome{Status: StatusSkipped, Reason: ReasonUnsupported})
		return
	}
	x, _, err := s.cfg.Drive.FullText(ctx, f, j.Version, s.cfg.Limits.MaxTextChars)
	if err != nil {
		finish(nil, Outcome{Status: StatusFailed, Reason: ReasonError})
		return
	}
	if !x.Extracted {
		finish(nil, Outcome{Status: StatusSkipped, Reason: ReasonUnsupported})
		return
	}
	if strings.TrimSpace(drive.PageMarker().ReplaceAllString(x.Text, "")) == "" {
		finish(nil, Outcome{Status: StatusSkipped, Reason: ReasonNoText})
		return
	}
	lim := s.cfg.Limits
	chunks := Split(x.Text, s.cfg.Chunk, lim.MaxChunksPerFile+1)
	if len(chunks) > lim.MaxChunksPerFile {
		finish(nil, Outcome{Status: StatusSkipped, Reason: ReasonTooLarge})
		return
	}
	if n, err := s.cfg.Store.ChunkCount(ctx, org); err == nil && n+len(chunks) > lim.MaxChunksPerOrg {
		finish(nil, Outcome{Status: StatusSkipped, Reason: ReasonOrgLimit})
		return
	}
	loc := ""
	if s.cfg.Locale != nil {
		loc = s.cfg.Locale(ctx, org)
	}
	lang := DetectLang(x.Text, LangOfLocale(loc))
	out := Outcome{Status: StatusIndexed, Chunks: len(chunks)}
	stored := make([]StoredChunk, len(chunks))
	for i, c := range chunks {
		stored[i] = StoredChunk{Chunk: c, Lang: lang}
	}
	out.Embedded, out.Model, out.Reason = s.embedChunks(ctx, org, j.FileID, stored)
	finish(stored, out)
}

// embedChunks fills the vectors it can (reusing those of unchanged chunks).
func (s *Service) embedChunks(ctx context.Context, org, fileID string, cs []StoredChunk) (int, string, string) {
	if s.cfg.Embeddings == nil {
		return 0, "", ReasonEmbedOff
	}
	ch := s.cfg.Embeddings(ctx, org)
	if ch.Embedder == nil {
		return 0, "", ch.Reason
	}
	model := ch.Embedder.Model()
	prev, _ := s.cfg.Store.PreviousVectors(ctx, org, fileID, model)
	var need []int
	n := 0
	for i := range cs {
		if v, ok := prev[cs[i].Hash]; ok {
			cs[i].Vec, cs[i].Model = v, model
			n++
			continue
		}
		need = append(need, i)
	}
	if len(need) == 0 {
		return n, model, ""
	}
	texts := make([]string, len(need))
	est := 0
	for k, i := range need {
		texts[k] = embedText(cs[i].Chunk)
		est += utf8.RuneCountInString(texts[k])/4 + 1
	}
	if reason := s.allowEmbed(ctx, org, est); reason != "" {
		return n, model, reason
	}
	ectx, cancel := context.WithTimeout(ctx, s.cfg.Limits.EmbedTimeout)
	defer cancel()
	vecs, tokens, err := ch.Embedder.Embed(ectx, texts)
	if err != nil || len(vecs) != len(texts) {
		s.cfg.Log.Warn("docindex: embeddings failed: keyword only", "org", org, "provider", ch.Embedder.Provider())
		return n, model, ReasonEmbedFailure
	}
	s.bookEmbed(ctx, org, max(tokens, est), model)
	for k, i := range need {
		cs[i].Vec, cs[i].Model = vecs[k], model
	}
	return n + len(need), model, ""
}

func embedText(c Chunk) string {
	if c.Heading != "" {
		return c.Heading + "\n" + c.Text
	}
	return c.Text
}

// allowEmbed applies the per-day token cap and the budget ("" = allowed).
func (s *Service) allowEmbed(ctx context.Context, org string, tokens int) string {
	used, err := s.cfg.Store.EmbedUsage(ctx, org, s.now())
	if err != nil || used+tokens > s.cfg.Limits.DailyEmbedTokens {
		return ReasonEmbedCap
	}
	if s.cfg.Spend != nil && !s.cfg.Spend.Allow(ctx, org, s.cost(tokens)) {
		return ReasonEmbedBudget
	}
	return ""
}

func (s *Service) cost(tokens int) float64 {
	return float64(tokens) * s.cfg.Limits.EmbedPricePerM / 1e6
}

func (s *Service) bookEmbed(ctx context.Context, org string, tokens int, model string) {
	usd := s.cost(tokens)
	if err := s.cfg.Store.AddEmbedUsage(ctx, org, s.now(), tokens, usd); err != nil {
		s.cfg.Log.Warn("docindex: embed usage", "org", org, "err", err)
	}
	if s.cfg.Spend != nil {
		s.cfg.Spend.Record(ctx, org, tokens, usd, model)
	}
}

// ---- settings and status ----

// Settings returns the organization's index setting.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return s.cfg.Store.Settings(ctx, s.org(ctx))
}

// SetEnabled turns indexing on (the backfill starts) or off (the index of the
// organization is deleted; searches fall back to the file-level search).
func (s *Service) SetEnabled(ctx context.Context, actor string, on bool) (Settings, error) {
	org := s.org(ctx)
	now := s.now()
	st := Settings{Enabled: on, UpdatedBy: actor, UpdatedAt: &now}
	if err := s.cfg.Store.SetSettings(ctx, org, st); err != nil {
		return Settings{}, err
	}
	if !on {
		if err := s.cfg.Store.DropAll(ctx, org); err != nil {
			return st, err
		}
	} else {
		s.mu.Lock()
		delete(s.reconciled, org)
		s.mu.Unlock()
		s.Notify(org, "")
	}
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, actor, "doc_index.settings_changed", map[string]any{"enabled": on})
	}
	return st, nil
}

// EmbeddingsInfo says whether semantic search is available for the organization.
type EmbeddingsInfo struct {
	Available bool   `json:"available"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Note      string `json:"note,omitempty"`
	UsedToday int    `json:"tokens_today"`
	DailyCap  int    `json:"daily_token_cap"`
}

// Embeddings reports the embeddings state of the organization.
func (s *Service) Embeddings(ctx context.Context) EmbeddingsInfo {
	org := s.org(ctx)
	info := EmbeddingsInfo{DailyCap: s.cfg.Limits.DailyEmbedTokens}
	info.UsedToday, _ = s.cfg.Store.EmbedUsage(ctx, org, s.now())
	if s.cfg.Embeddings == nil {
		info.Reason, info.Note = ReasonEmbedOff, "no embeddings provider is configured: search is keyword only"
		return info
	}
	ch := s.cfg.Embeddings(ctx, org)
	if ch.Embedder == nil {
		info.Reason, info.Note = ch.Reason, ch.Note
		return info
	}
	info.Available, info.Provider, info.Model = true, ch.Embedder.Provider(), ch.Embedder.Model()
	return info
}

// Status counts the index states under a folder ("" or "/" = everything).
func (s *Service) Status(ctx context.Context, under string) (StatusCounts, error) {
	var u []string
	if under != "" && under != "/" {
		c, err := drive.CleanPath(under)
		if err != nil {
			return StatusCounts{}, err
		}
		u = []string{c}
	}
	return s.cfg.Store.Status(ctx, s.org(ctx), u)
}
