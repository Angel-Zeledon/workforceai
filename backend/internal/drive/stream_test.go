package drive_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

// sinkStore is an object store that keeps only the size and digest of what it
// receives (like S3 from the backend's point of view: nothing stays in memory).
type sinkStore struct {
	mu   sync.Mutex
	objs map[string]objstore.Info
}

func newSink() *sinkStore { return &sinkStore{objs: map[string]objstore.Info{}} }

func (s *sinkStore) Put(_ context.Context, key string, r io.Reader, size int64, o objstore.PutOptions) (objstore.Info, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return objstore.Info{}, err
	}
	if n != size {
		return objstore.Info{}, io.ErrUnexpectedEOF
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if o.SHA256 != "" && o.SHA256 != sum {
		return objstore.Info{}, errors.New("digest mismatch")
	}
	info := objstore.Info{Key: key, Size: n, SHA256: sum}
	s.mu.Lock()
	s.objs[key] = info
	s.mu.Unlock()
	return info, nil
}

func (s *sinkStore) Get(_ context.Context, key string) (io.ReadCloser, objstore.Info, error) {
	s.mu.Lock()
	info, ok := s.objs[key]
	s.mu.Unlock()
	if !ok {
		return nil, objstore.Info{}, objstore.ErrNotFound
	}
	return io.NopCloser(&pattern{left: info.Size}), info, nil
}

func (s *sinkStore) Head(_ context.Context, key string) (objstore.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.objs[key]; ok {
		return i, nil
	}
	return objstore.Info{}, objstore.ErrNotFound
}

func (s *sinkStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objs, key)
	s.mu.Unlock()
	return nil
}

// pattern generates n bytes of valid text without allocating them.
type pattern struct{ left, off int64 }

const line = "linea de un archivo grande, con acentos: canción, año, señal\n"

func (p *pattern) Read(b []byte) (int, error) {
	if p.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(b))
	if n > p.left {
		n = p.left
	}
	for i := int64(0); i < n; i++ {
		b[i] = line[(p.off+i)%int64(len(line))]
	}
	p.off += n
	p.left -= n
	return int(n), nil
}

func patternSum(n int64) string {
	h := sha256.New()
	_, _ = io.Copy(h, &pattern{left: n})
	return hex.EncodeToString(h.Sum(nil))
}

func streamSvc(t *testing.T, lim drive.Limits) (*drive.Service, *sinkStore, string) {
	t.Helper()
	dir := t.TempDir()
	sink := newSink()
	return drive.New(drive.Config{Store: drive.NewMemStore(), Objects: sink, OrgID: "o1", Limits: lim, SpoolDir: dir}), sink, dir
}

func spoolLeft(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

// A file far above the in-memory cap is streamed to the store with constant memory.
func TestWriteStreamBigFileConstantMemory(t *testing.T) {
	const size = 96 << 20
	svc, sink, dir := streamSvc(t, drive.Limits{MaxFileBytes: 4 << 20, MaxUploadBytes: 128 << 20, QuotaBytes: 1 << 30})
	h := drive.Human("u1")
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	res, err := svc.WriteStream(context.Background(), h, drive.StreamInput{Path: "/Shared/big/log.txt", Body: &pattern{left: size}, Size: size, Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapInuse) - int64(before.HeapInuse); grew > 32<<20 {
		t.Fatalf("heap grew %d MB for a %d MB upload: the body is being buffered", grew>>20, size>>20)
	}
	if !res.Created || res.File.Size != size || res.File.SHA256 != patternSum(size) {
		t.Fatalf("result: %+v", res.File)
	}
	if info, err := sink.Head(context.Background(), res.Version.ObjectKey); err != nil || info.Size != size || info.SHA256 != res.File.SHA256 {
		t.Fatalf("stored: %+v %v", info, err)
	}
	if res.File.Extraction != "text" || !strings.Contains(res.File.SearchText, "canción") {
		t.Fatalf("a big text file keeps the text of its first part for search: %q", res.File.Extraction)
	}
	if n := spoolLeft(t, dir); n != 0 {
		t.Fatalf("%d temporary files left", n)
	}
	// The in-memory path keeps its own (smaller) cap.
	if _, err := svc.Write(context.Background(), h, drive.WriteInput{Path: "/Shared/mem.txt", Data: bytes.Repeat([]byte("a"), 5<<20)}); !errors.Is(err, drive.ErrTooLarge) {
		t.Fatalf("in-memory write over MaxFileBytes: %v", err)
	}
	u, _ := svc.Usage(context.Background())
	if u.MaxUploadBytes != 128<<20 || u.MaxFileBytes != 4<<20 {
		t.Fatalf("usage limits: %+v", u)
	}
}

// failReader fails the test if anything reads it.
type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) { f.t.Fatal("the body was read"); return 0, io.EOF }

func TestWriteStreamRefusals(t *testing.T) {
	ctx := context.Background()
	h := drive.Human("u1")
	svc, sink, dir := streamSvc(t, drive.Limits{MaxFileBytes: 1 << 20, MaxUploadBytes: 2 << 20, QuotaBytes: 3 << 20})

	// announced size over the cap or the quota: refused before reading
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.txt", Body: failReader{t}, Size: 3 << 20}); !errors.Is(err, drive.ErrTooLarge) {
		t.Fatalf("announced too large: %v", err)
	}
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.exe", Body: failReader{t}, Size: 10}); !errors.Is(err, drive.ErrUnsupportedType) {
		t.Fatalf("type: %v", err)
	}
	// unknown size, body over the cap
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.txt", Body: &pattern{left: 2<<20 + 1}, Size: -1}); !errors.Is(err, drive.ErrTooLarge) {
		t.Fatalf("streamed too large: %v", err)
	}
	// NUL byte in a text file, far from the start
	body := io.MultiReader(&pattern{left: 1 << 20}, bytes.NewReader([]byte{0}))
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.txt", Body: body, Size: -1}); err == nil || !strings.Contains(err.Error(), "content_mismatch") {
		t.Fatalf("NUL: %v", err)
	}
	// wrong magic number
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.pdf", Body: strings.NewReader("not a pdf"), Size: -1}); err == nil || !strings.Contains(err.Error(), "content_mismatch") {
		t.Fatalf("pdf magic: %v", err)
	}
	// the client goes away in the middle
	cut := io.MultiReader(&pattern{left: 1000}, errReader{io.ErrUnexpectedEOF})
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/a.txt", Body: cut, Size: -1}); !errors.Is(err, drive.ErrUploadInterrupted) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("interrupted: %v", err)
	}
	if len(sink.objs) != 0 {
		t.Fatalf("nothing may be stored: %v", sink.objs)
	}
	if n := spoolLeft(t, dir); n != 0 {
		t.Fatalf("%d temporary files left", n)
	}
	// quota: a second file that does not fit
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/one.txt", Body: &pattern{left: 2 << 20}, Size: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/two.txt", Body: &pattern{left: 2 << 20}, Size: -1}); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// A multi-byte rune split across the 64 KB read chunks is still valid UTF-8; a
// broken one is not.
func TestWriteStreamUTF8AcrossChunks(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := streamSvc(t, drive.Limits{MaxUploadBytes: 8 << 20})
	for _, pad := range []int{64<<10 - 1, 64<<10 - 2, 64<<10 - 3, 64 << 10} {
		body := append(bytes.Repeat([]byte("x"), pad), []byte("€ñ😀 fin")...)
		if _, err := svc.WriteStream(ctx, drive.Human("u"), drive.StreamInput{Path: "/Shared/u.txt", Body: bytes.NewReader(body), Size: -1}); err != nil {
			t.Fatalf("pad %d: %v", pad, err)
		}
	}
	bad := append(bytes.Repeat([]byte("x"), 64<<10-1), 0xe2, 0x82) // truncated "€" at the very end
	if _, err := svc.WriteStream(ctx, drive.Human("u"), drive.StreamInput{Path: "/Shared/b.txt", Body: bytes.NewReader(bad), Size: -1}); err == nil {
		t.Fatal("a truncated rune at the end must be refused")
	}
}

// New versions, unchanged content and restores go through the stream too.
func TestNewVersionStreamAndRestore(t *testing.T) {
	ctx := context.Background()
	h := drive.Human("u1")
	svc, _, dir := streamSvc(t, drive.Limits{MaxFileBytes: 1 << 20, MaxUploadBytes: 16 << 20})
	w, err := svc.WriteStream(ctx, h, drive.StreamInput{Path: "/Shared/r.txt", Body: &pattern{left: 8 << 20}, Size: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := svc.NewVersionStream(ctx, h, w.File.ID, strings.NewReader("short"), 5, "v2", &w.File.CurrentVersion, "upload")
	if err != nil || v2.File.CurrentVersion != 2 {
		t.Fatalf("v2: %+v %v", v2.File, err)
	}
	same, err := svc.NewVersionStream(ctx, h, w.File.ID, strings.NewReader("short"), -1, "", nil, "upload")
	if err != nil || !same.Unchanged {
		t.Fatalf("unchanged: %+v %v", same, err)
	}
	// restoring the 8 MB version (over MaxFileBytes) works: it is streamed, not read into memory
	r, err := svc.RestoreVersion(ctx, h, w.File.ID, 1, nil)
	if err != nil || r.File.CurrentVersion != 3 || r.File.SHA256 != w.File.SHA256 {
		t.Fatalf("restore: %+v %v", r.File, err)
	}
	if n := spoolLeft(t, dir); n != 0 {
		t.Fatalf("%d temporary files left", n)
	}
}
