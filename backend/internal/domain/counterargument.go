package domain

import "time"

// Counterargument is the devil's advocate review of ONE high-risk approval (B8): the case against doing what the
// approval asks, what could go wrong and what to check first. It is advisory: it never approves, rejects or blocks
// anything, and agents cannot read it as a decision. One row per approval (generated once, then cached), scoped by
// organization. It carries no secrets: it is derived from the approval card, which is already sanitized.
type Counterargument struct {
	ApprovalID      string    `json:"approval_id"`
	Counterargument string    `json:"counterargument"`
	Risks           []string  `json:"risks"`
	Checks          []string  `json:"checks"`
	Locale          string    `json:"locale,omitempty"`
	Model           string    `json:"model,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	CostUSD         float64   `json:"cost_usd"`
	GeneratedBy     string    `json:"generated_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	// Advisory is always true: the review is a second opinion for the human, never a decision.
	Advisory bool `json:"advisory"`
	// Cached is set by the service when the answer comes from a previous generation (nothing was charged now).
	Cached bool `json:"cached,omitempty"`
}
