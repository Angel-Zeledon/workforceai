package projects_test

import (
	"encoding/json"

	"aiworkforce/backend/internal/domain"
)

func domainSeedOrg(budget float64) domain.Organization { return domain.SeedOrg(budget) }

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
