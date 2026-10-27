package linkimports

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// Drive is what the imports need of the drive (internal/drive.Service implements it).
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

// Sealer seals the auth header with the organization's data key (internal/vault.Vault implements it).
type Sealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

// Config wires the service.
type Config struct {
	Store   Store
	Drive   Drive
	Fetcher *Fetcher
	// Customers is optional: without it an import cannot have a customer.
	Customers Customers
	// Sealer is optional: without it (no CONNECTIONS_KEK) an import cannot carry an auth header.
	Sealer Sealer
	OrgID  string // the default (demo) org
	// Gate refuses runs while the organization is in read-only mode (ErrReadOnly) or its kill switch is set
	// (ErrKillSwitch). Any other error refuses as read-only (fail closed). nil = always open (tests).
	Gate func(ctx context.Context, org string) error
	// Audit records every write and every run (metadata only: never the content, the query string nor the header).
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	// Emit publishes a realtime event to the organization (nil = none).
	Emit func(ctx context.Context, org, eventType string, payload map[string]any)
	Now  func() time.Time
	Log  *slog.Logger
}

// Service is the link import use-case layer.
type Service struct {
	cfg     Config
	running sync.Map // org|id -> struct{}: one run per import at a time
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Fetcher == nil {
		cfg.Fetcher = &Fetcher{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

// EventDataLanded is the realtime event of a run that wrote new data (docs/architecture/03-eventos-realtime.md).
const EventDataLanded = "link_import.data_landed"

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

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

// ---- validation ----

func cleanText(v string, field string, maxRunes int, required bool) (string, error) {
	v = strings.Join(strings.FieldsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	v = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, v)
	if v == "" && required {
		return "", invalid("%s is required", field)
	}
	if utf8.RuneCountInString(v) > maxRunes {
		return "", invalid("%s must be at most %d characters", field, maxRunes)
	}
	return v, nil
}

// Slugify makes a file- and folder-friendly slug of a name ("Ventas (diario)" -> "ventas-diario").
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
		return "import"
	}
	return out
}

func cleanStem(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if i := strings.LastIndexByte(v, '.'); i > 0 && allowedExt(strings.ToLower(v[i:])) {
		v = v[:i] // the extension follows the downloaded type
	}
	if strings.ContainsAny(v, `/\`) || strings.HasPrefix(v, ".") {
		return "", invalid("file_stem must be a plain name without folders")
	}
	v, err := cleanText(v, "file_stem", MaxFileStem, false)
	if err != nil {
		return "", err
	}
	if _, err := drive.CleanName(v + ".csv"); err != nil {
		return "", invalid("file_stem: %v", err)
	}
	return v, nil
}

func cleanSchedule(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ScheduleOnce, nil
	}
	for _, s := range AllSchedules {
		if s == v {
			return v, nil
		}
	}
	return "", invalid("schedule must be one of once, hourly, daily, weekly")
}

// forbiddenHeaders are names the transport owns or that would change the request beyond authentication.
var forbiddenHeaders = map[string]bool{"host": true, "content-length": true, "transfer-encoding": true, "connection": true, "upgrade": true,
	"te": true, "trailer": true, "keep-alive": true, "proxy-authorization": true, "proxy-connection": true, "expect": true,
	"user-agent": true, "accept-encoding": true, "range": true, "forwarded": true, "x-forwarded-for": true, "x-forwarded-host": true}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r > 0x7e || r <= 0x20 || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, r) {
			return false
		}
	}
	return true
}

func cleanHeaderName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if len(n) > MaxHeaderName || !isToken(n) {
		return "", invalid("header.name must be a valid HTTP header name (at most %d characters)", MaxHeaderName)
	}
	if forbiddenHeaders[strings.ToLower(n)] || strings.HasPrefix(strings.ToLower(n), "sec-") || strings.HasPrefix(strings.ToLower(n), "proxy-") {
		return "", invalid("header.name %q cannot be set", n)
	}
	return http.CanonicalHeaderKey(n), nil
}

func cleanHeaderValue(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > MaxHeaderValue {
		return "", invalid("header.value must have between 1 and %d characters", MaxHeaderValue)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", invalid("header.value cannot contain control characters")
		}
	}
	return v, nil
}

func sealPurpose(id string) string { return "link_import_header:" + id }

func hint(v string) string {
	if len(v) <= 4 {
		return ""
	}
	return v[len(v)-4:]
}

// applyHeader sets, keeps or clears the header of li from in (nil = keep).
func (s *Service) applyHeader(ctx context.Context, org string, li *LinkImport, in *HeaderInput) (changed bool, err error) {
	if in == nil {
		return false, nil
	}
	if in.Clear || (strings.TrimSpace(in.Name) == "" && strings.TrimSpace(in.Value) == "") {
		changed = li.HeaderName != "" || len(li.HeaderSealed) > 0
		li.HeaderName, li.HeaderSealed, li.HeaderHint = "", nil, ""
		return changed, nil
	}
	name, err := cleanHeaderName(in.Name)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(in.Value) == "" {
		if len(li.HeaderSealed) == 0 {
			return false, invalid("header.value is required")
		}
		changed = li.HeaderName != name
		li.HeaderName = name // rename only: the sealed value stays
		return changed, nil
	}
	val, err := cleanHeaderValue(in.Value)
	if err != nil {
		return false, err
	}
	if s.cfg.Sealer == nil || !s.cfg.Sealer.Enabled() {
		return false, invalid("an auth header needs the credential vault (CONNECTIONS_KEK) on this server")
	}
	sealed, err := s.cfg.Sealer.Seal(ctx, org, sealPurpose(li.ID), []byte(val))
	if err != nil {
		return false, err
	}
	li.HeaderName, li.HeaderSealed, li.HeaderHint = name, sealed, hint(val)
	return true, nil
}

func (s *Service) driveMax() int64 {
	if s.cfg.Drive == nil {
		return drive.DefaultMaxFileBytes
	}
	return s.cfg.Drive.Limits().MaxFileBytes
}

// folderOf validates the destination of a new import. Without a customer it is absolute under /Shared or an
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
			return "", "", invalid("the folder of a customer import must be inside %s", cpath)
		}
		if rel == "" {
			rel = "Imports/Links/" + slug
		}
		abs, err := drive.CleanPath(cpath + "/" + strings.Trim(rel, "/"))
		if err != nil || !drive.IsUnder(abs, cpath) {
			return "", "", invalid("folder: the path is not valid inside %s", cpath)
		}
		return strings.TrimPrefix(strings.TrimPrefix(abs, cpath), "/"), abs, nil
	}
	if folder == "" {
		folder = "/" + drive.RootShared + "/Imports/Links/" + slug
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

func (s *Service) checkURL(raw string) (string, error) {
	u, err := s.cfg.Fetcher.CheckURL(raw)
	if err != nil {
		code, _ := CodeOf(err)
		msg := map[string]string{
			"url_scheme_not_allowed":      "the link must start with https://",
			"address_not_allowed":         "the link points to a private or reserved address",
			"host_not_allowed":            "the link's host is internal or not valid",
			"url_port_not_allowed":        "the link's port is not allowed (80, 443, 8080, 8443)",
			"url_credentials_not_allowed": "the link cannot carry a user or password; use the auth header",
		}[code]
		if msg == "" {
			msg = "the link is not a valid URL"
		}
		return "", fmt.Errorf("%w: url: %s (%s)", domain.ErrInvalid, msg, code)
	}
	return u.String(), nil
}

// ---- admin ----

func hostOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			rest = rest[:j]
		}
		return strings.ToLower(rest)
	}
	return ""
}

func (s *Service) view(ctx context.Context, org string, li LinkImport) View {
	v := View{LinkImport: li, HeaderSet: len(li.HeaderSealed) > 0, Host: hostOf(li.URL), FolderPath: li.Folder}
	v.HeaderSealed = nil
	if li.CustomerID != "" {
		v.FolderPath = ""
		if p, err := s.customerFolder(ctx, org, li.CustomerID); err == nil {
			v.FolderPath = p + "/" + li.Folder
		}
	}
	return v
}

// List lists the imports of the caller's organization, newest first.
func (s *Service) List(ctx context.Context) ([]View, error) {
	org := s.org(ctx)
	ls, err := s.cfg.Store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(ls))
	for _, li := range ls {
		out = append(out, s.view(ctx, org, li))
	}
	return out, nil
}

// Get returns one import.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	org := s.org(ctx)
	li, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, org, li), nil
}

// Create saves an import. A scheduled import is due at once (the scheduler picks it up within a minute); a
// "once" import runs when a person presses "run now" (the UI does it right after creating it).
func (s *Service) Create(ctx context.Context, actor string, in Input) (View, error) {
	org := s.org(ctx)
	name, err := cleanText(in.Name, "name", MaxName, true)
	if err != nil {
		return View{}, err
	}
	link, err := s.checkURL(in.URL)
	if err != nil {
		return View{}, err
	}
	stem, err := cleanStem(in.FileStem)
	if err != nil {
		return View{}, err
	}
	sched, err := cleanSchedule(in.Schedule)
	if err != nil {
		return View{}, err
	}
	existing, err := s.cfg.Store.List(ctx, org)
	if err != nil {
		return View{}, err
	}
	if len(existing) >= MaxActive {
		return View{}, fmt.Errorf("%w: at most %d link imports per organization", domain.ErrConflict, MaxActive)
	}
	slugs := map[string]bool{}
	for _, e := range existing {
		slugs[e.Slug] = true
	}
	id := newID("lnk_")
	slug := Slugify(name)
	if slugs[slug] {
		slug += "-" + id[len(id)-4:]
	}
	customerID := strings.TrimSpace(in.CustomerID)
	stored, abs, err := s.folderOf(ctx, org, in.Folder, slug, customerID)
	if errors.Is(err, ErrCustomerArchived) {
		return View{}, invalid("customer_id: the customer does not exist or is archived")
	}
	if err != nil {
		return View{}, err
	}
	now := s.now()
	li := LinkImport{ID: id, Name: name, Slug: slug, URL: link, Folder: stored, CustomerID: customerID, FileStem: stem, Schedule: sched,
		Enabled: in.Enabled == nil || *in.Enabled, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if _, err := s.applyHeader(ctx, org, &li, in.Header); err != nil {
		return View{}, err
	}
	if sched != ScheduleOnce {
		li.NextRunAt = &now
	}
	if err := s.cfg.Store.Create(ctx, org, li); err != nil {
		return View{}, err
	}
	s.audit(ctx, org, actor, "link_import.created", id, map[string]any{"name": name, "host": hostOf(link), "folder": abs,
		"customer_id": customerID, "schedule": sched, "enabled": li.Enabled, "header_set": len(li.HeaderSealed) > 0})
	return s.view(ctx, org, li), nil
}

// Update edits name, link, file name, schedule, enabled and the header (the destination never changes here).
// Fields left empty keep their value.
func (s *Service) Update(ctx context.Context, actor, id string, in Input) (View, error) {
	org := s.org(ctx)
	li, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return View{}, err
	}
	if strings.TrimSpace(in.Name) != "" {
		if li.Name, err = cleanText(in.Name, "name", MaxName, true); err != nil {
			return View{}, err
		}
	}
	urlChanged := false
	if strings.TrimSpace(in.URL) != "" {
		link, err := s.checkURL(in.URL)
		if err != nil {
			return View{}, err
		}
		urlChanged = link != li.URL
		li.URL = link
	}
	if in.FileStem != "" {
		if li.FileStem, err = cleanStem(in.FileStem); err != nil {
			return View{}, err
		}
	}
	prevSched, prevEnabled := li.Schedule, li.Enabled
	if strings.TrimSpace(in.Schedule) != "" {
		if li.Schedule, err = cleanSchedule(in.Schedule); err != nil {
			return View{}, err
		}
	}
	if in.Enabled != nil {
		li.Enabled = *in.Enabled
	}
	headerChanged, err := s.applyHeader(ctx, org, &li, in.Header)
	if err != nil {
		return View{}, err
	}
	now := s.now()
	if li.Schedule == ScheduleOnce {
		li.NextRunAt = nil
	} else if li.Schedule != prevSched || (li.Enabled && !prevEnabled) || li.NextRunAt == nil {
		next := now
		if li.LastRunAt != nil {
			next = li.LastRunAt.Add(Interval(li.Schedule))
			if next.Before(now) {
				next = now
			}
		}
		li.NextRunAt = &next
	}
	li.UpdatedAt = now
	if err := s.cfg.Store.Update(ctx, org, li); err != nil {
		return View{}, err
	}
	s.audit(ctx, org, actor, "link_import.updated", id, map[string]any{"name": li.Name, "host": hostOf(li.URL), "url_changed": urlChanged,
		"schedule": li.Schedule, "enabled": li.Enabled, "header_changed": headerChanged, "header_set": len(li.HeaderSealed) > 0})
	return s.view(ctx, org, li), nil
}

// Delete removes an import and its run history. The files it wrote stay in the drive; the sealed header is
// gone with the row.
func (s *Service) Delete(ctx context.Context, actor, id string) error {
	org := s.org(ctx)
	li, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.Delete(ctx, org, id); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "link_import.deleted", id, map[string]any{"name": li.Name, "host": hostOf(li.URL)})
	return nil
}

// Runs lists the last runs of an import (metadata only).
func (s *Service) Runs(ctx context.Context, id string, limit int) ([]Run, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultRunsShown
	}
	limit = min(limit, MaxRunsShown)
	rs, err := s.cfg.Store.Runs(ctx, org, id, limit)
	if rs == nil {
		rs = []Run{}
	}
	return rs, err
}

// ---- runs ----

// RunNow runs an import at once on behalf of a person. A refused or failed run is still a recorded run: the
// result carries its status and code.
func (s *Service) RunNow(ctx context.Context, actor, id string) (Run, error) {
	org := s.org(ctx)
	li, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Run{}, err
	}
	return s.run(ctx, org, li, TriggerManual, actor)
}

func (s *Service) gate(ctx context.Context, org string) string {
	if s.cfg.Gate == nil {
		return ""
	}
	err := s.cfg.Gate(ctx, org)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrKillSwitch):
		return CodeKillSwitch
	}
	return CodeReadOnly // read-only, or controls unreadable (fail closed)
}

func (s *Service) lock(org, id string) bool {
	_, busy := s.running.LoadOrStore(org+"|"+id, struct{}{})
	return !busy
}

func (s *Service) unlock(org, id string) { s.running.Delete(org + "|" + id) }

// run performs one attempt and records it. Only a busy import returns an error without a recorded run.
func (s *Service) run(ctx context.Context, org string, li LinkImport, trigger, actor string) (Run, error) {
	ctx = application.WithOrg(ctx, org)
	if !s.lock(org, li.ID) {
		return Run{}, fmt.Errorf("%w: this import is already running", domain.ErrConflict)
	}
	defer s.unlock(org, li.ID)
	start := s.now()
	r := Run{ID: newID("lnr_"), ImportID: li.ID, Trigger: trigger, Actor: actor, StartedAt: start}
	if code := s.gate(ctx, org); code != "" {
		r.Status, r.Code = RunSkipped, code
	} else {
		s.attempt(ctx, org, li, &r)
	}
	r.DurationMS = s.now().Sub(start).Milliseconds()
	if err := s.cfg.Store.RecordRun(ctx, org, r); err != nil {
		return r, err
	}
	d := map[string]any{"run_id": r.ID, "trigger": trigger, "status": r.Status, "code": r.Code, "host": hostOf(li.URL),
		"size": r.Size, "file_id": r.FileID, "version": r.Version, "customer_id": li.CustomerID, "duration_ms": r.DurationMS}
	if r.HTTPStatus != 0 {
		d["http_status"] = r.HTTPStatus
	}
	if r.Hash != "" {
		d["sha256"] = r.Hash
	}
	auditActor := actor
	if auditActor == "" {
		auditActor = "link_import:" + li.ID
	}
	action := "link_import.run"
	if r.Status == RunSkipped {
		action = "link_import.run_skipped"
	}
	s.audit(ctx, org, auditActor, action, li.ID, d)
	if r.Status == RunOK && s.cfg.Emit != nil {
		s.cfg.Emit(ctx, org, EventDataLanded, map[string]any{"import_id": li.ID, "import_name": li.Name, "run_id": r.ID,
			"customer_id": li.CustomerID, "trigger": trigger, "fetched_at": start,
			"file": map[string]any{"file_id": r.FileID, "path": r.Path, "name": baseName(r.Path), "size": r.Size, "version": r.Version}})
	}
	return r, nil
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Service) fail(r *Run, code string) {
	r.Status, r.Code = RunError, code
}

// attempt downloads and stores; it fills r with the outcome.
func (s *Service) attempt(ctx context.Context, org string, li LinkImport, r *Run) {
	folder, err := s.destination(ctx, org, li)
	if err != nil {
		if errors.Is(err, ErrCustomerArchived) {
			s.fail(r, CodeCustomerUnavailable)
		} else {
			s.fail(r, CodeDrive)
		}
		return
	}
	var h *Header
	if len(li.HeaderSealed) > 0 {
		if s.cfg.Sealer == nil || !s.cfg.Sealer.Enabled() {
			s.fail(r, CodeHeaderUnavailable)
			return
		}
		plain, err := s.cfg.Sealer.Unseal(ctx, org, sealPurpose(li.ID), li.HeaderSealed)
		if err != nil {
			s.fail(r, CodeHeaderUnavailable)
			return
		}
		h = &Header{Name: li.HeaderName, Value: string(plain)}
		defer func() {
			for i := range plain {
				plain[i] = 0
			}
		}()
	}
	dl, err := s.cfg.Fetcher.Fetch(ctx, li.URL, h, s.driveMax())
	if err != nil {
		code, st := CodeOf(err)
		s.fail(r, code)
		r.HTTPStatus = st
		return
	}
	sum := sha256.Sum256(dl.Data)
	r.Hash, r.Size, r.Mime = hex.EncodeToString(sum[:]), int64(len(dl.Data)), dl.Mime
	stem := li.FileStem
	if stem == "" {
		stem = li.Slug
	}
	me := drive.LinkImportP(li.ID)
	target, err := s.freeName(ctx, folder, stem+dl.Ext, li.ID[len(li.ID)-6:], me)
	if err != nil {
		s.fail(r, CodeDrive)
		return
	}
	// Same content as last time, and that file still holds it: nothing to write.
	if r.Hash == li.LastHash && li.LastPath == target {
		if f, err := s.cfg.Drive.FileByPath(ctx, target); err == nil && f.DeletedAt == nil && f.SHA256 == r.Hash {
			r.Status, r.FileID, r.Path, r.Version = RunUnchanged, f.ID, f.Path, f.CurrentVersion
			return
		}
	}
	res, err := s.cfg.Drive.Write(ctx, me, drive.WriteInput{Path: target, Data: dl.Data, Source: "link_import",
		Message: "imported from the link " + li.Name})
	if err != nil {
		s.fail(r, driveCode(err))
		return
	}
	r.FileID, r.Path, r.Version, r.Mime = res.File.ID, res.File.Path, res.Version.Version, res.File.Mime
	if res.Unchanged {
		r.Status = RunUnchanged
		return
	}
	r.Status = RunOK
}

func driveCode(err error) string {
	var de *drive.Error
	switch {
	case errors.Is(err, drive.ErrReadOnly):
		return CodeReadOnly
	case errors.Is(err, drive.ErrQuota):
		return CodeQuota
	case errors.Is(err, drive.ErrUnsupportedType):
		return CodeTypeNotAllowed
	case errors.As(err, &de) && de.Code == "content_mismatch":
		return CodeContentMismatch
	case errors.Is(err, drive.ErrTooLarge):
		return CodeTooLarge
	}
	return CodeDrive
}

// destination is the absolute folder of a run (the customer's folder resolved now).
func (s *Service) destination(ctx context.Context, org string, li LinkImport) (string, error) {
	if li.CustomerID == "" {
		return li.Folder, nil
	}
	cpath, err := s.customerFolder(ctx, org, li.CustomerID)
	if err != nil {
		return "", err
	}
	abs, err := drive.CleanPath(cpath + "/" + li.Folder)
	if err != nil || !drive.IsUnder(abs, cpath) {
		return "", fmt.Errorf("%w: the folder left the customer's folder", ErrCustomerArchived)
	}
	return abs, nil
}

// freeName returns the path to write: the name itself when it is free or a file this import owns (a new version
// of it), otherwise the name with the import suffix ("ventas-1a2b3c.csv"). A file a person or an agent wrote, or
// touched, is never written over.
func (s *Service) freeName(ctx context.Context, folder, name, short string, me drive.Principal) (string, error) {
	ext := ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		ext = name[i:]
	}
	stem := strings.TrimSuffix(name, ext)
	for _, c := range []string{name, stem + "-" + short + ext, stem + "-" + short + "-2" + ext, stem + "-" + short + "-3" + ext} {
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
	return "", fmt.Errorf("%w: no free file name", domain.ErrConflict)
}

// ---- scheduler ----

// RunDue runs every scheduled import that is due in the given organizations. A period is claimed before it runs
// (compare-and-set on next_run_at), so two schedulers never run it twice; a skipped run (read-only, kill switch)
// still consumes its period and records why.
func (s *Service) RunDue(ctx context.Context, orgs []string) {
	now := s.now()
	for _, org := range orgs {
		octx := application.WithOrg(ctx, org)
		due, err := s.cfg.Store.Due(octx, org, now)
		if err != nil {
			s.cfg.Log.Warn("link imports: due", "org", org, "err", err)
			continue
		}
		for _, li := range due {
			if ctx.Err() != nil {
				return
			}
			if !li.Enabled || li.NextRunAt == nil || Interval(li.Schedule) == 0 {
				continue
			}
			next := now.Add(Interval(li.Schedule))
			ok, err := s.cfg.Store.Claim(octx, org, li.ID, *li.NextRunAt, next)
			if err != nil || !ok {
				continue
			}
			if _, err := s.run(octx, org, li, TriggerSchedule, ""); err != nil && !errors.Is(err, domain.ErrConflict) {
				s.cfg.Log.Warn("link imports: run", "org", org, "import", li.ID, "err", err)
			}
		}
	}
}

// Start ticks every interval and runs what is due in every organization (the rituals pattern: in process, no
// new dependency).
func (s *Service) Start(ctx context.Context, every time.Duration, lister application.OrgLister) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				orgs := []string{s.cfg.OrgID}
				if lister != nil {
					if ids, err := lister.ListOrgIDs(ctx); err == nil {
						orgs = application.WithDefaultOrg(ids, s.cfg.OrgID)
					} else {
						s.cfg.Log.Warn("link imports: list organizations", "err", err)
					}
				}
				s.RunDue(ctx, orgs)
			}
		}
	}()
}
