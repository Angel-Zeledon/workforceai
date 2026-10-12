package projects

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Project inputs (H1): the data a large project needs before it starts. A
// template declares InputSpec entries (a trial balance per subsidiary, the FX
// rates...); the draft of a project turns them into ProjectInput entries that a
// person fills (upload, pick from the drive, sample data or a typed value).
// Launch is blocked until the required ones are satisfied or the person chooses
// to go on without them (recorded and told to the agents). The files live in the
// drive under /Projects/<project>/Entradas/; the project only keeps references.

const (
	InputFile   = "file"
	InputFolder = "folder"
	InputText   = "text"
	InputNumber = "number"
	InputSelect = "select"

	// InputsFolderName is the folder of the inputs inside the project folder.
	InputsFolderName = "Entradas"

	InputSourceUpload = "upload"
	InputSourceDrive  = "drive"
	InputSourceDemo   = "demo"

	IssueBadInput = "invalid_input"

	maxInputs        = 40
	maxInputFiles    = 60
	maxInputText     = 2000
	maxInputAccept   = 12
	maxInputOptions  = 30
	maxInputLabelLen = 120
	maxInputDescLen  = 600
)

var inputKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var acceptRe = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// InputSpec is one input a template declares.
type InputSpec struct {
	Key            string `json:"key"`
	LabelKey       string `json:"label_key,omitempty"`
	Label          string `json:"label"`
	DescriptionKey string `json:"description_key,omitempty"`
	Description    string `json:"description"`
	// Kind: file (one file), folder (several files), text, number or select.
	Kind string `json:"kind"`
	// Accept lists the file extensions allowed (without dot): csv, xlsx, pdf, md...
	Accept   []string `json:"accept,omitempty"`
	Required bool     `json:"required"`
	Example  string   `json:"example,omitempty"`
	// Options are the values of a select input.
	Options []string `json:"options,omitempty"`
	// MinFiles is the number of files that satisfy a folder input (default 1).
	MinFiles int `json:"min_files,omitempty"`
	// PerParam: one file per item of this template parameter (a number or a list),
	// e.g. one trial balance per subsidiary. It sets MinFiles when the draft is created.
	PerParam string `json:"per_param,omitempty"`
}

func (s InputSpec) files() bool { return s.Kind == InputFile || s.Kind == InputFolder }

func (s InputSpec) need() int {
	if !s.files() {
		return 0
	}
	if s.Kind == InputFile {
		return 1
	}
	return max(1, s.MinFiles)
}

// InputFileRef is one file attached to an input (a reference to a drive file).
type InputFileRef struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Source  string    `json:"source"` // upload | drive | demo
	AddedBy string    `json:"added_by,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// ProjectInput is an input of a project: the (resolved) spec plus what was provided.
type ProjectInput struct {
	InputSpec
	Files []InputFileRef `json:"files"`
	Value string         `json:"value,omitempty"`
	// Satisfied is computed when the project is read.
	Satisfied bool `json:"satisfied"`
}

// Done reports whether the input has what it needs.
func (p ProjectInput) Done() bool { return p.satisfied() }

func (p ProjectInput) satisfied() bool {
	switch p.Kind {
	case InputFile, InputFolder:
		return len(p.Files) >= p.need()
	case InputNumber:
		_, err := strconv.ParseFloat(strings.TrimSpace(p.Value), 64)
		return err == nil
	case InputSelect:
		for _, o := range p.Options {
			if o == p.Value {
				return true
			}
		}
		return false
	}
	return strings.TrimSpace(p.Value) != ""
}

// InputsWaiver records that a person chose to launch without all the required inputs.
type InputsWaiver struct {
	By      string    `json:"by,omitempty"`
	At      time.Time `json:"at"`
	Missing []string  `json:"missing"`
}

// InputsView is what the API and the draft screen read.
type InputsView struct {
	// Folder is the drive folder of the inputs (empty until something was added).
	Folder  string         `json:"folder,omitempty"`
	Items   []ProjectInput `json:"items"`
	Missing []string       `json:"missing"` // keys of the required inputs not satisfied
	// Ready: every required input is satisfied.
	Ready  bool          `json:"ready"`
	Waived *InputsWaiver `json:"waived,omitempty"`
	// DemoSet is the name of the sample dataset of the template ("" when none) and
	// DemoUsed whether it was loaded into this project.
	DemoSet  string `json:"demo_set,omitempty"`
	DemoUsed bool   `json:"demo_used,omitempty"`
}

// viewOfInputs computes satisfaction and the missing required keys.
func viewOfInputs(r Record) InputsView {
	v := InputsView{Folder: r.InputsFolder, Items: make([]ProjectInput, 0, len(r.Inputs)), Missing: []string{}, Waived: r.InputsWaived,
		DemoSet: r.DemoSet, DemoUsed: r.DemoData}
	for _, in := range r.Inputs {
		if in.Files == nil {
			in.Files = []InputFileRef{}
		}
		in.Satisfied = in.satisfied()
		if in.Required && !in.Satisfied {
			v.Missing = append(v.Missing, in.Key)
		}
		v.Items = append(v.Items, in)
	}
	v.Ready = len(v.Missing) == 0
	return v
}

// InputsOf is the view of the inputs of a record.
func InputsOf(r Record) InputsView { return viewOfInputs(r) }

// missingRequired lists the required inputs not satisfied.
func missingRequired(r Record) []string { return viewOfInputs(r).Missing }

// inputsFor resolves the inputs a draft of this template needs for its parameters.
func (t Template) inputsFor(params map[string]string, locale string) []ProjectInput {
	if len(t.Inputs) == 0 {
		return nil
	}
	out := make([]ProjectInput, 0, len(t.Inputs))
	for _, sp := range t.Inputs {
		sp = resolveSpec(sp, locale)
		if sp.PerParam != "" {
			if n := t.paramCount(sp.PerParam, params, locale); n > 0 {
				sp.MinFiles = n
			}
		}
		out = append(out, ProjectInput{InputSpec: sp, Files: []InputFileRef{}})
	}
	return out
}

func resolveSpec(sp InputSpec, locale string) InputSpec {
	if sp.LabelKey != "" && hasKey(sp.LabelKey) {
		sp.Label = tr(locale, sp.LabelKey, nil)
	}
	if sp.DescriptionKey != "" && hasKey(sp.DescriptionKey) {
		sp.Description = tr(locale, sp.DescriptionKey, nil)
	}
	return sp
}

// resolvedInputs returns the specs with their texts in the locale (for the catalog).
func (t Template) resolvedInputs(locale string) []InputSpec {
	if len(t.Inputs) == 0 {
		return nil
	}
	out := make([]InputSpec, len(t.Inputs))
	for i, sp := range t.Inputs {
		out[i] = resolveSpec(sp, locale)
	}
	return out
}

// paramCount is the number a parameter stands for: its value (number) or its item count (list).
func (t Template) paramCount(key string, params map[string]string, locale string) int {
	for _, p := range t.Params {
		if p.Key != key {
			continue
		}
		raw := strings.TrimSpace(params[key])
		if p.kind() == ParamList {
			if raw == "" {
				raw = p.Default
				if p.DefaultKey != "" && hasKey(p.DefaultKey) {
					raw = tr(locale, p.DefaultKey, nil)
				}
			}
			n := 0
			for _, it := range splitList(raw) {
				if cleanItem(it) != "" {
					n++
				}
			}
			return min(n, p.maxItems())
		}
		def, _ := strconv.Atoi(strings.TrimSpace(p.Default))
		lo, hi := p.Min, p.Max
		if hi == 0 {
			hi = 1000
		}
		return clampParam(raw, def, lo, hi)
	}
	return 0
}

// AcceptsName reports whether a file name has an allowed extension.
func (s InputSpec) AcceptsName(name string) bool {
	if len(s.Accept) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	for _, a := range s.Accept {
		if strings.EqualFold(strings.TrimPrefix(a, "."), ext) {
			return true
		}
	}
	return false
}

// validateInputSpecs checks the inputs a template declares.
func validateInputSpecs(specs []InputSpec, params []TemplateParam) []Issue {
	var issues []Issue
	bad := func(key string) { issues = append(issues, Issue{Severity: "error", NodeID: key, Code: IssueBadInput}) }
	if len(specs) > maxInputs {
		return append(issues, Issue{Severity: "error", Code: IssueTooLarge})
	}
	pk := map[string]bool{}
	for _, p := range params {
		pk[p.Key] = true
	}
	seen := map[string]bool{}
	for _, sp := range specs {
		if !inputKeyRe.MatchString(sp.Key) || seen[sp.Key] {
			bad(sp.Key)
		}
		seen[sp.Key] = true
		switch sp.Kind {
		case InputFile, InputFolder, InputText, InputNumber, InputSelect:
		default:
			bad(sp.Key)
		}
		if strings.TrimSpace(sp.Label) == "" && sp.LabelKey == "" {
			bad(sp.Key)
		}
		if len([]rune(sp.Label)) > maxInputLabelLen || len([]rune(sp.Description)) > maxInputDescLen || len([]rune(sp.Example)) > maxInputDescLen {
			bad(sp.Key)
		}
		if len(sp.Accept) > maxInputAccept {
			bad(sp.Key)
		}
		for _, a := range sp.Accept {
			if !acceptRe.MatchString(strings.ToLower(strings.TrimPrefix(a, "."))) {
				bad(sp.Key)
			}
		}
		if sp.Kind == InputSelect && (len(sp.Options) == 0 || len(sp.Options) > maxInputOptions) {
			bad(sp.Key)
		}
		if sp.Kind != InputSelect && len(sp.Options) > 0 {
			bad(sp.Key)
		}
		if !sp.files() && (len(sp.Accept) > 0 || sp.MinFiles != 0 || sp.PerParam != "") {
			bad(sp.Key)
		}
		if sp.MinFiles < 0 || sp.MinFiles > maxInputFiles {
			bad(sp.Key)
		}
		if sp.PerParam != "" && !pk[sp.PerParam] {
			bad(sp.Key)
		}
	}
	return issues
}

// cleanInputSpecs trims the texts of user-defined inputs.
func cleanInputSpecs(in []InputSpec) []InputSpec {
	out := make([]InputSpec, 0, len(in))
	for _, sp := range in {
		sp.Key = strings.TrimSpace(sp.Key)
		sp.LabelKey, sp.DescriptionKey = "", ""
		sp.Label = truncRunes(strings.TrimSpace(sp.Label), maxInputLabelLen)
		sp.Description = truncRunes(strings.TrimSpace(sp.Description), maxInputDescLen)
		sp.Example = truncRunes(strings.TrimSpace(sp.Example), maxInputDescLen)
		acc := make([]string, 0, len(sp.Accept))
		for _, a := range sp.Accept {
			acc = append(acc, strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a), ".")))
		}
		sp.Accept = acc
		if len(sp.Accept) == 0 {
			sp.Accept = nil
		}
		out = append(out, sp)
	}
	return out
}

// ---- service ----

// Record returns the stored record of a project (for the API layer that moves the files of the inputs).
func (s *Service) Record(ctx context.Context, id string) (Record, error) { return s.get(ctx, id) }

// Inputs returns the inputs of a project.
func (s *Service) Inputs(ctx context.Context, id string) (InputsView, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return InputsView{}, err
	}
	return viewOfInputs(rec), nil
}

// UpdateInputs changes the inputs of a DRAFT project under the project lock.
// fn edits the record (Inputs, InputsFolder, DemoData); the new view is returned.
func (s *Service) UpdateInputs(ctx context.Context, id string, fn func(r *Record) error) (InputsView, error) {
	rec, err := s.update(ctx, id, func(r *Record) error {
		if r.Status != StatusDraft {
			return fmt.Errorf("%w: the inputs can only change while the project is a draft", domain.ErrConflict)
		}
		return fn(r)
	})
	if err != nil {
		return InputsView{}, err
	}
	s.audit(ctx, "project.inputs_changed", id, map[string]any{"missing": len(missingRequired(rec))})
	return viewOfInputs(rec), nil
}

// InputByKey finds an input of a record (nil when absent).
func InputByKey(r *Record, key string) *ProjectInput {
	for i := range r.Inputs {
		if r.Inputs[i].Key == key {
			return &r.Inputs[i]
		}
	}
	return nil
}

// AddInputFile attaches a file reference to an input (replacing one of the same path).
func AddInputFile(in *ProjectInput, f InputFileRef) error {
	if !in.files() {
		return fmt.Errorf("%w: %q takes a value, not files", domain.ErrInvalid, in.Key)
	}
	for i := range in.Files {
		if in.Files[i].Path == f.Path {
			in.Files[i] = f
			return nil
		}
	}
	if in.Kind == InputFile {
		in.Files = []InputFileRef{f} // one file: the new one replaces the old
		return nil
	}
	if len(in.Files) >= maxInputFiles {
		return fmt.Errorf("%w: too many files in %q (at most %d)", domain.ErrInvalid, in.Key, maxInputFiles)
	}
	in.Files = append(in.Files, f)
	return nil
}

// SetInputValue sets the value of a text, number or select input.
func (s *Service) SetInputValue(ctx context.Context, id, key, value string) (InputsView, error) {
	value = strings.TrimSpace(value)
	return s.UpdateInputs(ctx, id, func(r *Record) error {
		in := InputByKey(r, key)
		if in == nil {
			return fmt.Errorf("%w: unknown input %q", domain.ErrNotFound, key)
		}
		if in.files() {
			return fmt.Errorf("%w: %q takes files, not a value", domain.ErrInvalid, key)
		}
		if len([]rune(value)) > maxInputText {
			return fmt.Errorf("%w: the value is too long", domain.ErrInvalid)
		}
		old := in.Value
		in.Value = value
		if value != "" && !in.satisfied() {
			in.Value = old
			return fmt.Errorf("%w: invalid_input_value: %q", domain.ErrInvalid, key)
		}
		return nil
	})
}

// inputsHint is appended to the description of every task of a project that has
// inputs: where they are and what is expected of the agent. The list itself
// travels in the workspace of the run (see cmd/server driveWorkspace).
func inputsHint(r Record) string {
	if len(r.Inputs) == 0 {
		return ""
	}
	waived := r.InputsWaived != nil
	provided := false
	for _, in := range r.Inputs {
		provided = provided || len(in.Files) > 0 || in.Value != ""
	}
	if !provided && !waived {
		return ""
	}
	if r.Locale == "en" {
		if waived {
			return "\n\nThe project was launched WITHOUT all the required input data (missing: " + strings.Join(r.InputsWaived.Missing, ", ") +
				"): use the inputs that exist and label every other figure as an ASSUMPTION."
		}
		return "\n\nThe project has input data (folder " + r.InputsFolder + ", see WORKSPACE > PROJECT INPUTS): compute from those files and cite file and rows."
	}
	if waived {
		return "\n\nEl proyecto se lanzó SIN todos los datos de entrada requeridos (faltan: " + strings.Join(r.InputsWaived.Missing, ", ") +
			"): usa los insumos que existan y rotula como SUPUESTO toda otra cifra."
	}
	return "\n\nEl proyecto tiene datos de entrada (carpeta " + r.InputsFolder + ", ver ESPACIO DE TRABAJO > ENTRADAS DEL PROYECTO): calcula a partir de esos archivos y cita archivo y filas."
}
