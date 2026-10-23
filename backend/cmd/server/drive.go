package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// objectStoreFromEnv picks where file bytes live: an S3-compatible store when
// S3_ENDPOINT and S3_BUCKET are set (MinIO locally, S3/R2 in production),
// otherwise Postgres (bytea, migration 480) with a database, otherwise memory.
// The S3 credentials stay in the backend environment.
func objectStoreFromEnv(ctx context.Context, log *slog.Logger, pg *postgres.Store) objstore.Store {
	endpoint, bucket := os.Getenv("S3_ENDPOINT"), os.Getenv("S3_BUCKET")
	if endpoint != "" && bucket != "" {
		pathStyle := true
		if v, err := strconv.ParseBool(os.Getenv("S3_USE_PATH_STYLE")); err == nil {
			pathStyle = v
		}
		s3, err := objstore.NewS3(objstore.S3Config{Endpoint: endpoint, Bucket: bucket, AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"), Region: os.Getenv("S3_REGION"), UsePathStyle: pathStyle, Prefix: os.Getenv("S3_PREFIX")})
		if err != nil {
			log.Error("S3 object store misconfigured: files fall back to the database", "err", err)
		} else {
			if err := s3.CreateBucket(ctx); err != nil {
				log.Warn("S3 bucket check", "bucket", bucket, "err", err) // usually: already exists, or no permission to create
			}
			log.Info("files are stored in the S3-compatible object store", "endpoint", endpoint, "bucket", bucket)
			return s3
		}
	}
	if pg != nil {
		return &postgres.ObjectStore{S: pg}
	}
	return objstore.NewMemory()
}

// driveLimits reads DRIVE_MAX_FILE_MB and DRIVE_QUOTA_MB (0 or unset: defaults).
func driveLimits() drive.Limits {
	var l drive.Limits
	if v, err := strconv.ParseInt(os.Getenv("DRIVE_MAX_FILE_MB"), 10, 64); err == nil && v > 0 {
		l.MaxFileBytes = v << 20
	}
	if v, err := strconv.ParseInt(os.Getenv("DRIVE_QUOTA_MB"), 10, 64); err == nil && v > 0 {
		l.QuotaBytes = v << 20
	}
	return l
}

// projectDirectory answers which projects an agent works on: launched projects
// (not drafts, not finished) with at least one node assigned to it.
type projectDirectory struct{ store projects.Store }

func (d projectDirectory) AgentProjects(ctx context.Context, org, agentID string) ([]drive.ProjectRef, error) {
	recs, err := d.store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []drive.ProjectRef
	for _, r := range recs {
		switch r.Status {
		case projects.StatusDraft, projects.StatusDone, projects.StatusFailed, projects.StatusCancelled:
			continue
		}
		for _, n := range r.Nodes {
			if n.AgentID != nil && *n.AgentID == agentID {
				out = append(out, drive.ProjectRef{ID: r.ID, Name: r.Name})
				break
			}
		}
	}
	return out, nil
}

// wireDrive builds the drive (folders, files and versions; bytes in the object
// store) and serves the agents' fs.* tools through the Tool Gateway.
func wireDrive(ctx context.Context, cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	cw connWiring, pstore projects.Store, objects objstore.Store, orch *application.Orchestrator, cust *customers.Service) (*drive.Service, *docindex.Service) {

	var doc *docindex.Service // passage index (docindex.go): fed by the drive audit, searched by fs.search
	var dstore drive.Store = drive.NewMemStore()
	if pg != nil {
		dstore = &postgres.DriveStore{S: pg}
	}
	svc := drive.New(drive.Config{Store: dstore, Objects: objects, OrgID: cfg.App.OrgID, Limits: driveLimits(), Log: log,
		WriteGate: driveReadOnlyGate(cw.ctl),
		Audit: func(actx context.Context, org string, e drive.AuditEntry) {
			d := map[string]any{"actor_kind": e.Actor.Kind}
			for k, v := range e.Details {
				d[k] = v
			}
			rec.Audit(application.WithOrg(actx, org), domain.AuditLog{Actor: e.Actor.ID, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, Details: d})
			if doc != nil {
				doc.OnDriveAudit(org, e.Action, e.Entity, e.EntityID) // new version, restore, trash: (re)index or drop
			}
		}})
	doc = newDocIndex(log, pg, svc, cfg.App.OrgID)
	if cw.gw != nil {
		tool := &drive.GatewayTool{FS: &drive.AgentFS{Svc: svc, Projects: projectDirectory{store: pstore}, Render: renderFromEnv(log),
			Vision: drive.VisionLimitsFromEnv(), Chunks: doc, Customers: customerDirectory(cust)}, Suspects: cw.sus,
			CustomerOf: customerOf(cust), // B12: fs.* of a customer-scoped request stay inside that customer
			AgentTools: func(actx context.Context, org, agentID string) ([]string, error) {
				a, err := store.GetAgent(application.WithOrg(actx, org), org, agentID)
				if err != nil {
					return nil, err
				}
				return a.Tools, nil
			},
			// file.activity: which file an agent just listed/read/searched/wrote (path only).
			NotifyEdit: func(_ context.Context, _, _, taskID, path string, from, to, added, removed int) {
				orch.NoteTaskEdit(taskID, path, application.EditNote{From: from, To: to, Added: added, Removed: removed})
			},
			Notify: func(actx context.Context, org, agentID, taskID, action, path string) {
				rec.Emit(application.WithOrg(actx, org), application.Action{Type: "file.activity", AgentID: agentID, Entity: "file", SkipAudit: true,
					Payload: map[string]any{"agent_id": agentID, "task_id": taskID, "action": action, "path": path}})
				orch.NoteTaskFile(taskID, action, path) // the chat message of the task points to what it wrote
			}}
		cw.gw.AddNative(tool)
	}
	log.Info("drive ready", "agents_tool", drive.ToolName, "max_file_bytes", svc.Limits().MaxFileBytes, "quota_bytes", svc.Limits().QuotaBytes,
		"object_store", fmt.Sprintf("%T", objects))
	return svc, doc
}

// driveWorkspace resolves the folders an agent works in for a request: its
// desk, the project folder when the request is a launched project, and /Shared.
// Folders are created on demand; in read-only mode (or on any error) the paths
// are still given, the drive itself refuses the writes.
type driveWorkspace struct {
	drive  *drive.Service
	pstore projects.Store
	store  application.Store                // the agent's role (work kit)
	locale func(ctx context.Context) string // organization language (default "es")
	// customerOf (B12, optional) resolves the customer of a request: its agents get their per-customer desk.
	customerOf func(ctx context.Context, org, requestID string) (string, error)
}

func (d driveWorkspace) Workspace(ctx context.Context, org, agentID, requestID string) *application.RunWorkspace {
	ctx = application.WithOrg(ctx, org)
	ws := &application.RunWorkspace{Desk: "/" + drive.RootAgents + "/" + agentID, Shared: "/" + drive.RootShared}
	if f, err := d.drive.EnsureOwnedFolder(ctx, drive.RootAgents, agentID, agentID); err == nil {
		ws.Desk = f.Path
		d.seedDesk(ctx, org, agentID, f.Path)
	}
	if d.customerOf != nil && requestID != "" {
		// B12: a clone works in its per-customer desk (the general desk is closed to customer-scoped tasks).
		if cid, err := d.customerOf(ctx, org, requestID); err == nil && cid != "" {
			ws.Desk = drive.CustomerDesk(agentID, cid)
		}
	}
	if requestID == "" || d.pstore == nil {
		return ws
	}
	if rec, err := d.pstore.ByRequest(ctx, org, requestID); err == nil {
		if f, err := d.drive.EnsureOwnedFolder(ctx, drive.RootProjects, rec.ID, rec.Name); err == nil {
			ws.Project = f.Path
		}
		addProjectInputs(ws, rec)
	}
	return ws
}

// addProjectInputs tells the agents which data the project was given (H1).
func addProjectInputs(ws *application.RunWorkspace, rec projects.Record) {
	if len(rec.Inputs) == 0 {
		return
	}
	v := projects.InputsOf(rec)
	ws.InputsFolder = v.Folder
	if v.Waived != nil {
		ws.InputsWaived, ws.InputsMissing = true, v.Waived.Missing
	}
	for _, it := range v.Items {
		wi := application.WorkspaceInput{Key: it.Key, Label: it.Label, Description: it.Description, Kind: it.Kind, Required: it.Required, Value: it.Value}
		for _, f := range it.Files {
			wi.Files = append(wi.Files, application.WorkspaceInputFile{Path: f.Path, Size: f.Size})
			wi.Demo = wi.Demo || f.Source == projects.InputSourceDemo
		}
		if len(wi.Files) == 0 && wi.Value == "" {
			continue // nothing was provided for it: the waiver lists what is missing
		}
		ws.Inputs = append(ws.Inputs, wi)
	}
}

// seedDesk puts the role's work kit on an EMPTY desk: a "how I work" note from
// the role template plus the templates of its trade (roles: starter_files).
// A desk that already has anything is never touched; read-only mode simply
// refuses the writes (they are retried the next time the desk is resolved).
func (d driveWorkspace) seedDesk(ctx context.Context, org, agentID, desk string) {
	if d.store == nil {
		return
	}
	l, err := d.drive.List(ctx, desk)
	if err != nil || len(l.Files) > 0 || len(l.Folders) > 0 {
		return
	}
	a, err := d.store.GetAgent(ctx, org, agentID)
	if err != nil {
		return
	}
	tpl, ok := roles.Get(a.Role)
	if !ok {
		return
	}
	loc := "es"
	if d.locale != nil {
		loc = d.locale(ctx)
	}
	x := tpl.Text(loc)
	var b strings.Builder
	title, resp := "Cómo trabajo", "Mis responsabilidades"
	if strings.HasPrefix(loc, "en") {
		title, resp = "How I work", "My responsibilities"
	}
	fmt.Fprintf(&b, "# %s: %s\n\n%s\n\n## %s\n\n", title, x.Title, x.Persona, resp)
	for _, r := range x.Responsibilities {
		b.WriteString("- " + r + "\n")
	}
	files := map[string]string{map[bool]string{true: "how-i-work.md", false: "como-trabajo.md"}[strings.HasPrefix(loc, "en")]: b.String()}
	for _, f := range tpl.StarterFiles {
		files[f.Name] = f.Content(loc)
	}
	for name, content := range files {
		_, _ = d.drive.Write(ctx, drive.System(), drive.WriteInput{Path: desk + "/" + name, Data: []byte(content), Message: "work kit", Source: "starter"})
	}
}

// driveReadOnlyGate refuses every drive change in read-only mode; an unreadable
// controls state also refuses (fail closed).
func driveReadOnlyGate(ctl *controls.Service) func(ctx context.Context, org string) error {
	if ctl == nil {
		return nil
	}
	return func(ctx context.Context, org string) error {
		st, err := ctl.State(ctx, org)
		if err != nil || st.Mode == controls.ModeReadOnly {
			return drive.ErrReadOnly
		}
		return nil
	}
}

// driveFileResolver lets the chat point to drive files (id, version, type, size; never content).
type driveFileResolver struct{ drive *drive.Service }

func (d driveFileResolver) ResolveFile(ctx context.Context, org, path string) (domain.FileRef, bool) {
	f, err := d.drive.FileByPath(application.WithOrg(ctx, org), path)
	if err != nil || f.DeletedAt != nil {
		return domain.FileRef{}, false
	}
	return domain.FileRef{FileID: f.ID, Path: f.Path, Name: f.Name, Mime: f.Mime, Size: f.Size, Version: f.CurrentVersion}, true
}
