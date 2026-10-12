package api

import (
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/demodata"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/projects"
)

// Project inputs API (H1): the data a project needs before it starts. The files
// live in the drive under /Projects/<project>/Entradas/ (uploaded, copied from
// another drive folder, or generated sample data); the project keeps references.
//
//	GET    /projects/{id}/inputs
//	PUT    /projects/{id}/inputs/{key}/upload?name=   raw body
//	POST   /projects/{id}/inputs/{key}/link           {paths: [...]}  copies drive files into Entradas
//	PUT    /projects/{id}/inputs/{key}/value          {value}
//	DELETE /projects/{id}/inputs/{key}/files?path=
//	POST   /projects/{id}/inputs/demo                 sample data for every input still missing

func (s *server) mountProjectInputs(r chi.Router) {
	if s.Projects == nil {
		return
	}
	edit := s.can(auth.PermRequestsCreate)
	files := s.can(auth.PermFilesWrite)
	r.With(s.can(auth.PermTasksRead)).Get("/projects/{id}/inputs", s.getProjectInputs)
	r.With(edit, files).Put("/projects/{id}/inputs/{key}/upload", s.uploadProjectInput)
	r.With(edit, files).Post("/projects/{id}/inputs/{key}/link", s.linkProjectInput)
	r.With(edit).Put("/projects/{id}/inputs/{key}/value", s.valueProjectInput)
	r.With(edit, files).Delete("/projects/{id}/inputs/{key}/files", s.removeProjectInputFile)
	r.With(edit, files).Post("/projects/{id}/inputs/demo", s.demoProjectInputs)
}

func (s *server) needDrive(w http.ResponseWriter) bool {
	if s.Drive == nil {
		codeErr(w, http.StatusServiceUnavailable, "drive_unavailable", "the drive is not configured")
		return false
	}
	return true
}

// ensureInputsFolder creates /Projects/<project>/Entradas and returns its path.
func (s *server) ensureInputsFolder(r *http.Request, rec projects.Record) (string, error) {
	f, err := s.Drive.EnsureOwnedFolder(r.Context(), drive.RootProjects, rec.ID, rec.Name)
	if err != nil {
		return "", err
	}
	p := f.Path + "/" + projects.InputsFolderName
	if _, err := s.Drive.Mkdir(r.Context(), s.drivePrincipal(r), p); err != nil {
		return "", err
	}
	return p, nil
}

// syncInputs drops the references to files that no longer exist (deleted by hand).
func (s *server) syncInputs(r *http.Request, id string) projects.InputsView {
	rec, err := s.Projects.Record(r.Context(), id)
	if err != nil || rec.Status != projects.StatusDraft || s.Drive == nil {
		return projects.InputsOf(rec)
	}
	gone := false
	for _, in := range rec.Inputs {
		for _, f := range in.Files {
			if fr, err := s.Drive.FileByPath(r.Context(), f.Path); err != nil || fr.DeletedAt != nil {
				gone = true
			}
		}
	}
	if !gone {
		return projects.InputsOf(rec)
	}
	v, err := s.Projects.UpdateInputs(r.Context(), id, func(rc *projects.Record) error {
		for i := range rc.Inputs {
			keep := rc.Inputs[i].Files[:0:0]
			for _, f := range rc.Inputs[i].Files {
				if fr, err := s.Drive.FileByPath(r.Context(), f.Path); err == nil && fr.DeletedAt == nil {
					keep = append(keep, f)
				}
			}
			rc.Inputs[i].Files = keep
		}
		return nil
	})
	if err != nil {
		return projects.InputsOf(rec)
	}
	return v
}

func (s *server) getProjectInputs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Projects.Record(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.syncInputs(r, id))
}

// pickInput loads the draft and one of its file inputs.
func (s *server) pickInput(w http.ResponseWriter, r *http.Request) (projects.Record, projects.InputSpec, bool) {
	rec, err := s.Projects.Record(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return rec, projects.InputSpec{}, false
	}
	if rec.Status != projects.StatusDraft {
		codeErr(w, http.StatusConflict, "not_draft", "the inputs can only change while the project is a draft")
		return rec, projects.InputSpec{}, false
	}
	in := projects.InputByKey(&rec, chi.URLParam(r, "key"))
	if in == nil {
		codeErr(w, http.StatusNotFound, "unknown_input", "this project has no such input")
		return rec, projects.InputSpec{}, false
	}
	return rec, in.InputSpec, true
}

func (s *server) attachFile(r *http.Request, id, key, folder string, f projects.InputFileRef) (projects.InputsView, error) {
	return s.Projects.UpdateInputs(r.Context(), id, func(rc *projects.Record) error {
		in := projects.InputByKey(rc, key)
		if in == nil {
			return domain.ErrNotFound
		}
		rc.InputsFolder = folder
		return projects.AddInputFile(in, f)
	})
}

// cleanUploadName keeps only the base name and refuses what the drive would refuse.
func cleanUploadName(name string) (string, bool) {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "", false
	}
	if _, err := drive.CleanName(name); err != nil {
		return "", false
	}
	return name, true
}

func (s *server) uploadProjectInput(w http.ResponseWriter, r *http.Request) {
	if !s.needDrive(w) {
		return
	}
	rec, spec, ok := s.pickInput(w, r)
	if !ok {
		return
	}
	if spec.Kind != projects.InputFile && spec.Kind != projects.InputFolder {
		codeErr(w, http.StatusBadRequest, "input_not_file", "this input takes a value, not files")
		return
	}
	name, ok := cleanUploadName(r.URL.Query().Get("name"))
	if !ok {
		codeErr(w, http.StatusBadRequest, "name_invalid", "a valid file name is required (?name=)")
		return
	}
	if !spec.AcceptsName(name) {
		codeErr(w, http.StatusUnsupportedMediaType, "input_type_not_accepted", "this input accepts: "+strings.Join(spec.Accept, ", "))
		return
	}
	data, ok := s.readBody(w, r)
	if !ok {
		return
	}
	folder, err := s.ensureInputsFolder(r, rec)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	res, err := s.Drive.Write(r.Context(), s.drivePrincipal(r), drive.WriteInput{Path: folder + "/" + name, Data: data, Source: "project_input", Message: "input " + spec.Key})
	if err != nil {
		s.driveFail(w, err)
		return
	}
	v, err := s.attachFile(r, rec.ID, spec.Key, folder, projects.InputFileRef{Path: res.File.Path, Name: res.File.Name, Size: res.File.Size,
		Source: projects.InputSourceUpload, AddedBy: s.actor(r), AddedAt: time.Now().UTC()})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) linkProjectInput(w http.ResponseWriter, r *http.Request) {
	if !s.needDrive(w) {
		return
	}
	rec, spec, ok := s.pickInput(w, r)
	if !ok {
		return
	}
	if spec.Kind != projects.InputFile && spec.Kind != projects.InputFolder {
		codeErr(w, http.StatusBadRequest, "input_not_file", "this input takes a value, not files")
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if len(body.Paths) == 0 || len(body.Paths) > 60 {
		codeErr(w, http.StatusBadRequest, "paths_invalid", "between 1 and 60 drive paths are required")
		return
	}
	folder, err := s.ensureInputsFolder(r, rec)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	var v projects.InputsView
	limit := s.Drive.Limits().MaxFileBytes
	for _, p := range body.Paths {
		src, err := s.Drive.FileByPath(r.Context(), p)
		if err != nil || src.DeletedAt != nil {
			codeErr(w, http.StatusNotFound, "file_not_found", "not found in the drive: "+p)
			return
		}
		if !spec.AcceptsName(src.Name) {
			codeErr(w, http.StatusUnsupportedMediaType, "input_type_not_accepted", src.Name+": this input accepts: "+strings.Join(spec.Accept, ", "))
			return
		}
		dst := folder + "/" + src.Name
		if src.Path != dst { // a copy: the agents read the project folder, and the original stays untouched
			rc, _, err := s.Drive.Open(r.Context(), src, 0)
			if err != nil {
				s.driveFail(w, err)
				return
			}
			data, err := io.ReadAll(io.LimitReader(rc, limit+1))
			_ = rc.Close()
			if err != nil || int64(len(data)) > limit {
				codeErr(w, http.StatusRequestEntityTooLarge, "too_large", "the file exceeds the upload limit")
				return
			}
			if _, err := s.Drive.Write(r.Context(), s.drivePrincipal(r), drive.WriteInput{Path: dst, Data: data, Source: "project_input", Message: "copy of " + src.Path}); err != nil {
				s.driveFail(w, err)
				return
			}
		}
		v, err = s.attachFile(r, rec.ID, spec.Key, folder, projects.InputFileRef{Path: dst, Name: src.Name, Size: src.Size,
			Source: projects.InputSourceDrive, AddedBy: s.actor(r), AddedAt: time.Now().UTC()})
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) valueProjectInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value string `json:"value"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Projects.SetInputValue(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "key"), body.Value)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) removeProjectInputFile(w http.ResponseWriter, r *http.Request) {
	if !s.needDrive(w) {
		return
	}
	rec, spec, ok := s.pickInput(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	var removed bool
	v, err := s.Projects.UpdateInputs(r.Context(), rec.ID, func(rc *projects.Record) error {
		in := projects.InputByKey(rc, spec.Key)
		if in == nil {
			return domain.ErrNotFound
		}
		keep := in.Files[:0:0]
		for _, f := range in.Files {
			if f.Path == p {
				removed = true
				continue
			}
			keep = append(keep, f)
		}
		in.Files = keep
		return nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if !removed {
		codeErr(w, http.StatusNotFound, "file_not_attached", "that file is not attached to this input")
		return
	}
	// The file in Entradas is a copy made for this project: it goes to the trash (reversible). Best effort.
	if fr, err := s.Drive.FileByPath(r.Context(), p); err == nil && strings.Contains(fr.Path, "/"+projects.InputsFolderName+"/") {
		_ = s.Drive.DeleteFile(r.Context(), s.drivePrincipal(r), fr.ID)
	}
	writeJSON(w, http.StatusOK, v)
}

// demoProjectInputs loads the sample dataset of the template into the inputs that are still missing.
func (s *server) demoProjectInputs(w http.ResponseWriter, r *http.Request) {
	if !s.needDrive(w) {
		return
	}
	rec, err := s.Projects.Record(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if rec.Status != projects.StatusDraft {
		codeErr(w, http.StatusConflict, "not_draft", "the inputs can only change while the project is a draft")
		return
	}
	if rec.DemoSet == "" || !demodata.Has(rec.DemoSet) {
		codeErr(w, http.StatusNotFound, "no_demo_data", "this project has no sample data")
		return
	}
	params := demodata.Params{Locale: rec.Locale}
	if rec.DemoParams != nil {
		params = *rec.DemoParams
	}
	set, err := demodata.Generate(rec.DemoSet, params)
	if err != nil {
		s.fail(w, err)
		return
	}
	folder, err := s.ensureInputsFolder(r, rec)
	if err != nil {
		s.driveFail(w, err)
		return
	}
	view := projects.InputsOf(rec)
	open := map[string]bool{} // inputs that still need something
	for _, it := range view.Items {
		open[it.Key] = !it.Satisfied
	}
	type written struct {
		input string
		ref   projects.InputFileRef
	}
	var done []written
	for _, f := range set.Files {
		if f.Input != "" && !open[f.Input] {
			continue // not an input of this project, or already satisfied by the user
		}
		res, err := s.Drive.Write(r.Context(), s.drivePrincipal(r), drive.WriteInput{Path: folder + "/" + f.Name, Data: f.Data, Source: "demo_data", Message: "sample data " + rec.DemoSet})
		if err != nil {
			s.driveFail(w, err)
			return
		}
		if f.Input != "" {
			done = append(done, written{f.Input, projects.InputFileRef{Path: res.File.Path, Name: res.File.Name, Size: res.File.Size,
				Source: projects.InputSourceDemo, AddedBy: s.actor(r), AddedAt: time.Now().UTC()}})
		}
	}
	v, err := s.Projects.UpdateInputs(r.Context(), rec.ID, func(rc *projects.Record) error {
		rc.InputsFolder, rc.DemoData = folder, true
		for _, d := range done {
			if in := projects.InputByKey(rc, d.input); in != nil {
				if err := projects.AddInputFile(in, d.ref); err != nil {
					return err
				}
			}
		}
		for k, val := range set.Values {
			if in := projects.InputByKey(rc, k); in != nil && !in.Done() && in.Value == "" {
				in.Value = val
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
