package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// FileSource gives the sandbox read access to the files an agent may use as
// inputs. The drive (F1) implements it; the sandbox never reaches storage
// directly. Read must enforce the organization boundary and return
// domain.ErrNotFound for a file of another organization.
type FileSource interface {
	Read(ctx context.Context, org, fileID string) (name string, r io.ReadCloser, err error)
}

// FileSink receives the files produced by a run and stores them attributed to
// the agent (the drive writes a new version in a folder; folderHint is a
// suggestion such as "outputs/<run id>", never a trusted path). It returns a
// reference the agent and the UI can use.
type FileSink interface {
	WriteOutput(ctx context.Context, org, agentID, folderHint, name string, r io.Reader) (fileRef string, err error)
}

// MemoryFiles is an in-memory FileSource and FileSink (tests, demo).
type MemoryFiles struct {
	mu    sync.Mutex
	files map[string]memFile // org + "/" + id
	n     int
}

type memFile struct {
	Name, Agent, Folder, Path string
	Data                      []byte
}

// NewMemoryFiles creates an empty MemoryFiles.
func NewMemoryFiles() *MemoryFiles { return &MemoryFiles{files: map[string]memFile{}} }

// Put registers an input file.
func (m *MemoryFiles) Put(org, id, name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[org+"/"+id] = memFile{Name: name, Data: append([]byte(nil), data...)}
}

// Read implements FileSource.
func (m *MemoryFiles) Read(_ context.Context, org, id string) (string, io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[org+"/"+id]
	if !ok && strings.HasPrefix(id, "/") { // a drive-like path (PutAt)
		for k, g := range m.files {
			if strings.HasPrefix(k, org+"/") && g.Path == id {
				f, ok = g, true
				break
			}
		}
	}
	if !ok {
		return "", nil, domain.ErrNotFound
	}
	return f.Name, io.NopCloser(bytes.NewReader(f.Data)), nil
}

// WriteOutput implements FileSink.
func (m *MemoryFiles) WriteOutput(_ context.Context, org, agentID, folderHint, name string, r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	id := fmt.Sprintf("mem_out_%d", m.n)
	m.files[org+"/"+id] = memFile{Name: name, Agent: agentID, Folder: folderHint, Data: b}
	return id, nil
}

// Output returns a stored file (tests).
func (m *MemoryFiles) Output(org, id string) (name, agent, folder string, data []byte, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[org+"/"+id]
	return f.Name, f.Agent, f.Folder, f.Data, ok
}

// IDs lists the stored file ids of an organization (tests).
func (m *MemoryFiles) IDs(org string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.files {
		if len(k) > len(org)+1 && k[:len(org)+1] == org+"/" {
			out = append(out, k[len(org)+1:])
		}
	}
	sort.Strings(out)
	return out
}
