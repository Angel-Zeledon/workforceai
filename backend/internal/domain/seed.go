package domain

// SeedOrg is the demo organization. Its agents come from the seed role
// templates (roles.SeedAgents).
func SeedOrg(budget float64) Organization {
	return Organization{ID: DemoOrgID, Name: "Demo", Slug: "demo", BudgetUSD: budget}
}
