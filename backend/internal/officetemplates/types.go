// Package officetemplates is F3 "save a department as a template and take it to another company"
// (docs/architecture/office-templates.md): a versioned JSON file with the same model as a ready-to-hire team of the
// catalog (F2: the people are roles, each with what they do here) extended with what an office made its own: the
// names, instructions and tone of its people, the skills they use, its own work templates, schedules and goals.
//
// The file is built from an ALLOW-LIST: every field is copied one by one from the types of this package, never by
// copying an internal structure. Ids, connections, credentials, memory, drive files, people, customers, audit, chats,
// autonomy and local paths have no field here, so they cannot travel. Importing parses the file strictly (unknown
// fields, sizes and counts are refused) and runs the texts through the same filters as the rest of the product.
package officetemplates

import (
	"errors"
	"time"
)

// Format is the identifier of the file; FormatVersion the version of its schema this build reads and writes.
const (
	Format        = "aiw.office-template"
	FormatVersion = 1
)

// Kind of template.
const (
	KindDepartment = "department" // one department
	KindCompany    = "company"    // the whole company (every department the exporter can see)
)

// Limits of a template file (also the limits of the import).
const (
	MaxFileBytes        = 3 << 20 // the whole file
	MaxDepartments      = 12
	MaxAgents           = 20 // the office cap: a template never brings more people than an office holds
	MaxSkills           = 20
	MaxWorkflows        = 10
	MaxSchedules        = 20
	MaxGoals            = 10
	MaxName             = 80
	MaxText             = 280
	MaxDoes             = 400
	MaxResponsibilities = 10
	MaxResponsibility   = 200
	MaxScheduleParams   = 10
	MaxParamValue       = 200
	// MaxStoredPerOrg is how many imported templates an organization keeps.
	MaxStoredPerOrg = 30
)

// ErrTooLarge marks a file over MaxFileBytes.
var ErrTooLarge = errors.New("template file too large")

// Template is the file. Every list is present (possibly empty) so the shape is stable.
type Template struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	Kind          string `json:"kind"`
	// Name, For and Solves are the team texts of F2 (name, who it is for, what it solves).
	Name        string       `json:"name"`
	For         string       `json:"for,omitempty"`
	Solves      string       `json:"solves,omitempty"`
	Departments []Department `json:"departments"`
	Agents      []Agent      `json:"agents"`
	Skills      []Skill      `json:"skills"`
	Workflows   []Workflow   `json:"workflows"`
	Schedules   []Schedule   `json:"schedules"`
	Goals       []Goal       `json:"goals"`
}

// Department is a department of the template: a key the rest of the file points at and its name. "general" is the
// company's General office.
type Department struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// GeneralKey is the key of the General office in a template.
const GeneralKey = "general"

// Agent is one person of the template: the role (as in a catalog team) and what this office made its own.
type Agent struct {
	Department string `json:"department"`
	Role       string `json:"role"`
	Does       string `json:"does,omitempty"`
	// Name is the name the person had; the importing company keeps it when it is free there.
	Name string `json:"name,omitempty"`
	// Work profile: extra instructions, creativity, tone and skill tags.
	Instructions string   `json:"instructions,omitempty"`
	Creativity   string   `json:"creativity,omitempty"`
	Tone         string   `json:"tone,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	// Responsibilities of the job description.
	Responsibilities []string `json:"responsibilities,omitempty"`
	// Skills are the names of the library skills assigned to the person.
	Skills []string `json:"skills,omitempty"`
}

// Skill is a library skill in the Agent Skills format: the SKILL.md and the text files of its active version.
type Skill struct {
	Name    string      `json:"name"`
	SkillMD string      `json:"skill_md"`
	Files   []SkillFile `json:"files,omitempty"`
}

// SkillFile is a text resource of a skill.
type SkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Workflow is an organization work template (G2) with the agents of its steps written as ROLES.
type Workflow struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Category    string      `json:"category,omitempty"`
	Params      []Param     `json:"params,omitempty"`
	Inputs      []Input     `json:"inputs,omitempty"`
	Objectives  []Objective `json:"objectives"`
}

// Param is a parameter of a work template.
type Param struct {
	Key      string `json:"key"`
	Label    string `json:"label,omitempty"`
	Default  string `json:"default,omitempty"`
	Type     string `json:"type,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	Min      int    `json:"min,omitempty"`
	Max      int    `json:"max,omitempty"`
}

// Input is a piece of data a work template needs before it starts.
type Input struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"`
	Accept      []string `json:"accept,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Example     string   `json:"example,omitempty"`
	Options     []string `json:"options,omitempty"`
	MinFiles    int      `json:"min_files,omitempty"`
	PerParam    string   `json:"per_param,omitempty"`
}

// Objective is a phase of a work template.
type Objective struct {
	Key       string  `json:"key"`
	Title     string  `json:"title"`
	Workflows []Block `json:"workflows"`
}

// Block is a workflow (a group of steps) of an objective.
type Block struct {
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Repeat []string `json:"repeat,omitempty"`
	Nodes  []Node   `json:"nodes"`
}

// Node is a step. Role is the role of the agent that does it (never an agent id).
type Node struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind,omitempty"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Role        string    `json:"role,omitempty"`
	Complexity  string    `json:"complexity,omitempty"`
	Acceptance  []string  `json:"acceptance,omitempty"`
	Deps        []string  `json:"deps,omitempty"`
	Secs        float64   `json:"secs,omitempty"`
	Approval    *Approval `json:"approval,omitempty"`
	Delegate    *Delegate `json:"delegate,omitempty"`
}

// Approval is the human approval a step asks for.
type Approval struct {
	Action string `json:"action"`
	Risk   string `json:"risk"`
}

// Delegate is the sub-step a step hands to another role.
type Delegate struct {
	Role       string `json:"role"`
	Title      string `json:"title,omitempty"`
	Complexity string `json:"complexity,omitempty"`
}

// Schedule is a simple recurring task (a catalog workflow template at a time of day). It is imported switched off.
type Schedule struct {
	Template string            `json:"template"`
	Params   map[string]string `json:"params,omitempty"`
	Hour     int               `json:"hour"`
	Minute   int               `json:"minute"`
	Timezone string            `json:"timezone"`
	Weekdays []int             `json:"weekdays,omitempty"`
}

// Goal is an active goal of the company or of a department of the template.
type Goal struct {
	Department  string `json:"department,omitempty"` // "" = the company
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Period      string `json:"period,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
}

// Stored is a template imported into an organization (it shows in the Teams gallery, "Your templates").
type Stored struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	Template    Template  `json:"template"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}
