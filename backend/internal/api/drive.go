package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
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
	r.With(write).Post("/drive/upload", s.driveUploadMany)
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

// driveStatus maps a drive error to its HTTP status, stable code and message
// (500 = unexpected: the caller logs it).
func driveStatus(err error) (int, string, string) {
	var de *drive.Error
	code, msg := "", err.Error()
	if errors.As(err, &de) {
		code, msg = de.Code, de.Msg
	}
	switch {
	case errors.Is(err, drive.ErrReadOnly):
		return http.StatusLocked, code, msg
	case errors.Is(err, drive.ErrTooLarge), errors.Is(err, drive.ErrQuota):
		return http.StatusRequestEntityTooLarge, code, msg
	case errors.Is(err, drive.ErrUnsupportedType):
		return http.StatusUnsupportedMediaType, code, msg
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, code, msg
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, code, msg
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, code, msg
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusBadRequest, code, msg
	}
	return http.StatusInternalServerError, code, msg
}

// driveFail maps drive errors: the stable code always travels in the body.
func (s *server) driveFail(w http.ResponseWriter, err error) {
	status, code, msg := driveStatus(err)
	if status == http.StatusInternalServerError {
		s.fail(w, err) // unexpected: 500, logged
		return
	}
	body := map[string]any{"code": code, "error": msg}
	var de *drive.Error
	if errors.As(err, &de) {
		if de.Current > 0 {
			body["current_version"] = de.Current
		}
		if pl := de.PlanLimit; pl != nil {
			body["limit_kind"], body["limit"], body["plan"] = pl.Kind, pl.Limit, pl.Plan
		}
	}
	writeJSON(w, status, body)
}

// driveNeed checks what the acting person may do at path (office folders, company edition): nil when the person
// has level; domain.ErrNotFound when the person may not see it at all (a private office is never revealed);
// domain.ErrForbidden when the person may only read and asked to change. Outside /Offices everything is edit.
// The 404 reads like the drive's own "folder not found", so hidden and missing cannot be told apart.
func (s *server) driveNeed(r *http.Request, path, level string) error {
	return s.driveNeedAt(r.Context(), path, level, "folder")
}

// driveNeedAt is driveNeed for a context (commands) and for what the path names ("file" or "folder"), so the 404
// is word for word the drive's own for a missing file or folder.
func (s *server) driveNeedAt(ctx context.Context, path, level, what string) error {
	have := s.Drive.PersonAccess(ctx, application.PersonFrom(ctx), path)
	if have == drive.AccessNone {
		return driveNotFound(what)
	}
	if level == drive.AccessEdit && have != drive.AccessEdit {
		return &drive.Error{Code: "read_only_folder", Kind: domain.ErrForbidden, Msg: "you can read this office's folder but not change it"}
	}
	return nil
}

// driveNotFound is the drive's own not-found error (drive.mapStore): same code and text.
func driveNotFound(what string) error {
	return &drive.Error{Code: "not_found", Kind: domain.ErrNotFound, Msg: what + " not found"}
}

// driveVisible keeps the items the acting person may see, resolved in one VisibleTo call for the whole list.
func driveVisible[T any](s *server, r *http.Request, items []T, path func(T) string) []T {
	return driveVisibleAt(s, r.Context(), items, path)
}

func driveVisibleAt[T any](s *server, ctx context.Context, items []T, path func(T) string) []T {
	if len(items) == 0 || s.Drive == nil {
		return items
	}
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = path(it)
	}
	ok := s.Drive.VisibleTo(ctx, application.PersonFrom(ctx), paths)
	out := make([]T, 0, len(items))
	for i, it := range items {
		if ok[i] {
			out = append(out, it)
		}
	}
	return out
}

func folderPath(f drive.Folder) string { return f.Path }
func filePath(f drive.File) string     { return f.Path }
func hitPath(h drive.Hit) string       { return h.Path }

// driveFileFor loads the file {id} and checks the person's level on its path; false = the answer was written.
func (s *server) driveFileFor(w http.ResponseWriter, r *http.Request, level string) (drive.File, bool) {
	f, err := s.Drive.GetFile(r.Context(), chi.URLParam(r, "id"))
	if err == nil {
		err = s.driveNeedAt(r.Context(), f.Path, level, "file")
	}
	if err != nil {
		s.driveFail(w, err)
		return drive.File{}, false
	}
	return f, true
}

// driveFolderFor checks the person's level on the active folder {id}; false = the answer was written.
func (s *server) driveFolderFor(w http.ResponseWriter, r *http.Request, level string) (drive.Folder, bool) {
	f, err := s.Drive.FolderByID(r.Context(), chi.URLParam(r, "id"))
	if err == nil {
		err = s.driveNeed(r, f.Path, level)
	}
	if err != nil {
		s.driveFail(w, err)
		return drive.Folder{}, false
	}
	return f, true
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

// readBody reads a raw body into memory capped at the drive's in-memory limit
// (project inputs). Drive uploads are streamed instead (drive_upload.go).
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
	if err := s.driveNeed(r, p, drive.AccessRead); err != nil {
		s.driveFail(w, err)
		return
	}
	l, err := s.Drive.List(r.Context(), p)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	l.Folders, l.Files = driveVisible(s, r, l.Folders, folderPath), driveVisible(s, r, l.Files, filePath)
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
	hits = driveVisible(s, r, hits, hitPath)
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
	t.Folders, t.Files = driveVisible(s, r, t.Folders, folderPath), driveVisible(s, r, t.Files, filePath)
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
	if err := s.driveNeed(r, in.Path, drive.AccessEdit); err != nil {
		s.driveFail(w, err)
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
	if _, ok := s.driveFolderFor(w, r, drive.AccessEdit); !ok {
		return
	}
	if err := s.driveNeed(r, in.Path, drive.AccessEdit); err != nil {
		s.driveFail(w, err)
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
	if _, ok := s.driveFolderFor(w, r, drive.AccessEdit); !ok {
		return
	}
	if err := s.Drive.DeleteFolder(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// driveRestoreFolder: only the roots of a deletion can be restored, and the trash lists exactly those, so the
// path to authorize comes from there; a folder that is not in it is not found (as the drive itself would say).
func (s *server) driveRestoreFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.Drive.Trash(r.Context())
	if err != nil {
		s.driveFail(w, err)
		return
	}
	need := driveNotFound("folder") // what the drive answers for a folder that is not in the trash
	for _, f := range t.Folders {
		if f.ID == id {
			need = s.driveNeed(r, f.Path, drive.AccessEdit)
			break
		}
	}
	if need != nil {
		s.driveFail(w, need)
		return
	}
	f, err := s.Drive.RestoreFolder(r.Context(), s.drivePrincipal(r), id)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *server) driveFile(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// driveContent: GET /drive/files/{id}/content[?version=n][&download=1]. Streamed;
// never sniffed, never cached by shared caches, sandboxed if opened directly.
func (s *server) driveContent(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
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
	_, _ = io.Copy(&downloadWriter{w: w, rc: http.NewResponseController(w)}, rc) // a big file is not cut by WriteTimeout
}

// driveText: GET /drive/files/{id}/text?version=&offset=&max_chars=. The
// extracted text (what an agent would read), paged.
func (s *server) driveText(w http.ResponseWriter, r *http.Request) {
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
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
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
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

func (s *server) driveVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.driveFileFor(w, r, drive.AccessRead); !ok {
		return
	}
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
	if _, ok := s.driveFileFor(w, r, drive.AccessEdit); !ok {
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
	f, ok := s.driveFileFor(w, r, drive.AccessRead)
	if !ok {
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
	if _, ok := s.driveFileFor(w, r, drive.AccessEdit); !ok {
		return
	}
	if err := s.driveNeed(r, in.Path, drive.AccessEdit); err != nil {
		s.driveFail(w, err)
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
	if _, ok := s.driveFileFor(w, r, drive.AccessEdit); !ok {
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
	if _, ok := s.driveFileFor(w, r, drive.AccessEdit); !ok {
		return
	}
	if err := s.Drive.DeleteFile(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id")); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) driveRestoreFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.driveFileFor(w, r, drive.AccessEdit); !ok { // GetFile also finds a file in the trash
		return
	}
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
	gs = driveVisible(s, r, gs, func(g drive.Grant) string { return g.Path })
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
	// whoever opens a folder to an agent must be able to change it
	if err := s.driveNeed(r, in.Path, drive.AccessEdit); err != nil {
		s.driveFail(w, err)
		return
	}
	g, err := s.Drive.AddGrant(r.Context(), s.drivePrincipal(r), in.AgentID, in.Path)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// driveRevokeGrant: changing what an agent reads at a path needs edit there too (the grant is looked up among the
// active ones; an unknown id is left to the drive, which answers not found).
func (s *server) driveRevokeGrant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	gs, err := s.Drive.Grants(r.Context(), "")
	if err != nil {
		s.driveFail(w, err)
		return
	}
	for _, g := range gs {
		if g.ID == id {
			if err := s.driveNeed(r, g.Path, drive.AccessEdit); err != nil {
				s.driveFail(w, err)
				return
			}
			break
		}
	}
	if err := s.Drive.RevokeGrant(r.Context(), s.drivePrincipal(r), id); err != nil {
		s.driveFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
