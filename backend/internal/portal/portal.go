// Package portal implements the client portal (feature B13, docs/architecture/client-portal.md): a
// read-only, revocable link that shows ONE project's progress to the firm's client, with the firm's
// branding.
//
// Rules that never change:
//   - a link is a bearer token for ONE project; only its SHA-256 is stored and the plaintext is shown
//     once, at creation (the same style as invitations, internal/auth/invitations.go);
//   - only humans with org:manage create, edit and revoke links; agents have no tool for it;
//   - the public view is built by an allowlist (public.go): a dedicated DTO with titles, coarse states
//     and dates. Costs, agents, prompts, approvals, notes, ids and connection data are never copied in;
//   - creation, edition, revocation and the first view are audited (never the token).
package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/imagenorm"
	"aiworkforce/backend/internal/projects"
)

// Limits.
const (
	MaxLabel         = 80
	MaxTitle         = 120
	MaxDisplayName   = 80
	MaxDeliverables  = 100
	MaxActivePerProj = 20
	MaxExpiryDays    = 365
	MaxLogoInput     = 2 << 20   // bytes accepted on upload
	MaxLogoBytes     = 256 << 10 // bytes stored after normalization
	LogoMaxSide      = 512       // pixels
	tokenBytes       = 32
)

// Link states (derived, never stored).
const (
	LinkActive  = "active"
	LinkExpired = "expired"
	LinkRevoked = "revoked"
)

// Errors. ErrUnavailable covers unknown, revoked and expired tokens alike: the public caller cannot tell
// them apart.
var (
	ErrUnavailable = errors.New("portal: link unavailable")
	ErrNoLogo      = errors.New("portal: no logo")
)

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Branding is the firm's look on the public page (one per organization).
type Branding struct {
	DisplayName string    `json:"display_name"`
	AccentColor string    `json:"accent_color"`
	HasLogo     bool      `json:"has_logo"`
	UpdatedBy   string    `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
	LogoMime    string    `json:"-"`
	Logo        []byte    `json:"-"`
}

// Link is one share link of a project.
type Link struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	Label          string     `json:"label"` // internal note ("which client"), never shown on the page
	Title          string     `json:"title"` // what the client reads as the project title ("" = the project name)
	DeliverableIDs []string   `json:"deliverable_ids"`
	TokenHash      string     `json:"-"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	RevokedBy      string     `json:"revoked_by,omitempty"`
	FirstViewedAt  *time.Time `json:"first_viewed_at"`
	LastViewedAt   *time.Time `json:"last_viewed_at"`
}

// Status derives the state of the link at now.
func (l Link) Status(now time.Time) string {
	switch {
	case l.RevokedAt != nil:
		return LinkRevoked
	case l.ExpiresAt != nil && !l.ExpiresAt.After(now):
		return LinkExpired
	}
	return LinkActive
}

// LinkView is the link plus its derived status (never the token).
type LinkView struct {
	Link
	Status string `json:"status"`
}

// CreatedLink is returned once, at creation: Token is the only copy of the secret.
type CreatedLink struct {
	LinkView
	Token string `json:"token"`
}

// LinkInput creates or edits a link. ExpiresInDays 0 = no expiry (creation only).
type LinkInput struct {
	Label          string   `json:"label"`
	Title          string   `json:"title"`
	ExpiresInDays  int      `json:"expires_in_days"`
	DeliverableIDs []string `json:"deliverable_ids"`
}

// BrandingInput edits the text fields of the branding (the logo has its own calls).
type BrandingInput struct {
	DisplayName string `json:"display_name"`
	AccentColor string `json:"accent_color"`
}

// Candidate is a deliverable of the project the firm may mark as shareable.
type Candidate struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Completed bool      `json:"completed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists branding and links. Every method but LinkByHash is scoped to org.
type Store interface {
	GetBranding(ctx context.Context, org string) (Branding, error) // domain.ErrNotFound = defaults
	PutBranding(ctx context.Context, org string, b Branding) error
	CreateLink(ctx context.Context, org string, l Link) error
	UpdateLink(ctx context.Context, org string, l Link) error // label, deliverable ids, updated_at
	GetLink(ctx context.Context, org, id string) (Link, error)
	ListLinks(ctx context.Context, org, projectID string) ([]Link, error)
	RevokeLink(ctx context.Context, org, id, by string, at time.Time) error
	// LinkByHash finds a link before the tenant is known (the hash is the only key).
	LinkByHash(ctx context.Context, hash string) (org string, l Link, err error)
	// MarkViewed records a view; first is true for the very first one. It may skip the write when the
	// last view is recent (at most one write a minute per link).
	MarkViewed(ctx context.Context, org, id string, at time.Time) (first bool, err error)
}

// Config wires the service.
type Config struct {
	Store Store
	OrgID string // the default (demo) org
	// Project returns the full detail of a project of org.
	Project func(ctx context.Context, org, id string) (projects.Detail, error)
	// Deliverables lists the artifacts of a project of org (nil = no deliverables).
	Deliverables func(ctx context.Context, org, projectID string) ([]artifacts.Meta, error)
	// OrgName is the fallback firm name when the branding has none (nil = none).
	OrgName func(ctx context.Context, org string) (string, error)
	// Audit records every write and the first view (metadata only, never the token).
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	Now   func() time.Time
}

// Service is the client portal use-case layer.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) audit(ctx context.Context, org, actor, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, actor, action, id, d)
	}
}

// ---- tokens ----

func newToken() (plain, hash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, HashToken(plain), nil
}

// HashToken is the stored form of a token (SHA-256, hex).
func HashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// plausibleToken rejects garbage before any database work.
func plausibleToken(t string) bool {
	if len(t) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "pl_" + hex.EncodeToString(b)
}

// ---- validation ----

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

// cleanText trims, drops control characters and checks the length in runes.
func cleanText(field, v string, max int) (string, error) {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
	if utf8.RuneCountInString(v) > max {
		return "", invalid("%s must be at most %d characters", field, max)
	}
	return v, nil
}

func cleanLabels(in LinkInput) (label, title string, err error) {
	if label, err = cleanText("label", in.Label, MaxLabel); err != nil {
		return "", "", err
	}
	title, err = cleanText("title", in.Title, MaxTitle)
	return label, title, err
}

func dedupe(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ---- branding ----

// Branding returns the branding of the caller's organization (defaults when none).
func (s *Service) Branding(ctx context.Context) (Branding, error) {
	return s.branding(ctx, s.org(ctx))
}

func (s *Service) branding(ctx context.Context, org string) (Branding, error) {
	b, err := s.cfg.Store.GetBranding(ctx, org)
	if errors.Is(err, domain.ErrNotFound) {
		return Branding{}, nil
	}
	b.HasLogo = len(b.Logo) > 0
	return b, err
}

// SetBranding updates the firm name and accent color (the logo is kept).
func (s *Service) SetBranding(ctx context.Context, actor string, in BrandingInput) (Branding, error) {
	org := s.org(ctx)
	name, err := cleanText("display_name", in.DisplayName, MaxDisplayName)
	if err != nil {
		return Branding{}, err
	}
	color := strings.TrimSpace(in.AccentColor)
	if color != "" && !colorRe.MatchString(color) {
		return Branding{}, invalid("accent_color must look like #1a2b3c")
	}
	b, err := s.branding(ctx, org)
	if err != nil {
		return Branding{}, err
	}
	b.DisplayName, b.AccentColor, b.UpdatedBy, b.UpdatedAt = name, strings.ToLower(color), actor, s.now()
	if err := s.cfg.Store.PutBranding(ctx, org, b); err != nil {
		return Branding{}, err
	}
	s.audit(ctx, org, actor, "portal_branding.updated", org, map[string]any{"has_name": name != "", "accent_color": b.AccentColor})
	return b, nil
}

// SetLogo stores a new logo. The image is decoded and re-encoded (PNG/JPEG/GIF in, PNG/JPEG out, no
// metadata, at most LogoMaxSide pixels), so what is served is only pixels, never the uploaded bytes.
func (s *Service) SetLogo(ctx context.Context, actor string, data []byte) (Branding, error) {
	org := s.org(ctx)
	if len(data) == 0 {
		return Branding{}, invalid("empty image")
	}
	if len(data) > MaxLogoInput {
		return Branding{}, invalid("image larger than %d bytes", MaxLogoInput)
	}
	img, err := imagenorm.Normalize(data, imagenorm.Options{MaxSide: LogoMaxSide, MaxBytes: MaxLogoBytes, MaxInput: MaxLogoInput, MaxPixels: 25_000_000})
	if err != nil {
		return Branding{}, invalid("%s", imagenorm.CodeOf(err))
	}
	b, err := s.branding(ctx, org)
	if err != nil {
		return Branding{}, err
	}
	b.Logo, b.LogoMime, b.HasLogo, b.UpdatedBy, b.UpdatedAt = img.Data, img.Mime, true, actor, s.now()
	if err := s.cfg.Store.PutBranding(ctx, org, b); err != nil {
		return Branding{}, err
	}
	s.audit(ctx, org, actor, "portal_branding.logo_set", org, map[string]any{"mime": img.Mime, "width": img.Width, "height": img.Height, "bytes": len(img.Data)})
	return b, nil
}

// DeleteLogo removes the logo.
func (s *Service) DeleteLogo(ctx context.Context, actor string) (Branding, error) {
	org := s.org(ctx)
	b, err := s.branding(ctx, org)
	if err != nil {
		return Branding{}, err
	}
	b.Logo, b.LogoMime, b.HasLogo, b.UpdatedBy, b.UpdatedAt = nil, "", false, actor, s.now()
	if err := s.cfg.Store.PutBranding(ctx, org, b); err != nil {
		return Branding{}, err
	}
	s.audit(ctx, org, actor, "portal_branding.logo_removed", org, nil)
	return b, nil
}

// Logo returns the caller's organization logo.
func (s *Service) Logo(ctx context.Context) (mime string, data []byte, err error) {
	b, err := s.branding(ctx, s.org(ctx))
	if err != nil {
		return "", nil, err
	}
	if !b.HasLogo {
		return "", nil, ErrNoLogo
	}
	return b.LogoMime, b.Logo, nil
}

// ---- links ----

func (s *Service) view(l Link) LinkView {
	if l.DeliverableIDs == nil {
		l.DeliverableIDs = []string{}
	}
	return LinkView{Link: l, Status: l.Status(s.now())}
}

// project loads the project (it must exist in the caller's organization).
func (s *Service) project(ctx context.Context, org, id string) (projects.Detail, error) {
	if s.cfg.Project == nil {
		return projects.Detail{}, domain.ErrNotFound
	}
	return s.cfg.Project(application.WithOrg(ctx, org), org, id)
}

func (s *Service) deliverables(ctx context.Context, org, projectID string) ([]artifacts.Meta, error) {
	if s.cfg.Deliverables == nil {
		return nil, nil
	}
	return s.cfg.Deliverables(application.WithOrg(ctx, org), org, projectID)
}

// checkDeliverables keeps only ids that are artifacts of the project (unknown ids are an error: the
// firm must know exactly what it shares).
func (s *Service) checkDeliverables(ctx context.Context, org, projectID string, ids []string) ([]string, error) {
	ids = dedupe(ids)
	if len(ids) > MaxDeliverables {
		return nil, invalid("at most %d deliverables", MaxDeliverables)
	}
	if len(ids) == 0 {
		return ids, nil
	}
	metas, err := s.deliverables(ctx, org, projectID)
	if err != nil {
		return nil, err
	}
	of := map[string]bool{}
	for _, m := range metas {
		of[m.ID] = true
	}
	for _, id := range ids {
		if !of[id] {
			return nil, invalid("deliverable %q is not part of this project", id)
		}
	}
	return ids, nil
}

// Candidates lists the deliverables of a project the firm may share (archived ones excluded).
func (s *Service) Candidates(ctx context.Context, projectID string) ([]Candidate, error) {
	org := s.org(ctx)
	if _, err := s.project(ctx, org, projectID); err != nil {
		return nil, err
	}
	metas, err := s.deliverables(ctx, org, projectID)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, m := range metas {
		if m.Status == artifacts.StatusArchived {
			continue
		}
		out = append(out, Candidate{ID: m.ID, Title: m.Title, Kind: string(m.Kind), Status: m.Status, Completed: completed(m), UpdatedAt: m.UpdatedAt})
	}
	return out, nil
}

// completed is the server rule for "a deliverable the client may see": a human approved it (or it was
// sent). Approved artifacts are also locked, so what the client sees cannot change behind the firm.
func completed(m artifacts.Meta) bool {
	return m.Status == artifacts.StatusApproved || m.Status == artifacts.StatusSent
}

// ListLinks lists the links of a project, newest first.
func (s *Service) ListLinks(ctx context.Context, projectID string) ([]LinkView, error) {
	org := s.org(ctx)
	if _, err := s.project(ctx, org, projectID); err != nil {
		return nil, err
	}
	ls, err := s.cfg.Store.ListLinks(ctx, org, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]LinkView, 0, len(ls))
	for _, l := range ls {
		out = append(out, s.view(l))
	}
	return out, nil
}

// CreateLink creates a share link for a project and returns the token once.
func (s *Service) CreateLink(ctx context.Context, actor, projectID string, in LinkInput) (CreatedLink, error) {
	org := s.org(ctx)
	if _, err := s.project(ctx, org, projectID); err != nil {
		return CreatedLink{}, err
	}
	label, title, err := cleanLabels(in)
	if err != nil {
		return CreatedLink{}, err
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > MaxExpiryDays {
		return CreatedLink{}, invalid("expires_in_days must be between 0 (never) and %d", MaxExpiryDays)
	}
	ids, err := s.checkDeliverables(ctx, org, projectID, in.DeliverableIDs)
	if err != nil {
		return CreatedLink{}, err
	}
	existing, err := s.cfg.Store.ListLinks(ctx, org, projectID)
	if err != nil {
		return CreatedLink{}, err
	}
	now := s.now()
	active := 0
	for _, l := range existing {
		if l.Status(now) == LinkActive {
			active++
		}
	}
	if active >= MaxActivePerProj {
		return CreatedLink{}, fmt.Errorf("%w: at most %d active links per project", domain.ErrConflict, MaxActivePerProj)
	}
	plain, hash, err := newToken()
	if err != nil {
		return CreatedLink{}, err
	}
	l := Link{ID: newID(), ProjectID: projectID, Label: label, Title: title, DeliverableIDs: ids, TokenHash: hash, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if in.ExpiresInDays > 0 {
		exp := now.Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
		l.ExpiresAt = &exp
	}
	if err := s.cfg.Store.CreateLink(ctx, org, l); err != nil {
		return CreatedLink{}, err
	}
	s.audit(ctx, org, actor, "portal_link.created", l.ID, map[string]any{"project_id": projectID, "expires_at": l.ExpiresAt, "deliverables": len(ids)})
	return CreatedLink{LinkView: s.view(l), Token: plain}, nil
}

func (s *Service) linkOf(ctx context.Context, org, projectID, id string) (Link, error) {
	l, err := s.cfg.Store.GetLink(ctx, org, id)
	if err != nil {
		return Link{}, err
	}
	if l.ProjectID != projectID {
		return Link{}, domain.ErrNotFound
	}
	return l, nil
}

// UpdateLink edits the label and the shared deliverables of a link (the expiry and the token never change).
func (s *Service) UpdateLink(ctx context.Context, actor, projectID, id string, in LinkInput) (LinkView, error) {
	org := s.org(ctx)
	l, err := s.linkOf(ctx, org, projectID, id)
	if err != nil {
		return LinkView{}, err
	}
	if l.RevokedAt != nil {
		return LinkView{}, fmt.Errorf("%w: the link is revoked", domain.ErrConflict)
	}
	label, title, err := cleanLabels(in)
	if err != nil {
		return LinkView{}, err
	}
	ids, err := s.checkDeliverables(ctx, org, projectID, in.DeliverableIDs)
	if err != nil {
		return LinkView{}, err
	}
	l.Label, l.Title, l.DeliverableIDs, l.UpdatedAt = label, title, ids, s.now()
	if err := s.cfg.Store.UpdateLink(ctx, org, l); err != nil {
		return LinkView{}, err
	}
	s.audit(ctx, org, actor, "portal_link.updated", l.ID, map[string]any{"project_id": projectID, "deliverables": len(ids)})
	return s.view(l), nil
}

// RevokeLink kills a link for good. Idempotent for an already revoked link.
func (s *Service) RevokeLink(ctx context.Context, actor, projectID, id string) (LinkView, error) {
	org := s.org(ctx)
	l, err := s.linkOf(ctx, org, projectID, id)
	if err != nil {
		return LinkView{}, err
	}
	if l.RevokedAt != nil {
		return s.view(l), nil
	}
	now := s.now()
	if err := s.cfg.Store.RevokeLink(ctx, org, id, actor, now); err != nil {
		return LinkView{}, err
	}
	l.RevokedAt, l.RevokedBy = &now, actor
	s.audit(ctx, org, actor, "portal_link.revoked", l.ID, map[string]any{"project_id": projectID})
	return s.view(l), nil
}

// resolve finds the active link of a token. Unknown, revoked and expired tokens all give ErrUnavailable.
func (s *Service) resolve(ctx context.Context, token string) (string, Link, error) {
	token = strings.TrimSpace(token)
	if !plausibleToken(token) {
		return "", Link{}, ErrUnavailable
	}
	org, l, err := s.cfg.Store.LinkByHash(ctx, HashToken(token))
	if errors.Is(err, domain.ErrNotFound) {
		return "", Link{}, ErrUnavailable
	}
	if err != nil {
		return "", Link{}, err
	}
	if l.Status(s.now()) != LinkActive {
		return "", Link{}, ErrUnavailable
	}
	return org, l, nil
}

// PublicView is what the client sees. See public.go for the allowlist.
func (s *Service) PublicView(ctx context.Context, token string) (View, error) {
	org, l, err := s.resolve(ctx, token)
	if err != nil {
		return View{}, err
	}
	d, err := s.project(ctx, org, l.ProjectID)
	if errors.Is(err, domain.ErrNotFound) {
		return View{}, ErrUnavailable
	}
	if err != nil {
		return View{}, err
	}
	var shared []artifacts.Meta
	if len(l.DeliverableIDs) > 0 {
		metas, err := s.deliverables(ctx, org, l.ProjectID)
		if err != nil {
			return View{}, err
		}
		want := map[string]bool{}
		for _, id := range l.DeliverableIDs {
			want[id] = true
		}
		for _, m := range metas {
			if want[m.ID] && completed(m) && m.ProjectID != nil && *m.ProjectID == l.ProjectID {
				shared = append(shared, m)
			}
		}
	}
	b, err := s.branding(ctx, org)
	if err != nil {
		return View{}, err
	}
	if b.DisplayName == "" && s.cfg.OrgName != nil {
		if n, err := s.cfg.OrgName(ctx, org); err == nil {
			b.DisplayName = n
		}
	}
	v := buildView(b, d, shared, l, s.now())
	now := s.now()
	if first, err := s.cfg.Store.MarkViewed(ctx, org, l.ID, now); err == nil && first {
		s.audit(ctx, org, "portal", "portal_link.first_viewed", l.ID, map[string]any{"project_id": l.ProjectID})
	}
	return v, nil
}

// PublicLogo returns the logo of the firm behind an active token.
func (s *Service) PublicLogo(ctx context.Context, token string) (string, []byte, error) {
	org, _, err := s.resolve(ctx, token)
	if err != nil {
		return "", nil, err
	}
	b, err := s.branding(ctx, org)
	if err != nil {
		return "", nil, err
	}
	if !b.HasLogo {
		return "", nil, ErrNoLogo
	}
	return b.LogoMime, b.Logo, nil
}
