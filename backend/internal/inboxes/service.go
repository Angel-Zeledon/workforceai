package inboxes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// Drive is what the inboxes need of the drive (internal/drive.Service implements it).
type Drive interface {
	Write(ctx context.Context, p drive.Principal, in drive.WriteInput) (drive.WriteResult, error)
	FileByPath(ctx context.Context, path string) (drive.File, error)
	FolderByPath(ctx context.Context, path string) (drive.Folder, error)
	Limits() drive.Limits
}

// Customers resolves the drive folder of a customer (B12). CustomerFolder fails with domain.ErrNotFound for an
// unknown customer and domain.ErrConflict for an archived one.
type Customers interface {
	CustomerFolder(ctx context.Context, org, customerID string) (string, error)
}

// Config wires the service.
type Config struct {
	Store Store
	Drive Drive
	// Customers is optional: without it an inbox cannot have a customer.
	Customers Customers
	OrgID     string // the default (demo) org
	// Gate refuses pushes while the organization is in read-only mode (ErrReadOnly) or its kill switch is set
	// (ErrKillSwitch). Any other error refuses as read-only (fail closed). nil = always open (tests).
	Gate func(ctx context.Context, org string) error
	// Audit records every write and every push (metadata only: never the token nor the content).
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	// Emit publishes a realtime event to the organization (nil = none).
	Emit func(ctx context.Context, org, eventType string, payload map[string]any)
	Now  func() time.Time
}

// Service is the data inbox use-case layer.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service { return &Service{cfg: cfg} }

// EventDataReceived is the realtime event of an accepted push (docs/architecture/03-eventos-realtime.md).
const EventDataReceived = "inbox.data_received"

// PushPath is the public endpoint of an inbox, relative to the API origin.
func PushPath(id string) string { return "/api/v1/inbox/" + id }

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

// ---- tokens and ids ----

func newToken() (plain, hash, hint string, err error) {
	b := make([]byte, tokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plain = TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, HashToken(plain), plain[len(plain)-4:], nil
}

// HashToken is the stored form of a token (SHA-256, hex).
func HashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// plausibleToken rejects garbage before any database work.
func plausibleToken(t string) bool {
	if !strings.HasPrefix(t, TokenPrefix) {
		return false
	}
	body := t[len(TokenPrefix):]
	if len(body) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ---- validation ----

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

func cleanName(v string) (string, error) {
	v = strings.Join(strings.FieldsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	v = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, v)
	if v == "" {
		return "", invalid("name is required")
	}
	if utf8.RuneCountInString(v) > MaxName {
		return "", invalid("name must be at most %d characters", MaxName)
	}
	return v, nil
}

// Slugify makes a folder-friendly slug of a name ("Pedidos ERP (diario)" -> "pedidos-erp-diario").
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // accents
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "inbox"
	}
	return out
}

func cleanKinds(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{KindJSON, KindCSV}, nil
	}
	seen := map[string]bool{}
	for _, k := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		switch k {
		case KindJSON, KindCSV, KindFile:
			seen[k] = true
		default:
			return nil, invalid("allowed: unknown content kind %q (json, csv, files)", k)
		}
	}
	out := []string{}
	for _, k := range AllKinds {
		if seen[k] {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *Service) driveMax() int64 {
	if s.cfg.Drive == nil {
		return drive.DefaultMaxFileBytes
	}
	return s.cfg.Drive.Limits().MaxFileBytes
}

func (s *Service) cleanLimits(maxBytes int64, rate int) (int64, int, error) {
	dmax := s.driveMax()
	if maxBytes == 0 {
		maxBytes = min(DefaultMaxBytes, dmax)
	}
	if maxBytes < MinMaxBytes || maxBytes > dmax {
		return 0, 0, invalid("max_bytes must be between %d and %d (DRIVE_MAX_FILE_MB)", MinMaxBytes, dmax)
	}
	if rate == 0 {
		rate = DefaultRatePerMin
	}
	if rate < 1 || rate > MaxRatePerMin {
		return 0, 0, invalid("rate_per_min must be between 1 and %d", MaxRatePerMin)
	}
	return maxBytes, rate, nil
}

// folderOf validates the destination of a new inbox. Without a customer it is absolute under /Shared or an
// existing /Projects/<project> folder; with a customer it is relative to the customer's folder.
func (s *Service) folderOf(ctx context.Context, org, folder, slug, customerID string) (stored, abs string, err error) {
	folder = strings.TrimSpace(folder)
	if customerID != "" {
		cpath, err := s.customerFolder(ctx, org, customerID)
		if err != nil {
			return "", "", err
		}
		rel := folder
		if drive.IsUnder(rel, cpath) {
			rel = strings.TrimPrefix(strings.TrimPrefix(rel, cpath), "/")
		} else if strings.HasPrefix(rel, "/") {
			return "", "", invalid("the folder of a customer inbox must be inside %s", cpath)
		}
		if rel == "" {
			rel = "Imports/" + slug
		}
		abs, err := drive.CleanPath(cpath + "/" + strings.Trim(rel, "/"))
		if err != nil {
			return "", "", invalid("folder: %v", err)
		}
		return strings.TrimPrefix(strings.TrimPrefix(abs, cpath), "/"), abs, nil
	}
	if folder == "" {
		folder = "/" + drive.RootShared + "/Imports/" + slug
	}
	segs, err := drive.Segments(folder)
	if err != nil {
		return "", "", invalid("folder: %v", err)
	}
	switch {
	case len(segs) >= 1 && segs[0] == drive.RootShared:
	case len(segs) >= 2 && segs[0] == drive.RootProjects:
		if s.cfg.Drive != nil {
			if _, err := s.cfg.Drive.FolderByPath(application.WithOrg(ctx, org), drive.JoinPath(segs[:2]...)); err != nil {
				return "", "", invalid("folder: the project folder %s does not exist", drive.JoinPath(segs[:2]...))
			}
		}
	default:
		return "", "", invalid("folder must be under /Shared or an existing /Projects/<project> folder (use customer_id for a customer's folder)")
	}
	abs = drive.JoinPath(segs...)
	return abs, abs, nil
}

func (s *Service) customerFolder(ctx context.Context, org, customerID string) (string, error) {
	if s.cfg.Customers == nil {
		return "", invalid("customers are not available")
	}
	p, err := s.cfg.Customers.CustomerFolder(application.WithOrg(ctx, org), org, customerID)
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrConflict):
		return "", fmt.Errorf("%w: %w", ErrCustomerArchived, err)
	case err != nil:
		return "", err
	case p == "":
		return "", fmt.Errorf("%w: the customer has no drive folder", ErrCustomerArchived)
	}
	return p, nil
}

// ---- admin ----

func (s *Service) view(ctx context.Context, org string, i Inbox) View {
	if i.Allowed == nil {
		i.Allowed = []string{}
	}
	v := View{Inbox: i, Status: i.Status(), PushPath: PushPath(i.ID), FolderPath: i.Folder}
	if i.CustomerID != "" {
		v.FolderPath = ""
		if p, err := s.customerFolder(ctx, org, i.CustomerID); err == nil {
			v.FolderPath = p + "/" + i.Folder
		}
	}
	return v
}

// List lists the inboxes of the caller's organization, newest first.
func (s *Service) List(ctx context.Context) ([]View, error) {
	org := s.org(ctx)
	is, err := s.cfg.Store.ListInboxes(ctx, org)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(is))
	for _, i := range is {
		out = append(out, s.view(ctx, org, i))
	}
	return out, nil
}

// Get returns one inbox.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	org := s.org(ctx)
	i, err := s.cfg.Store.GetInbox(ctx, org, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, org, i), nil
}

// Create makes an inbox and returns its token once.
func (s *Service) Create(ctx context.Context, actor string, in Input) (Created, error) {
	org := s.org(ctx)
	name, err := cleanName(in.Name)
	if err != nil {
		return Created{}, err
	}
	kinds, err := cleanKinds(in.Allowed)
	if err != nil {
		return Created{}, err
	}
	maxBytes, rate, err := s.cleanLimits(in.MaxBytes, in.RatePerMin)
	if err != nil {
		return Created{}, err
	}
	existing, err := s.cfg.Store.ListInboxes(ctx, org)
	if err != nil {
		return Created{}, err
	}
	active, slugs := 0, map[string]bool{}
	for _, e := range existing {
		if e.Status() == StatusActive {
			active++
		}
		slugs[e.Slug] = true
	}
	if active >= MaxActive {
		return Created{}, fmt.Errorf("%w: at most %d active inboxes per organization", domain.ErrConflict, MaxActive)
	}
	id := newID("ibx_")
	slug := Slugify(name)
	if slugs[slug] {
		slug += "-" + id[len(id)-4:]
	}
	customerID := strings.TrimSpace(in.CustomerID)
	stored, abs, err := s.folderOf(ctx, org, in.Folder, slug, customerID)
	if errors.Is(err, ErrCustomerArchived) {
		return Created{}, invalid("customer_id: the customer does not exist or is archived")
	}
	if err != nil {
		return Created{}, err
	}
	plain, hash, hint, err := newToken()
	if err != nil {
		return Created{}, err
	}
	now := s.now()
	i := Inbox{ID: id, Name: name, Slug: slug, Folder: stored, CustomerID: customerID, Allowed: kinds, MaxBytes: maxBytes, RatePerMin: rate,
		TokenHash: hash, TokenHint: hint, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := s.cfg.Store.CreateInbox(ctx, org, i); err != nil {
		return Created{}, err
	}
	s.audit(ctx, org, actor, "inbox.created", id, map[string]any{"name": name, "folder": abs, "customer_id": customerID, "allowed": kinds,
		"max_bytes": maxBytes, "rate_per_min": rate})
	v := s.view(ctx, org, i)
	return Created{View: v, Token: plain}, nil
}

// Update edits name, allowed kinds and limits (the destination and the token never change here).
func (s *Service) Update(ctx context.Context, actor, id string, in Input) (View, error) {
	org := s.org(ctx)
	i, err := s.cfg.Store.GetInbox(ctx, org, id)
	if err != nil {
		return View{}, err
	}
	if i.RevokedAt != nil {
		return View{}, fmt.Errorf("%w: the inbox is revoked", domain.ErrConflict)
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return View{}, err
	}
	kinds, err := cleanKinds(in.Allowed)
	if err != nil {
		return View{}, err
	}
	maxBytes, rate, err := s.cleanLimits(in.MaxBytes, in.RatePerMin)
	if err != nil {
		return View{}, err
	}
	i.Name, i.Allowed, i.MaxBytes, i.RatePerMin, i.UpdatedAt = name, kinds, maxBytes, rate, s.now()
	if err := s.cfg.Store.UpdateInbox(ctx, org, i); err != nil {
		return View{}, err
	}
	s.audit(ctx, org, actor, "inbox.updated", id, map[string]any{"name": name, "allowed": kinds, "max_bytes": maxBytes, "rate_per_min": rate})
	return s.view(ctx, org, i), nil
}

// Rotate replaces the token: the old one stops working at once. The new token is returned once.
func (s *Service) Rotate(ctx context.Context, actor, id string) (Created, error) {
	org := s.org(ctx)
	i, err := s.cfg.Store.GetInbox(ctx, org, id)
	if err != nil {
		return Created{}, err
	}
	if i.RevokedAt != nil {
		return Created{}, fmt.Errorf("%w: the inbox is revoked", domain.ErrConflict)
	}
	plain, hash, hint, err := newToken()
	if err != nil {
		return Created{}, err
	}
	now := s.now()
	if err := s.cfg.Store.SetToken(ctx, org, id, hash, hint, now); err != nil {
		return Created{}, err
	}
	i.TokenHash, i.TokenHint, i.RotatedAt, i.UpdatedAt = hash, hint, &now, now
	s.audit(ctx, org, actor, "inbox.token_rotated", id, nil)
	return Created{View: s.view(ctx, org, i), Token: plain}, nil
}

// Revoke kills an inbox for good (its files stay in the drive). Idempotent.
func (s *Service) Revoke(ctx context.Context, actor, id string) (View, error) {
	org := s.org(ctx)
	i, err := s.cfg.Store.GetInbox(ctx, org, id)
	if err != nil {
		return View{}, err
	}
	if i.RevokedAt != nil {
		return s.view(ctx, org, i), nil
	}
	now := s.now()
	if err := s.cfg.Store.RevokeInbox(ctx, org, id, actor, now); err != nil {
		return View{}, err
	}
	i.RevokedAt, i.RevokedBy = &now, actor
	s.audit(ctx, org, actor, "inbox.revoked", id, nil)
	return s.view(ctx, org, i), nil
}

// Pushes lists the last pushes of an inbox (metadata only).
func (s *Service) Pushes(ctx context.Context, id string, limit int) ([]Push, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.GetInbox(ctx, org, id); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultPushesListed
	}
	limit = min(limit, MaxPushesListed)
	ps, err := s.cfg.Store.Pushes(ctx, org, id, limit)
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].Files == nil {
			ps[i].Files = []PushedFile{}
		}
	}
	return ps, nil
}

// ---- public push ----

// Authenticate resolves the inbox of a token. Unknown, revoked and mismatched tokens all give ErrUnauthorized.
func (s *Service) Authenticate(ctx context.Context, inboxID, token string) (string, Inbox, error) {
	token = strings.TrimSpace(token)
	if !plausibleToken(token) {
		return "", Inbox{}, ErrUnauthorized
	}
	org, i, err := s.cfg.Store.InboxByHash(ctx, HashToken(token))
	if errors.Is(err, domain.ErrNotFound) {
		return "", Inbox{}, ErrUnauthorized
	}
	if err != nil {
		return "", Inbox{}, err
	}
	if i.ID != inboxID || i.RevokedAt != nil {
		return "", Inbox{}, ErrUnauthorized
	}
	return org, i, nil
}

// PushInput is one push request.
type PushInput struct {
	ContentType string
	FileName    string // X-Filename (raw bodies only)
	IdemKey     string // X-Idempotency-Key ("" = none)
	Body        io.Reader
}

// MaxBody is the cap the HTTP layer puts on the body of a push to this inbox.
func (s *Service) MaxBody(i Inbox) int64 { return s.payloadCap(i) + MultipartOverhead }

func (s *Service) payloadCap(i Inbox) int64 {
	return min(i.MaxBytes, s.driveMax())
}

func (s *Service) gate(ctx context.Context, org string) error {
	if s.cfg.Gate == nil {
		return nil
	}
	err := s.cfg.Gate(ctx, org)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrKillSwitch):
		return ErrKillSwitch
	}
	return ErrReadOnly // read-only, or controls unreadable (fail closed)
}

func idemHash(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil
	}
	if len(key) > MaxIdempotencyKey {
		return "", bad("X-Idempotency-Key is longer than %d characters", MaxIdempotencyKey)
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return "", bad("X-Idempotency-Key must be printable ASCII without spaces")
		}
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]), nil
}

func replay(p Push) Result {
	files := p.Files
	if files == nil {
		files = []PushedFile{}
	}
	return Result{PushID: p.ID, InboxID: p.InboxID, ReceivedAt: p.ReceivedAt, Size: p.Size, Files: files, Replayed: true}
}

// Push stores the body of a push in the inbox's folder. The caller authenticated the inbox (Authenticate) and
// applied the rate limit. The content is stored as is: it is data for people and agents, never instructions.
func (s *Service) Push(ctx context.Context, org string, ibx Inbox, in PushInput) (Result, error) {
	ctx = application.WithOrg(ctx, org)
	if err := s.gate(ctx, org); err != nil {
		s.refused(ctx, org, ibx, err)
		return Result{}, err
	}
	key, err := idemHash(in.IdemKey)
	if err != nil {
		return Result{}, err
	}
	if key != "" {
		if p, err := s.cfg.Store.PushByKey(ctx, org, ibx.ID, key); err == nil {
			if p.Status != PushDone {
				return Result{}, ErrInProgress
			}
			return replay(p), nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return Result{}, err
		}
	}
	parsed, err := Parse(in.ContentType, in.FileName, in.Body, s.payloadCap(ibx))
	if err != nil {
		return Result{}, err
	}
	for _, p := range parsed.Parts {
		if !ibx.Allows(p.Kind) {
			return Result{}, fmt.Errorf("%w: this inbox does not accept %s content", ErrKindNotAllowed, p.Kind)
		}
	}
	now := s.now()
	push := Push{ID: newID("psh_"), InboxID: ibx.ID, IdemKey: key, Status: PushPending, Size: parsed.Size, ReceivedAt: now}
	if err := s.cfg.Store.ReservePush(ctx, org, push); err != nil {
		if errors.Is(err, domain.ErrConflict) && key != "" { // lost a race with the same key
			if p, e2 := s.cfg.Store.PushByKey(ctx, org, ibx.ID, key); e2 == nil && p.Status == PushDone {
				return replay(p), nil
			}
			return Result{}, ErrInProgress
		}
		return Result{}, err
	}
	files, err := s.write(ctx, org, ibx, push, parsed.Parts)
	if err != nil {
		_ = s.cfg.Store.ReleasePush(ctx, org, ibx.ID, push.ID)
		if errors.Is(err, drive.ErrReadOnly) {
			err = ErrReadOnly
		}
		s.refused(ctx, org, ibx, err)
		return Result{}, err
	}
	push.Files, push.Status = files, PushDone
	if err := s.cfg.Store.CompletePush(ctx, org, push); err != nil {
		return Result{}, err
	}
	meta := make([]map[string]any, 0, len(files))
	for _, f := range files {
		meta = append(meta, map[string]any{"file_id": f.FileID, "kind": f.Kind, "mime": f.Mime, "size": f.Size, "version": f.Version, "created": f.Created})
	}
	s.audit(ctx, org, "inbox:"+ibx.ID, "inbox.push_received", ibx.ID, map[string]any{"push_id": push.ID, "size": parsed.Size, "files": meta,
		"customer_id": ibx.CustomerID, "idempotency_key": key != "", "ignored_fields": parsed.IgnoredFields})
	if s.cfg.Emit != nil {
		ev := make([]map[string]any, 0, len(files))
		for _, f := range files {
			ev = append(ev, map[string]any{"file_id": f.FileID, "path": f.Path, "name": f.Name, "size": f.Size, "mime": f.Mime, "version": f.Version})
		}
		s.cfg.Emit(ctx, org, EventDataReceived, map[string]any{"inbox_id": ibx.ID, "inbox_name": ibx.Name, "push_id": push.ID, "customer_id": ibx.CustomerID,
			"size": parsed.Size, "files": ev, "received_at": now})
	}
	return Result{PushID: push.ID, InboxID: ibx.ID, ReceivedAt: now, Size: parsed.Size, Files: files, IgnoredFields: parsed.IgnoredFields}, nil
}

// refused audits a push an authenticated caller could not make for a reason the admin should see.
func (s *Service) refused(ctx context.Context, org string, ibx Inbox, err error) {
	code := ""
	switch {
	case errors.Is(err, ErrReadOnly):
		code = "read_only_mode"
	case errors.Is(err, ErrKillSwitch):
		code = "kill_switch_active"
	case errors.Is(err, ErrCustomerArchived):
		code = "customer_unavailable"
	case errors.Is(err, drive.ErrQuota):
		code = "quota_exceeded"
	default:
		return
	}
	s.audit(ctx, org, "inbox:"+ibx.ID, "inbox.push_refused", ibx.ID, map[string]any{"code": code})
}

// destination is the absolute folder of a push (the customer's folder resolved now).
func (s *Service) destination(ctx context.Context, org string, ibx Inbox) (string, error) {
	if ibx.CustomerID == "" {
		return ibx.Folder, nil
	}
	cpath, err := s.customerFolder(ctx, org, ibx.CustomerID)
	if err != nil {
		return "", err
	}
	return drive.CleanPath(cpath + "/" + ibx.Folder)
}

func (s *Service) write(ctx context.Context, org string, ibx Inbox, push Push, parts []Part) ([]PushedFile, error) {
	folder, err := s.destination(ctx, org, ibx)
	if err != nil {
		return nil, err
	}
	me := drive.InboxP(ibx.ID)
	short := push.ID[len(push.ID)-6:]
	out := make([]PushedFile, 0, len(parts))
	for n, p := range parts {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("%s-%s-%s%s", ibx.Slug, push.ReceivedAt.Format("20060102-150405"), short, p.Ext)
			if len(parts) > 1 {
				name = fmt.Sprintf("%s-%s-%s-%d%s", ibx.Slug, push.ReceivedAt.Format("20060102-150405"), short, n+1, p.Ext)
			}
		}
		target, err := s.freeName(ctx, folder, name, short, me)
		if err != nil {
			return nil, err
		}
		res, err := s.cfg.Drive.Write(ctx, me, drive.WriteInput{Path: target, Data: p.Data, Source: "inbox",
			Message: "pushed to the data inbox " + ibx.Name})
		if err != nil {
			return nil, err
		}
		out = append(out, PushedFile{FileID: res.File.ID, Path: res.File.Path, Name: res.File.Name, Version: res.Version.Version, Size: res.File.Size,
			Mime: res.File.Mime, Kind: p.Kind, Created: res.Created, Unchanged: res.Unchanged})
	}
	return out, nil
}

// freeName returns the path to write: the name itself when it is free or a file this inbox owns (a new
// version of it), otherwise the name with the push suffix ("orders-a1b2c3.csv"). A file a person or an agent
// wrote, or touched, is never written over.
func (s *Service) freeName(ctx context.Context, folder, name, short string, me drive.Principal) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	cands := []string{name, stem + "-" + short + ext}
	for _, c := range cands {
		if utf8.RuneCountInString(c) > drive.MaxNameRunes {
			c = SanitizeName(string([]rune(stem)[:max(1, drive.MaxNameRunes-utf8.RuneCountInString(ext)-8)]) + "-" + short + ext)
		}
		p := folder + "/" + c
		f, err := s.cfg.Drive.FileByPath(ctx, p)
		if errors.Is(err, domain.ErrNotFound) {
			return p, nil
		}
		if err != nil {
			return "", err
		}
		if f.DeletedAt == nil && f.CreatedBy == me && f.UpdatedBy == me {
			return p, nil
		}
	}
	return folder + "/" + stem + "-" + newID("")[:12] + ext, nil
}
