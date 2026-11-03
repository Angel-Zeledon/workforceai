package policy

// Plan catalog: Free, Pro and Max (docs/architecture/approvals-notify.md section 3.1).
//
// A plan is a price plus a set of HARD limits. The AI allowance is the monthly spend cap of the plan, in USD at the
// platform's cost (model_prices.json), enforced by the budget like the free cap always was. The other limits are counts
// checked where the thing is created (agents, active projects, schedules, seats, storage); going over a limit never
// deletes anything, it only refuses the next one. PlanPaid is the legacy "no plan" of a deployment without plans: no
// cap and the system-wide maximums only.

// Plans of the catalog.
const (
	PlanPro = "pro"
	PlanMax = "max"
)

// Tier is one plan of the catalog. A zero count means "no plan limit" (the system-wide maximum still applies).
type Tier struct {
	ID string `json:"id"`
	// PriceMonthlyUSD is the price of one month; PriceYearlyUSD of a year paid up front (two months free).
	PriceMonthlyUSD float64 `json:"price_monthly_usd"`
	PriceYearlyUSD  float64 `json:"price_yearly_usd"`
	// AIAllowanceUSD is the monthly HARD cap of AI spend, reset on the 1st of the month (UTC).
	AIAllowanceUSD    float64 `json:"ai_allowance_usd"`
	MaxAgents         int     `json:"max_agents"`
	MaxActiveProjects int     `json:"max_active_projects"`
	MaxSchedules      int     `json:"max_schedules"`
	MaxSeats          int     `json:"max_seats"` // members plus pending invitations
	StorageBytes      int64   `json:"storage_bytes"`
	// Batches (one template launched for many customers): how many customers one batch may have and how many of its
	// projects run at the same time. A batch counts as ONE active project, whatever its size.
	MaxBatchCustomers int `json:"max_batch_customers"`
	BatchParallel     int `json:"batch_parallel"`
}

// Catalog is every plan a customer can be on, cheapest first.
//
// Prices follow the AI allowance: at full use the AI costs about 40% of the price (Pro 12 of 29, Max 45 of 99), which
// leaves room for the payment fee, hosting and storage. Free covers trying the product for real (about a hundred chat
// turns a month with the default Gemini Flash) and stops before it costs anything that matters.
//
// Agents are not what a plan sells: a new office already has 7 and an industry pack adds up to 10, so every plan gets
// the full office (20, the system-wide maximum). Plans differ in what costs money: AI, parallel work, storage, people.
var Catalog = []Tier{
	{ID: PlanFree, AIAllowanceUSD: 1,
		MaxAgents: 20, MaxActiveProjects: 1, MaxSchedules: 2, MaxSeats: 2, StorageBytes: 500 << 20, MaxBatchCustomers: 10, BatchParallel: 2},
	{ID: PlanPro, PriceMonthlyUSD: 29, PriceYearlyUSD: 290, AIAllowanceUSD: 12,
		MaxAgents: 20, MaxActiveProjects: 5, MaxSchedules: 10, MaxSeats: 10, StorageBytes: 10 << 30, MaxBatchCustomers: 100, BatchParallel: 3},
	{ID: PlanMax, PriceMonthlyUSD: 99, PriceYearlyUSD: 990, AIAllowanceUSD: 45,
		MaxAgents: 20, MaxActiveProjects: 20, MaxSchedules: 20, MaxSeats: 50, StorageBytes: 50 << 30, MaxBatchCustomers: 500, BatchParallel: 5},
}

// TierOf returns the catalog entry of a plan. PlanPaid (and anything unknown) has no plan limits.
func TierOf(plan string) (Tier, bool) {
	for _, t := range Catalog {
		if t.ID == plan {
			return t, true
		}
	}
	return Tier{ID: plan}, false
}

// IsCatalogPlan reports whether plan is one a customer can choose.
func IsCatalogPlan(plan string) bool {
	_, ok := TierOf(plan)
	return ok
}

// HardCapped reports whether the plan's AI spend has a hard monthly cap: every catalog plan does, the legacy
// PlanPaid does not.
func HardCapped(plan string) bool { return IsCatalogPlan(plan) }

// Rank orders plans for "is this an upgrade": free < pro < max; the legacy PlanPaid sits above them all.
func Rank(plan string) int {
	for i, t := range Catalog {
		if t.ID == plan {
			return i
		}
	}
	return len(Catalog)
}

// Plan limit kinds (domain.PlanLimitError.Kind; the frontend maps them to a translated text and an upgrade link).
const (
	LimitAgents   = "agents"
	LimitProjects = "active_projects"
	LimitSched    = "schedules"
	LimitSeats    = "seats"
	LimitStorage  = "storage"
)

// AtLimit reports whether one more item would pass limit (0 = no plan limit).
func AtLimit(current, limit int) bool { return limit > 0 && current >= limit }
