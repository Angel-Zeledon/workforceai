package meetings

import (
	"context"
	"testing"
)

type fakePeople []PersonInfo

func (f fakePeople) People(context.Context, string) ([]PersonInfo, error) { return f, nil }

type fakeAgentList []AgentInfo

func (f fakeAgentList) Agents(context.Context, string) ([]AgentInfo, error) { return f, nil }

func TestDirectoryMergesPeopleAndAgents(t *testing.T) {
	s := New(Config{Store: NewMemStore(),
		People: fakePeople{{ID: "u2", Name: "bob Ruiz", Role: "member"}, {ID: "u1", Name: "Ana García", Role: "admin"}, {ID: "", Name: "ghost"}},
		Agents: fakeAgentList{{ID: "sales", Name: "Sofia Sales", Role: "sales"}, {ID: "legal", Name: "Leo Legal", Role: "legal"}}})
	got, err := s.Directory(context.Background(), "o1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"person:u1", "person:u2", "agent:legal", "agent:sales"}
	if len(got) != len(want) {
		t.Fatalf("directory = %+v", got)
	}
	for i, w := range want {
		if got[i].Kind+":"+got[i].ID != w {
			t.Errorf("entry %d = %s:%s, want %s", i, got[i].Kind, got[i].ID, w)
		}
	}
}

func TestDirectoryWithoutPortsIsEmpty(t *testing.T) {
	s := New(Config{Store: NewMemStore()})
	got, err := s.Directory(context.Background(), "o1")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestDirectoryEntriesAreValidAttendees(t *testing.T) {
	s := New(Config{Store: NewMemStore(), Agents: fakeAgentList{{ID: "sales", Name: "Sofia Sales"}}})
	in := []Attendee{{Kind: KindPerson, ID: "u1", Name: "Ana"}, {Kind: KindAgent, ID: "sales"}}
	out, err := s.normAttendees(context.Background(), "o1", in)
	if err != nil || len(out) != 2 || out[1].Name != "Sofia Sales" {
		t.Fatalf("attendees = %+v %v", out, err)
	}
}
