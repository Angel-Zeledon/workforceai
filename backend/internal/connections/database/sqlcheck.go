package database

import (
	"strings"

	"aiworkforce/backend/internal/connections"
)

// Stable codes of a refused or failed query (what the agent, the UI and the audit see; never raw driver text).
const (
	CodeSQLEmpty          = "sql_empty"
	CodeSQLTooLong        = "sql_too_long"
	CodeSQLInvalid        = "sql_invalid"
	CodeSQLMultiple       = "sql_multiple_statements"
	CodeSQLNotReadOnly    = "sql_not_read_only"
	CodeSQLForbiddenFunc  = "sql_forbidden_function"
	CodeSQLSchema         = "sql_schema_not_allowed"
	CodeSQLTimeout        = "sql_timeout"
	CodeReadOnlyMissing   = "db_read_only_not_enforced"
	CodeUnreachable       = "db_unreachable"
	CodeAddressNotAllowed = "db_address_not_allowed"
	CodeHostNotFound      = "db_host_not_found"
	CodeTLS               = "db_tls_failed"
	CodeDatabaseNotFound  = "db_not_found"
	CodeNotConfigured     = "db_not_configured"
)

// MaxSQLChars bounds the text of one query.
const MaxSQLChars = 20000

// The first word of an accepted statement. A statement that starts with one of these and is a single statement
// can only change data through the words refused below (or through a function: the READ ONLY transaction).
var firstWords = map[string]bool{"select": true, "with": true, "explain": true, "values": true, "table": true}

// forbiddenWords are refused anywhere outside literals, quoted identifiers and comments: data-modifying statements
// (inside WITH or EXPLAIN), SELECT INTO, row locks (FOR UPDATE), EXPLAIN ANALYZE (it runs the statement) and every
// other statement keyword the owner listed. A column really named like one of them must be written in quotes.
var forbiddenWords = map[string]bool{
	"insert": true, "update": true, "delete": true, "merge": true, "create": true, "alter": true, "drop": true,
	"truncate": true, "grant": true, "revoke": true, "copy": true, "call": true, "do": true, "set": true, "reset": true,
	"lock": true, "listen": true, "notify": true, "unlisten": true, "vacuum": true, "analyze": true, "analyse": true,
	"into": true, "execute": true, "prepare": true, "deallocate": true, "declare": true, "begin": true, "commit": true,
	"rollback": true, "savepoint": true,
}

// forbiddenFuncs are functions refused by name (any schema): sleeping, reaching another server or the server's
// files, large objects, changing settings or sequences, signalling backends, running SQL given as text (which would
// slip past this check), replication and backups. A user-defined function that writes still fails: the
// transaction is READ ONLY.
var forbiddenFuncs = map[string]bool{
	"set_config": true, "nextval": true, "setval": true, "pg_terminate_backend": true, "pg_cancel_backend": true,
	"pg_reload_conf": true, "pg_rotate_logfile": true, "pg_notify": true, "pg_stat_file": true, "pg_switch_wal": true,
	"pg_switch_xlog": true, "pg_promote": true, "pg_start_backup": true, "pg_stop_backup": true, "loread": true,
	"lowrite": true, "pg_log_backend_memory_contexts": true, "pg_import_system_collations": true, "txid_current": true,
	"pg_current_xact_id": true,
}

var forbiddenFuncPrefixes = []string{"pg_sleep", "dblink", "lo_", "pg_read_", "pg_ls_", "pg_file_", "pg_advisory", "pg_try_advisory",
	"query_to_xml", "table_to_xml", "cursor_to_xml", "database_to_xml", "schema_to_xml", "pg_logical_", "pg_create_", "pg_drop_",
	"pg_replication_", "pg_backup_", "pg_wal_replay_", "pg_stat_reset"}

func forbiddenFunc(name string) bool {
	if forbiddenFuncs[name] {
		return true
	}
	for _, p := range forbiddenFuncPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

type tokKind int

const (
	tWord   tokKind = iota // unquoted identifier or keyword (lowercased in text)
	tQuoted                // "quoted identifier" (exact text)
	tString                // any literal string
	tNumber
	tParam // $1
	tPunct // one character: ; ( ) . , and operators
)

type token struct {
	kind tokKind
	text string
	pos  int // byte offset in the query
}

func isIdentStart(c byte) bool { return c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isIdentChar(c byte) bool  { return isIdentStart(c) || c == '$' || (c >= '0' && c <= '9') }

// lex splits a Postgres query into tokens, skipping comments and the inside of literals. It follows the server's
// rules (nested block comments, doubled-quote escapes, E-string backslash escapes, $tag$ quoting); an unterminated literal or
// comment is an error.
func lex(q string) ([]token, error) {
	var out []token
	invalid := &connections.ProviderError{Code: CodeSQLInvalid}
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			depth := 0
			for {
				if i+1 >= len(q) {
					return nil, invalid
				}
				if q[i] == '/' && q[i+1] == '*' {
					depth, i = depth+1, i+2
					continue
				}
				if q[i] == '*' && q[i+1] == '/' {
					depth, i = depth-1, i+2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
		case c == '\'':
			end, err := quoted(q, i, false)
			if err != nil {
				return nil, invalid
			}
			out, i = append(out, token{tString, "", i}), end
		case (c == 'e' || c == 'E') && i+1 < len(q) && q[i+1] == '\'':
			end, err := quoted(q, i+1, true)
			if err != nil {
				return nil, invalid
			}
			out, i = append(out, token{tString, "", i}), end
		case (c|0x20 == 'b' || c|0x20 == 'x' || c|0x20 == 'n') && i+1 < len(q) && q[i+1] == '\'':
			end, err := quoted(q, i+1, false)
			if err != nil {
				return nil, invalid
			}
			out, i = append(out, token{tString, "", i}), end
		case (c == 'u' || c == 'U') && i+2 < len(q) && q[i+1] == '&' && (q[i+2] == '\'' || q[i+2] == '"'):
			end, err := quoted(q, i+2, false)
			if err != nil {
				return nil, invalid
			}
			kind := tString
			if q[i+2] == '"' {
				kind = tQuoted
			}
			out, i = append(out, token{kind, strings.ReplaceAll(q[i+3:end-1], `""`, `"`), i}), end
		case c == '"':
			end, err := quoted(q, i, false)
			if err != nil {
				return nil, invalid
			}
			out, i = append(out, token{tQuoted, strings.ReplaceAll(q[i+1:end-1], `""`, `"`), i}), end
		case c == '$':
			j := i + 1
			if j < len(q) && q[j] >= '0' && q[j] <= '9' {
				for j < len(q) && q[j] >= '0' && q[j] <= '9' {
					j++
				}
				out, i = append(out, token{tParam, q[i:j], i}), j
				continue
			}
			for j < len(q) && isIdentChar(q[j]) && q[j] != '$' {
				j++
			}
			if j >= len(q) || q[j] != '$' {
				out, i = append(out, token{tPunct, "$", i}), i+1
				continue
			}
			tag := q[i : j+1]
			k := strings.Index(q[j+1:], tag)
			if k < 0 {
				return nil, invalid
			}
			out, i = append(out, token{tString, "", i}), j+1+k+len(tag)
		case isIdentStart(c):
			j := i
			for j < len(q) && isIdentChar(q[j]) {
				j++
			}
			out, i = append(out, token{tWord, strings.ToLower(q[i:j]), i}), j
		case c >= '0' && c <= '9':
			j := i
			for j < len(q) && (isIdentChar(q[j]) || q[j] == '.') {
				j++
			}
			out, i = append(out, token{tNumber, q[i:j], i}), j
		default:
			out, i = append(out, token{tPunct, string(c), i}), i+1
		}
	}
	return out, nil
}

// quoted returns the offset just past the literal that opens at q[start] (a ' or a "), where a doubled quote is an
// escaped quote and, with backslash, \x escapes the next character.
func quoted(q string, start int, backslash bool) (int, error) {
	qc := q[start]
	for i := start + 1; i < len(q); i++ {
		switch {
		case backslash && q[i] == '\\':
			i++
		case q[i] == qc:
			if i+1 < len(q) && q[i+1] == qc {
				i++
				continue
			}
			return i + 1, nil
		}
	}
	return 0, errUnterminated
}

var errUnterminated = &connections.ProviderError{Code: CodeSQLInvalid}

// CheckSQL accepts ONE read statement (SELECT, WITH, EXPLAIN without ANALYZE, VALUES, TABLE) and returns it without
// its trailing semicolons. It is the first of three locks: the statement then runs through the extended protocol
// (the server refuses a second statement) inside a READ ONLY transaction that is always rolled back.
func CheckSQL(q string) (string, error) {
	_, clean, err := check(q)
	return clean, err
}

func check(q string) ([]token, string, error) {
	if strings.TrimSpace(q) == "" {
		return nil, "", &connections.ProviderError{Code: CodeSQLEmpty}
	}
	if len(q) > MaxSQLChars {
		return nil, "", &connections.ProviderError{Code: CodeSQLTooLong}
	}
	if strings.IndexByte(q, 0) >= 0 {
		return nil, "", &connections.ProviderError{Code: CodeSQLInvalid}
	}
	toks, err := lex(q)
	if err != nil {
		return nil, "", err
	}
	// trailing semicolons end the statement; any other one starts a second statement
	end := len(toks)
	for end > 0 && toks[end-1].kind == tPunct && toks[end-1].text == ";" {
		end--
	}
	if end == 0 {
		return nil, "", &connections.ProviderError{Code: CodeSQLEmpty}
	}
	clean := q
	if end < len(toks) {
		clean = q[:toks[end].pos]
	}
	toks = toks[:end]
	first := ""
	for _, t := range toks {
		if t.kind == tPunct && t.text == "(" {
			continue
		}
		if t.kind == tWord {
			first = t.text
		}
		break
	}
	if !firstWords[first] {
		return nil, "", &connections.ProviderError{Code: CodeSQLNotReadOnly}
	}
	for i, t := range toks {
		switch t.kind {
		case tPunct:
			if t.text == ";" {
				return nil, "", &connections.ProviderError{Code: CodeSQLMultiple}
			}
		case tWord:
			if forbiddenWords[t.text] {
				return nil, "", &connections.ProviderError{Code: CodeSQLNotReadOnly}
			}
			// FOR SHARE / FOR KEY SHARE take row locks (FOR UPDATE is refused by "update")
			if t.text == "share" && i > 0 && toks[i-1].kind == tWord && (toks[i-1].text == "for" || toks[i-1].text == "key") {
				return nil, "", &connections.ProviderError{Code: CodeSQLNotReadOnly}
			}
		}
		if (t.kind == tWord || t.kind == tQuoted) && i+1 < len(toks) && toks[i+1].kind == tPunct && toks[i+1].text == "(" &&
			forbiddenFunc(strings.ToLower(t.text)) {
			return nil, "", &connections.ProviderError{Code: CodeSQLForbiddenFunc}
		}
	}
	return toks, strings.TrimSpace(clean), nil
}

// qualifiers returns the names written before a "." (schema or table qualifiers), as the server would fold them:
// unquoted names lowercased, quoted names exact.
func qualifiers(toks []token) []string {
	var out []string
	for i := 0; i+1 < len(toks); i++ {
		if (toks[i].kind == tWord || toks[i].kind == tQuoted) && toks[i+1].kind == tPunct && toks[i+1].text == "." {
			out = append(out, toks[i].text)
		}
	}
	return out
}
