package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// Drive API (docs/architecture/drive.md, docs/architecture/08-api.md): the files
// and folders of the organization. Every route runs as a person; agents reach
// the drive only through the fs.* tools of the Tool Gateway, never through
// this API. Every change is versioned and reversible (restore a version,
// restore from the trash).

func (s *server) mountDrive(r chi.Router) {
	if s.Drive == nil {
		return
	}
	read, write := s.can(auth.PermFilesRead), s.can(auth.PermFilesWrite)
	del, grant := s.can(auth.PermFilesDelete), s.can(auth.PermFilesGrant)
	r.With(read).Get("/drive/list", s.driveList)
	r.With(read).Get("/drive/search", s.driveSearch)
	r.With(read).Get("/drive/usage", s.driveUsage)
	r.With(read).Get("/drive/trash", s.driveTrash)
	r.With(write).Post("/drive/folders", s.driveMkdir)
	r.With(write).Post("/drive/folders/{id}/move", s.driveMoveFolder)
	r.With(del).Delete("/drive/folders/{id}", s.driveDeleteFolder)
	r.With(write).Post("/drive/folders/{id}/restore", s.driveRestoreFolder)
	r.With(write).Put("/drive/upload", s.driveUpload)
	r.With(read).Get("/drive/files/{id}", s.driveFile)
	r.With(read).Get("/drive/files/{id}/content", s.driveContent)
	r.With(read).Get("/drive/files/{id}/text", s.driveText)
	r.With(read).Get("/drive/files/{id}/preview", s.drivePreview)
	r.With(write).Put("/drive/files/{id}/content", s.driveNewVersion)
	r.With(read).Get("/drive/files/{id}/versions", s.driveVersions)
	r.With(write).Post("/drive/files/{id}/versions/{n}/restore", s.driveRestoreVersion)
	r.With(read).Get("/drive/files/{id}/diff", s.driveDiff)
	r.With(write).Post("/drive/files/{id}/move", s.driveMoveFile)
	r.With(write).Put("/drive/files/{id}/tags", s.driveTags)
	r.With(del).Delete("/drive/files/{id}", s.driveDeleteFile)
	r.With(write).Post("/drive/files/{id}/restore", s.driveRestoreFile)
	r.With(read).Get("/drive/grants", s.driveGrants)
	r.With(grant).Post("/drive/grants", s.driveAddGrant)
	r.With(grant).Delete("/drive/grants/{id}", s.driveRevokeGrant)
}

func (s *server) drivePrincipal(r *http.Request) drive.Principal { return drive.Human(s.actor(r)) }

// driveFail maps drive errors: the stable code always travels in the body.
func (s *server) driveFail(w http.ResponseWriter, err error) {
	var de *drive.Error
	code := ""
	if errors.As(err, &de) {
		code = de.Code
	}
	var status int
	switch {
	case errors.Is(err, drive.ErrReadOnly):
		status = http.StatusLocked
	case errors.Is(err, drive.ErrTooLarge), errors.Is(err, drive.ErrQuota):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, drive.ErrUnsupportedType):
		status = http.StatusUnsupportedMediaType
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
	default:
		s.fail(w, err) // unexpected: 500, logged
		return
	}
	body := map[string]any{"code": code, "error": err.Error()}
	if de != nil {
		body["error"] = de.Msg
		if de.Current > 0 {
			body["current_version"] = de.Current
		}
	}
	writeJSON(w, status, body)
}

func queryInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(key))
	return n
}

// expectedVersion reads ?expected_version= (optimistic locking); absent = nil.
func expectedVersion(r *http.Request) *int {
	v := r.URL.Query().Get("expected_version")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &n
}

// readBody reads a raw upload capped at the drive's per-file limit.
func (s *server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	limit := s.Drive.Limits().MaxFileBytes
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			codeErr(w, http.StatusRequestEntityTooLarge, "too_large", "the file exceeds the upload limit")
			return nil, false
		}
		s.fail(w, errors.Join(domain.ErrInvalid, err))
		return nil, false
	}
	return data, true
}

// driveList: GET /drive/list?path=/Projects. 200 {folder, folders, files}.
func (s *server) driveList(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	l, err := s.Drive.List(r.Context(), p)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// driveSearch: GET /drive/search?q=&path=&limit=. Full text over names and
// extracted text (Spanish stemming by default).
func (s *server) driveSearch(w http.ResponseWriter, r *http.Request) {
	q := drive.RetrieveQuery{Query: r.URL.Query().Get("q"), Limit: queryInt(r, "limit"), Config: r.URL.Query().Get("config")}
	if p := r.URL.Query().Get("path"); p != "" {
		q.Under = []string{p}
	}
	hits, err := s.Drive.Retrieve(r.Context(), q)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": list(hits)})
}

func (s *server) driveUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.Drive.Usage(r.Context())
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *server) driveTrash(w http.ResponseWriter, r *http.Request) {
	t, err := s.Drive.Trash(r.Context())
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// driveMkdir: POST /drive/folders {path}. Creates the missing folders.
func (s *server) driveMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	f, err := s.Drive.Mkdir(r.Context(), s.drivePrincipal(r), in.Path)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

type drivePathBody struct {
	Path string `json:"path"`
}

func (s *server) driveMoveFolder(w http.ResponseWriter, r *http.Request) {
	var in drivePathBody
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	f, err := s.Drive.MoveFolder(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), in.Path)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *server) driveDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if err := s.Drive.DeleteFolder(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) driveRestoreFolder(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.RestoreFolder(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// driveUpload: PUT /drive/upload?path=/Shared/plan.xlsx&message=&expected_version=
// with the raw file as body. Creates the file or adds a version to it. 201
// created, 200 new version (or unchanged); 409 version_conflict; 413; 415; 423.
func (s *server) driveUpload(w http.ResponseWriter, r *http.Request) {
	data, ok := s.readBody(w, r)
	if !ok {
		return
	}
	res, err := s.Drive.Write(r.Context(), s.drivePrincipal(r), drive.WriteInput{Path: r.URL.Query().Get("path"), Data: data,
		Message: r.URL.Query().Get("message"), Expected: expectedVersion(r), Source: "upload"})
	if err != nil {
		s.driveFail(w, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}

func (s *server) driveFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// driveContent: GET /drive/files/{id}/content[?version=n][&download=1]. Streamed;
// never sniffed, never cached by shared caches, sandboxed if opened directly.
func (s *server) driveContent(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	rc, v, err := s.Drive.Download(r.Context(), s.drivePrincipal(r), f, queryInt(r, "version"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	defer rc.Close()
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", f.Mime)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": f.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Length", strconv.FormatInt(v.Size, 10))
	h.Set("ETag", `"`+v.SHA256+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// driveText: GET /drive/files/{id}/text?version=&offset=&max_chars=. The
// extracted text (what an agent would read), paged.
func (s *server) driveText(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	t, err := s.Drive.ReadText(r.Context(), f, queryInt(r, "version"), queryInt(r, "offset"), queryInt(r, "max_chars"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// drivePreview: GET /drive/files/{id}/preview?version=. A bounded JSON form of an
// office file (xlsx sheets, docx blocks) read by the server; the browser never
// parses the untrusted package.
func (s *server) drivePreview(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	p, v, err := s.Drive.PreviewOf(r.Context(), f, queryInt(r, "version"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"file_id": f.ID, "version": v.Version, "preview": p})
}

// driveNewVersion: PUT /drive/files/{id}/content?message=&expected_version= raw body.
func (s *server) driveNewVersion(w http.ResponseWriter, r *http.Request) {
	data, ok := s.readBody(w, r)
	if !ok {
		return
	}
	res, err := s.Drive.NewVersion(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), data, r.URL.Query().Get("message"), expectedVersion(r), "upload")
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) driveVersions(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Drive.Versions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": list(vs)})
}

// driveRestoreVersion: POST /drive/files/{id}/versions/{n}/restore?expected_version=.
// Restoring adds a NEW version equal to version n (history is never rewritten).
func (s *server) driveRestoreVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil {
		s.fail(w, errors.Join(domain.ErrInvalid, err))
		return
	}
	res, err := s.Drive.RestoreVersion(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), n, expectedVersion(r))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// driveDiff: GET /drive/files/{id}/diff?from=&to= (text files).
func (s *server) driveDiff(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	d, err := s.Drive.Diff(r.Context(), f, queryInt(r, "from"), queryInt(r, "to"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) driveMoveFile(w http.ResponseWriter, r *http.Request) {
	var in drivePathBody
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	f, err := s.Drive.MoveFile(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), in.Path)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *server) driveTags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tags []string `json:"tags"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	f, err := s.Drive.SetTags(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), in.Tags)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *server) driveDeleteFile(w http.ResponseWriter, r *http.Request) {
	if err := s.Drive.DeleteFile(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) driveRestoreFile(w http.ResponseWriter, r *http.Request) {
	f, err := s.Drive.RestoreFile(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"))
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// driveGrants: GET /drive/grants?agent_id=. Folders an agent may also read.
func (s *server) driveGrants(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agent == "" {
		codeErr(w, http.StatusBadRequest, "invalid", "agent_id is required")
		return
	}
	gs, err := s.Drive.Grants(r.Context(), agent)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": list(gs)})
}

// driveAddGrant: POST /drive/grants {agent_id, path}: the agent may read the
// folder (writes there still need approval).
func (s *server) driveAddGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
		Path    string `json:"path"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	g, err := s.Drive.AddGrant(r.Context(), s.drivePrincipal(r), in.AgentID, in.Path)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) driveRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if err := s.Drive.RevokeGrant(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
