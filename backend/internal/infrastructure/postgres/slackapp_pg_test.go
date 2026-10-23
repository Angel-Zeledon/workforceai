package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/slackapp"
)

// Slack channel (migration 801): settings round trip, single-use link codes, one winner per interaction key,
// tenant isolation under RLS and disconnect.
func TestSlackAppStoreAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	db := &slackapp.PG{DB: st}

	if _, found, err := db.GetRecord(ctx, "org-a"); err != nil || found {
		t.Fatalf("empty record = %v %v", found, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	rec := slackapp.Record{OrgID: "org-a", Enabled: true, SigningSecretEnc: []byte("sealed-1"), BotTokenEnc: []byte("sealed-2"), TokenLast4: "abcd",
		TeamID: "T1", TeamName: "Acme", BotUserID: "UBOT", ChannelID: "C0123456", MinRisk: "high", Locale: "pt", EmailMatching: false,
		HighRiskInApp: true, VerifiedAt: &now, UpdatedBy: "owner", UpdatedAt: now}
	if err := db.PutRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.GetRecord(ctx, "org-a")
	if err != nil || !found || got.TeamID != "T1" || string(got.BotTokenEnc) != "sealed-2" || got.MinRisk != "high" || got.Locale != "pt" || got.EmailMatching {
		t.Fatalf("round trip = %+v %v %v", got, found, err)
	}
	if _, found, _ := db.GetRecord(ctx, "org-b"); found {
		t.Fatal("org-b sees org-a's Slack app")
	}

	// Link codes: one winner, expiry honoured, bound to the organization.
	if err := db.CreateCode(ctx, "org-a", "h1", "user-1", now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConsumeCode(ctx, "org-b", "h1", now); !errors.Is(err, slackapp.ErrCodeInvalid) {
		t.Fatalf("org-b consumed org-a's code: %v", err)
	}
	if u, err := db.ConsumeCode(ctx, "org-a", "h1", now); err != nil || u != "user-1" {
		t.Fatalf("consume = %q %v", u, err)
	}
	if _, err := db.ConsumeCode(ctx, "org-a", "h1", now); !errors.Is(err, slackapp.ErrCodeInvalid) {
		t.Fatalf("second consume = %v", err)
	}
	_ = db.CreateCode(ctx, "org-a", "h2", "user-1", now.Add(-time.Second))
	if _, err := db.ConsumeCode(ctx, "org-a", "h2", now); !errors.Is(err, slackapp.ErrCodeInvalid) {
		t.Fatalf("expired consume = %v", err)
	}

	// Links.
	if err := db.PutLink(ctx, slackapp.Link{OrgID: "org-a", TeamID: "T1", SlackUserID: "U1", UserID: "user-1", Method: "code", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if l, err := db.GetLink(ctx, "org-a", "T1", "U1"); err != nil || l.UserID != "user-1" {
		t.Fatalf("link = %+v %v", l, err)
	}
	if _, err := db.GetLink(ctx, "org-b", "T1", "U1"); !errors.Is(err, slackapp.ErrNotFound) {
		t.Fatalf("org-b link = %v", err)
	}

	// Interaction dedupe: exactly one claim wins.
	won, err := db.Claim(ctx, "org-a", "k1", now)
	if err != nil || !won {
		t.Fatalf("first claim = %v %v", won, err)
	}
	if won, _ := db.Claim(ctx, "org-a", "k1", now); won {
		t.Fatal("a retried interaction was claimed twice")
	}
	if won, _ := db.Claim(ctx, "org-b", "k1", now); !won {
		t.Fatal("the same key in another organization must be independent")
	}
	if err := db.SetOutcome(ctx, "org-a", "k1", "approved"); err != nil {
		t.Fatal(err)
	}

	// Messages.
	if err := db.PutMessage(ctx, "org-a", "ap1", "C0123456", "1.2"); err != nil {
		t.Fatal(err)
	}
	if ch, ts, err := db.GetMessage(ctx, "org-a", "ap1"); err != nil || ch != "C0123456" || ts != "1.2" {
		t.Fatalf("message = %s %s %v", ch, ts, err)
	}

	// RLS: org-b can neither read nor write org-a rows.
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM slack_app_settings WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d org-a settings rows", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO slack_user_links (org_id, team_id, slack_user_id, user_id) VALUES ('org-a','T1','U9','x')`); err == nil {
		t.Error("org-b wrote an org-a link")
	}

	// Disconnect removes everything of org-a only.
	if err := db.DeleteAll(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.GetRecord(ctx, "org-a"); found {
		t.Fatal("still configured after disconnect")
	}
	if _, err := db.GetLink(ctx, "org-a", "T1", "U1"); !errors.Is(err, slackapp.ErrNotFound) {
		t.Fatal("links survived the disconnect")
	}
	if won, _ := db.Claim(ctx, "org-b", "k1", now); won {
		t.Fatal("disconnecting org-a removed org-b rows")
	}
}

func TestMigration801DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	down, err := os.ReadFile("../../../migrations/down/801_slack_app_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('slack_app_settings')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/801_slack_app.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	for _, tbl := range []string{"slack_app_settings", "slack_user_links", "slack_link_codes", "slack_interactions", "slack_approval_messages"} {
		var forced bool
		_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname=$1`, tbl).Scan(&forced)
		if !forced {
			t.Errorf("RLS not forced on %s after re-applying", tbl)
		}
	}
}
