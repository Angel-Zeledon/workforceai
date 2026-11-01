package drive_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/policy"
)

// The plan's storage, when smaller than the deployment quota, is the quota: reaching it is still ErrQuota (413) but
// says it is the plan's, so the client can offer an upgrade. The deployment quota keeps its own code.
func TestPlanStorageIsAPlanLimit(t *testing.T) {
	ctx := application.WithOrg(context.Background(), "o1")
	h := drive.Human("u")
	planBytes := int64(150)
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1",
		Limits: drive.Limits{MaxFileBytes: 100, QuotaBytes: 250},
		PlanLimits: func(context.Context, string) policy.Tier {
			return policy.Tier{ID: policy.PlanFree, StorageBytes: planBytes}
		}})
	if u, err := svc.Usage(ctx); err != nil || u.QuotaBytes != 150 {
		t.Fatalf("usage reports the plan's quota: %+v %v", u, err)
	}
	if _, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte(strings.Repeat("a", 100))}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/b.txt", Data: []byte(strings.Repeat("b", 100))})
	var de *drive.Error
	if !errors.Is(err, drive.ErrQuota) || !errors.As(err, &de) || de.Code != "plan_limit" || de.PlanLimit == nil ||
		de.PlanLimit.Kind != policy.LimitStorage || de.PlanLimit.Limit != 150 || de.PlanLimit.Plan != policy.PlanFree {
		t.Fatalf("plan storage: %#v", err)
	}
	if _, ok := domain.AsPlanLimit(err); ok {
		t.Fatal("a storage refusal stays a 413 quota error, not a 409 conflict")
	}

	// A plan with more storage than the deployment: the deployment quota is the one reached.
	planBytes = 1 << 30
	if _, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/c.txt", Data: []byte(strings.Repeat("c", 100))}); err != nil {
		t.Fatalf("fits the deployment quota: %v", err)
	}
	_, err = svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/d.txt", Data: []byte(strings.Repeat("d", 100))})
	if !errors.As(err, &de) || de.Code != "quota_exceeded" || de.PlanLimit != nil {
		t.Fatalf("deployment quota: %#v", err)
	}
}
