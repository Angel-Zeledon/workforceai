package tools

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------- calendar

// CalEvent is a calendar entry.
type CalEvent struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Attendees []string  `json:"attendees,omitempty"`
}

// Calendar is a fake in-memory calendar (working hours 09:00-18:00 UTC).
type Calendar struct {
	mu     sync.Mutex
	now    func() time.Time
	ids    idGen
	Events []CalEvent
}

// NewCalendar seeds a couple of events for tomorrow.
func NewCalendar(now func() time.Time) *Calendar {
	d := now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	return &Calendar{now: now, Events: []CalEvent{
		{ID: "ev-001", Title: "Daily de ventas", Start: d.Add(9 * time.Hour), End: d.Add(9*time.Hour + 30*time.Minute), Attendees: []string{"laura@acme.com"}},
		{ID: "ev-002", Title: "Revisión financiera", Start: d.Add(11 * time.Hour), End: d.Add(12 * time.Hour)},
	}}
}

func (c *Calendar) Name() string { return "calendar" }

func (c *Calendar) Actions() []ActionSpec {
	return []ActionSpec{
		{"list_events", true, "List events in a day (date=YYYY-MM-DD)"},
		{"find_slots", true, "Find free slots (date, duration_min)"},
		{"schedule", false, "Create an event (title, start, duration_min, attendees)"},
		{"cancel", false, "Cancel an event (event_id)"},
	}
}

func (c *Calendar) Execute(_ context.Context, call Call) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch call.Action {
	case "list_events":
		day, err := c.day(call.Args)
		if err != nil {
			return Result{}, err
		}
		var out []CalEvent
		for _, e := range c.Events {
			if e.Start.UTC().Truncate(24 * time.Hour).Equal(day) {
				out = append(out, e)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
		return Result{OK: true, Summary: pl(len(out), "event"), Data: map[string]any{"events": out}}, nil
	case "find_slots":
		day, err := c.day(call.Args)
		if err != nil {
			return Result{}, err
		}
		dur := time.Duration(argInt(call.Args, "duration_min", 30)) * time.Minute
		if dur <= 0 || dur > 8*time.Hour {
			return Result{}, invalid("duration_min out of range")
		}
		var slots []string
		for t := day.Add(9 * time.Hour); !t.Add(dur).After(day.Add(18 * time.Hour)); t = t.Add(30 * time.Minute) {
			if !c.busy(t, t.Add(dur)) {
				slots = append(slots, t.Format(time.RFC3339))
			}
		}
		return Result{OK: true, Summary: pl(len(slots), "free slot"), Data: map[string]any{"slots": slots}}, nil
	case "schedule":
		title := argStr(call.Args, "title")
		if title == "" {
			return Result{}, invalid("title is required")
		}
		start, err := parseTime(argStr(call.Args, "start"))
		if err != nil {
			return Result{}, err
		}
		dur := time.Duration(argInt(call.Args, "duration_min", 30)) * time.Minute
		if dur <= 0 || dur > 8*time.Hour {
			return Result{}, invalid("duration_min out of range")
		}
		if c.busy(start, start.Add(dur)) {
			return Result{}, invalid("slot is busy")
		}
		ev := CalEvent{ID: c.ids.next("ev"), Title: title, Start: start, End: start.Add(dur), Attendees: argList(call.Args, "attendees")}
		c.Events = append(c.Events, ev)
		return Result{OK: true, Summary: "Scheduled " + title + " at " + start.Format(time.RFC3339), Data: map[string]any{"event": ev}}, nil
	case "cancel":
		id := argStr(call.Args, "event_id")
		for i, e := range c.Events {
			if e.ID == id {
				c.Events = append(c.Events[:i], c.Events[i+1:]...)
				return Result{OK: true, Summary: "Cancelled " + e.Title}, nil
			}
		}
		return Result{}, invalid("event %q: %v", id, ErrNotFound)
	}
	return Result{}, unknownAction("calendar", call.Action)
}

func (c *Calendar) day(a map[string]any) (time.Time, error) {
	s := argStr(a, "date")
	if s == "" {
		return c.now().UTC().Truncate(24 * time.Hour), nil
	}
	t, err := parseTime(s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC().Truncate(24 * time.Hour), nil
}

func (c *Calendar) busy(s, e time.Time) bool {
	for _, ev := range c.Events {
		if s.Before(ev.End) && e.After(ev.Start) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- crm

// Contact is a CRM contact. Only contacts in the CRM are "known" to policy.
type Contact struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Company string   `json:"company"`
	Notes   []string `json:"notes,omitempty"`
}

// Deal is a CRM opportunity.
type Deal struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ContactID string  `json:"contact_id"`
	Value     float64 `json:"value"`
	Stage     string  `json:"stage"`
}

// CRM is a fake in-memory CRM. It implements policy.ContactBook.
type CRM struct {
	mu       sync.Mutex
	now      func() time.Time
	ids      idGen
	Contacts []Contact
	Deals    []Deal
}

// NewCRM seeds contacts and deals (Acme $50,000 matches the demo scenario).
func NewCRM(now func() time.Time) *CRM {
	return &CRM{now: now,
		Contacts: []Contact{
			{ID: "c-001", Name: "Laura Gómez", Email: "laura@acme.com", Company: "Acme Corp"},
			{ID: "c-002", Name: "Pedro Silva", Email: "pedro@globex.com", Company: "Globex"},
		},
		Deals: []Deal{
			{ID: "d-001", Name: "Rediseño Acme", ContactID: "c-001", Value: 50000, Stage: "proposal"},
			{ID: "d-002", Name: "Soporte Globex", ContactID: "c-002", Value: 8000, Stage: "negotiation"},
		}}
}

// IsKnown implements policy.ContactBook.
func (c *CRM) IsKnown(_ string, address string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := addrOf(address)
	for _, ct := range c.Contacts {
		if strings.EqualFold(ct.Email, a) {
			return true
		}
	}
	return false
}

func (c *CRM) Name() string { return "crm" }

func (c *CRM) Actions() []ActionSpec {
	return []ActionSpec{
		{"search", true, "Search contacts and deals by text (query)"},
		{"get_contact", true, "Get a contact by id or email"},
		{"list_deals", true, "List deals (optional stage)"},
		{"add_contact", false, "Add a contact (name, email, company) - makes it a trusted recipient"},
		{"update_deal", false, "Update a deal (deal_id, stage, value)"},
		{"log_note", false, "Attach a note (contact_id, note)"},
	}
}

func (c *CRM) Execute(_ context.Context, call Call) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch call.Action {
	case "search":
		q := strings.ToLower(argStr(call.Args, "query"))
		if q == "" {
			return Result{}, invalid("query is required")
		}
		var cs []Contact
		var ds []Deal
		for _, ct := range c.Contacts {
			if strings.Contains(strings.ToLower(ct.Name+" "+ct.Email+" "+ct.Company), q) {
				cs = append(cs, ct)
			}
		}
		for _, d := range c.Deals {
			if strings.Contains(strings.ToLower(d.Name+" "+d.Stage), q) {
				ds = append(ds, d)
			}
		}
		return Result{OK: true, Summary: pl(len(cs), "contact") + ", " + pl(len(ds), "deal"), Data: map[string]any{"contacts": cs, "deals": ds}}, nil
	case "get_contact":
		for _, ct := range c.Contacts {
			if ct.ID == argStr(call.Args, "id") || (argStr(call.Args, "email") != "" && strings.EqualFold(ct.Email, addrOf(argStr(call.Args, "email")))) {
				return Result{OK: true, Summary: ct.Name + " (" + ct.Company + ")", Data: map[string]any{"contact": ct}}, nil
			}
		}
		return Result{}, invalid("contact: %v", ErrNotFound)
	case "list_deals":
		stage := strings.ToLower(argStr(call.Args, "stage"))
		var ds []Deal
		for _, d := range c.Deals {
			if stage == "" || strings.EqualFold(d.Stage, stage) {
				ds = append(ds, d)
			}
		}
		return Result{OK: true, Summary: pl(len(ds), "deal"), Data: map[string]any{"deals": ds}}, nil
	case "add_contact":
		email := addrOf(argStr(call.Args, "email"))
		if argStr(call.Args, "name") == "" || !strings.Contains(email, "@") {
			return Result{}, invalid("name and a valid email are required")
		}
		for _, ct := range c.Contacts {
			if strings.EqualFold(ct.Email, email) {
				return Result{OK: true, Summary: "Contact already exists", Data: map[string]any{"contact": ct}}, nil
			}
		}
		ct := Contact{ID: c.ids.next("c-new"), Name: argStr(call.Args, "name"), Email: email, Company: argStr(call.Args, "company")}
		c.Contacts = append(c.Contacts, ct)
		return Result{OK: true, Summary: "Added " + ct.Name, Data: map[string]any{"contact": ct}}, nil
	case "update_deal":
		for i := range c.Deals {
			if c.Deals[i].ID == argStr(call.Args, "deal_id") {
				if s := argStr(call.Args, "stage"); s != "" {
					c.Deals[i].Stage = s
				}
				if v, ok := argNum(call.Args, "value"); ok {
					if v < 0 {
						return Result{}, invalid("value must be >= 0")
					}
					c.Deals[i].Value = v
				}
				return Result{OK: true, Summary: "Updated " + c.Deals[i].Name, Data: map[string]any{"deal": c.Deals[i]}}, nil
			}
		}
		return Result{}, invalid("deal: %v", ErrNotFound)
	case "log_note":
		note := argStr(call.Args, "note")
		if note == "" {
			return Result{}, invalid("note is required")
		}
		for i := range c.Contacts {
			if c.Contacts[i].ID == argStr(call.Args, "contact_id") {
				c.Contacts[i].Notes = append(c.Contacts[i].Notes, note)
				return Result{OK: true, Summary: "Note logged for " + c.Contacts[i].Name}, nil
			}
		}
		return Result{}, invalid("contact: %v", ErrNotFound)
	}
	return Result{}, unknownAction("crm", call.Action)
}
