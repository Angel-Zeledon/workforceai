package database

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/webtool"
)

// Limits bound one call. They are ours: the database user cannot widen them.
type Limits struct {
	MaxRows          int
	MaxBytes         int
	MaxCellChars     int
	StatementTimeout time.Duration
	IdleTimeout      time.Duration
	LockTimeout      time.Duration
	ConnectTimeout   time.Duration
}

// DefaultLimits are the limits of every query (docs/architecture/database-sources.md).
var DefaultLimits = Limits{MaxRows: 500, MaxBytes: 1 << 20, MaxCellChars: 2000, StatementTimeout: 15 * time.Second,
	IdleTimeout: 20 * time.Second, LockTimeout: 3 * time.Second, ConnectTimeout: 8 * time.Second}

// session is one short-lived connection inside a READ ONLY transaction. Nothing is ever committed.
type session struct {
	conn *pgx.Conn
	tx   pgx.Tx
	pass string // only to scrub it from error text
}

var errBlocked = errors.New(CodeAddressNotAllowed)

// netFuncs returns the resolver and dialer of a database connection. The host is admin-written, so by default it
// must not become a way into the server's own network: every name is resolved here, all answers must be public,
// and the dialer re-checks the final address (no DNS rebinding window). allowPrivate (self-hosted,
// DATABASE_ALLOW_PRIVATE) lets an organization reach a database on its own network.
func netFuncs(allowPrivate bool, timeout time.Duration) (pgconn.LookupFunc, pgconn.DialFunc) {
	if allowPrivate {
		d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		return net.DefaultResolver.LookupHost, d.DialContext
	}
	guard := webtool.NetGuard{}
	control := func(_, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return errBlocked
		}
		a, err := netip.ParseAddr(h)
		if err != nil || webtool.CheckAddr(a) != nil {
			return errBlocked
		}
		return nil
	}
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: control}
	lookup := func(ctx context.Context, host string) ([]string, error) {
		addrs, err := guard.Resolve(ctx, host)
		if err != nil {
			if errors.Is(err, webtool.ErrResolve) {
				return nil, errHostNotFound
			}
			return nil, errBlocked
		}
		out := make([]string, len(addrs))
		for i, a := range addrs {
			out[i] = a.String()
		}
		return out, nil
	}
	return lookup, d.DialContext
}

var errHostNotFound = errors.New(CodeHostNotFound)

// config builds the driver configuration WITHOUT reading a connection string written by anyone: every field is
// set from the validated Database. The password is set by the caller, inside the vault callback.
func config(d connections.Database, lim Limits, allowPrivate bool) (*pgx.ConnConfig, error) {
	u := url.URL{Scheme: "postgres", User: url.User(d.User), Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: "/" + d.Name}
	q := url.Values{}
	q.Set("sslmode", d.SSLMode)
	q.Set("connect_timeout", strconv.Itoa(int(lim.ConnectTimeout/time.Second)))
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, &connections.ProviderError{Code: connections.CodeInvalidArgs}
	}
	if cfg.TLSConfig != nil && cfg.TLSConfig.MinVersion < tls.VersionTLS12 {
		cfg.TLSConfig.MinVersion = tls.VersionTLS12
	}
	cfg.LookupFunc, cfg.DialFunc = netFuncs(allowPrivate, lim.ConnectTimeout)
	// The extended protocol: the server itself refuses a second statement in one call.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	cfg.StatementCacheCapacity, cfg.DescriptionCacheCapacity = 0, 0
	cfg.RuntimeParams = map[string]string{"application_name": "aiworkforce-readonly"}
	return cfg, nil
}

// open connects (the password is lent by the vault for this call only) and starts the READ ONLY transaction with
// our timeouts. It refuses to go on if the server does not report the transaction as read only.
func open(ctx context.Context, h connections.Handle, lim Limits, allowPrivate bool) (*session, error) {
	if h.Database == nil {
		return nil, &connections.ProviderError{Code: CodeNotConfigured}
	}
	if h.Secret == nil {
		return nil, &connections.ProviderError{Code: connections.CodeConnectionUnavailable}
	}
	cfg, err := config(*h.Database, lim, allowPrivate)
	if err != nil {
		return nil, err
	}
	s := &session{}
	err = h.Secret(ctx, func(pw []byte) error {
		cfg.Password = string(pw)
		s.pass = cfg.Password
		c, cerr := pgx.ConnectConfig(ctx, cfg)
		s.conn = c
		return cerr
	})
	if err != nil {
		return nil, classifyConnect(err)
	}
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		s.close()
		return nil, classifyConnect(err)
	}
	s.tx = tx
	// One round trip: set_config(..., true) is SET LOCAL. standard_conforming_strings keeps the server's reading of
	// literals equal to CheckSQL's.
	ms := func(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }
	var ro string
	err = tx.QueryRow(ctx, `SELECT set_config('statement_timeout', $1, true), set_config('idle_in_transaction_session_timeout', $2, true),
		set_config('lock_timeout', $3, true), set_config('standard_conforming_strings', 'on', true), current_setting('transaction_read_only')`,
		ms(lim.StatementTimeout), ms(lim.IdleTimeout), ms(lim.LockTimeout)).Scan(nil, nil, nil, nil, &ro)
	if err != nil {
		s.close()
		return nil, classifyConnect(err)
	}
	if ro != "on" {
		s.close()
		return nil, &connections.ProviderError{Code: CodeReadOnlyMissing}
	}
	return s, nil
}

// close always rolls back (nothing is ever committed) and closes the connection.
func (s *session) close() {
	if s == nil || s.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.tx != nil {
		_ = s.tx.Rollback(ctx)
	}
	_ = s.conn.Close(ctx)
}

// setSearchPath narrows unqualified names to the allowed schemas (pg_catalog is always searched first).
func (s *session) setSearchPath(ctx context.Context, schemas []string) error {
	if len(schemas) == 0 {
		return nil
	}
	quoted := make([]string, len(schemas))
	for i, sc := range schemas {
		quoted[i] = pgx.Identifier{sc}.Sanitize()
	}
	_, err := s.tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, strings.Join(quoted, ", "))
	return err
}

// classifyConnect turns a driver error into a stable code. Driver text (it names host, user and database) never
// leaves this function.
func classifyConnect(err error) error {
	var pe *connections.ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &connections.ProviderError{Code: CodeUnreachable}
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.Code == "28P01" || pg.Code == "28000":
			return &connections.ProviderError{Code: connections.CodeReauthRequired}
		case pg.Code == "3D000":
			return &connections.ProviderError{Code: CodeDatabaseNotFound}
		case pg.Code == "53300" || pg.Code == "57P03":
			return &connections.ProviderError{Code: connections.CodeRateLimit}
		}
		return &connections.ProviderError{Code: connections.CodeProviderError}
	}
	switch {
	case errors.Is(err, errBlocked):
		return &connections.ProviderError{Code: CodeAddressNotAllowed}
	case errors.Is(err, errHostNotFound):
		return &connections.ProviderError{Code: CodeHostNotFound}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "tls") || strings.Contains(msg, "ssl") || strings.Contains(msg, "x509") || strings.Contains(msg, "certificate"):
		return &connections.ProviderError{Code: CodeTLS}
	case strings.Contains(msg, "no such host"):
		return &connections.ProviderError{Code: CodeHostNotFound}
	case strings.Contains(msg, "password authentication failed"):
		return &connections.ProviderError{Code: connections.CodeReauthRequired}
	}
	return &connections.ProviderError{Code: CodeUnreachable}
}

// scrub removes the password from a server message (Suspects does it again at the gateway).
func (s *session) scrub(msg string) string {
	if s != nil && len(s.pass) >= 4 {
		msg = strings.ReplaceAll(msg, s.pass, "[REDACTED]")
	}
	return msg
}

// queryError classifies an error of the agent's statement. Mistakes the agent can fix (syntax, unknown table or
// column, bad value, missing privilege) come back as data; a write refused by the READ ONLY transaction, a timeout
// and anything else are errors with a stable code.
func (s *session) queryError(err error) (*connections.DBError, error) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &connections.ProviderError{Code: CodeSQLTimeout}
		}
		return nil, classifyConnect(err)
	}
	switch {
	case pg.Code == "25006": // read_only_sql_transaction: the database refused a write
		return nil, &connections.ProviderError{Code: CodeSQLNotReadOnly}
	case pg.Code == "57014": // query_canceled: our statement_timeout
		return nil, &connections.ProviderError{Code: CodeSQLTimeout}
	case pg.Code == "55P03": // lock_not_available: our lock_timeout
		return nil, &connections.ProviderError{Code: CodeSQLTimeout}
	case strings.HasPrefix(pg.Code, "42"), strings.HasPrefix(pg.Code, "22"), pg.Code == "0A000", pg.Code == "21000", pg.Code == "2BP01", pg.Code == "P0001":
		msg := pg.Message
		if pg.Hint != "" {
			msg += " (" + pg.Hint + ")"
		}
		return &connections.DBError{SQLState: pg.Code, Message: connections.Truncate(s.scrub(msg), 300)}, nil
	}
	return nil, &connections.ProviderError{Code: connections.CodeProviderError}
}

// query runs one checked statement and reads at most lim.MaxRows rows and lim.MaxBytes bytes. Values come back in
// the server's text form (exactly what psql shows); NULL is nil.
func (s *session) query(ctx context.Context, sql string, lim Limits) (connections.DBResult, error) {
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	start := time.Now()
	rows, err := s.tx.Query(qctx, sql)
	if err != nil {
		de, e := s.queryError(err)
		return connections.DBResult{Error: de, ElapsedMS: int(time.Since(start).Milliseconds())}, e
	}
	res := connections.DBResult{Columns: []connections.DBField{}, Rows: [][]*string{}}
	tm := s.conn.TypeMap()
	for _, f := range rows.FieldDescriptions() {
		typ := "oid:" + strconv.FormatUint(uint64(f.DataTypeOID), 10)
		if t, ok := tm.TypeForOID(f.DataTypeOID); ok {
			typ = t.Name
		}
		res.Columns = append(res.Columns, connections.DBField{Name: f.Name, Type: typ})
	}
	bytes := 0
	for rows.Next() {
		if len(res.Rows) >= lim.MaxRows {
			res.Truncated, res.TruncatedBy = true, "rows"
			break
		}
		raw := rows.RawValues()
		row := make([]*string, len(raw))
		for i, v := range raw {
			if v == nil {
				continue
			}
			bytes += len(v)
			str := connections.Truncate(s.scrub(string(v)), lim.MaxCellChars)
			row[i] = &str
		}
		res.Rows = append(res.Rows, row)
		if bytes > lim.MaxBytes {
			res.Truncated, res.TruncatedBy = true, "bytes"
			break
		}
	}
	stopped := res.Truncated
	if stopped {
		cancel() // stop the server now: the rest is never read (the connection is closed right after)
	}
	rows.Close()
	res.RowCount, res.ElapsedMS = len(res.Rows), int(time.Since(start).Milliseconds())
	if err := rows.Err(); err != nil && !stopped {
		de, e := s.queryError(err)
		res.Error = de
		if e != nil {
			return res, e
		}
		res.Rows, res.RowCount = [][]*string{}, 0
	}
	return res, nil
}

// checkSchemas refuses names qualified with a schema of this database the admin did not allow (best effort: the
// hard limit is the privileges of the database user).
func (s *session) checkSchemas(ctx context.Context, toks []token, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	quals := qualifiers(toks)
	if len(quals) == 0 {
		return nil
	}
	rows, err := s.tx.Query(ctx, `SELECT nspname FROM pg_namespace`)
	if err != nil {
		return classifyConnect(err)
	}
	exists := map[string]bool{"pg_catalog": true, "information_schema": true}
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			exists[n] = true
		}
	}
	rows.Close()
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, q := range quals {
		if exists[q] && !ok[q] {
			return &connections.ProviderError{Code: CodeSQLSchema}
		}
	}
	return nil
}

const schemaSQL = `SELECT n.nspname, c.relname, c.relkind::text, COALESCE(obj_description(c.oid, 'pg_class'), ''),
	GREATEST(c.reltuples, -1)::bigint, COALESCE(a.attname, ''), COALESCE(format_type(a.atttypid, a.atttypmod), ''),
	COALESCE(a.attnotnull, false), COALESCE(col_description(c.oid, a.attnum), '')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE c.relkind IN ('r', 'v', 'm', 'p', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
  AND ($1::text[] IS NULL OR n.nspname = ANY($1::text[]))
  AND ($2 = '' OR n.nspname = $2) AND ($3 = '' OR c.relname = $3)
  AND has_schema_privilege(n.oid, 'USAGE') AND has_table_privilege(c.oid, 'SELECT')
ORDER BY n.nspname, c.relname, a.attnum
LIMIT 20000`

var relKinds = map[string]string{"r": "table", "p": "table", "v": "view", "m": "materialized_view", "f": "foreign_table"}

// schema lists the tables and views the database user may read, with columns and comments.
func (s *session) schema(ctx context.Context, allowed []string, f connections.SchemaFilter, maxTables int) (connections.DBSchema, error) {
	var arr any
	if len(allowed) > 0 {
		arr = allowed
	}
	rows, err := s.tx.Query(ctx, schemaSQL, arr, f.Schema, f.Table)
	if err != nil {
		return connections.DBSchema{}, classifyConnect(err)
	}
	defer rows.Close()
	out := connections.DBSchema{Tables: []connections.DBTable{}}
	for rows.Next() {
		var sc, name, kind, comment, col, typ, colComment string
		var est int64
		var notNull bool
		if err := rows.Scan(&sc, &name, &kind, &comment, &est, &col, &typ, &notNull, &colComment); err != nil {
			return connections.DBSchema{}, classifyConnect(err)
		}
		n := len(out.Tables)
		if n == 0 || out.Tables[n-1].Schema != sc || out.Tables[n-1].Name != name {
			if n >= maxTables {
				out.Truncated = true
				break
			}
			out.Tables = append(out.Tables, connections.DBTable{Schema: sc, Name: name, Kind: relKinds[kind], Comment: connections.Truncate(comment, 300),
				RowsEstimate: est, Columns: []connections.DBColumn{}})
			n++
		}
		if col != "" {
			t := &out.Tables[n-1]
			t.Columns = append(t.Columns, connections.DBColumn{Name: col, Type: typ, NotNull: notNull, Comment: connections.Truncate(colComment, 200)})
		}
	}
	if err := rows.Err(); err != nil && !out.Truncated {
		return connections.DBSchema{}, classifyConnect(err)
	}
	return out, nil
}

func describe(t connections.DBTable) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s.%s (%s", t.Schema, t.Name, t.Kind)
	if t.RowsEstimate >= 0 {
		fmt.Fprintf(&b, ", ~%d filas", t.RowsEstimate)
	}
	b.WriteString(")")
	if t.Comment != "" {
		b.WriteString(" -- " + t.Comment)
	}
	for _, c := range t.Columns {
		fmt.Fprintf(&b, "\n  %s %s", c.Name, c.Type)
		if c.NotNull {
			b.WriteString(" not null")
		}
		if c.Comment != "" {
			b.WriteString(" -- " + c.Comment)
		}
	}
	return b.String()
}
