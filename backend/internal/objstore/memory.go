package objstore

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
	"time"
)

// Memory is the in-memory Store (tests, demo mode without a database). Data is
// lost on restart.
type Memory struct {
	mu   sync.Mutex
	m    map[string]memObj
	Now  func() time.Time
	puts int
}

type memObj struct {
	data []byte
	info Info
}

// NewMemory creates an empty Memory store.
func NewMemory() *Memory { return &Memory{m: map[string]memObj{}} }

var _ Store = (*Memory)(nil)

func (s *Memory) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Put implements Store.
func (s *Memory) Put(ctx context.Context, key string, r io.Reader, size int64, o PutOptions) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	max := o.MaxBytes
	if size >= 0 {
		max = size
	} else if max <= 0 {
		max = 1 << 30
	}
	b, err := ReadAllBounded(r, max)
	if err != nil {
		return Info{}, err
	}
	if size >= 0 && int64(len(b)) != size {
		return Info{}, io.ErrUnexpectedEOF
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	info := Info{Key: key, Size: int64(len(b)), SHA256: SumBytes(b), ContentType: o.ContentType, ModifiedAt: s.now()}
	s.mu.Lock()
	s.m[key] = memObj{data: b, info: info}
	s.puts++
	s.mu.Unlock()
	return info, nil
}

// Get implements Store.
func (s *Memory) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	s.mu.Lock()
	o, ok := s.m[key]
	s.mu.Unlock()
	if !ok {
		return nil, Info{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(slices.Clone(o.data))), o.info, nil
}

// Head implements Store.
func (s *Memory) Head(_ context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.m[key]
	if !ok {
		return Info{}, ErrNotFound
	}
	return o.info, nil
}

// Delete implements Store.
func (s *Memory) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	return nil
}

// Len is the number of stored objects (tests).
func (s *Memory) Len() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.m) }

// Keys lists the stored keys (tests).
func (s *Memory) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
