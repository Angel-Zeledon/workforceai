package sandbox

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var (
	_ FileLocator = (*DriveFiles)(nil)
	_ FileLocator = (*MemoryFiles)(nil)
)

// Locate implements FileLocator: the canonical id and path of a drive file
// (ref is a file id or an absolute path), in the organization of the request.
func (d *DriveFiles) Locate(ctx context.Context, org, ref string) (FileLoc, error) {
	ctx = application.WithOrg(ctx, org)
	if strings.HasPrefix(ref, "/") {
		f, err := d.Drive.FileByPath(ctx, ref)
		if err != nil || f.DeletedAt != nil {
			return FileLoc{}, domain.ErrNotFound
		}
		return FileLoc{ID: f.ID, Path: f.Path}, nil
	}
	f, err := d.Drive.GetFile(ctx, ref)
	if err != nil || f.DeletedAt != nil {
		return FileLoc{}, domain.ErrNotFound
	}
	return FileLoc{ID: f.ID, Path: f.Path}, nil
}

// PutAt registers an input file that has a drive-like path (tests, demo).
func (m *MemoryFiles) PutAt(org, id, path string, data []byte) {
	m.Put(org, id, path[strings.LastIndex(path, "/")+1:], data)
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.files[org+"/"+id]
	f.Path = path
	m.files[org+"/"+id] = f
}

// Locate implements FileLocator for MemoryFiles.
func (m *MemoryFiles) Locate(_ context.Context, org, ref string) (FileLoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.files[org+"/"+ref]; ok && f.Path != "" {
		return FileLoc{ID: ref, Path: f.Path}, nil
	}
	for k, f := range m.files {
		if strings.HasPrefix(k, org+"/") && f.Path != "" && f.Path == ref {
			return FileLoc{ID: strings.TrimPrefix(k, org+"/"), Path: f.Path}, nil
		}
	}
	return FileLoc{}, domain.ErrNotFound
}
