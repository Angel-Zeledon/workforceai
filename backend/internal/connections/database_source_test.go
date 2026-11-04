package connections

import (
	"errors"
	"strings"
	"testing"
)

func TestDatabaseNormalizeRejectsBadConfig(t *testing.T) {
	bad := []Database{
		{Host: "", User: "u"},
		{Host: "db.example.test", User: ""},
		{Host: "db.example.test", User: "u", Port: 70000},
		{Host: "db.example.test", User: "u", SSLMode: "prefer"},
		{Host: "db.example.test", User: "u", Schemas: []string{`public"; DROP`}},
		{Host: "db example", User: "u"},
		{Host: "db.example.test", User: "u", Engine: "oracle"},
	}
	for _, d := range bad {
		if _, err := d.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v (%v)", d, err)
		}
	}
	if _, err := (Database{Engine: "mysql", Host: "h", User: "u"}).Normalize(); err == nil || !strings.Contains(err.Error(), "engine_not_supported") {
		t.Fatalf("mysql must say it is not supported yet: %v", err)
	}
	got, err := Database{Host: " DB.Example.test. ", User: " app ", Schemas: []string{"public", "public", "sales"}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Engine != EnginePostgres || got.Host != "db.example.test" || got.Port != 5432 || got.SSLMode != "require" || got.Name != "postgres" || got.User != "app" {
		t.Fatalf("defaults: %+v", got)
	}
	if len(got.Schemas) != 2 || got.Account() != "app@db.example.test:5432/postgres" {
		t.Fatalf("schemas/account: %+v %s", got.Schemas, got.Account())
	}
}

func TestParseConnStringSplitsThePassword(t *testing.T) {
	d, pass, err := ParseConnString("postgresql://u.ref:p%40ss%3Aw0rd@aws-0-x.pooler.example.test:6543/db?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if pass != "p@ss:w0rd" {
		t.Fatalf("password %q", pass)
	}
	if d.User != "u.ref" || d.Host != "aws-0-x.pooler.example.test" || d.Port != 6543 || d.Name != "db" || d.SSLMode != "verify-full" {
		t.Fatalf("config %+v", d)
	}
	if strings.Contains(d.Account(), "p@ss") {
		t.Fatal("the account label carries the password")
	}
	for _, bad := range []string{"mysql://u:p@h/db", "not a url", "postgresql:///db", "postgres://u:p@h:abc/db"} {
		if _, _, err := ParseConnString(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if d, _, err := ParseConnString("postgres://u:p@h/db"); err != nil || d.SSLMode != "require" {
		t.Fatalf("sslmode defaults to require: %+v %v", d, err)
	}
}
