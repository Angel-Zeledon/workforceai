package database

import (
	"testing"

	"aiworkforce/backend/internal/connections"
)

func code(err error) string {
	if err == nil {
		return ""
	}
	return connections.Classify(err).Code
}

func TestCheckSQL(t *testing.T) {
	ok := map[string]string{
		"SELECT 1":                                              "SELECT 1",
		"  select * from t limit 5 ;;  ":                        "select * from t limit 5",
		"SELECT 1; -- trailing comment":                         "SELECT 1",
		"WITH x AS (SELECT 1) SELECT * FROM x":                  "WITH x AS (SELECT 1) SELECT * FROM x",
		"EXPLAIN SELECT * FROM t":                               "EXPLAIN SELECT * FROM t",
		"(SELECT 1) UNION (SELECT 2)":                           "(SELECT 1) UNION (SELECT 2)",
		"VALUES (1), (2)":                                       "VALUES (1), (2)",
		"TABLE customers":                                       "TABLE customers",
		"SELECT ';DROP TABLE t' AS s":                           "SELECT ';DROP TABLE t' AS s",
		`SELECT "update", "insert" FROM t`:                      `SELECT "update", "insert" FROM t`,
		"SELECT $$ ; DELETE FROM t $$":                          "SELECT $$ ; DELETE FROM t $$",
		"SELECT $a$ x $b$ ; $a$":                                "SELECT $a$ x $b$ ; $a$",
		"SELECT E'it\\'s; delete'":                              "SELECT E'it\\'s; delete'",
		"SELECT 1 /* ; drop /* nested */ table t */":            "SELECT 1 /* ; drop /* nested */ table t */",
		"SELECT name FROM t ORDER BY 1 FETCH FIRST 3 ROWS ONLY": "SELECT name FROM t ORDER BY 1 FETCH FIRST 3 ROWS ONLY",
		"SELECT comment, load FROM reviews":                     "SELECT comment, load FROM reviews",
		"SELECT CASE WHEN a THEN 1 END FROM t":                  "SELECT CASE WHEN a THEN 1 END FROM t",
		"SELECT current_setting('statement_timeout')":           "SELECT current_setting('statement_timeout')",
		"SELECT share FROM portfolio":                           "SELECT share FROM portfolio",
	}
	for in, want := range ok {
		got, err := CheckSQL(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	bad := map[string]string{
		"":                         CodeSQLEmpty,
		" ;; ":                     CodeSQLEmpty,
		"-- only a comment":        CodeSQLEmpty,
		"SELECT 1; DROP TABLE t":   CodeSQLMultiple,
		"SELECT 1;SELECT 2":        CodeSQLMultiple,
		"INSERT INTO t VALUES (1)": CodeSQLNotReadOnly,
		"UPDATE t SET a=1":         CodeSQLNotReadOnly,
		"DELETE FROM t":            CodeSQLNotReadOnly,
		"MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE": CodeSQLNotReadOnly,
		"CREATE TABLE x (a int)":                                CodeSQLNotReadOnly,
		"ALTER TABLE t ADD c int":                               CodeSQLNotReadOnly,
		"DROP TABLE t":                                          CodeSQLNotReadOnly,
		"TRUNCATE t":                                            CodeSQLNotReadOnly,
		"GRANT ALL ON t TO public":                              CodeSQLNotReadOnly,
		"COPY t TO '/tmp/x'":                                    CodeSQLNotReadOnly,
		"CALL p()":                                              CodeSQLNotReadOnly,
		"DO $$ BEGIN END $$":                                    CodeSQLNotReadOnly,
		"SET statement_timeout = 0":                             CodeSQLNotReadOnly,
		"RESET ALL":                                             CodeSQLNotReadOnly,
		"LOCK TABLE t":                                          CodeSQLNotReadOnly,
		"LISTEN x":                                              CodeSQLNotReadOnly,
		"NOTIFY x":                                              CodeSQLNotReadOnly,
		"VACUUM t":                                              CodeSQLNotReadOnly,
		"SHOW ALL":                                              CodeSQLNotReadOnly,
		"BEGIN":                                                 CodeSQLNotReadOnly,
		"COMMIT":                                                CodeSQLNotReadOnly,
		"WITH x AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM x": CodeSQLNotReadOnly,
		"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x":            CodeSQLNotReadOnly,
		"EXPLAIN ANALYZE SELECT 1":                                         CodeSQLNotReadOnly,
		"EXPLAIN (ANALYZE) DELETE FROM t":                                  CodeSQLNotReadOnly,
		"EXPLAIN EXECUTE p":                                                CodeSQLNotReadOnly,
		"SELECT * INTO new_t FROM t":                                       CodeSQLNotReadOnly,
		"SELECT * FROM t FOR UPDATE":                                       CodeSQLNotReadOnly,
		"SELECT * FROM t FOR NO KEY UPDATE":                                CodeSQLNotReadOnly,
		"SELECT * FROM t FOR SHARE":                                        CodeSQLNotReadOnly,
		"SELECT * FROM t FOR KEY SHARE":                                    CodeSQLNotReadOnly,
		"SELECT pg_sleep(60)":                                              CodeSQLForbiddenFunc,
		"SELECT pg_catalog.pg_sleep_for('1 minute')":                       CodeSQLForbiddenFunc,
		`SELECT "pg_sleep"(5)`:                                             CodeSQLForbiddenFunc,
		"SELECT * FROM dblink('host=x', 'select 1') AS t(a int)":           CodeSQLForbiddenFunc,
		"SELECT lo_import('/etc/passwd')":                                  CodeSQLForbiddenFunc,
		"SELECT lo_export(1, '/tmp/x')":                                    CodeSQLForbiddenFunc,
		"SELECT pg_read_file('/etc/passwd')":                               CodeSQLForbiddenFunc,
		"SELECT pg_ls_dir('.')":                                            CodeSQLForbiddenFunc,
		"SELECT set_config('transaction_read_only', 'off', true)":          CodeSQLForbiddenFunc,
		"SELECT nextval('s')":                                              CodeSQLForbiddenFunc,
		"SELECT setval('s', 1)":                                            CodeSQLForbiddenFunc,
		"SELECT pg_terminate_backend(1)":                                   CodeSQLForbiddenFunc,
		"SELECT pg_advisory_lock(1)":                                       CodeSQLForbiddenFunc,
		"SELECT query_to_xml('delete from t', true, true, '')":             CodeSQLForbiddenFunc,
		"SELECT pg_notify('c', 'x')":                                       CodeSQLForbiddenFunc,
		"SELECT 'unterminated":                                             CodeSQLInvalid,
		"SELECT 1 /* open":                                                 CodeSQLInvalid,
		"SELECT $x$ open":                                                  CodeSQLInvalid,
		"SELECT 1\x00":                                                     CodeSQLInvalid,
	}
	for in, want := range bad {
		if _, err := CheckSQL(in); code(err) != want {
			t.Errorf("%q: code %q, want %q", in, code(err), want)
		}
	}
	long := "SELECT " + string(make([]byte, MaxSQLChars))
	if _, err := CheckSQL(long); code(err) != CodeSQLTooLong {
		t.Errorf("long query: %v", err)
	}
}

func TestQualifiers(t *testing.T) {
	toks, _, err := check(`SELECT a.x, "Sales".y FROM auth.users a JOIN "Sales".orders ON true WHERE 'secret.x' = b.c`)
	if err != nil {
		t.Fatal(err)
	}
	got := qualifiers(toks)
	want := []string{"a", "Sales", "auth", "Sales", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
