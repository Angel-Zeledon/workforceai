package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// PDF blobs (docs/architecture/agent-workspaces.md sec. 5.7, simplified): the
// bytes of an uploaded PDF are stored as an immutable blob of the organization
// (Postgres bytea, migration 370, or memory without a database) and the pdf
// artifact gets a new version whose content points at it (`blob_id`). The old
// blob stays reachable from the old versions. Uploads are human-only, checked
// by content type AND magic bytes, size-capped, audited and refused while the
// organization is in read-only mode.

// MaxPDFBytes caps an uploaded PDF (the plan's 25 MB).
const MaxPDFBytes = 25 << 20

const mimePDF = "application/pdf"

// ErrReadOnly: the organization is in read-only mode (or its controls could not
// be read: fail closed). The API answers 423 read_only_mode.
var ErrReadOnly = errors.New("read_only_mode: the organization is in read-only mode")

// Blob is an immutable uploaded file of one organization.
type Blob struct {
	ID         string    `json:"blob_id"`
	ArtifactID string    `json:"artifact_id"`
	MIME       string    `json:"mime"`
	Filename   string    `json:"filename"`
	SHA256     string    `json:"sha256"`
	Size       int       `json:"size"`
	CreatedBy  ActorRef  `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	Data       []byte    `json:"-"`
}

// BlobStore persists blobs, scoped by organization (RLS in Postgres).
type BlobStore interface {
	PutBlob(ctx context.Context, org string, b Blob) error
	GetBlob(ctx context.Context, org, id string) (Blob, error)
}

// MemBlobs is the in-memory BlobStore (demo mode without a database and tests).
type MemBlobs struct {
	mu sync.Mutex
	m  map[string]Blob // org|id
}

func NewMemBlobs() *MemBlobs { return &MemBlobs{m: map[string]Blob{}} }

func (b *MemBlobs) PutBlob(_ context.Context, org string, x Blob) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[key(org, x.ID)]; ok {
		return domain.ErrConflict
	}
	x.Data = slices.Clone(x.Data)
	b.m[key(org, x.ID)] = x
	return nil
}

func (b *MemBlobs) GetBlob(_ context.Context, org, id string) (Blob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	x, ok := b.m[key(org, id)]
	if !ok {
		return Blob{}, domain.ErrNotFound
	}
	x.Data = slices.Clone(x.Data)
	return x, nil
}

var blobIDRe = regexp.MustCompile(`^blob_[a-z0-9]{16,40}$`)

// ValidBlobID reports whether s has the shape of a blob id (used before any lookup).
func ValidBlobID(s string) bool { return blobIDRe.MatchString(s) }

func newBlobID() string { return "blob_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24] }

// IsPDF checks the declared content type and the magic bytes ("%PDF-" at offset 0).
func IsPDF(contentType string, data []byte) error {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt != mimePDF {
		return fmt.Errorf("%w: the file must be sent as application/pdf", ErrUnsupportedMedia)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return fmt.Errorf("%w: the file does not start with the PDF signature", ErrNotPDF)
	}
	return nil
}

// pageRe counts page objects ("/Type /Page", not "/Pages"). It is an estimate
// for the artifact card: the viewer shows the real count from pdf.js, and
// compressed object streams can hide pages (then 0 = unknown).
var pageRe = regexp.MustCompile(`/Type\s*/Page[^s]`)

func countPages(data []byte) int {
	n := len(pageRe.FindAllIndex(data, maxPDFPages+1))
	return min(n, maxPDFPages)
}

const maxPDFPages = 10000

// cleanFilename keeps the base name, drops control and quoting characters and ensures ".pdf".
func cleanFilename(s string) string {
	s = path.Base(strings.ReplaceAll(strings.TrimSpace(s), "\\", "/"))
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`"<>|*?:/\;`, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." {
		s = "document"
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if !strings.HasSuffix(strings.ToLower(s), ".pdf") {
		s += ".pdf"
	}
	return s
}

// UploadInput is POST /artifacts/{id}/pdf.
type UploadInput struct {
	Filename    string
	ContentType string
	Data        []byte
	BaseVersion int // 0 = the current head
}

// UploadResult is the answer of an upload.
type UploadResult struct {
	Blob    Blob `json:"blob"`
	Version int  `json:"version"`
	Pages   int  `json:"pages"`
}

func (s *Service) blobs() BlobStore { return s.cfg.Blobs }

// UploadPDF stores the bytes of a PDF and writes a new version of the pdf
// artifact pointing at them.
func (s *Service) UploadPDF(ctx context.Context, actor Actor, id string, in UploadInput) (UploadResult, error) {
	org := s.org(ctx)
	if actor.IsAgent() {
		return UploadResult{}, forbidden("agents cannot upload files")
	}
	if s.cfg.WriteGate != nil {
		if err := s.cfg.WriteGate(ctx, org); err != nil {
			return UploadResult{}, err
		}
	}
	if len(in.Data) == 0 {
		return UploadResult{}, invalid("empty_file: the file is empty")
	}
	if len(in.Data) > MaxPDFBytes {
		return UploadResult{}, fmt.Errorf("%w: the PDF exceeds %d MB", ErrTooLarge, MaxPDFBytes>>20)
	}
	if err := IsPDF(in.ContentType, in.Data); err != nil {
		return UploadResult{}, err
	}
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return UploadResult{}, err
	}
	if cur.Meta.Kind != KindPDF {
		return UploadResult{}, invalid("a %s artifact does not hold a PDF file", cur.Meta.Kind)
	}
	if err := s.authorizeWrite(actor, cur.Meta); err != nil {
		return UploadResult{}, err
	}
	if cur.Meta.Locked {
		return UploadResult{}, conflictErr("the artifact is %s and locked: move it back to draft first", cur.Meta.Status)
	}
	base := in.BaseVersion
	if base == 0 {
		base = cur.Meta.HeadVersion
	}
	sum := sha256.Sum256(in.Data)
	b := Blob{ID: newBlobID(), ArtifactID: id, MIME: mimePDF, Filename: cleanFilename(in.Filename), SHA256: hex.EncodeToString(sum[:]),
		Size: len(in.Data), CreatedBy: actor.Ref(), CreatedAt: s.now(), Data: in.Data}
	if err := s.blobs().PutBlob(ctx, org, b); err != nil {
		return UploadResult{}, err
	}
	var root map[string]any
	if err := decodeNum(cur.Content, &root); err != nil || root == nil {
		root = map[string]any{"schema": KindPDF.Schema(), "annotations": []any{}}
	}
	pages := countPages(in.Data)
	root["blob_id"], root["pages"], root["filename"], root["size_bytes"] = b.ID, pages, b.Filename, b.Size
	raw, err := json.Marshal(root)
	if err != nil {
		return UploadResult{}, err
	}
	summary := L(cur.Meta.Locale, "PDF subido: ", "PDF uploaded: ") + b.Filename
	res, changed, err := s.saveLocked(ctx, org, actor, id, SaveInput{BaseVersion: base, Content: raw, Summary: summary, source: "pdf_upload"})
	if err != nil {
		// The blob is left without a version pointing at it (harmless: only reachable by id within the org).
		return UploadResult{}, err
	}
	if changed {
		s.propagate(ctx, org, id, res.Version, 0)
	}
	s.audit(ctx, actor, "artifact.pdf_uploaded", id, map[string]any{"blob_id": b.ID, "sha256": b.SHA256, "size": b.Size, "filename": b.Filename,
		"pages": pages, "version": res.Version})
	return UploadResult{Blob: b, Version: res.Version, Pages: pages}, nil
}

// PDFBlob returns a stored blob of the organization. A download as attachment is audited.
func (s *Service) PDFBlob(ctx context.Context, actor Actor, blobID string, download bool) (Blob, error) {
	if !ValidBlobID(blobID) {
		return Blob{}, domain.ErrNotFound
	}
	b, err := s.blobs().GetBlob(ctx, s.org(ctx), blobID)
	if err != nil {
		return Blob{}, err
	}
	if download {
		s.audit(ctx, actor, "artifact.pdf_downloaded", b.ArtifactID, map[string]any{"blob_id": b.ID, "sha256": b.SHA256, "size": b.Size})
	}
	return b, nil
}

// Upload errors with their own HTTP status (the last two are also ErrInvalid).
var (
	ErrTooLarge         = errors.New("too_large")                                     // 413
	ErrUnsupportedMedia = fmt.Errorf("%w: unsupported_media_type", domain.ErrInvalid) // 415
	ErrNotPDF           = fmt.Errorf("%w: not_a_pdf", domain.ErrInvalid)              // 400
)

// checkPDF validates a pdf content: blob_id is null or a blob id; pages a small number.
func checkPDF(root map[string]any) error {
	switch v := root["blob_id"].(type) {
	case nil:
	case string:
		if !ValidBlobID(v) {
			return invalid("blob_id must be null or an uploaded blob id")
		}
	default:
		return invalid("blob_id must be null or a string")
	}
	if p, ok := root["pages"]; ok {
		n, ok := p.(json.Number)
		i, err := n.Int64()
		if !ok || err != nil || i < 0 || i > maxPDFPages {
			return invalid("pages must be an integer between 0 and %d", maxPDFPages)
		}
	}
	if a, ok := root["annotations"]; ok && a != nil {
		if _, ok := a.([]any); !ok {
			return invalid("annotations must be a list")
		}
	}
	return nil
}
