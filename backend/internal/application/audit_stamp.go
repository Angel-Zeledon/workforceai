package application

import (
	"context"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Audit stamp (B7, docs/architecture/internal-auditor.md): the internal auditor's tasks carry
// output.metrics["audit_status"] = verified | inconsistency | unverified. These helpers turn that
// into the stamp the UI shows on a report and on a project node. They only read what the auditor
// declared (already downgraded by enforceAuditEvidence when it cited no evidence); they never
// compute a verdict of their own.

// AuditNone is the aggregate status of a request without any finished audit.
const AuditNone = "none"

// Bounds of the stamp: enough to explain an inconsistency without shipping a whole output.
const (
	maxStampFindings = 20
	maxStampEvidence = 40
)

// TaskAudit is the stamp of one finished audit task.
type TaskAudit struct {
	TaskID          string     `json:"task_id"`
	Title           string     `json:"title"`
	AgentID         string     `json:"agent_id"`
	Status          string     `json:"status"` // verified | inconsistency | unverified
	Summary         string     `json:"summary"`
	Findings        []string   `json:"findings"`
	Evidence        []string   `json:"evidence"`
	Compared        int        `json:"compared"`
	Inconsistencies int        `json:"inconsistencies"`
	Confidence      float64    `json:"confidence"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// RequestAudit is GET /requests/{id}/audit: every finished audit of the request and one aggregate
// status (inconsistency if any audit found one; verified only if every audit verified; none
// without audits; otherwise unverified).
type RequestAudit struct {
	RequestID string      `json:"request_id"`
	Status    string      `json:"status"`
	Audits    []TaskAudit `json:"audits"`
}

// normalizeAuditStatus maps a declared status to one of the three known values ("" when absent).
// Anything unknown is "unverified": a stamp never says more than the auditor proved.
func normalizeAuditStatus(v any) string {
	s, _ := v.(string)
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case AuditVerified:
		return AuditVerified
	case AuditInconsistency:
		return AuditInconsistency
	default:
		return AuditUnverified
	}
}

// TaskAuditOf is the stamp of a task, or nil when the task is not a finished audit.
func TaskAuditOf(t domain.Task) *TaskAudit {
	if t.Output == nil || t.Status != domain.TaskDone {
		return nil
	}
	st := normalizeAuditStatus(t.Output.Metrics["audit_status"])
	if st == "" {
		return nil
	}
	a := &TaskAudit{TaskID: t.ID, Title: t.Title, AgentID: t.AgentID, Status: st, Summary: t.Output.Summary,
		Findings: boundStrings(t.Output.Findings, maxStampFindings), Evidence: boundStrings(t.Output.Evidence, maxStampEvidence),
		Compared: metricInt(t.Output.Metrics["audit_compared"]), Inconsistencies: metricInt(t.Output.Metrics["audit_inconsistencies"]),
		Confidence: t.Output.Confidence, FinishedAt: t.FinishedAt}
	return a
}

// AuditOfTasks aggregates the audits of one request.
func AuditOfTasks(requestID string, tasks []domain.Task) RequestAudit {
	out := RequestAudit{RequestID: requestID, Status: AuditNone, Audits: []TaskAudit{}}
	verified := 0
	for _, t := range tasks {
		a := TaskAuditOf(t)
		if a == nil {
			continue
		}
		out.Audits = append(out.Audits, *a)
		if a.Status == AuditVerified {
			verified++
		}
	}
	switch {
	case len(out.Audits) == 0:
	case anyAudit(out.Audits, AuditInconsistency):
		out.Status = AuditInconsistency
	case verified == len(out.Audits):
		out.Status = AuditVerified
	default:
		out.Status = AuditUnverified
	}
	return out
}

func anyAudit(as []TaskAudit, status string) bool {
	for _, a := range as {
		if a.Status == status {
			return true
		}
	}
	return false
}

func boundStrings(in []string, n int) []string {
	out := make([]string, 0, min(len(in), n))
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if len(out) == n {
			break
		}
		out = append(out, s)
	}
	return out
}

// RequestAudit reads the audit stamp of one request (domain.ErrNotFound when the request does not exist).
func (q *Queries) RequestAudit(ctx context.Context, requestID string) (RequestAudit, error) {
	if _, err := q.Store.GetRequest(ctx, q.org(ctx), requestID); err != nil {
		return RequestAudit{}, err
	}
	tasks, err := q.Store.ListTasksByRequest(ctx, q.org(ctx), requestID)
	if err != nil {
		return RequestAudit{}, err
	}
	return AuditOfTasks(requestID, tasks), nil
}

// metricInt reads a count that may come back from JSON as float64.
func metricInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
