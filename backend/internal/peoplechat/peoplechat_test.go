package peoplechat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

type bus struct {
	mu sync.Mutex
	ev []domain.Event
}

func (b *bus) Publish(_ context.Context, e domain.Event) error {
	b.mu.Lock()
	b.ev = append(b.ev, e)
	b.mu.Unlock()
	return nil
}
func (b *bus) last(typ string) (domain.Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.ev) - 1; i >= 0; i-- {
		if b.ev[i].Type == typ {
			return b.ev[i], true
		}
	}
	return domain.Event{}, false
}

var (
	owner  = Actor{UserID: "u-owner", Role: auth.RoleOwner}
	admin  = Actor{UserID: "u-admin", Role: auth.RoleAdmin}
	ana    = Actor{UserID: "u-ana", Role: auth.RoleMember}
	bob    = Actor{UserID: "u-bob", Role: auth.RoleMember}
	carol  = Actor{UserID: "u-carol", Role: auth.RoleMember}
	viewer = Actor{UserID: "u-view", Role: auth.RoleViewer}
)

func setup(t *testing.T) (*Service, *bus, context.Context) {
	t.Helper()
	st := NewMemoryStore()
	st.SetPeople("o1", []Person{
		{ID: "u-owner", Name: "Olga Owner", Role: "owner"}, {ID: "u-admin", Name: "Adam Admin", Role: "admin"},
		{ID: "u-ana", Name: "Ana García", Role: "member"}, {ID: "u-bob", Name: "Bob Ruiz", Role: "member"},
		{ID: "u-carol", Name: "Carol Ana", Role: "member"}, {ID: "u-view", Name: "Vera Viewer", Role: "viewer"},
	})
	st.SetPeople("o2", []Person{{ID: "u-ana", Name: "Ana Other Org", Role: "member"}})
	b := &bus{}
	svc := &Service{Store: st, Pub: b, Agents: func(context.Context, string) ([]AgentRef, error) {
		return []AgentRef{{ID: "sales", Name: "Sofia Sales"}, {ID: "legal", Name: "Leo Legal"}}, nil
	}}
	return svc, b, context.Background()
}

func TestUnreadCount(t *testing.T) {
	cases := []struct{ last, read, want int64 }{{10, 4, 6}, {5, 5, 0}, {3, 9, 0}, {4, -2, 4}, {0, 0, 0}}
	for _, c := range cases {
		if got := Unread(c.last, c.read); got != c.want {
			t.Errorf("Unread(%d,%d)=%d want %d", c.last, c.read, got, c.want)
		}
	}
}

func TestDMKeyIsOrderIndependent(t *testing.T) {
	if DMKey("a", "b") != DMKey("b", "a") {
		t.Error("DM key must not depend on the order")
	}
}

func TestPermissionMatrix(t *testing.T) {
	members := []Member{{Kind: MemberUser, ID: ana.UserID, Role: "owner"}, {Kind: MemberUser, ID: bob.UserID, Role: "member"}, {Kind: MemberAgent, ID: "sales", Role: "member"}}
	org := Channel{Kind: KindOrg}
	dept := Channel{Kind: KindDepartment}
	grp := Channel{Kind: KindGroup}
	dm := Channel{Kind: KindDM}
	type row struct {
		name               string
		ch                 Channel
		a                  Actor
		read, post, manage bool
	}
	rows := []row{
		{"org/member", org, carol, true, true, false},
		{"org/viewer reads, cannot post", org, viewer, true, false, false},
		{"dept/member in", dept, bob, true, true, false},
		{"dept/non member", dept, carol, false, false, false},
		{"dept/admin supervises, cannot post", dept, admin, true, false, true},
		{"group/owner manages", grp, ana, true, true, true},
		{"group/member", grp, bob, true, true, false},
		{"group/outsider", grp, carol, false, false, false},
		{"group/admin does not read private groups", grp, admin, false, false, true},
		{"dm/participant", dm, bob, true, true, false},
		{"dm/admin cannot read", dm, owner, false, false, false},
		{"no user id", grp, Actor{Role: auth.RoleOwner}, false, false, false},
	}
	for _, r := range rows {
		if got := CanRead(r.ch, members, r.a); got != r.read {
			t.Errorf("%s: CanRead=%v want %v", r.name, got, r.read)
		}
		if got := CanPost(r.ch, members, r.a); got != r.post {
			t.Errorf("%s: CanPost=%v want %v", r.name, got, r.post)
		}
		if got := CanManage(r.ch, members, r.a); got != r.manage {
			t.Errorf("%s: CanManage=%v want %v", r.name, got, r.manage)
		}
	}
	arch := Channel{Kind: KindGroup, Archived: true}
	if CanPost(arch, members, ana) {
		t.Error("archived channels are read-only")
	}
}

func TestCanCreate(t *testing.T) {
	if CanCreate(KindDepartment, ana) || CanCreate(KindProject, bob) || CanCreate(KindOrg, ana) {
		t.Error("members cannot create shared channels")
	}
	if !CanCreate(KindDepartment, admin) || !CanCreate(KindProject, owner) {
		t.Error("admins create shared channels")
	}
	if !CanCreate(KindGroup, ana) || !CanCreate(KindDM, ana) {
		t.Error("members open groups and DMs")
	}
	if CanCreate(KindGroup, viewer) || CanCreate(KindDM, viewer) || CanCreate("weird", owner) {
		t.Error("viewers cannot start conversations; unknown kinds never")
	}
}

func TestAudience(t *testing.T) {
	ms := []Member{{Kind: MemberUser, ID: "a"}, {Kind: MemberAgent, ID: "sales"}, {Kind: MemberUser, ID: "b"}}
	if got := Audience(Channel{Kind: KindGroup}, ms); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("audience = %v (agents have no sockets)", got)
	}
	if Audience(Channel{Kind: KindOrg}, ms) != nil {
		t.Error("the all-hands channel goes to the whole org (nil)")
	}
	if got := Audience(Channel{Kind: KindGroup}, nil); got == nil || len(got) != 0 {
		t.Error("an empty private channel must have an empty, non-nil audience (never widen to the org)")
	}
}

func TestParseMentions(t *testing.T) {
	c := []Candidate{
		{MemberUser, "u-ana", "Ana García"}, {MemberUser, "u-bob", "Bob Ruiz"}, {MemberUser, "u-carol", "Carol Ana"},
		{MemberAgent, "sales", "Sofia Sales"}, {MemberAgent, "legal", "Leo Legal"}, {MemberUser, "u-bob2", "Bob Lee"},
	}
	got := func(s string) []Mention { return ParseMentions(s, c) }
	if m := got("hi @anagarcia and @sofia!"); len(m) != 2 || m[0].ID != "u-ana" || m[1].ID != "sales" || m[1].Kind != MemberAgent {
		t.Errorf("full and first name: %+v", m)
	}
	if m := got("@ana.garcía @ANA.GARCIA"); len(m) != 1 || m[0].ID != "u-ana" {
		t.Errorf("accents and case are folded: %+v", m)
	}
	if m := got("ping @bob"); len(m) != 0 {
		t.Errorf("an ambiguous first name must mention nobody: %+v", m)
	}
	if m := got("ping @bob.ruiz and @bob_lee"); len(m) != 2 {
		t.Errorf("dotted/underscored full names disambiguate: %+v", m)
	}
	if m := got("mail me at x@ana.com"); len(m) != 0 {
		t.Errorf("an e-mail is not a mention: %+v", m)
	}
	if m := got("@sales @sales @Sofia"); len(m) != 1 {
		t.Errorf("one mention per person: %+v", m)
	}
	if m := got("@nobody @ @@"); len(m) != 0 {
		t.Errorf("unknown handles: %+v", m)
	}
	if m := got("@leo, can you?"); len(m) != 1 || m[0].ID != "legal" {
		t.Errorf("punctuation after a handle: %+v", m)
	}
}

func TestCleanBody(t *testing.T) {
	if _, err := CleanBody("   "); err == nil {
		t.Error("blank body must fail")
	}
	if _, err := CleanBody("a\x00b"); err == nil {
		t.Error("control characters must fail")
	}
	long := make([]rune, MaxBody+1)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := CleanBody(string(long)); err == nil {
		t.Error("too long must fail")
	}
	if s, err := CleanBody(" hola\nmundo "); err != nil || s != "hola\nmundo" {
		t.Errorf("CleanBody = %q, %v", s, err)
	}
}

func TestDMIsPrivateAndIdempotent(t *testing.T) {
	svc, b, ctx := setup(t)
	d1, err := svc.OpenDM(ctx, "o1", ana, bob.UserID)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := svc.OpenDM(ctx, "o1", bob, ana.UserID)
	if err != nil || d2.ID != d1.ID {
		t.Fatalf("a DM opened from the other side must be the same channel: %v %v", d2.ID, err)
	}
	if _, err := svc.OpenDM(ctx, "o1", ana, ana.UserID); err == nil {
		t.Error("a DM with yourself is invalid")
	}
	if _, err := svc.OpenDM(ctx, "o1", ana, "u-ghost"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a DM with someone outside the org: %v", err)
	}
	if _, err := svc.Post(ctx, "o1", ana, d1.ID, "hola Bob"); err != nil {
		t.Fatal(err)
	}
	ev, ok := b.last("pchat.message")
	if !ok || len(ev.Audience) != 2 {
		t.Fatalf("a DM message must be addressed to exactly the two people: %+v", ev.Audience)
	}
	// carol and the owner cannot see it: not found, not forbidden (existence is private)
	for _, who := range []Actor{carol, owner} {
		if _, err := svc.Messages(ctx, "o1", who, d1.ID, 0, 10); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s reading a DM: %v", who.UserID, err)
		}
		if _, err := svc.Post(ctx, "o1", who, d1.ID, "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s posting in a DM: %v", who.UserID, err)
		}
		chs, _ := svc.Channels(ctx, "o1", who)
		for _, c := range chs {
			if c.ID == d1.ID {
				t.Errorf("%s lists a DM that is not theirs", who.UserID)
			}
		}
	}
}

func TestOrgIsolationOfChannels(t *testing.T) {
	svc, _, ctx := setup(t)
	g, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	other := Actor{UserID: "u-ana", Role: auth.RoleMember} // same user id, another organization
	if _, err := svc.Messages(ctx, "o2", other, g.ID, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("a channel id from another org must not resolve: %v", err)
	}
	chs, _ := svc.Channels(ctx, "o2", other)
	for _, c := range chs {
		if c.ID == g.ID {
			t.Error("channel leaked across organizations")
		}
	}
}

func TestUnreadFlowAndMentions(t *testing.T) {
	svc, b, ctx := setup(t)
	g, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "launch", Members: []Member{{Kind: MemberUser, ID: bob.UserID}, {Kind: MemberAgent, ID: "sales"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"hello team", "@bob.ruiz can you look?", "thanks"} {
		if _, err := svc.Post(ctx, "o1", ana, g.ID, body); err != nil {
			t.Fatal(err)
		}
	}
	find := func(a Actor) ChannelView {
		chs, err := svc.Channels(ctx, "o1", a)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range chs {
			if c.ID == g.ID {
				return c
			}
		}
		t.Fatalf("channel not listed for %s", a.UserID)
		return ChannelView{}
	}
	if v := find(bob); v.Unread != 3 || v.MentionsUnread != 1 || v.Preview != "thanks" {
		t.Errorf("bob: unread=%d mentions=%d preview=%q", v.Unread, v.MentionsUnread, v.Preview)
	}
	if v := find(ana); v.Unread != 0 {
		t.Errorf("the author reads their own messages, got %d", v.Unread)
	}
	if err := svc.MarkRead(ctx, "o1", bob, g.ID, 2); err != nil {
		t.Fatal(err)
	}
	if v := find(bob); v.Unread != 1 || v.MentionsUnread != 0 {
		t.Errorf("after reading up to 2: unread=%d mentions=%d", v.Unread, v.MentionsUnread)
	}
	if err := svc.MarkRead(ctx, "o1", bob, g.ID, 0); err != nil { // 0 = up to the last
		t.Fatal(err)
	}
	if v := find(bob); v.Unread != 0 {
		t.Errorf("read all: %d", v.Unread)
	}
	if ev, ok := b.last("pchat.read"); !ok || len(ev.Audience) != 1 || ev.Audience[0] != bob.UserID {
		t.Errorf("read sync goes only to the reader: %+v", ev.Audience)
	}
	// the marker never moves back
	_ = svc.MarkRead(ctx, "o1", bob, g.ID, 1)
	if v := find(bob); v.Unread != 0 {
		t.Errorf("the read marker moved back: %d", v.Unread)
	}
}

func TestAgentMembersAndMentionHook(t *testing.T) {
	svc, _, ctx := setup(t)
	var hits []string
	svc.OnAgentMention = func(_ context.Context, org string, ch Channel, m Message, agent string) {
		hits = append(hits, org+"/"+agent+"/"+m.Body)
	}
	g, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "deal", Members: []Member{{Kind: MemberAgent, ID: "sales"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "bad", Members: []Member{{Kind: MemberAgent, ID: "ghost"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown agent cannot be a member: %v", err)
	}
	_, _ = svc.Post(ctx, "o1", ana, g.ID, "@sofia draft the offer")
	_, _ = svc.Post(ctx, "o1", ana, g.ID, "@leo and you too") // legal is not a member: no hook
	if len(hits) != 1 || hits[0] != "o1/sales/@sofia draft the offer" {
		t.Errorf("hook calls = %v", hits)
	}
	if _, err := svc.PostAsAgent(ctx, "o1", "sales", g.ID, "On it"); err != nil {
		t.Errorf("a member agent answers: %v", err)
	}
	if _, err := svc.PostAsAgent(ctx, "o1", "legal", g.ID, "I am not here"); !errors.Is(err, ErrForbidden) {
		t.Errorf("a non-member agent cannot post: %v", err)
	}
	if len(hits) != 1 {
		t.Error("an agent's own answer must not trigger itself")
	}
}

func TestManageMembers(t *testing.T) {
	svc, _, ctx := setup(t)
	g, _ := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindGroup, Name: "g", Members: []Member{{Kind: MemberUser, ID: bob.UserID}}})
	if err := svc.AddMember(ctx, "o1", bob, g.ID, Member{Kind: MemberUser, ID: carol.UserID}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a plain member cannot add: %v", err)
	}
	if err := svc.AddMember(ctx, "o1", ana, g.ID, Member{Kind: MemberAgent, ID: "legal"}); err != nil {
		t.Errorf("the owner adds an agent: %v", err)
	}
	if err := svc.AddMember(ctx, "o1", ana, g.ID, Member{Kind: MemberUser, ID: "u-ghost"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a stranger cannot be added: %v", err)
	}
	if err := svc.AddMember(ctx, "o1", ana, g.ID, Member{Kind: MemberUser, ID: carol.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Messages(ctx, "o1", carol, g.ID, 0, 10); err != nil {
		t.Errorf("an added member reads: %v", err)
	}
	if err := svc.RemoveMember(ctx, "o1", carol, g.ID, MemberUser, carol.UserID); err != nil {
		t.Errorf("anyone may leave a group: %v", err)
	}
	if _, err := svc.Messages(ctx, "o1", carol, g.ID, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("after leaving the channel is gone: %v", err)
	}
	if err := svc.RemoveMember(ctx, "o1", bob, g.ID, MemberUser, ana.UserID); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member cannot remove the owner: %v", err)
	}
	dm, _ := svc.OpenDM(ctx, "o1", ana, bob.UserID)
	if err := svc.AddMember(ctx, "o1", ana, dm.ID, Member{Kind: MemberUser, ID: carol.UserID}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a DM never grows: %v", err)
	}
}

func TestSharedChannelsAreAdminOnlyAndViewersReadOnly(t *testing.T) {
	svc, _, ctx := setup(t)
	if _, err := svc.Create(ctx, "o1", ana, CreateInput{Kind: KindDepartment, Name: "Ventas", ScopeKey: "sales"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("member creating a department channel: %v", err)
	}
	d, err := svc.Create(ctx, "o1", admin, CreateInput{Kind: KindDepartment, Name: "Ventas", ScopeKey: "sales", Members: []Member{{Kind: MemberUser, ID: ana.UserID}}})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := svc.Create(ctx, "o1", owner, CreateInput{Kind: KindDepartment, Name: "Ventas 2", ScopeKey: "sales"})
	if again.ID != d.ID {
		t.Error("one channel per department")
	}
	if _, err := svc.Post(ctx, "o1", ana, d.ID, "hola equipo"); err != nil {
		t.Errorf("department member posts: %v", err)
	}
	if _, err := svc.Post(ctx, "o1", carol, d.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("outsider: %v", err)
	}
	// all-hands: everybody reads, the viewer cannot write
	chs, _ := svc.Channels(ctx, "o1", viewer)
	if len(chs) == 0 || chs[0].Kind != KindOrg {
		t.Fatalf("viewer sees the all-hands channel first: %+v", chs)
	}
	if _, err := svc.Post(ctx, "o1", viewer, chs[0].ID, "hi"); !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer posting: %v", err)
	}
	if _, err := svc.Post(ctx, "o1", carol, chs[0].ID, "hi @ana.garcia"); err != nil {
		t.Errorf("member posts to all-hands: %v", err)
	}
	cs, _ := svc.Channels(ctx, "o1", ana)
	var org ChannelView
	for _, c := range cs {
		if c.Kind == KindOrg {
			org = c
		}
	}
	if org.Unread != 1 || org.MentionsUnread != 1 {
		t.Errorf("ana on the all-hands channel: unread=%d mentions=%d", org.Unread, org.MentionsUnread)
	}
	// the admin supervises the department channel without being a member and sees it listed
	as, _ := svc.Channels(ctx, "o1", admin)
	found := false
	for _, c := range as {
		found = found || c.ID == d.ID
	}
	if !found {
		t.Error("admins list department channels")
	}
}
