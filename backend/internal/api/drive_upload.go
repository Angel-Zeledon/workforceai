package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/drive"
)

// Uploads (docs/architecture/drive.md, "Uploads"). The body is never read
// whole into memory: the drive copies it to a temporary file while hashing it
// and then streams it to the object store. The cap is Limits.MaxUploadBytes
// (DRIVE_MAX_UPLOAD_MB, larger when S3 is configured).
//
//   - PUT  /drive/upload?path=&message=&expected_version=  raw body, one file
//   - PUT  /drive/files/{id}/content?message=&expected_version=  raw body, new version
//   - POST /drive/upload?folder=&message=  multipart/form-data, several files;
//     each part's filename may carry a relative path ("sub/a.txt", folder uploads)

const (
	// uploadIdle is how long an upload may send nothing before it is cut (the
	// server's ReadTimeout would otherwise end every upload after 30 s).
	uploadIdle = 2 * time.Minute
	// MaxMultipartFiles caps the files of one multipart upload.
	MaxMultipartFiles = 100
)

// progressReader pushes the read deadline forward while data keeps arriving:
// a slow-but-alive upload of any size passes, a stalled one is cut.
type progressReader struct {
	r    io.Reader
	rc   *http.ResponseController
	idle time.Duration
	last time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	if now := time.Now(); now.Sub(p.last) > time.Second {
		p.last = now
		_ = p.rc.SetReadDeadline(now.Add(p.idle))
	}
	return p.r.Read(b)
}

// uploadBody wraps the request body for a streamed upload: an announced length
// over limit is refused at once, the body is capped and the deadlines follow
// the transfer instead of the fixed server timeouts.
func (s *server) uploadBody(w http.ResponseWriter, r *http.Request, limit int64) (io.Reader, bool) {
	if r.ContentLength > limit {
		codeErr(w, http.StatusRequestEntityTooLarge, "file_too_large", "the file exceeds the upload limit")
		return nil, false
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(uploadIdle))
	_ = rc.SetWriteDeadline(time.Time{}) // answered after the body is stored; refreshed below
	return &progressReader{r: http.MaxBytesReader(w, r.Body, limit+1), rc: rc, idle: uploadIdle}, true
}

// uploadDone gives the response a normal write window once the body is stored.
func uploadDone(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
}

// driveUpload: PUT /drive/upload?path=/Shared/plan.xlsx&message=&expected_version=
// with the raw file as body. Creates the file or adds a version to it. 201
// created, 200 new version (or unchanged); 409 version_conflict; 413; 415; 423.
func (s *server) driveUpload(w http.ResponseWriter, r *http.Request) {
	body, ok := s.uploadBody(w, r, s.Drive.Limits().MaxUploadBytes)
	if !ok {
		return
	}
	res, err := s.Drive.WriteStream(r.Context(), s.drivePrincipal(r), drive.StreamInput{Path: r.URL.Query().Get("path"), Body: body,
		Size: r.ContentLength, Message: r.URL.Query().Get("message"), Expected: expectedVersion(r), Source: "upload"})
	uploadDone(w)
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

// driveNewVersion: PUT /drive/files/{id}/content?message=&expected_version= raw body.
func (s *server) driveNewVersion(w http.ResponseWriter, r *http.Request) {
	body, ok := s.uploadBody(w, r, s.Drive.Limits().MaxUploadBytes)
	if !ok {
		return
	}
	res, err := s.Drive.NewVersionStream(r.Context(), s.drivePrincipal(r), chi.URLParam(r, "id"), body, r.ContentLength,
		r.URL.Query().Get("message"), expectedVersion(r), "upload")
	uploadDone(w)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// uploadItem is the outcome of one file of a multipart upload.
type uploadItem struct {
	Name    string             `json:"name"`
	Path    string             `json:"path,omitempty"`
	Status  int                `json:"status"`
	Result  *drive.WriteResult `json:"result,omitempty"`
	Code    string             `json:"code,omitempty"`
	Error   string             `json:"error,omitempty"`
	Created bool               `json:"created,omitempty"`
}

// partPath is the path a part asks for under folder: the raw filename of the
// part (multipart.Part.FileName drops directories, folder uploads need them).
// The drive validates the result (no traversal, names, depth).
func partPath(folder, contentDisposition string) (string, string, bool) {
	_, params, err := mime.ParseMediaType(contentDisposition)
	if err != nil {
		return "", "", false
	}
	raw := strings.ReplaceAll(params["filename"], "\\", "/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", "", false
	}
	return strings.TrimRight(folder, "/") + "/" + raw, path.Base(raw), true
}

// driveUploadMany: POST /drive/upload?folder=/Shared/Docs&message= with
// multipart/form-data; every file part is streamed to the drive in turn (no
// part is ever whole in memory). 200 {results: [{name, path, status, result |
// code, error}]}: one failed file does not undo the others.
func (s *server) driveUploadMany(w http.ResponseWriter, r *http.Request) {
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" || !strings.HasPrefix(folder, "/") {
		codeErr(w, http.StatusBadRequest, "invalid", "folder is required")
		return
	}
	limit := s.Drive.Limits().MaxUploadBytes
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(uploadIdle))
	_ = rc.SetWriteDeadline(time.Time{})
	// The request as a whole: at most MaxMultipartFiles files of at most limit each.
	r.Body = http.MaxBytesReader(w, r.Body, limit*MaxMultipartFiles+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		codeErr(w, http.StatusBadRequest, "invalid", "expected multipart/form-data")
		return
	}
	p := s.drivePrincipal(r)
	msg := r.URL.Query().Get("message")
	results := []uploadItem{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			uploadDone(w)
			results = append(results, uploadItem{Status: http.StatusBadRequest, Code: "upload_interrupted", Error: "the upload was interrupted"})
			writeJSON(w, http.StatusOK, map[string]any{"results": results})
			return
		}
		full, name, ok := partPath(folder, part.Header.Get("Content-Disposition"))
		if !ok || part.FormName() != "file" {
			_ = part.Close() // a field, not a file: ignored
			continue
		}
		if len(results) >= MaxMultipartFiles {
			_ = part.Close()
			results = append(results, uploadItem{Name: name, Path: full, Status: http.StatusRequestEntityTooLarge, Code: "too_many_files",
				Error: "too many files in one upload"})
			continue
		}
		body := &progressReader{r: part, rc: rc, idle: uploadIdle}
		res, err := s.Drive.WriteStream(r.Context(), p, drive.StreamInput{Path: full, Body: body, Size: -1, Message: msg, Source: "upload"})
		_ = part.Close()
		item := uploadItem{Name: name, Path: full}
		if err != nil {
			item.Status, item.Code, item.Error = driveStatus(err)
			if item.Status == http.StatusInternalServerError {
				s.Log.Error("drive upload", "err", err)
				item.Error = "internal error"
			}
		} else {
			res := res
			item.Status, item.Result, item.Created = http.StatusOK, &res, res.Created
			if res.Created {
				item.Status = http.StatusCreated
			}
		}
		results = append(results, item)
	}
	uploadDone(w)
	if len(results) == 0 {
		codeErr(w, http.StatusBadRequest, "invalid", "no file in the upload")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// downloadWriter pushes the write deadline forward while a download flows, so a
// big file is not cut by the fixed WriteTimeout of REST responses.
type downloadWriter struct {
	w    io.Writer
	rc   *http.ResponseController
	last time.Time
}

func (d *downloadWriter) Write(b []byte) (int, error) {
	if now := time.Now(); now.Sub(d.last) > time.Second {
		d.last = now
		_ = d.rc.SetWriteDeadline(now.Add(uploadIdle))
	}
	return d.w.Write(b)
}
