package agenthistory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"aiworkforce/backend/internal/policy"
)

// SetSecret records a secret field by its fingerprint only (an empty value removes it).
func (c *Config) SetSecret(name, value string) {
	if value == "" {
		delete(c.Secrets, name)
		return
	}
	if c.Secrets == nil {
		c.Secrets = map[string]string{}
	}
	h := sha256.Sum256([]byte(value))
	c.Secrets[name] = hex.EncodeToString(h[:8])
}

func sortedStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Normalize sorts the lists and fills empty ones so that equal configurations marshal to equal bytes.
func (c Config) Normalize() Config {
	c.Tags = sortedStrings(c.Tags)
	c.Tools = sortedStrings(c.Tools)
	if c.Creativity == "" {
		c.Creativity = "medium"
	}
	skills := []SkillRef{}
	for _, s := range c.Skills {
		if s.ID != "" && !slices.ContainsFunc(skills, func(x SkillRef) bool { return x.ID == s.ID }) {
			skills = append(skills, s)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	c.Skills = skills
	if c.Permissions != nil {
		p := Permissions{Exceptions: []Exception{}, FloorLifted: sortedStrings(c.Permissions.FloorLifted)}
		for _, e := range c.Permissions.Exceptions {
			if e.Mode != policy.ModeAskIf {
				e.If = nil
			}
			p.Exceptions = append(p.Exceptions, e)
		}
		sort.Slice(p.Exceptions, func(i, j int) bool { return p.Exceptions[i].Category < p.Exceptions[j].Category })
		c.Permissions = &p
	}
	if c.Job != nil {
		j := *c.Job
		if j.Responsibilities == nil {
			j.Responsibilities = []string{}
		}
		if len(j.Schedule) > 0 {
			var buf bytes.Buffer
			if json.Compact(&buf, j.Schedule) == nil {
				j.Schedule = buf.Bytes()
			}
		}
		c.Job = &j
	}
	if c.Access != nil {
		a := *c.Access
		a.Shares = sortedStrings(a.Shares)
		c.Access = &a
	}
	folders := []FolderGrant{}
	for _, f := range c.Folders {
		if f.Path != "" {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Path < folders[j].Path })
	c.Folders = folders
	if len(c.Secrets) == 0 {
		c.Secrets = nil
	}
	return c
}

func fieldsOf(c Config) map[string]json.RawMessage {
	raw, _ := json.Marshal(c.Normalize())
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &m)
	return m
}

// Diff compares two configurations field by field, in display order. Secret fields only say they changed.
func Diff(from, to Config) []Change {
	a, b := fieldsOf(from), fieldsOf(to)
	out := []Change{}
	for _, f := range Fields {
		if !bytes.Equal(a[f], b[f]) {
			out = append(out, Change{Field: f, From: a[f], To: b[f]})
		}
	}
	names := []string{}
	for n := range from.Secrets {
		names = append(names, n)
	}
	for n := range to.Secrets {
		if _, ok := from.Secrets[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if from.Secrets[n] != to.Secrets[n] {
			out = append(out, Change{Field: "secret:" + n, Secret: true})
		}
	}
	return out
}

// ChangedFields are the names of the changed fields.
func ChangedFields(from, to Config) []string {
	out := []string{}
	for _, c := range Diff(from, to) {
		out = append(out, c.Field)
	}
	return out
}
