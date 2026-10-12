package skills

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/sanitize"
)

// Actor is who performs a change. Admin is true for owners and administrators
// (and for the open demo): their edits to an active skill are their own review;
// a member's edits wait for one. Permissions themselves are checked by the API.
type Actor struct {
	ID     string
	Admin  bool
	System bool
}

// Config wires the service.
type Config struct {
	Store Store
	Drive *drive.Service
	OrgID string
	// Audit records every change (metadata only, never file content). nil = none.
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	// AgentRole tells whether an agent exists and its role (fail closed on error).
	AgentRole func(ctx context.Context, org, agentID string) (role string, err error)
	// RoleDefaults are the skill names a role template assigns by default.
	RoleDefaults func(role string) []string
	// Locale is the organization language for the starter skills (default "es").
	Locale func(ctx context.Context) string
	Now    func() time.Time
	Log    *slog.Logger
}

// Service is the skill library use-case layer.
type Service struct {
	cfg    Config
	seeded sync.Map // org -> struct{}
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "skl_" + hex.EncodeToString(b)
}

func (s *Service) audit(ctx context.Context, org string, a Actor, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, a.ID, action, id, d)
	}
}

func (s *Service) principal(a Actor) drive.Principal {
	if a.System {
		return drive.System()
	}
	return drive.Human(a.ID)
}

func mapStore(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return notFound("%s not found", what)
	case errors.Is(err, domain.ErrConflict):
		return conflict("conflict", "%s was changed by someone else; reload and try again", what)
	}
	return err
}

// VersionDir is the drive folder of a version.
func VersionDir(name string, n int) string { return fmt.Sprintf("%s/%s/v%d", DriveRoot, name, n) }

// ---- package <-> drive ----

func sum(b []byte) string { return objstore.SumBytes(b) }

// writeFile creates a drive file that must not exist yet (versions are
// immutable). A retry after a half-finished write succeeds when the existing
// bytes are identical.
func (s *Service) writeFile(ctx context.Context, p drive.Principal, path string, data []byte, msg string) error {
	zero := 0
	_, err := s.cfg.Drive.Write(ctx, p, drive.WriteInput{Path: path, Data: data, Message: msg, Expected: &zero, Source: "skill"})
	if err == nil {
		return nil
	}
	if f, e2 := s.cfg.Drive.FileByPath(ctx, path); e2 == nil {
		if cur, e3 := s.readDrive(ctx, f); e3 == nil && sum(cur) == sum(data) {
			return nil
		}
	}
	return err
}

func (s *Service) readDrive(ctx context.Context, f drive.File) ([]byte, error) {
	rc, _, err := s.cfg.Drive.Open(ctx, f, 0)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
}

func (s *Service) writePackage(ctx context.Context, org string, a Actor, name string, n int, pkg Package) ([]FileMeta, int64, error) {
	ctx = application.WithOrg(ctx, org)
	dir := VersionDir(name, n)
	msg := fmt.Sprintf("skill %s v%d", name, n)
	all := append([]File{{Path: SkillFile, Content: pkg.SkillMD}}, pkg.Files...)
	var metas []FileMeta
	var total int64
	for _, f := range all {
		data := []byte(f.Content)
		if err := s.writeFile(ctx, s.principal(a), dir+"/"+f.Path, data, msg); err != nil {
			return nil, 0, err
		}
		metas = append(metas, FileMeta{Path: f.Path, Size: int64(len(data)), SHA256: sum(data), Script: IsScript(f.Path)})
		total += int64(len(data))
	}
	return metas, total, nil
}

// readPackage reads a version back from the drive and verifies every file
// against the manifest: a file edited or removed behind the library's back
// makes the version unusable instead of silently changing what agents follow.
func (s *Service) readPackage(ctx context.Context, org string, sk Skill, v Version) (Package, error) {
	ctx = application.WithOrg(ctx, org)
	dir := VersionDir(sk.Name, v.Version)
	var skillMD string
	var files []File
	for _, m := range v.Files {
		f, err := s.cfg.Drive.FileByPath(ctx, dir+"/"+m.Path)
		if err != nil {
			return Package{}, &Error{Code: "files_missing", Kind: domain.ErrNotFound, Msg: "a file of this skill version is missing from the drive: " + m.Path}
		}
		b, err := s.readDrive(ctx, f)
		if err != nil {
			return Package{}, err
		}
		if sum(b) != m.SHA256 {
			return Package{}, &Error{Code: "integrity", Kind: domain.ErrConflict, Msg: "a file of this skill version was changed outside the library: " + m.Path}
		}
		if m.Path == SkillFile {
			skillMD = string(b)
		} else {
			files = append(files, File{Path: m.Path, Content: string(b)})
		}
	}
	return Build(skillMD, files)
}

// scan produces the warnings a reviewer must see.
func scan(pkg Package, notes []string) []Warning {
	out := []Warning{}
	all := append([]File{{Path: SkillFile, Content: pkg.SkillMD}}, pkg.Files...)
	for _, f := range all {
		if score, hits := sanitize.ScoreInjection(f.Content); score >= sanitize.InjectionThreshold {
			out = append(out, Warning{Code: "injection", Path: f.Path, Hits: hits})
		}
	}
	for _, f := range pkg.Files {
		if IsScript(f.Path) {
			out = append(out, Warning{Code: "script", Path: f.Path})
		}
	}
	if len(pkg.Front.AllowedTools) > 0 {
		out = append(out, Warning{Code: "allowed_tools", Detail: strings.Join(pkg.Front.AllowedTools, " ")})
	}
	for _, n := range notes {
		out = append(out, Warning{Code: "import_note", Detail: n})
	}
	return out
}

// ---- inputs and views ----

// Input is a skill as edited in the UI or sent to the API.
type Input struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Instructions string            `json:"instructions"`
	AllowedTools []string          `json:"allowed_tools"`
	License      string            `json:"license"`
	Metadata     map[string]string `json:"metadata"`
	Files        []File            `json:"files"`
	Changelog    string            `json:"changelog"`
	// Source is "created" (default) or "from_work" (saved from a finished task).
	Source string `json:"source"`
}

func (in Input) toPackage() (Package, error) {
	desc := strings.Join(strings.Fields(in.Description), " ")
	fm := Frontmatter{Name: strings.TrimSpace(in.Name), Description: desc, License: strings.TrimSpace(in.License),
		AllowedTools: cleanTools(in.AllowedTools), Metadata: in.Metadata, Extra: map[string]string{}}
	if !ValidName(fm.Name) {
		return Package{}, invalid("name_invalid", "name must match ^[a-z0-9-]{1,64}$")
	}
	if desc == "" {
		return Package{}, invalid("description_required", "description is required")
	}
	if strings.TrimSpace(in.Instructions) == "" {
		return Package{}, invalid("instructions_required", "instructions are required")
	}
	for _, f := range in.Files {
		if strings.EqualFold(f.Path, SkillFile) {
			return Package{}, invalid("path_invalid", "SKILL.md is edited through the instructions field")
		}
	}
	return Build(RenderSkillMD(fm, in.Instructions), in.Files)
}

func cleanTools(in []string) []string {
	var out []string
	for _, t := range in {
		t = strings.Join(strings.Fields(t), "")
		if t != "" && utf8.RuneCountInString(t) <= 60 && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func cleanChangelog(c string) string {
	c = strings.Join(strings.Fields(strings.ToValidUTF8(c, "")), " ")
	if r := []rune(c); len(r) > MaxChangelog {
		c = string(r[:MaxChangelog])
	}
	return c
}

// Detail is a skill with the content of one of its versions.
type Detail struct {
	Skill
	Version      Version           `json:"version"`
	Instructions string            `json:"instructions"`
	SkillMD      string            `json:"skill_md"`
	License      string            `json:"license,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	AllowedTools []string          `json:"allowed_tools"`
	Files        []File            `json:"files"` // resource files with their text
	Assignments  []Assignment      `json:"assignments"`
}

func (s *Service) detail(ctx context.Context, org string, sk Skill, v Version) (Detail, error) {
	pkg, err := s.readPackage(ctx, org, sk, v)
	if err != nil {
		return Detail{}, err
	}
	as, _ := s.cfg.Store.AssignmentsOfSkill(ctx, org, sk.ID)
	if as == nil {
		as = []Assignment{}
	}
	files := pkg.Files
	if files == nil {
		files = []File{}
	}
	tools := pkg.Front.AllowedTools
	if tools == nil {
		tools = []string{}
	}
	return Detail{Skill: sk, Version: v, Instructions: pkg.Body, SkillMD: pkg.SkillMD, License: pkg.Front.License,
		Metadata: pkg.Front.Metadata, AllowedTools: tools, Files: files, Assignments: as}, nil
}

// ---- reading ----

// List returns the live skills of the organization.
func (s *Service) List(ctx context.Context) ([]Skill, error) {
	org := s.org(ctx)
	s.Ensure(ctx, org)
	l, err := s.cfg.Store.ListSkills(ctx, org)
	if l == nil {
		l = []Skill{}
	}
	return l, err
}

func (s *Service) skill(ctx context.Context, org, id string) (Skill, error) {
	sk, err := s.cfg.Store.GetSkill(ctx, org, id)
	return sk, mapStore(err, "skill")
}

// Get returns a skill with the content of a version (0 = latest).
func (s *Service) Get(ctx context.Context, id string, version int) (Detail, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Detail{}, err
	}
	if version <= 0 {
		version = sk.CurrentVersion
	}
	v, err := s.cfg.Store.VersionOf(ctx, org, id, version)
	if err != nil {
		return Detail{}, mapStore(err, "version")
	}
	return s.detail(ctx, org, sk, v)
}

// Versions lists the history, newest first.
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	org := s.org(ctx)
	if _, err := s.skill(ctx, org, id); err != nil {
		return nil, err
	}
	vs, err := s.cfg.Store.Versions(ctx, org, id)
	if err != nil {
		return nil, err
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Version > vs[j].Version })
	return append([]Version{}, vs...), nil
}

// ---- writing ----

// Create adds a skill as a draft (version 1). A skill that was deleted and has
// the same name is brought back with a new version.
func (s *Service) Create(ctx context.Context, a Actor, in Input) (Detail, error) {
	s.Ensure(ctx, s.org(ctx))
	pkg, err := in.toPackage()
	if err != nil {
		return Detail{}, err
	}
	src := SourceCreated
	if in.Source == SourceFromWork {
		src = SourceFromWork
	}
	return s.createFrom(ctx, a, pkg, src, StatusDraft, cleanChangelog(in.Changelog), false)
}

func (s *Service) createFrom(ctx context.Context, a Actor, pkg Package, source, status, changelog string, builtin bool) (Detail, error) {
	org := s.org(ctx)
	name := pkg.Front.Name
	now := s.now()
	existing, err := s.cfg.Store.SkillByName(ctx, org, name)
	if err == nil && existing.DeletedAt == nil {
		return Detail{}, conflict("name_taken", "a skill named %q already exists", name)
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Detail{}, err
	}
	n, restoredDeleted := 1, err == nil
	if restoredDeleted {
		n = existing.CurrentVersion + 1
	}
	metas, total, err := s.writePackage(ctx, org, a, name, n, pkg)
	if err != nil {
		return Detail{}, err
	}
	if changelog == "" {
		changelog = map[string]string{SourceCreated: "Created", SourceImported: "Imported", SourceFromWork: "Saved from a finished task"}[source]
	}
	v := Version{Version: n, Author: a.ID, CreatedAt: now, Changelog: changelog, Description: pkg.Front.Description,
		AllowedTools: nonNil(pkg.Front.AllowedTools), Files: metas, Warnings: scan(pkg, pkg.Notes), Source: source, Bytes: total}
	sk := Skill{ID: newID(), Name: name, Description: pkg.Front.Description, Status: status, Source: source, Builtin: builtin,
		CurrentVersion: n, CreatedBy: a.ID, CreatedAt: now, UpdatedAt: now}
	if status == StatusActive {
		sk.ActiveVersion, sk.ActiveDescription = n, pkg.Front.Description
		sk.ReviewedBy, sk.ReviewedAt = a.ID, &now
	}
	v.SkillID = sk.ID
	if restoredDeleted {
		sk = existing
		sk.DeletedAt, sk.Description, sk.Status, sk.Source, sk.Builtin = nil, pkg.Front.Description, status, source, builtin
		sk.CurrentVersion, sk.ActiveVersion, sk.ActiveDescription, sk.UpdatedAt = n, 0, "", now
		sk.ReviewedBy, sk.ReviewedAt, sk.ReviewNote = "", nil, ""
		v.SkillID = sk.ID
		err = s.cfg.Store.AddVersion(ctx, org, existing.CurrentVersion, sk, v)
	} else {
		err = s.cfg.Store.CreateSkill(ctx, org, sk, v)
	}
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return Detail{}, conflict("name_taken", "a skill named %q already exists", name)
		}
		return Detail{}, err
	}
	act := "skill.created"
	if source == SourceImported {
		act = "skill.imported"
	}
	s.audit(ctx, org, a, act, sk.ID, map[string]any{"name": name, "version": n, "source": source, "status": status, "files": len(metas), "warnings": warningCodes(v.Warnings)})
	return s.detail(ctx, org, sk, v)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func warningCodes(ws []Warning) []string {
	out := []string{}
	for _, w := range ws {
		if !slices.Contains(out, w.Code) {
			out = append(out, w.Code)
		}
	}
	return out
}

// Update saves a new version. A member's change to an active skill waits for a
// review (agents keep the approved version meanwhile); an administrator's change
// is itself the review. Identical content creates nothing.
func (s *Service) Update(ctx context.Context, a Actor, id string, in Input) (Detail, bool, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Detail{}, false, err
	}
	if in.Name != "" && in.Name != sk.Name {
		return Detail{}, false, invalid("name_immutable", "the name of a skill cannot change; create a new skill instead")
	}
	in.Name = sk.Name
	pkg, err := in.toPackage()
	if err != nil {
		return Detail{}, false, err
	}
	cur, err := s.cfg.Store.VersionOf(ctx, org, id, sk.CurrentVersion)
	if err != nil {
		return Detail{}, false, mapStore(err, "version")
	}
	if samePackage(cur, pkg) {
		d, err := s.detail(ctx, org, sk, cur)
		return d, false, err
	}
	d, err := s.addVersion(ctx, a, sk, pkg, cleanChangelog(in.Changelog), 0, nil)
	return d, true, err
}

func samePackage(v Version, pkg Package) bool {
	want := map[string]string{SkillFile: sum([]byte(pkg.SkillMD))}
	for _, f := range pkg.Files {
		want[f.Path] = sum([]byte(f.Content))
	}
	if len(want) != len(v.Files) {
		return false
	}
	for _, m := range v.Files {
		if want[m.Path] != m.SHA256 {
			return false
		}
	}
	return true
}

func (s *Service) addVersion(ctx context.Context, a Actor, sk Skill, pkg Package, changelog string, restoredFrom int, notes []string) (Detail, error) {
	org := s.org(ctx)
	n := sk.CurrentVersion + 1
	metas, total, err := s.writePackage(ctx, org, a, sk.Name, n, pkg)
	if err != nil {
		return Detail{}, err
	}
	now := s.now()
	if changelog == "" {
		changelog = "Edited"
		if restoredFrom > 0 {
			changelog = fmt.Sprintf("Restored from v%d", restoredFrom)
		}
	}
	v := Version{SkillID: sk.ID, Version: n, Author: a.ID, CreatedAt: now, Changelog: changelog, Description: pkg.Front.Description,
		AllowedTools: nonNil(pkg.Front.AllowedTools), Files: metas, Warnings: scan(pkg, notes), Source: sk.Source, RestoredFrom: restoredFrom, Bytes: total}
	expected := sk.CurrentVersion
	sk.CurrentVersion, sk.Description, sk.UpdatedAt = n, pkg.Front.Description, now
	switch sk.Status {
	case StatusActive:
		if a.Admin || a.System {
			sk.ActiveVersion, sk.ActiveDescription, sk.ReviewedBy, sk.ReviewedAt, sk.ReviewNote = n, pkg.Front.Description, a.ID, &now, ""
		} else {
			sk.Status = StatusInReview
		}
	case StatusDisabled, StatusDraft, StatusInReview:
		// stays; a person decides
	}
	if err := s.cfg.Store.AddVersion(ctx, org, expected, sk, v); err != nil {
		return Detail{}, mapStore(err, "skill")
	}
	act := "skill.version_created"
	if restoredFrom > 0 {
		act = "skill.restored"
	}
	s.audit(ctx, org, a, act, sk.ID, map[string]any{"name": sk.Name, "version": n, "restored_from": restoredFrom, "status": sk.Status, "warnings": warningCodes(v.Warnings)})
	return s.detail(ctx, org, sk, v)
}

// Restore makes an old version the newest one (a new version; nothing is lost).
func (s *Service) Restore(ctx context.Context, a Actor, id string, version int) (Detail, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Detail{}, err
	}
	old, err := s.cfg.Store.VersionOf(ctx, org, id, version)
	if err != nil {
		return Detail{}, mapStore(err, "version")
	}
	if version == sk.CurrentVersion {
		return Detail{}, conflict("already_current", "that is already the latest version")
	}
	pkg, err := s.readPackage(ctx, org, sk, old)
	if err != nil {
		return Detail{}, err
	}
	return s.addVersion(ctx, a, sk, pkg, "", version, nil)
}

// Submit sends a draft for review.
func (s *Service) Submit(ctx context.Context, a Actor, id string) (Skill, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Skill{}, err
	}
	if sk.Status != StatusDraft {
		return Skill{}, conflict("invalid_state", "only a draft can be sent for review")
	}
	sk.Status, sk.UpdatedAt = StatusInReview, s.now()
	if err := s.cfg.Store.UpdateSkill(ctx, org, sk); err != nil {
		return Skill{}, mapStore(err, "skill")
	}
	s.audit(ctx, org, a, "skill.submitted", sk.ID, map[string]any{"name": sk.Name, "version": sk.CurrentVersion})
	return sk, nil
}

// Review approves (activates) or rejects the latest version. version must be
// the one the reviewer saw. Only call it for administrators.
func (s *Service) Review(ctx context.Context, a Actor, id string, approve bool, version int, note string) (Skill, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Skill{}, err
	}
	if version != sk.CurrentVersion {
		return Skill{}, conflict("stale_review", "the skill has a newer version (v%d) than the one reviewed (v%d)", sk.CurrentVersion, version)
	}
	now := s.now()
	note = cleanChangelog(note)
	if approve {
		if sk.Status == StatusActive && sk.ActiveVersion == sk.CurrentVersion {
			return Skill{}, conflict("already_active", "this version is already active")
		}
		v, err := s.cfg.Store.VersionOf(ctx, org, id, version)
		if err != nil {
			return Skill{}, mapStore(err, "version")
		}
		if _, err := s.readPackage(ctx, org, sk, v); err != nil { // never activate files that do not match
			return Skill{}, err
		}
		sk.Status, sk.ActiveVersion, sk.ActiveDescription = StatusActive, version, v.Description
		sk.ReviewNote = ""
	} else {
		if sk.Status == StatusActive && sk.ActiveVersion == sk.CurrentVersion {
			return Skill{}, conflict("invalid_state", "an active version cannot be rejected; disable the skill instead")
		}
		if sk.ActiveVersion > 0 && sk.Status != StatusDisabled {
			sk.Status = StatusActive // the previously approved version keeps working
		} else if sk.Status != StatusDisabled {
			sk.Status = StatusDraft
		}
		sk.ReviewNote = note
	}
	sk.ReviewedBy, sk.ReviewedAt, sk.UpdatedAt = a.ID, &now, now
	if err := s.cfg.Store.UpdateSkill(ctx, org, sk); err != nil {
		return Skill{}, mapStore(err, "skill")
	}
	act := "skill.approved"
	if !approve {
		act = "skill.rejected"
	}
	s.audit(ctx, org, a, act, sk.ID, map[string]any{"name": sk.Name, "version": version, "note": note})
	return sk, nil
}

// Disable stops agents from receiving the skill (reversible: approve it again).
func (s *Service) Disable(ctx context.Context, a Actor, id string) (Skill, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Skill{}, err
	}
	if sk.Status == StatusDisabled {
		return sk, nil
	}
	sk.Status, sk.UpdatedAt = StatusDisabled, s.now()
	if err := s.cfg.Store.UpdateSkill(ctx, org, sk); err != nil {
		return Skill{}, mapStore(err, "skill")
	}
	s.audit(ctx, org, a, "skill.disabled", sk.ID, map[string]any{"name": sk.Name})
	return sk, nil
}

// Delete removes the skill from the library and from every agent. The drive
// files stay (nothing is erased); creating the name again brings it back.
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteSkill(ctx, org, id, s.now()); err != nil {
		return mapStore(err, "skill")
	}
	s.audit(ctx, org, a, "skill.deleted", sk.ID, map[string]any{"name": sk.Name})
	return nil
}

// ---- import / export ----

// ImportResult is what an import did.
type ImportResult struct {
	Detail
	Notes []string `json:"notes"`
}

// Import creates a skill from an uploaded .zip or a single SKILL.md. It always
// starts in review: a person must read it and approve it. rename (optional)
// stores it under another name.
func (s *Service) Import(ctx context.Context, a Actor, data []byte, rename string) (ImportResult, error) {
	s.Ensure(ctx, s.org(ctx))
	var pkg Package
	var err error
	if len(data) >= 4 && string(data[:4]) == "PK\x03\x04" {
		pkg, err = ReadZip(data)
	} else {
		pkg, err = ReadSkillMD(data)
	}
	if err != nil {
		return ImportResult{}, err
	}
	if rename = strings.TrimSpace(rename); rename != "" && rename != pkg.Front.Name {
		if !ValidName(rename) {
			return ImportResult{}, invalid("name_invalid", "name must match ^[a-z0-9-]{1,64}$")
		}
		pkg.Front.Name = rename
		pkg.SkillMD = RenderSkillMD(pkg.Front, pkg.Body)
		pkg.Notes = append(pkg.Notes, "imported under the name "+rename)
	}
	d, err := s.createFrom(ctx, a, pkg, SourceImported, StatusInReview, "Imported", false)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Detail: d, Notes: nonNil(pkg.Notes)}, nil
}

// Export returns a version as a .zip in the standard Agent Skills layout.
func (s *Service) Export(ctx context.Context, a Actor, id string, version int) ([]byte, string, error) {
	pkg, sk, n, err := s.exportPackage(ctx, id, version)
	if err != nil {
		return nil, "", err
	}
	b, err := pkg.Zip()
	if err != nil {
		return nil, "", err
	}
	s.audit(ctx, s.org(ctx), a, "skill.exported", sk.ID, map[string]any{"name": sk.Name, "version": n, "format": "zip"})
	return b, sk.Name + ".zip", nil
}

// ExportMD returns only the SKILL.md of a version.
func (s *Service) ExportMD(ctx context.Context, a Actor, id string, version int) ([]byte, string, error) {
	pkg, sk, n, err := s.exportPackage(ctx, id, version)
	if err != nil {
		return nil, "", err
	}
	s.audit(ctx, s.org(ctx), a, "skill.exported", sk.ID, map[string]any{"name": sk.Name, "version": n, "format": "md"})
	return []byte(pkg.SkillMD), "SKILL.md", nil
}

func (s *Service) exportPackage(ctx context.Context, id string, version int) (Package, Skill, int, error) {
	org := s.org(ctx)
	sk, err := s.skill(ctx, org, id)
	if err != nil {
		return Package{}, Skill{}, 0, err
	}
	if version <= 0 {
		version = sk.CurrentVersion
	}
	v, err := s.cfg.Store.VersionOf(ctx, org, id, version)
	if err != nil {
		return Package{}, Skill{}, 0, mapStore(err, "version")
	}
	pkg, err := s.readPackage(ctx, org, sk, v)
	return pkg, sk, version, err
}

func withOrg(ctx context.Context, org string) context.Context { return application.WithOrg(ctx, org) }
