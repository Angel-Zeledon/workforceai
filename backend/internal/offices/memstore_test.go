package offices_test

import (
	"testing"

	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/offices/officestest"
)

func TestMemStoreContract(t *testing.T) { officestest.Run(t, offices.NewMemStore(), "org-a", "org-b") }
