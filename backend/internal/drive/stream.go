package drive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
)

// Streamed uploads (docs/architecture/drive.md, "Uploads"). A person's upload
// is copied to a temporary file on disk while its SHA-256 is computed, checked
// there (type, size, quota), and then streamed to the object store with its
// length and digest (S3 verifies the digest). Memory stays constant whatever the
// size: only the first bytes (type check) and, for files small enough to have
// their text extracted, the extraction window are read into RAM.

// content is the body of one version: in memory (agents, imports) or spooled
// to a temporary file (people's uploads, restores).
type content struct {
	size int64
	sum  string
	data []byte
	file *os.File
}

func memContent(b []byte) *content {
	return &content{size: int64(len(b)), sum: objstore.SumBytes(b), data: b}
}

func (c *content) reader() io.Reader {
	if c.file != nil {
		return io.NewSectionReader(c.file, 0, c.size)
	}
	return bytes.NewReader(c.data)
}

// head returns the first min(n, size) bytes.
func (c *content) head(n int64) ([]byte, error) {
	if c.file == nil {
		return c.data[:min(int64(len(c.data)), n)], nil
	}
	b := make([]byte, min(n, c.size))
	if _, err := io.ReadFull(io.NewSectionReader(c.file, 0, c.size), b); err != nil {
		return nil, err
	}
	return b, nil
}

// close removes the temporary file (no-op in memory).
func (c *content) close() {
	if c == nil || c.file == nil {
		return
	}
	name := c.file.Name()
	_ = c.file.Close()
	_ = os.Remove(name)
}

// check is CheckContent without holding the whole file in memory.
func (c *content) check(name string) error {
	if c.file == nil {
		return CheckContent(name, c.data)
	}
	t, ok := TypeOf(name)
	if !ok {
		return CheckContent(name, nil) // unsupported_type
	}
	ext := strings.ToLower(path.Ext(name))
	switch {
	case ext == ".docx" || ext == ".xlsx" || ext == ".pptx":
		return checkOOXMLAt(ext, c.file, c.size)
	case t.kind == kindText:
		ok, err := validText(io.NewSectionReader(c.file, 0, c.size))
		if err != nil {
			return err
		}
		if !ok {
			return invalid("content_mismatch", "a text file must be valid UTF-8 without NUL bytes")
		}
		return nil
	default:
		h, err := c.head(16) // magic numbers (pdf, images)
		if err != nil {
			return err
		}
		return CheckContent(name, h)
	}
}

// extract is Extract with the same bounds the readers use (readLimit): a file
// bigger than what fs.read / the indexer would read is stored without text.
func (c *content) extract(name string) Extracted {
	if c.file == nil {
		return Extract(name, c.data, MaxIndexChars)
	}
	if IsTextName(name) {
		b, err := c.head(MaxExtractBytes + 1) // extractText cuts and says so
		if err != nil {
			return Extracted{Kind: "none", Note: "could not read the file for extraction"}
		}
		return Extract(name, b, MaxIndexChars)
	}
	if lim := readLimit(name); c.size > lim {
		return Extracted{Kind: "none", Note: fmt.Sprintf("file larger than %d MB: text not extracted", lim>>20)}
	}
	b, err := c.head(c.size)
	if err != nil {
		return Extracted{Kind: "none", Note: "could not read the file for extraction"}
	}
	return Extract(name, b, MaxIndexChars)
}

// validText reports whether r is UTF-8 without NUL bytes, reading it in chunks
// (a rune split across two chunks is carried over).
func validText(r io.Reader) (bool, error) {
	const chunk = 64 << 10
	buf := make([]byte, chunk+utf8.UTFMax)
	carry := 0
	for {
		n, err := r.Read(buf[carry : carry+chunk])
		n += carry
		b := buf[:n]
		if bytes.IndexByte(b, 0) >= 0 {
			return false, nil
		}
		cut := n
		if err == nil { // more follows: keep back an incomplete rune at the end
			for i := n - 1; i >= 0 && i >= n-utf8.UTFMax; i-- {
				if utf8.RuneStart(b[i]) {
					if !utf8.FullRune(b[i:n]) {
						cut = i
					}
					break
				}
			}
		}
		if !utf8.Valid(b[:cut]) {
			return false, nil
		}
		carry = copy(buf, b[cut:n])
		switch {
		case errors.Is(err, io.EOF):
			return carry == 0, nil
		case err != nil:
			return false, err
		}
	}
}

// checkOOXMLAt is checkOOXML over a file: only the zip directory is read.
func checkOOXMLAt(ext string, r io.ReaderAt, size int64) error {
	bad := func() error { return invalid("content_mismatch", "the content is not a valid %s file", ext[1:]) }
	sig := make([]byte, 4)
	if _, err := r.ReadAt(sig, 0); err != nil || !bytes.Equal(sig, []byte("PK\x03\x04")) {
		return bad()
	}
	zr, err := zip.NewReader(r, size)
	if err != nil || len(zr.File) > 5000 {
		return bad()
	}
	need := map[string]string{".docx": "word/document.xml", ".xlsx": "xl/workbook.xml", ".pptx": "ppt/presentation.xml"}[ext]
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	if !have["[Content_Types].xml"] || !have[need] {
		return bad()
	}
	return nil
}

// ErrUploadInterrupted: the body could not be read to the end (the client went
// away, or sent less than it announced).
var ErrUploadInterrupted = errors.New("drive: upload interrupted")

// readErrReader remembers the error of the source (to tell a client that went
// away from a full disk).
type readErrReader struct {
	r   io.Reader
	err error
}

func (r *readErrReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

// spool copies r to a temporary file (at most maxBytes; more is file_too_large)
// and hashes it on the way. The caller closes the content.
func (s *Service) spool(r io.Reader, maxBytes int64) (*content, error) {
	f, err := os.CreateTemp(s.cfg.SpoolDir, "wfai-upload-*")
	if err != nil {
		return nil, fmt.Errorf("drive: spool: %w", err)
	}
	c := &content{file: f}
	src := &readErrReader{r: io.LimitReader(r, maxBytes+1)}
	hr := objstore.NewHashingReader(src)
	n, err := io.Copy(f, hr)
	if err != nil {
		c.close()
		if src.err != nil {
			var te interface{ TooLarge() bool }
			if errors.As(src.err, &te) && te.TooLarge() {
				return nil, tooLarge(maxBytes)
			}
			return nil, &Error{Kind: errors.Join(domain.ErrInvalid, ErrUploadInterrupted), Code: "upload_interrupted", Msg: "the upload was interrupted before the end"}
		}
		return nil, fmt.Errorf("drive: spool: %w", err) // disk full or similar: a server problem
	}
	if n > maxBytes {
		c.close()
		return nil, tooLarge(maxBytes)
	}
	c.size, c.sum = n, hr.Sum()
	return c, nil
}

// StreamInput is a person's upload: the body is read once, in a stream.
type StreamInput struct {
	Path string
	Body io.Reader
	// Size is the announced length (Content-Length), -1 when unknown. It only
	// lets an obviously too big upload be refused before reading it.
	Size     int64
	Message  string
	Expected *int
	Tags     []string
	Source   string
}

// precheck refuses what can be refused before reading the body: bad path or
// type, read-only mode, an announced size over the cap or the quota.
func (s *Service) precheck(ctx context.Context, p Principal, name string, size, maxBytes int64) error {
	if err := checkPrincipal(p); err != nil {
		return err
	}
	if _, err := CleanName(name); err != nil {
		return err
	}
	if _, ok := TypeOf(name); !ok {
		return CheckContent(name, nil)
	}
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	if size > 0 {
		return s.checkSize(ctx, org, size, 0, maxBytes)
	}
	return nil
}

// WriteStream is Write for a streamed body (cap Limits.MaxUploadBytes).
func (s *Service) WriteStream(ctx context.Context, p Principal, in StreamInput) (WriteResult, error) {
	maxBytes := s.cfg.Limits.MaxUploadBytes
	_, name, err := SplitFile(in.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if err := s.precheck(ctx, p, name, in.Size, maxBytes); err != nil {
		return WriteResult{}, err
	}
	c, err := s.spool(in.Body, maxBytes)
	if err != nil {
		return WriteResult{}, err
	}
	defer c.close()
	return s.write(ctx, p, in.Path, c, writeMeta{Message: in.Message, Expected: in.Expected, Tags: in.Tags, Source: in.Source}, maxBytes)
}

// NewVersionStream is NewVersion for a streamed body (cap Limits.MaxUploadBytes).
func (s *Service) NewVersionStream(ctx context.Context, p Principal, fileID string, body io.Reader, size int64, message string, expected *int, source string) (WriteResult, error) {
	maxBytes := s.cfg.Limits.MaxUploadBytes
	cur, err := s.cfg.Store.FileByID(ctx, s.org(ctx), fileID)
	if err != nil {
		return WriteResult{}, mapStore(err, "file")
	}
	if err := s.precheck(ctx, p, cur.Name, size, maxBytes); err != nil {
		return WriteResult{}, err
	}
	c, err := s.spool(body, maxBytes)
	if err != nil {
		return WriteResult{}, err
	}
	defer c.close()
	return s.newVersion(ctx, p, fileID, c, message, expected, source, maxBytes)
}
