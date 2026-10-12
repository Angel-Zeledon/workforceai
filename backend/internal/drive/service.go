package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
)

// Default limits (env DRIVE_MAX_FILE_BYTES and DRIVE_QUOTA_BYTES override them).
const (
	DefaultMaxFileBytes = 25 << 20
	DefaultQuotaBytes   = 5 << 30
)

// Limits caps what an organization can store.
type Limits struct {
	MaxFileBytes int64 // per file version
	QuotaBytes   int64 // total of every stored version of the organization
}

func (l Limits) norm() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.QuotaBytes <= 0 {
		l.QuotaBytes = DefaultQuotaBytes
	}
	return l
}

// AuditEntry is one audit record (metadata only: never file content).
type AuditEntry struct {
	Actor    Principal
	Action   string
	Entity   string // file | folder | grant
	EntityID string
	Details  map[string]any
}

// Config wires the service.
type Config struct {
	Store   Store
	Objects objstore.Store
	OrgID   string
	Limits  Limits
	// WriteGate refuses every change while the organization is in read-only
	// mode (returns ErrReadOnly; must fail closed). nil = always open (tests).
	WriteGate func(ctx context.Context, org string) error
	// Audit records every write. nil = no audit (tests only).
	Audit func(ctx context.Context, org string, e AuditEntry)
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the drive use case layer. It does NOT decide who may do what:
// humans are authorized by role in the API layer and agents by AgentFS.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service {
	cfg.Limits = cfg.Limits.norm()
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

// Limits returns the effective limits.
func (s *Service) Limits() Limits { return s.cfg.Limits }

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Service) gate(ctx context.Context, org string) error {
	if s.cfg.WriteGate == nil {
		return nil
	}
	if err := s.cfg.WriteGate(ctx, org); err != nil {
		if errors.Is(err, ErrReadOnly) {
			return err
		}
		return readOnlyErr() // an unreadable controls state refuses too (fail closed)
	}
	return nil
}

func (s *Service) audit(ctx context.Context, org string, p Principal, action, entity, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, AuditEntry{Actor: p, Action: action, Entity: entity, EntityID: id, Details: d})
	}
}

func checkPrincipal(p Principal) error {
	switch p.Kind {
	case PrincipalHuman, PrincipalAgent, PrincipalSystem:
		if strings.TrimSpace(p.ID) != "" {
			return nil
		}
	}
	return forbidden("forbidden", "a valid principal is required")
}

func mapStore(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return notFound("%s not found", what)
	case errors.Is(err, domain.ErrConflict):
		return conflict("conflict", "%s conflicts with an existing item", what)
	}
	return err
}

// ---- folders ----

// folderFor walks segs (validated) and returns the folder, creating missing
// ones when create is true. Only the well-known roots may exist at the top level.
func (s *Service) folderFor(ctx context.Context, org string, p Principal, segs []string, create bool, ref ...string) (Folder, bool, error) {
	if len(segs) == 0 {
		return Folder{}, false, invalid("path_invalid", "the root is not a folder you can write into")
	}
	if rootKind(segs[0]) == "" {
		return Folder{}, false, invalid("root_not_allowed", "top-level folders are /%s", strings.Join(Roots, ", /"))
	}
	var cur Folder
	created := false
	for i := range segs {
		path := JoinPath(segs[:i+1]...)
		f, err := s.cfg.Store.FolderByPath(ctx, org, path)
		if err == nil {
			cur = f
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return Folder{}, false, err
		}
		if !create {
			return Folder{}, false, notFound("folder %s not found", path)
		}
		now := s.now()
		nf := Folder{ID: newID("fld_"), ParentID: cur.ID, Name: segs[i], Path: path, RootKind: rootKind(segs[0]),
			CreatedBy: p, CreatedAt: now, UpdatedAt: now}
		if i == 1 && nf.RootKind == "agents" {
			nf.RefID = segs[1] // the desk of the agent with that id
		} else if i == 1 && len(ref) > 0 && i == len(segs)-1 {
			nf.RefID = ref[0]
		}
		if err := s.cfg.Store.CreateFolder(ctx, org, nf); err != nil {
			if errors.Is(err, domain.ErrConflict) { // lost a race: use the winner
				if f, e2 := s.cfg.Store.FolderByPath(ctx, org, path); e2 == nil {
					cur = f
					continue
				}
			}
			return Folder{}, false, mapStore(err, "folder")
		}
		s.audit(ctx, org, p, "drive.folder.created", "folder", nf.ID, map[string]any{"path": nf.Path})
		cur, created = nf, true
	}
	return cur, created, nil
}

// Mkdir creates a folder (and its parents). Idempotent.
func (s *Service) Mkdir(ctx context.Context, p Principal, path string) (Folder, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return Folder{}, err
	}
	segs, err := Segments(path)
	if err != nil {
		return Folder{}, err
	}
	if err := s.gate(ctx, org); err != nil {
		return Folder{}, err
	}
	f, _, err := s.folderFor(ctx, org, p, segs, true)
	return f, err
}

// EnsureOwnedFolder creates /<root>/<name> linked to a project, client or
// agent (RefID). A project whose name collides with another project's folder
// gets a short id suffix. Idempotent per ref.
func (s *Service) EnsureOwnedFolder(ctx context.Context, root, ref, displayName string) (Folder, error) {
	org := s.org(ctx)
	if rootKind(root) == "" || root == RootShared || ref == "" {
		return Folder{}, invalid("path_invalid", "a project, client or agent folder needs a root and a reference")
	}
	if err := s.gate(ctx, org); err != nil {
		return Folder{}, err
	}
	top, _, err := s.folderFor(ctx, org, System(), []string{root}, true)
	if err != nil {
		return Folder{}, err
	}
	kids, err := s.cfg.Store.ChildFolders(ctx, org, top.ID)
	if err != nil {
		return Folder{}, err
	}
	for _, k := range kids {
		if k.RefID == ref {
			return k, nil
		}
	}
	name := safeFolderName(displayName, ref)
	if root == RootAgents {
		name = ref // the desk is named by the agent id: stable and unambiguous
	}
	for _, k := range kids {
		if strings.EqualFold(k.Name, name) {
			name = safeFolderName(displayName+" ("+shortRef(ref)+")", ref)
			break
		}
	}
	f, _, err := s.folderFor(ctx, org, System(), []string{root, name}, true, ref)
	return f, err
}

func shortRef(ref string) string {
	if len(ref) > 6 {
		return ref[len(ref)-6:]
	}
	return ref
}

func safeFolderName(display, ref string) string {
	n := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "-", "\x00", "").Replace(display)
	n = strings.TrimRight(strings.TrimSpace(n), ". ")
	if r := []rune(n); len(r) > 80 {
		n = strings.TrimSpace(string(r[:80]))
	}
	if c, err := CleanName(n); err == nil {
		return c
	}
	return ref
}

// Listing returns a folder with its active children. "/" lists the well-known
// roots (those that do not exist yet are virtual).
func (s *Service) List(ctx context.Context, path string) (Listing, error) {
	org := s.org(ctx)
	segs, err := Segments(path)
	if err != nil {
		return Listing{}, err
	}
	if len(segs) == 0 {
		top, err := s.cfg.Store.ChildFolders(ctx, org, "")
		if err != nil {
			return Listing{}, err
		}
		have := map[string]bool{}
		for _, f := range top {
			have[f.Name] = true
		}
		for _, r := range Roots {
			if !have[r] {
				top = append(top, Folder{Name: r, Path: "/" + r, RootKind: rootKind(r), Virtual: true})
			}
		}
		slices.SortFunc(top, func(a, b Folder) int { return strings.Compare(a.Name, b.Name) })
		return Listing{Folder: Folder{Path: "/", Name: ""}, Folders: top, Files: []File{}}, nil
	}
	f, err := s.cfg.Store.FolderByPath(ctx, org, JoinPath(segs...))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && len(segs) == 1 && rootKind(segs[0]) != "" {
			return Listing{Folder: Folder{Path: JoinPath(segs...), Name: segs[0], RootKind: rootKind(segs[0]), Virtual: true}, Folders: []Folder{}, Files: []File{}}, nil
		}
		return Listing{}, mapStore(err, "folder")
	}
	kids, err := s.cfg.Store.ChildFolders(ctx, org, f.ID)
	if err != nil {
		return Listing{}, err
	}
	files, err := s.cfg.Store.FilesIn(ctx, org, f.ID)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Folder: f, Folders: nonNil(kids), Files: nonNilF(files)}, nil
}

func nonNil(f []Folder) []Folder {
	if f == nil {
		return []Folder{}
	}
	return f
}
func nonNilF(f []File) []File {
	if f == nil {
		return []File{}
	}
	return f
}

// FolderByPath returns an active folder.
func (s *Service) FolderByPath(ctx context.Context, path string) (Folder, error) {
	c, err := CleanPath(path)
	if err != nil {
		return Folder{}, err
	}
	f, err := s.cfg.Store.FolderByPath(ctx, s.org(ctx), c)
	return f, mapStore(err, "folder")
}

// ---- files: read ----

// GetFile returns the metadata of a file (also a deleted one, for the trash).
func (s *Service) GetFile(ctx context.Context, id string) (File, error) {
	f, err := s.cfg.Store.FileByID(ctx, s.org(ctx), id)
	return f, mapStore(err, "file")
}

// FileByPath returns an active file by its path.
func (s *Service) FileByPath(ctx context.Context, path string) (File, error) {
	org := s.org(ctx)
	fp, name, err := SplitFile(path)
	if err != nil {
		return File{}, err
	}
	fo, err := s.cfg.Store.FolderByPath(ctx, org, fp)
	if err != nil {
		return File{}, mapStore(err, "file")
	}
	f, err := s.cfg.Store.FileByName(ctx, org, fo.ID, name)
	return f, mapStore(err, "file")
}

// Versions lists the versions of a file, newest first.
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	vs, err := s.cfg.Store.Versions(ctx, s.org(ctx), id)
	return vs, mapStore(err, "file")
}

// Version returns one version (0 = current).
func (s *Service) Version(ctx context.Context, f File, n int) (Version, error) {
	if n <= 0 {
		n = f.CurrentVersion
	}
	v, err := s.cfg.Store.VersionOf(ctx, s.org(ctx), f.ID, n)
	return v, mapStore(err, "version")
}

// Open streams the bytes of a version (0 = current). The caller closes it.
func (s *Service) Open(ctx context.Context, f File, n int) (io.ReadCloser, Version, error) {
	v, err := s.Version(ctx, f, n)
	if err != nil {
		return nil, Version{}, err
	}
	rc, _, err := s.cfg.Objects.Get(ctx, v.ObjectKey)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil, Version{}, errf(domain.ErrNotFound, "content_missing", "the stored content of this version is missing")
		}
		return nil, Version{}, err
	}
	return rc, v, nil
}

// Download is Open for a person downloading the file: it is audited.
func (s *Service) Download(ctx context.Context, p Principal, f File, n int) (io.ReadCloser, Version, error) {
	rc, v, err := s.Open(ctx, f, n)
	if err == nil {
		s.audit(ctx, s.org(ctx), p, "drive.file.downloaded", "file", f.ID, map[string]any{"path": f.Path, "version": v.Version, "size": v.Size, "sha256": v.SHA256})
	}
	return rc, v, err
}

// TextResult is the bounded text of a file, ready to be delimited.
type TextResult struct {
	File    File      `json:"file"`
	Version Version   `json:"version"`
	Text    string    `json:"text"`
	Info    Extracted `json:"info"`
	// Offset and Returned are in characters of the extracted text.
	Offset    int  `json:"offset"`
	Returned  int  `json:"returned"`
	More      bool `json:"more"`
	NextStart int  `json:"next_offset,omitempty"`
}

// MaxReadChars is the hard cap of one text read.
const (
	DefaultReadChars = 8000
	MaxReadChars     = 32000
	readExtractCap   = 1_000_000
)

// ReadText extracts the text of a version (0 = current) and returns the window
// [offset, offset+maxChars). It never returns more than MaxReadChars.
func (s *Service) ReadText(ctx context.Context, f File, version, offset, maxChars int) (TextResult, error) {
	if maxChars <= 0 {
		maxChars = DefaultReadChars
	}
	maxChars = min(maxChars, MaxReadChars)
	offset = max(offset, 0)
	rc, v, err := s.Open(ctx, f, version)
	if err != nil {
		return TextResult{}, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, MaxExtractBytes+1))
	if err != nil {
		return TextResult{}, err
	}
	x := Extract(f.Name, data, readExtractCap)
	res := TextResult{File: f, Version: v, Info: x, Offset: offset}
	if !x.Extracted {
		return res, nil
	}
	rs := []rune(x.Text)
	if offset >= len(rs) {
		res.Info.Text = ""
		return res, nil
	}
	end := min(len(rs), offset+maxChars)
	res.Text = string(rs[offset:end])
	res.Returned = end - offset
	if end < len(rs) || x.Truncated {
		res.More = true
		res.NextStart = end
		res.Info.Truncated = true
	}
	res.Info.Text = ""
	return res, nil
}

// ---- files: write ----

// WriteInput creates a file or a new version of an existing one, by path.
type WriteInput struct {
	Path string
	Data []byte
	// Message describes the change (version history).
	Message string
	// Expected, when set, is the version the writer last saw: another value is
	// a version_conflict (optimistic locking).
	Expected *int
	// Tags replaces the tags when non-nil.
	Tags []string
	// Source is a free label for the audit ("upload", "agent_tool", "deliverable").
	Source string
}

// WriteResult is what a write did.
type WriteResult struct {
	File    File    `json:"file"`
	Version Version `json:"version"`
	Created bool    `json:"created"`
	// Unchanged: the content equals the current version, nothing was written.
	Unchanged bool `json:"unchanged"`
}

func cleanMessage(m string) string {
	m = strings.Join(strings.Fields(strings.ToValidUTF8(m, "")), " ")
	if utf8.RuneCountInString(m) > MaxMessage {
		m = string([]rune(m)[:MaxMessage])
	}
	return m
}

func (s *Service) checkSize(ctx context.Context, org string, n int64, extra int64) error {
	lim := s.cfg.Limits
	if n == 0 {
		return invalid("empty_file", "the file is empty")
	}
	if n > lim.MaxFileBytes {
		return errf(ErrTooLarge, "file_too_large", "the file exceeds the limit of %d MB", lim.MaxFileBytes>>20)
	}
	used, err := s.cfg.Store.UsedBytes(ctx, org)
	if err != nil {
		return err
	}
	if used+n+extra > lim.QuotaBytes {
		return errf(ErrQuota, "quota_exceeded", "the storage quota of the organization (%d MB) would be exceeded", lim.QuotaBytes>>20)
	}
	return nil
}

// Write creates the file at path or adds a version to it. Parents are created.
func (s *Service) Write(ctx context.Context, p Principal, in WriteInput) (WriteResult, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return WriteResult{}, err
	}
	folderPath, name, err := SplitFile(in.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if _, err := CleanName(name); err != nil {
		return WriteResult{}, err
	}
	if err := CheckContent(name, in.Data); err != nil {
		return WriteResult{}, err
	}
	tags := in.Tags
	if tags != nil {
		if tags, err = CleanTags(tags); err != nil {
			return WriteResult{}, err
		}
	}
	if err := s.gate(ctx, org); err != nil {
		return WriteResult{}, err
	}
	if err := s.checkSize(ctx, org, int64(len(in.Data)), 0); err != nil {
		return WriteResult{}, err
	}
	segs, _ := Segments(folderPath)
	folder, _, err := s.folderFor(ctx, org, p, segs, true)
	if err != nil {
		return WriteResult{}, err
	}
	cur, err := s.cfg.Store.FileByName(ctx, org, folder.ID, name)
	switch {
	case err == nil:
		return s.addVersion(ctx, org, p, cur, in.Data, cleanMessage(in.Message), in.Expected, tags, in.Source)
	case !errors.Is(err, domain.ErrNotFound):
		return WriteResult{}, err
	}
	if in.Expected != nil && *in.Expected != 0 {
		e := conflict("version_conflict", "the file does not exist yet (expected version %d)", *in.Expected)
		return WriteResult{}, e
	}
	now := s.now()
	ft, _ := TypeOf(name)
	x := Extract(name, in.Data, MaxIndexChars)
	f := File{ID: newID("fil_"), FolderID: folder.ID, Name: name, Path: folder.Path + "/" + name, Mime: ft.mime, Size: int64(len(in.Data)),
		CurrentVersion: 1, Tags: orEmpty(tags), CreatedBy: p, UpdatedBy: p, CreatedAt: now, UpdatedAt: now}
	f.SHA256, f.Extraction, f.SearchText = objstore.SumBytes(in.Data), extractionOf(x), searchTextOf(x)
	v, err := s.putVersion(ctx, org, f, 1, in.Data, p, cleanMessage(in.Message), now)
	if err != nil {
		return WriteResult{}, err
	}
	if err := s.cfg.Store.CreateFile(ctx, org, f, v); err != nil {
		_ = s.cfg.Objects.Delete(ctx, v.ObjectKey)
		return WriteResult{}, mapStore(err, "file")
	}
	s.audit(ctx, org, p, "drive.file.created", "file", f.ID, map[string]any{"path": f.Path, "size": f.Size, "sha256": f.SHA256, "version": 1, "source": in.Source})
	return WriteResult{File: f, Version: v, Created: true}, nil
}

func orEmpty(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

func extractionOf(x Extracted) string {
	if !x.Extracted {
		return "none"
	}
	return x.Kind
}

func searchTextOf(x Extracted) string {
	if !x.Extracted {
		return ""
	}
	return x.Text
}

// NewVersion adds a version to an existing file (by id).
func (s *Service) NewVersion(ctx context.Context, p Principal, fileID string, data []byte, message string, expected *int, source string) (WriteResult, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return WriteResult{}, err
	}
	cur, err := s.cfg.Store.FileByID(ctx, org, fileID)
	if err != nil {
		return WriteResult{}, mapStore(err, "file")
	}
	if cur.DeletedAt != nil {
		return WriteResult{}, conflict("is_deleted", "the file is in the trash: restore it first")
	}
	if err := CheckContent(cur.Name, data); err != nil {
		return WriteResult{}, err
	}
	if err := s.gate(ctx, org); err != nil {
		return WriteResult{}, err
	}
	if err := s.checkSize(ctx, org, int64(len(data)), 0); err != nil {
		return WriteResult{}, err
	}
	return s.addVersion(ctx, org, p, cur, data, cleanMessage(message), expected, nil, source)
}

func (s *Service) addVersion(ctx context.Context, org string, p Principal, cur File, data []byte, msg string, expected *int, tags []string, source string) (WriteResult, error) {
	if expected != nil && *expected != cur.CurrentVersion {
		e := conflict("version_conflict", "the file is at version %d, not %d: read it again before writing", cur.CurrentVersion, *expected)
		e.Current = cur.CurrentVersion
		return WriteResult{}, e
	}
	sum := objstore.SumBytes(data)
	if sum == cur.SHA256 && tags == nil {
		v, err := s.cfg.Store.VersionOf(ctx, org, cur.ID, cur.CurrentVersion)
		return WriteResult{File: cur, Version: v, Unchanged: true}, mapStore(err, "version")
	}
	now := s.now()
	x := Extract(cur.Name, data, MaxIndexChars)
	nf := cur
	nf.Size, nf.SHA256, nf.CurrentVersion, nf.UpdatedBy, nf.UpdatedAt = int64(len(data)), sum, cur.CurrentVersion+1, p, now
	nf.Extraction, nf.SearchText = extractionOf(x), searchTextOf(x)
	if tags != nil {
		nf.Tags = tags
	}
	v, err := s.putVersion(ctx, org, nf, nf.CurrentVersion, data, p, msg, now)
	if err != nil {
		return WriteResult{}, err
	}
	if err := s.cfg.Store.AddVersion(ctx, org, cur.CurrentVersion, nf, v); err != nil {
		_ = s.cfg.Objects.Delete(ctx, v.ObjectKey)
		if errors.Is(err, domain.ErrConflict) {
			e := conflict("version_conflict", "the file was changed meanwhile: read it again before writing")
			if f2, e2 := s.cfg.Store.FileByID(ctx, org, cur.ID); e2 == nil {
				e.Current = f2.CurrentVersion
			}
			return WriteResult{}, e
		}
		return WriteResult{}, mapStore(err, "file")
	}
	s.audit(ctx, org, p, "drive.file.version_created", "file", nf.ID, map[string]any{"path": nf.Path, "version": nf.CurrentVersion, "size": nf.Size,
		"sha256": sum, "previous_version": cur.CurrentVersion, "source": source})
	return WriteResult{File: nf, Version: v}, nil
}

// putVersion stores the bytes as an immutable object and returns the version row.
func (s *Service) putVersion(ctx context.Context, org string, f File, n int, data []byte, p Principal, msg string, at time.Time) (Version, error) {
	key := objstore.OrgKey(org, "drive", f.ID, fmt.Sprintf("v%d", n))
	sum := objstore.SumBytes(data)
	if _, err := s.cfg.Objects.Put(ctx, key, bytes.NewReader(data), int64(len(data)), objstore.PutOptions{ContentType: f.Mime, SHA256: sum, MaxBytes: s.cfg.Limits.MaxFileBytes}); err != nil {
		return Version{}, fmt.Errorf("drive: store content: %w", err)
	}
	return Version{FileID: f.ID, Version: n, ObjectKey: key, SHA256: sum, Size: int64(len(data)), Mime: f.Mime, Author: p, Message: msg, CreatedAt: at}, nil
}

// RestoreVersion makes an old version current again by writing a NEW version
// with its bytes (history is never rewritten).
func (s *Service) RestoreVersion(ctx context.Context, p Principal, fileID string, version int, expected *int) (WriteResult, error) {
	org := s.org(ctx)
	f, err := s.GetFile(ctx, fileID)
	if err != nil {
		return WriteResult{}, err
	}
	rc, old, err := s.Open(ctx, f, version)
	if err != nil {
		return WriteResult{}, err
	}
	defer rc.Close()
	data, err := objstore.ReadAllBounded(rc, s.cfg.Limits.MaxFileBytes)
	if err != nil {
		return WriteResult{}, err
	}
	res, err := s.NewVersion(ctx, p, fileID, data, fmt.Sprintf("Restored version %d", old.Version), expected, "restore_version")
	if err == nil {
		s.audit(ctx, org, p, "drive.version.restored", "file", fileID, map[string]any{"path": f.Path, "restored_version": old.Version, "new_version": res.File.CurrentVersion})
	}
	return res, err
}

// Diff compares two versions of a text file (0 = current).
func (s *Service) Diff(ctx context.Context, f File, from, to int) (DiffResult, error) {
	if !IsTextName(f.Name) {
		return DiffResult{}, invalid("not_text", "only text files can be compared")
	}
	read := func(n int) (string, int, error) {
		rc, v, err := s.Open(ctx, f, n)
		if err != nil {
			return "", 0, err
		}
		defer rc.Close()
		b, err := objstore.ReadAllBounded(rc, MaxExtractBytes)
		if err != nil {
			return "", 0, invalid("too_large_to_diff", "the file is too large to compare")
		}
		return strings.ToValidUTF8(string(b), "�"), v.Version, nil
	}
	if to <= 0 {
		to = f.CurrentVersion
	}
	if from <= 0 {
		from = max(to-1, 1)
	}
	a, fa, err := read(from)
	if err != nil {
		return DiffResult{}, err
	}
	b, fb, err := read(to)
	if err != nil {
		return DiffResult{}, err
	}
	lines, add, rem, tr := DiffText(a, b)
	u, tr2 := unified(lines, 3)
	if len(lines) > 20000 {
		lines, tr = lines[:20000], true
	}
	return DiffResult{From: fa, To: fb, Added: add, Removed: rem, Lines: lines, Truncated: tr || tr2, Unified: u}, nil
}

// ---- move, delete, restore ----

func sameExt(a, b string) bool {
	ea, eb := strings.ToLower(extOf(a)), strings.ToLower(extOf(b))
	return ea == eb
}

func extOf(n string) string {
	if i := strings.LastIndex(n, "."); i >= 0 {
		return n[i:]
	}
	return ""
}

// MoveFile moves and/or renames a file. The extension cannot change (the
// content was validated against it).
func (s *Service) MoveFile(ctx context.Context, p Principal, fileID, newPath string) (File, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return File{}, err
	}
	f, err := s.GetFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if f.DeletedAt != nil {
		return File{}, conflict("is_deleted", "the file is in the trash")
	}
	folderPath, name, err := SplitFile(newPath)
	if err != nil {
		return File{}, err
	}
	if !sameExt(f.Name, name) {
		return File{}, invalid("extension_change", "the file extension cannot change")
	}
	if err := s.gate(ctx, org); err != nil {
		return File{}, err
	}
	segs, _ := Segments(folderPath)
	folder, _, err := s.folderFor(ctx, org, p, segs, true)
	if err != nil {
		return File{}, err
	}
	old := f.Path
	f.FolderID, f.Name, f.UpdatedBy, f.UpdatedAt = folder.ID, name, p, s.now()
	if err := s.cfg.Store.UpdateFile(ctx, org, f); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return File{}, conflict("exists", "a file named %s already exists in %s", name, folder.Path)
		}
		return File{}, mapStore(err, "file")
	}
	f.Path = folder.Path + "/" + name
	s.audit(ctx, org, p, "drive.file.moved", "file", f.ID, map[string]any{"from": old, "to": f.Path})
	return f, nil
}

// SetTags replaces the tags of a file.
func (s *Service) SetTags(ctx context.Context, p Principal, fileID string, tags []string) (File, error) {
	org := s.org(ctx)
	f, err := s.GetFile(ctx, fileID)
	if err != nil {
		return File{}, err
	}
	if f.Tags, err = CleanTags(tags); err != nil {
		return File{}, err
	}
	if err := s.gate(ctx, org); err != nil {
		return File{}, err
	}
	f.UpdatedBy, f.UpdatedAt = p, s.now()
	if err := s.cfg.Store.UpdateFile(ctx, org, f); err != nil {
		return File{}, mapStore(err, "file")
	}
	s.audit(ctx, org, p, "drive.file.tagged", "file", f.ID, map[string]any{"path": f.Path, "tags": f.Tags})
	return f, nil
}

// MoveFolder moves/renames a folder (not a well-known root nor a project,
// client or desk folder).
func (s *Service) MoveFolder(ctx context.Context, p Principal, folderID, newPath string) (Folder, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return Folder{}, err
	}
	f, err := s.cfg.Store.FolderByID(ctx, org, folderID)
	if err != nil || f.DeletedAt != nil {
		return Folder{}, notFound("folder not found")
	}
	if f.ParentID == "" || f.RefID != "" {
		return Folder{}, invalid("protected_folder", "this folder cannot be moved or renamed")
	}
	segs, err := Segments(newPath)
	if err != nil {
		return Folder{}, err
	}
	if len(segs) < 2 {
		return Folder{}, invalid("path_invalid", "a folder must be inside a well-known root")
	}
	np := JoinPath(segs...)
	if IsUnder(np, f.Path) {
		return Folder{}, invalid("path_invalid", "a folder cannot be moved into itself")
	}
	if err := s.gate(ctx, org); err != nil {
		return Folder{}, err
	}
	depth, err := s.subtreeDepth(ctx, org, f.ID, 0)
	if err != nil {
		return Folder{}, err
	}
	if len(segs)+depth > MaxDepth {
		return Folder{}, invalid("path_invalid", "the moved tree would be deeper than %d levels", MaxDepth)
	}
	parent, _, err := s.folderFor(ctx, org, p, segs[:len(segs)-1], true)
	if err != nil {
		return Folder{}, err
	}
	if parent.ID == f.ID {
		return Folder{}, invalid("path_invalid", "a folder cannot be moved into itself")
	}
	old := f.Path
	if err := s.cfg.Store.MoveFolder(ctx, org, f.ID, parent.ID, segs[len(segs)-1], np, s.now()); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return Folder{}, conflict("exists", "a folder already exists at %s", np)
		}
		return Folder{}, mapStore(err, "folder")
	}
	f.ParentID, f.Name, f.Path = parent.ID, segs[len(segs)-1], np
	s.audit(ctx, org, p, "drive.folder.moved", "folder", f.ID, map[string]any{"from": old, "to": np})
	return f, nil
}

func (s *Service) subtreeDepth(ctx context.Context, org, id string, d int) (int, error) {
	if d > MaxDepth {
		return d, nil
	}
	kids, err := s.cfg.Store.ChildFolders(ctx, org, id)
	if err != nil {
		return 0, err
	}
	m := 0
	for _, k := range kids {
		x, err := s.subtreeDepth(ctx, org, k.ID, d+1)
		if err != nil {
			return 0, err
		}
		m = max(m, x+1)
	}
	return m, nil
}

// DeleteFile moves a file to the trash (recoverable; nothing is erased).
func (s *Service) DeleteFile(ctx context.Context, p Principal, fileID string) error {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return err
	}
	f, err := s.GetFile(ctx, fileID)
	if err != nil {
		return err
	}
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteFile(ctx, org, fileID, p, s.now()); err != nil {
		return mapStore(err, "file")
	}
	s.audit(ctx, org, p, "drive.file.deleted", "file", fileID, map[string]any{"path": f.Path, "version": f.CurrentVersion})
	return nil
}

// RestoreFile takes a file out of the trash.
func (s *Service) RestoreFile(ctx context.Context, p Principal, fileID string) (File, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return File{}, err
	}
	if err := s.gate(ctx, org); err != nil {
		return File{}, err
	}
	if err := s.cfg.Store.RestoreFile(ctx, org, fileID, s.now()); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return File{}, conflict("cannot_restore", "the folder is in the trash or a file with the same name exists: restore the folder or rename first")
		}
		return File{}, mapStore(err, "file")
	}
	f, err := s.GetFile(ctx, fileID)
	if err == nil {
		s.audit(ctx, org, p, "drive.file.restored", "file", fileID, map[string]any{"path": f.Path})
	}
	return f, err
}

// DeleteFolder moves a folder and everything in it to the trash.
func (s *Service) DeleteFolder(ctx context.Context, p Principal, folderID string) error {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return err
	}
	f, err := s.cfg.Store.FolderByID(ctx, org, folderID)
	if err != nil || f.DeletedAt != nil {
		return notFound("folder not found")
	}
	if f.ParentID == "" || f.RefID != "" {
		return invalid("protected_folder", "this folder cannot be deleted")
	}
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteFolder(ctx, org, folderID, p, s.now()); err != nil {
		return mapStore(err, "folder")
	}
	s.audit(ctx, org, p, "drive.folder.deleted", "folder", folderID, map[string]any{"path": f.Path})
	return nil
}

// RestoreFolder takes a folder (and what was deleted with it) out of the trash.
func (s *Service) RestoreFolder(ctx context.Context, p Principal, folderID string) (Folder, error) {
	org := s.org(ctx)
	if err := checkPrincipal(p); err != nil {
		return Folder{}, err
	}
	if err := s.gate(ctx, org); err != nil {
		return Folder{}, err
	}
	if err := s.cfg.Store.RestoreFolder(ctx, org, folderID, s.now()); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return Folder{}, conflict("cannot_restore", "the parent folder is in the trash or an item with the same name exists")
		}
		return Folder{}, mapStore(err, "folder")
	}
	f, err := s.cfg.Store.FolderByID(ctx, org, folderID)
	if err == nil {
		s.audit(ctx, org, p, "drive.folder.restored", "folder", folderID, map[string]any{"path": f.Path})
	}
	return f, err
}

// Trash lists what was deleted (roots of each deletion).
func (s *Service) Trash(ctx context.Context) (Trash, error) {
	return s.cfg.Store.Trash(ctx, s.org(ctx))
}

// Usage is the storage used by the organization and its limits.
type Usage struct {
	UsedBytes    int64 `json:"used_bytes"`
	QuotaBytes   int64 `json:"quota_bytes"`
	MaxFileBytes int64 `json:"max_file_bytes"`
}

// Usage returns the storage usage.
func (s *Service) Usage(ctx context.Context) (Usage, error) {
	n, err := s.cfg.Store.UsedBytes(ctx, s.org(ctx))
	return Usage{UsedBytes: n, QuotaBytes: s.cfg.Limits.QuotaBytes, MaxFileBytes: s.cfg.Limits.MaxFileBytes}, err
}

// ---- search / retriever ----

// RetrieveQuery is the input of a Retriever.
type RetrieveQuery struct {
	Query string
	// Under restricts the search to these path prefixes (nil = everything).
	Under  []string
	Limit  int
	Config string // "spanish" (default) | "simple"
	// Terms: "any of these words", accent-insensitive (see SearchQuery.Terms).
	Terms []string
}

// Retriever finds relevant stored text for a query. The drive implements it
// over names and extracted text; the long-term memory retriever of the agents
// will reuse the same interface (and can add semantic backends behind it).
type Retriever interface {
	Retrieve(ctx context.Context, q RetrieveQuery) ([]Hit, error)
}

var _ Retriever = (*Service)(nil)

// Retrieve implements Retriever.
func (s *Service) Retrieve(ctx context.Context, q RetrieveQuery) ([]Hit, error) {
	if len(q.Terms) > 0 {
		lim := q.Limit
		if lim <= 0 || lim > 50 {
			lim = 20
		}
		hits, err := s.cfg.Store.Search(ctx, s.org(ctx), SearchQuery{Terms: q.Terms, Under: q.Under, Limit: lim})
		if hits == nil {
			hits = []Hit{}
		}
		return hits, err
	}
	if strings.TrimSpace(q.Query) == "" {
		return []Hit{}, nil
	}
	if len(q.Query) > 500 {
		q.Query = q.Query[:500]
	}
	lim := q.Limit
	if lim <= 0 || lim > 50 {
		lim = 20
	}
	cfg := q.Config
	if cfg != "simple" {
		cfg = "spanish"
	}
	hits, err := s.cfg.Store.Search(ctx, s.org(ctx), SearchQuery{Query: q.Query, Under: q.Under, Limit: lim, Config: cfg})
	if hits == nil {
		hits = []Hit{}
	}
	return hits, err
}

// ---- grants ----

// AddGrant gives an agent read access to a folder subtree.
func (s *Service) AddGrant(ctx context.Context, by Principal, agentID, path string) (Grant, error) {
	org := s.org(ctx)
	if err := checkPrincipal(by); err != nil || by.IsAgent() {
		return Grant{}, forbidden("forbidden", "only people grant folder access")
	}
	c, err := CleanPath(path)
	if err != nil || c == "/" {
		return Grant{}, invalid("path_invalid", "a grant needs a folder path below /")
	}
	if strings.TrimSpace(agentID) == "" || len(agentID) > 80 {
		return Grant{}, invalid("agent_invalid", "an agent id is required")
	}
	if err := s.gate(ctx, org); err != nil {
		return Grant{}, err
	}
	g := Grant{ID: newID("grt_"), AgentID: agentID, Path: c, Mode: "read", CreatedBy: by, CreatedAt: s.now()}
	if err := s.cfg.Store.AddGrant(ctx, org, g); err != nil {
		return Grant{}, err
	}
	s.audit(ctx, org, by, "drive.grant.created", "grant", g.ID, map[string]any{"agent_id": agentID, "path": c, "mode": "read"})
	return g, nil
}

// Grants lists the active grants (agentID "" = all).
func (s *Service) Grants(ctx context.Context, agentID string) ([]Grant, error) {
	return s.cfg.Store.Grants(ctx, s.org(ctx), agentID)
}

// RevokeGrant revokes a grant.
func (s *Service) RevokeGrant(ctx context.Context, by Principal, id string) error {
	org := s.org(ctx)
	if err := checkPrincipal(by); err != nil || by.IsAgent() {
		return forbidden("forbidden", "only people revoke folder access")
	}
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	if err := s.cfg.Store.RevokeGrant(ctx, org, id, s.now()); err != nil {
		return mapStore(err, "grant")
	}
	s.audit(ctx, org, by, "drive.grant.revoked", "grant", id, nil)
	return nil
}
