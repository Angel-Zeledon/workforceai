package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/projects"
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
	cw connWiring, pstore projects.Store, objects objstore.Store) *drive.Service {

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
		}})
	if cw.gw != nil {
		tool := &drive.GatewayTool{FS: &drive.AgentFS{Svc: svc, Projects: projectDirectory{store: pstore}}, Suspects: cw.sus,
			AgentTools: func(actx context.Context, org, agentID string) ([]string, error) {
				a, err := store.GetAgent(application.WithOrg(actx, org), org, agentID)
				if err != nil {
					return nil, err
				}
				return a.Tools, nil
			}}
		cw.gw.AddNative(tool)
	}
	log.Info("drive ready", "agents_tool", drive.ToolName, "max_file_bytes", svc.Limits().MaxFileBytes, "quota_bytes", svc.Limits().QuotaBytes,
		"object_store", fmt.Sprintf("%T", objects))
	return svc
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
