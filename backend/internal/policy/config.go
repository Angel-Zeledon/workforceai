package policy

import "time"

// ApprovalThresholdUSD is the default amount above which approval is needed.
const ApprovalThresholdUSD = 10000.0

// DefaultOrgRules returns the baseline org rules.
func DefaultOrgRules() OrgRules {
	return OrgRules{
		AlwaysApprove:      append([]string(nil), DefaultAlwaysApprove...),
		MaxDelegationDepth: MaxDelegationDepthDefault,
		ApprovalAmount:     ApprovalThresholdUSD,
	}
}

// AllowActions builds one allow grant per action for an agent/tool.
func AllowActions(agent, tool string, actions ...string) []Grant {
	out := make([]Grant, 0, len(actions))
	for _, a := range actions {
		out = append(out, Grant{
			ID: agent + "." + tool + "." + a + ".allow", Agent: agent, Tool: tool, Action: a,
			Effect: EffectAllow,
		})
	}
	return out
}

func f64(v float64) *float64 { return &v }

// SecretaryGrants is the "Secretaria -> Email" example from the product brief:
// read/draft/reply authorized; new contacts, legal documents and amounts above
// $10,000 require approval.
func SecretaryGrants(agent string) []Grant {
	var g []Grant
	g = append(g, AllowActions(agent, "email", "read", "list", "search", "draft", "send_reply")...)
	g = append(g,
		Grant{ID: agent + ".email.send.approval", Agent: agent, Tool: "email", Action: "send", Effect: EffectRequireApproval,
			Description: "composing a brand-new email to a third party"},
		Grant{ID: agent + ".new_contact.approval", Agent: agent, Tool: "*", Action: "*", Effect: EffectRequireApproval,
			When:        Conditions{Recipient: RecipientNew},
			Description: "recipient is not a known contact"},
		Grant{ID: agent + ".legal_docs.approval", Agent: agent, Tool: "*", Action: "*", Effect: EffectRequireApproval,
			When:        Conditions{Categories: []string{"legal"}},
			Description: "legal documents need approval"},
		Grant{ID: agent + ".amount_10k.approval", Agent: agent, Tool: "*", Action: "*", Effect: EffectRequireApproval,
			When:        Conditions{AmountAbove: f64(ApprovalThresholdUSD)},
			Description: "amounts above $10,000 need approval"},
	)
	g = append(g, AllowActions(agent, "calendar", "list_events", "find_slots", "schedule")...)
	g = append(g, AllowActions(agent, "documents", "read", "search", "list", "draft", "create")...)
	g = append(g, AllowActions(agent, "crm", "search", "get_contact", "list_deals", "log_note")...)
	g = append(g, AllowActions(agent, "spreadsheet", "read")...)
	g = append(g, AllowActions(agent, "calculator", "*")...)
	return g
}

// DefaultConfig seeds the 7 spec agents (sales, hr, legal, accounting,
// analyst, operations, assistant) with sensible grants.
func DefaultConfig() Config {
	cfg := Config{
		Org: DefaultOrgRules(),
		Autonomy: map[string]Autonomy{
			"assistant": AutonomyRules, "sales": AutonomyRules, "hr": AutonomyRules,
			"legal": AutonomyRules, "accounting": AutonomyRules, "analyst": AutonomyRules,
			"operations": AutonomyRules,
		},
	}
	add := func(g ...Grant) { cfg.Grants = append(cfg.Grants, g...) }

	add(SecretaryGrants("assistant")...)

	// Sales: CRM, email to known contacts, proposals (send_proposal is org-approved).
	add(AllowActions("sales", "crm", "search", "get_contact", "list_deals", "log_note", "update_deal")...)
	// New contacts are trusted for outbound email, so creating one always needs approval
	// (otherwise injected content could "whitelist" an attacker).
	add(Grant{ID: "sales.crm.add_contact.approval", Agent: "sales", Tool: "crm", Action: "add_contact", Effect: EffectRequireApproval,
		Description: "adding a contact makes it a trusted recipient"})
	add(AllowActions("sales", "email", "read", "list", "search", "draft", "send_reply", "send")...)
	add(Grant{ID: "sales.new_contact.approval", Agent: "sales", Tool: "email", Action: "*", Effect: EffectRequireApproval,
		When: Conditions{Recipient: RecipientNew}, Description: "recipient is not a known contact"})
	add(AllowActions("sales", "documents", "read", "search", "list", "draft", "create", "send_proposal")...)
	add(AllowActions("sales", "calendar", "list_events", "find_slots", "schedule")...)
	add(AllowActions("sales", "spreadsheet", "read")...)
	add(AllowActions("sales", "calculator", "*")...)

	// HR
	add(AllowActions("hr", "documents", "read", "search", "list", "draft", "create")...)
	add(AllowActions("hr", "email", "read", "list", "search", "draft", "send_reply")...)
	add(Grant{ID: "hr.new_contact.approval", Agent: "hr", Tool: "email", Action: "*", Effect: EffectRequireApproval,
		When: Conditions{Recipient: RecipientNew}, Description: "recipient is not a known contact"})
	add(AllowActions("hr", "calendar", "list_events", "find_slots", "schedule")...)
	add(AllowActions("hr", "hris", "read", "hire", "fire")...) // hire/fire always need approval (org rule)
	add(AllowActions("hr", "spreadsheet", "read")...)
	add(AllowActions("hr", "calculator", "*")...)

	// Legal
	add(AllowActions("legal", "documents", "read", "search", "list", "draft", "create", "send_contract")...)
	add(AllowActions("legal", "email", "read", "list", "search", "draft")...)
	add(AllowActions("legal", "crm", "search", "get_contact")...)
	add(AllowActions("legal", "calculator", "*")...)

	// Accounting
	add(AllowActions("accounting", "spreadsheet", "read", "write", "compute")...)
	add(AllowActions("accounting", "calculator", "*")...)
	add(AllowActions("accounting", "documents", "read", "search", "list", "draft")...)
	add(AllowActions("accounting", "ledger", "read", "modify_financials", "make_payment")...) // org rules force approval
	add(AllowActions("accounting", "crm", "search", "get_contact", "list_deals")...)

	// Analyst
	add(AllowActions("analyst", "spreadsheet", "read", "write", "compute")...)
	add(AllowActions("analyst", "calculator", "*")...)
	add(AllowActions("analyst", "crm", "search", "get_contact", "list_deals")...)
	add(AllowActions("analyst", "documents", "read", "search", "list", "draft")...)

	// Operations
	add(AllowActions("operations", "calendar", "list_events", "find_slots", "schedule")...)
	add(AllowActions("operations", "spreadsheet", "read", "write", "compute")...)
	add(AllowActions("operations", "calculator", "*")...)
	add(AllowActions("operations", "documents", "read", "search", "list", "draft", "create")...)
	add(AllowActions("operations", "crm", "search", "get_contact", "list_deals")...)

	// Everyone: shared guardrails on outbound email volume.
	cfg.Limits = []Limit{
		{ID: "email.send.hourly", Agent: "*", Tool: "email", Action: "send*", Window: time.Hour, MaxCalls: 30},
		{ID: "payments.daily", Agent: "*", Tool: "*", Action: "make_payment", Window: 24 * time.Hour, MaxAmount: 50000, OnExceed: EffectDeny},
	}
	return cfg
}
