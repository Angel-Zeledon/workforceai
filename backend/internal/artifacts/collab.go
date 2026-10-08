package artifacts

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// Comments and proposals (docs/architecture/agent-workspaces.md sec. 5.5 and 8).
//   - A comment is a note anchored to a part of an artifact. Its body is data:
//     it is never executed or interpreted as an instruction.
//   - A proposal is a full replacement content an agent (with propose or edit
//     access) or a person suggests, based on a version. Accepting it writes a
//     version through the normal versioned save (three-way merge, optimistic
//     concurrency). Only a person with artifacts:approve accepts or rejects:
//     agents never decide a proposal, whatever their attachment mode.

const (
	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"

	maxCommentBody = 4000
	maxSummary     = 500
	maxMentions    = 20
)

type Comment struct {
	ID         string    `json:"id"`
	ArtifactID string    `json:"artifact_id"`
	ParentID   *string   `json:"parent_id,omitempty"`
	Anchor     *Anchor   `json:"anchor,omitempty"`
	Author     ActorRef  `json:"author"`
	Body       string    `json:"body"`
	Mentions   []string  `json:"mentions"`
	Resolved   bool      `json:"resolved"`
	ResolvedBy *string   `json:"resolved_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Proposal struct {
	ID              string          `json:"id"`
	ArtifactID      string          `json:"artifact_id"`
	BaseVersion     int             `json:"base_version"`
	Author          ActorRef        `json:"author"`
	Summary         string          `json:"summary"`
	Content         json.RawMessage `json:"content,omitempty"`
	Status          string          `json:"status"`
	ResolvedBy      *string         `json:"resolved_by,omitempty"`
	ResolvedNote    string          `json:"resolved_note,omitempty"`
	ResolvedVersion *int            `json:"resolved_version,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
}

// CollabStore persists comments and proposals, scoped by organization.
type CollabStore interface {
	AddComment(ctx context.Context, org string, c Comment) error
	ListComments(ctx context.Context, org, artifactID string) ([]Comment, error)
	GetComment(ctx context.Context, org, id string) (Comment, error)
	UpdateComment(ctx context.Context, org string, c Comment) error
	AddProposal(ctx context.Context, org string, p Proposal) error
	ListProposals(ctx context.Context, org, artifactID string) ([]Proposal, error)
	GetProposal(ctx context.Context, org, id string) (Proposal, error)
	UpdateProposal(ctx context.Context, org string, p Proposal) error
}

// MemCollab is the in-memory CollabStore (demo mode and tests).
type MemCollab struct {
	mu        sync.Mutex
	comments  map[string]Comment // org|id
	proposals map[string]Proposal
}

func NewMemCollab() *MemCollab {
	return &MemCollab{comments: map[string]Comment{}, proposals: map[string]Proposal{}}
}

var _ CollabStore = (*MemCollab)(nil)

func (m *MemCollab) AddComment(_ context.Context, org string, c Comment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments[key(org, c.ID)] = c
	return nil
}

func (m *MemCollab) ListComments(_ context.Context, org, artifactID string) ([]Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Comment{}
	for k, c := range m.comments {
		if strings.HasPrefix(k, org+"|") && c.ArtifactID == artifactID {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Comment) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (m *MemCollab) GetComment(_ context.Context, org, id string) (Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.comments[key(org, id)]
	if !ok {
		return Comment{}, domain.ErrNotFound
	}
	return c, nil
}

func (m *MemCollab) UpdateComment(_ context.Context, org string, c Comment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.comments[key(org, c.ID)]; !ok {
		return domain.ErrNotFound
	}
	m.comments[key(org, c.ID)] = c
	return nil
}

func (m *MemCollab) AddProposal(_ context.Context, org string, p Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proposals[key(org, p.ID)] = p
	return nil
}

func (m *MemCollab) ListProposals(_ context.Context, org, artifactID string) ([]Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Proposal{}
	for k, p := range m.proposals {
		if strings.HasPrefix(k, org+"|") && p.ArtifactID == artifactID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Proposal) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (m *MemCollab) GetProposal(_ context.Context, org, id string) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[key(org, id)]
	if !ok {
		return Proposal{}, domain.ErrNotFound
	}
	return p, nil
}

func (m *MemCollab) UpdateProposal(_ context.Context, org string, p Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.proposals[key(org, p.ID)]; !ok {
		return domain.ErrNotFound
	}
	m.proposals[key(org, p.ID)] = p
	return nil
}

func (s *Service) collab() CollabStore { return s.cfg.Collab }

// ---- comments ----

type CommentInput struct {
	Body     string   `json:"body"`
	ParentID string   `json:"parent_id,omitempty"`
	Anchor   *Anchor  `json:"anchor,omitempty"`
	Mentions []string `json:"mentions,omitempty"`
}

func (s *Service) Comments(ctx context.Context, id string) ([]Comment, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return nil, err
	}
	return s.collab().ListComments(ctx, org, id)
}

// canComment: people with artifacts:comment (checked by the API) and agents
// attached with at least propose access.
func canComment(actor Actor, m Meta) error {
	if actor.IsAgent() {
		if md := modeOf(m, actor.ID); md != ModePropose && md != ModeEdit {
			return forbidden("agent %s cannot comment on this artifact", actor.ID)
		}
	}
	return nil
}

func (s *Service) AddComment(ctx context.Context, actor Actor, id string, in CommentInput) (Comment, error) {
	org := s.org(ctx)
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Comment{}, err
	}
	if err := canComment(actor, cur.Meta); err != nil {
		return Comment{}, err
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len([]rune(body)) > maxCommentBody || len(in.Mentions) > maxMentions {
		return Comment{}, invalid("a comment needs a body of up to %d characters and at most %d mentions", maxCommentBody, maxMentions)
	}
	c := Comment{ID: "cmt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], ArtifactID: id, Anchor: in.Anchor, Author: actor.Ref(), Body: body,
		Mentions: slices.Clone(in.Mentions), CreatedAt: s.now()}
	if c.Mentions == nil {
		c.Mentions = []string{}
	}
	if in.ParentID != "" {
		p, err := s.collab().GetComment(ctx, org, in.ParentID)
		if err != nil || p.ArtifactID != id {
			return Comment{}, invalid("the parent comment does not belong to this artifact")
		}
		c.ParentID = &in.ParentID
	}
	if err := s.collab().AddComment(ctx, org, c); err != nil {
		return Comment{}, err
	}
	s.audit(ctx, actor, "artifact.comment_added", id, map[string]any{"comment_id": c.ID})
	s.emit(ctx, "artifact.comment_added", agentOf(c.Author), id, map[string]any{"comment": c}, "")
	return c, nil
}

func (s *Service) ResolveComment(ctx context.Context, actor Actor, id, cid string, resolved bool) (Comment, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return Comment{}, err
	}
	if actor.IsAgent() {
		return Comment{}, forbidden("agents cannot resolve comments")
	}
	c, err := s.collab().GetComment(ctx, org, cid)
	if err != nil || c.ArtifactID != id {
		return Comment{}, domain.ErrNotFound
	}
	c.Resolved = resolved
	c.ResolvedBy = nil
	if resolved {
		by := actor.ID
		c.ResolvedBy = &by
	}
	if err := s.collab().UpdateComment(ctx, org, c); err != nil {
		return Comment{}, err
	}
	s.audit(ctx, actor, "artifact.comment_resolved", id, map[string]any{"comment_id": cid, "resolved": resolved})
	s.emit(ctx, "artifact.comment_resolved", "", id, map[string]any{"comment_id": cid, "artifact_id": id, "resolved": resolved}, "")
	return c, nil
}

// ---- proposals ----

type ProposalInput struct {
	BaseVersion int             `json:"base_version"`
	Content     json.RawMessage `json:"content"`
	Summary     string          `json:"summary,omitempty"`
}

type DecisionInput struct {
	Decision string `json:"decision"` // accept|reject
	Note     string `json:"note,omitempty"`
}

func (s *Service) Proposals(ctx context.Context, id, status string) ([]Proposal, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return nil, err
	}
	all, err := s.collab().ListProposals(ctx, org, id)
	if err != nil || status == "" {
		return all, err
	}
	return slices.DeleteFunc(all, func(p Proposal) bool { return p.Status != status }), nil
}

// Propose records a suggested content. An agent needs propose or edit access;
// a person may suggest too. Nothing changes in the artifact until a person accepts.
func (s *Service) Propose(ctx context.Context, actor Actor, id string, in ProposalInput) (Proposal, error) {
	org := s.org(ctx)
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Proposal{}, err
	}
	m := cur.Meta
	if m.Status == StatusArchived {
		return Proposal{}, conflictErr("the artifact is archived")
	}
	if actor.IsAgent() {
		if md := modeOf(m, actor.ID); md != ModePropose && md != ModeEdit {
			return Proposal{}, forbidden("agent %s has no propose access to this artifact", actor.ID)
		}
	}
	if in.BaseVersion <= 0 || in.BaseVersion > m.HeadVersion {
		return Proposal{}, invalid("base_version must be between 1 and the head version %d", m.HeadVersion)
	}
	raw, _, err := parseContent(m.Kind, in.Content)
	if err != nil {
		return Proposal{}, err
	}
	if len([]rune(in.Summary)) > maxSummary {
		return Proposal{}, invalid("the summary is limited to %d characters", maxSummary)
	}
	p := Proposal{ID: "prop_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], ArtifactID: id, BaseVersion: in.BaseVersion, Author: actor.Ref(),
		Summary: strings.TrimSpace(in.Summary), Content: raw, Status: ProposalPending, CreatedAt: s.now()}
	if err := s.collab().AddProposal(ctx, org, p); err != nil {
		return Proposal{}, err
	}
	if err := s.refreshPending(ctx, org, m); err != nil {
		return Proposal{}, err
	}
	s.audit(ctx, actor, "artifact.proposal_created", id, map[string]any{"proposal_id": p.ID, "base_version": p.BaseVersion})
	s.emit(ctx, "artifact.proposal_created", agentOf(p.Author), id, map[string]any{"proposal": p, "artifact_id": id}, "")
	return p, nil
}

// refreshPending recounts the pending proposals into the artifact meta (caller holds the artifact lock).
func (s *Service) refreshPending(ctx context.Context, org string, m Meta) error {
	all, err := s.collab().ListProposals(ctx, org, m.ID)
	if err != nil {
		return err
	}
	n := 0
	for _, p := range all {
		if p.Status == ProposalPending {
			n++
		}
	}
	cur, err := s.cfg.Store.Get(ctx, org, m.ID)
	if err != nil {
		return err
	}
	meta := cur.Meta
	meta.PendingProposals = n
	if err := s.cfg.Store.UpdateMeta(ctx, org, meta); err != nil {
		return err
	}
	meta.HeadVersion, meta.SizeBytes, meta.LastAuthor = cur.Meta.HeadVersion, cur.Meta.SizeBytes, cur.Meta.LastAuthor
	s.emit(ctx, "artifact.updated", "", m.ID, map[string]any{"artifact": meta}, "")
	return nil
}

// Decide accepts or rejects a pending proposal. Only a person holding
// artifacts:approve may; agents are refused whatever their attachment mode.
func (s *Service) Decide(ctx context.Context, actor Actor, id, pid string, in DecisionInput) (Proposal, error) {
	org := s.org(ctx)
	if actor.IsAgent() || !actor.CanApprove {
		return Proposal{}, forbidden("only a person with approval rights decides a proposal")
	}
	defer s.lock(org, "decide:"+pid)() // one decision per proposal at a time
	if in.Decision != "accept" && in.Decision != "reject" {
		return Proposal{}, invalid("decision must be accept or reject")
	}
	if len([]rune(in.Note)) > maxSummary {
		return Proposal{}, invalid("the note is limited to %d characters", maxSummary)
	}
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return Proposal{}, err
	}
	p, err := s.collab().GetProposal(ctx, org, pid)
	if err != nil || p.ArtifactID != id {
		return Proposal{}, domain.ErrNotFound
	}
	if p.Status != ProposalPending {
		return Proposal{}, conflictErr("the proposal is already %s", p.Status)
	}
	now := s.now()
	by := actor.ID
	p.ResolvedBy, p.ResolvedNote, p.ResolvedAt = &by, strings.TrimSpace(in.Note), &now
	if in.Decision == "accept" {
		res, changed, err := s.saveLocked(ctx, org, actor, id, SaveInput{BaseVersion: p.BaseVersion, Content: p.Content,
			Summary: firstNonEmpty(p.Summary, L("", "Propuesta aceptada", "Proposal accepted")), source: "accept_proposal"})
		if err != nil {
			return Proposal{}, err // a *Conflict means the same unit changed since: nothing was written
		}
		if changed {
			s.propagate(ctx, org, id, res.Version, 0)
		}
		p.Status, p.ResolvedVersion = ProposalAccepted, &res.Version
	} else {
		p.Status = ProposalRejected
	}
	if err := s.collab().UpdateProposal(ctx, org, p); err != nil {
		return Proposal{}, err
	}
	func() {
		defer s.lock(org, id)()
		if cur, err := s.cfg.Store.Get(ctx, org, id); err == nil {
			_ = s.refreshPending(ctx, org, cur.Meta)
		}
	}()
	s.audit(ctx, actor, "artifact.proposal_"+p.Status, id, map[string]any{"proposal_id": pid, "author": p.Author, "note": p.ResolvedNote})
	s.emit(ctx, "artifact.proposal_resolved", "", id, map[string]any{"proposal_id": pid, "artifact_id": id, "status": p.Status, "resolved_by": actor.ID, "version": p.ResolvedVersion}, "")
	return p, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
