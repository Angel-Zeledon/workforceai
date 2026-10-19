package peoplechat

import (
	"strings"
	"unicode"

	"aiworkforce/backend/internal/auth"
)

// Actor is the person doing something: identity and role come from the verified session, never from the request body.
type Actor struct {
	UserID string
	Role   auth.Role
}

func (a Actor) isAdmin() bool { return a.Role == auth.RoleOwner || a.Role == auth.RoleAdmin }

func isMember(ms []Member, kind, id string) (Member, bool) {
	for _, m := range ms {
		if m.Kind == kind && m.ID == id {
			return m, true
		}
	}
	return Member{}, false
}

// CanCreate says whether the actor may create a channel of this kind: the all-hands, department and project
// channels are for admins and owners; any member of the organization (not a viewer) may open groups and DMs.
func CanCreate(kind string, a Actor) bool {
	switch kind {
	case KindOrg, KindDepartment, KindProject:
		return a.Role.Has(auth.PermMembersManage)
	case KindGroup, KindDM:
		return a.Role.Has(auth.PermConversationsPost)
	}
	return false
}

// CanRead says whether the actor sees the channel and its history.
//   - org: every person of the organization (viewers included);
//   - department / project: its members, and admins/owners (they supervise the shared spaces);
//   - group / dm: only its members (an admin does NOT read other people's private conversations).
func CanRead(ch Channel, ms []Member, a Actor) bool {
	if a.UserID == "" || !a.Role.Has(auth.PermConversationsRead) {
		return false
	}
	switch ch.Kind {
	case KindOrg:
		return true
	case KindDepartment, KindProject:
		_, in := isMember(ms, MemberUser, a.UserID)
		return in || a.isAdmin()
	case KindGroup, KindDM:
		_, in := isMember(ms, MemberUser, a.UserID)
		return in
	}
	return false
}

// CanPost: read access plus the right to write (viewers only read), on a channel that is not archived. Admins who
// only supervise a department/project channel must be members to write in it.
func CanPost(ch Channel, ms []Member, a Actor) bool {
	if ch.Archived || !a.Role.Has(auth.PermConversationsPost) || !CanRead(ch, ms, a) {
		return false
	}
	if ch.Kind == KindOrg {
		return true
	}
	_, in := isMember(ms, MemberUser, a.UserID)
	return in
}

// CanManage says whether the actor may add/remove members and archive: never on a DM (it is always the two people);
// on the other channels the channel owner, or an admin/owner of the organization.
func CanManage(ch Channel, ms []Member, a Actor) bool {
	if ch.Kind == KindDM || ch.Kind == KindOrg || a.UserID == "" {
		return false
	}
	if a.isAdmin() && a.Role.Has(auth.PermMembersManage) {
		return true
	}
	m, in := isMember(ms, MemberUser, a.UserID)
	return in && m.Role == "owner"
}

// CanLeave: anyone but a DM participant can leave (a member removing themselves is not a management action).
func CanLeave(ch Channel) bool { return ch.Kind != KindDM && ch.Kind != KindOrg }

// Audience lists the user ids whose sockets receive a message of the channel; nil means the whole organization
// (the all-hands channel). Agents have no sockets.
func Audience(ch Channel, ms []Member) []string {
	if ch.Kind == KindOrg {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if m.Kind == MemberUser {
			out = append(out, m.ID)
		}
	}
	if len(out) == 0 {
		out = []string{} // never nil: an empty audience must not widen to the whole org
	}
	return out
}

// Candidate is someone who can be mentioned.
type Candidate struct {
	Kind string
	ID   string
	Name string
}

var foldAccents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ñ", "n", "ç", "c")

// fold lowercases and removes the accents of the Latin letters used in Spanish and Portuguese names, so that
// "@garcia" mentions "García".
func fold(s string) string { return foldAccents.Replace(strings.ToLower(s)) }

func slug(s string, sep string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range fold(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevSep = false
		case !prevSep && b.Len() > 0:
			b.WriteString(sep)
			prevSep = true
		}
	}
	return strings.TrimSuffix(b.String(), sep)
}

// keys are the handles that mention a candidate: full name joined ("anagarcia"), dotted ("ana.garcia"), underscored,
// the id, and the first name (only when it is unambiguous among the candidates).
func (c Candidate) keys() []string {
	k := []string{slug(c.Name, ""), slug(c.Name, "."), slug(c.Name, "_"), slug(c.Name, "-"), strings.ToLower(c.ID)}
	return k
}

func firstName(name string) string {
	f := strings.Fields(name)
	if len(f) == 0 {
		return ""
	}
	return slug(f[0], "")
}

func handleChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'
}

// ParseMentions finds the @handles in body and resolves them against the candidates. A handle that matches nobody or
// is ambiguous (two candidates share it) mentions nobody; the same person is returned once.
func ParseMentions(body string, cands []Candidate) []Mention {
	first := map[string]int{}
	for _, c := range cands {
		if f := firstName(c.Name); f != "" {
			first[f]++
		}
	}
	exact := map[string][]int{}
	for i, c := range cands {
		for _, k := range c.keys() {
			if k != "" {
				if l := exact[k]; len(l) == 0 || l[len(l)-1] != i {
					exact[k] = append(exact[k], i)
				}
			}
		}
	}
	var out []Mention
	seen := map[string]bool{}
	rs := []rune(body)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '@' || (i > 0 && handleChar(rs[i-1])) {
			continue // not a handle start (an e-mail address like a@b is not a mention)
		}
		j := i + 1
		for j < len(rs) && handleChar(rs[j]) {
			j++
		}
		h := fold(strings.TrimRight(string(rs[i+1:j]), ".-_"))
		if h == "" {
			continue
		}
		idx := -1
		if l := exact[h]; len(l) == 1 {
			idx = l[0]
		} else if len(l) == 0 {
			for n, c := range cands {
				if f := firstName(c.Name); f == h && first[f] == 1 {
					idx = n
				}
			}
		}
		if idx < 0 {
			continue
		}
		c := cands[idx]
		if key := c.Kind + ":" + c.ID; !seen[key] {
			seen[key] = true
			out = append(out, Mention{Kind: c.Kind, ID: c.ID})
		}
		i = j - 1
	}
	return out
}

// CleanBody validates a message body: trimmed, non-empty, within MaxBody runes, without control characters other
// than newline and tab.
func CleanBody(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrInvalid
	}
	if len([]rune(s)) > MaxBody {
		return "", ErrInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", ErrInvalid
		}
	}
	return s, nil
}
