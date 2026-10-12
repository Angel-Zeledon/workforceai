package sandbox

import (
	"context"
	"io"
	"path"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// DriveFiles adapts the drive to the sandbox: it feeds the approved input files
// and stores the produced ones on the desk of the agent that asked for the run.
type DriveFiles struct {
	Drive *drive.Service
	// Notify (optional) is called with the path of each file written (file.activity).
	Notify func(ctx context.Context, org, agentID, path string)
}

var (
	_ FileSource = (*DriveFiles)(nil)
	_ FileSink   = (*DriveFiles)(nil)
)

// Read implements FileSource. ref is a file id or an absolute drive
// path. The organization comes from the request, never from the reference: a
// file of another organization is simply "not found".
func (d *DriveFiles) Read(ctx context.Context, org, ref string) (string, io.ReadCloser, error) {
	ctx = application.WithOrg(ctx, org)
	var f drive.File
	var err error
	if strings.HasPrefix(ref, "/") {
		f, err = d.Drive.FileByPath(ctx, ref)
	} else {
		f, err = d.Drive.GetFile(ctx, ref)
	}
	if err != nil {
		return "", nil, domain.ErrNotFound
	}
	if f.DeletedAt != nil {
		return "", nil, domain.ErrNotFound
	}
	rc, _, err := d.Drive.Open(ctx, f, 0)
	if err != nil {
		return "", nil, err
	}
	return f.Name, rc, nil
}

// WriteOutput implements FileSink: /Agents/<agent>/outputs/<run id>/<name>,
// written as the agent (a new version when the file exists). The returned
// reference is the path in the drive.
func (d *DriveFiles) WriteOutput(ctx context.Context, org, agentID, folderHint, name string, r io.Reader) (string, error) {
	ctx = application.WithOrg(ctx, org)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	desk, err := d.Drive.EnsureOwnedFolder(ctx, drive.RootAgents, agentID, agentID)
	if err != nil {
		return "", err
	}
	// The hint is only a suggestion (never trusted as a path): keep its last
	// segment, which is the run id, under the desk's outputs folder.
	sub := path.Base(strings.Trim(folderHint, "/"))
	p := desk.Path + "/outputs/" + sub + "/" + name
	res, err := d.Drive.Write(ctx, drive.AgentP(agentID), drive.WriteInput{Path: p, Data: data, Message: "code.run output", Source: "code_run"})
	if err != nil {
		return "", err
	}
	if d.Notify != nil {
		d.Notify(ctx, org, agentID, res.File.Path)
	}
	return res.File.Path, nil
}
