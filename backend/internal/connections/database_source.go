package connections

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// DatabaseProvider is the generic read-only database source (docs/architecture/database-sources.md). Like
// custom_api it is generic (not tied to a vendor), so VENDOR_INTEGRATIONS never switches it off.
const DatabaseProvider = "database"

// Database engines. Only Postgres is implemented; the field exists so MySQL can be added without a migration.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

// Database is the NON-secret configuration of a database source: where it is and as whom we log in. It is
// admin-written organization configuration. The password lives in the vault like every other credential and
// is never part of this struct (nor of any response, event, log or prompt).
type Database struct {
	Engine  string `json:"engine"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Name    string `json:"database"`
	User    string `json:"user"`
	SSLMode string `json:"sslmode"`
	// Description tells the agents what lives in this database (shown to them, admin-written).
	Description string `json:"description,omitempty"`
	// Schemas narrows what the agents see and may name (empty = every non-system schema). It is a
	// visibility limit enforced by us; the hard limit is the privileges of the database user.
	Schemas []string `json:"schemas,omitempty"`
}

// SSL modes accepted (libpq names). "require" is the default: encrypted, like every hosted Postgres expects.
var sslModes = map[string]bool{"disable": true, "require": true, "verify-ca": true, "verify-full": true}

const (
	maxDatabaseSchemas = 20
	maxDBDescription   = 500
)

var (
	dbHostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$|^\[?[0-9A-Fa-f:.]{2,45}\]?$`)
	dbIdentRe  = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,63}$`)
	dbSchemaRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]{0,62}$`)
)

// Normalize validates the configuration and returns it trimmed, with defaults applied (port 5432, sslmode
// require). It checks the syntax only: where the host resolves to is checked on every dial.
func (d Database) Normalize() (Database, error) {
	d.Engine = strings.ToLower(strings.TrimSpace(d.Engine))
	if d.Engine == "" || d.Engine == "postgresql" {
		d.Engine = EnginePostgres
	}
	if d.Engine == EngineMySQL {
		return Database{}, invalid("engine_not_supported: MySQL is not available yet; use PostgreSQL")
	}
	if d.Engine != EnginePostgres {
		return Database{}, invalid("engine_not_supported: engine must be postgres")
	}
	d.Host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d.Host)), ".")
	d.Host = strings.TrimSuffix(strings.TrimPrefix(d.Host, "["), "]")
	if d.Host == "" || !dbHostRe.MatchString(d.Host) {
		return Database{}, invalid("database.host must be a host name or an IP address")
	}
	if d.Port == 0 {
		d.Port = 5432
	}
	if d.Port < 1 || d.Port > 65535 {
		return Database{}, invalid("database.port must be between 1 and 65535")
	}
	d.Name, d.User = strings.TrimSpace(d.Name), strings.TrimSpace(d.User)
	if d.Name == "" {
		d.Name = "postgres"
	}
	if !dbIdentRe.MatchString(d.Name) {
		return Database{}, invalid("database.database is not a valid database name")
	}
	if d.User == "" || !dbIdentRe.MatchString(d.User) {
		return Database{}, invalid("database.user is required")
	}
	d.SSLMode = strings.ToLower(strings.TrimSpace(d.SSLMode))
	if d.SSLMode == "" {
		d.SSLMode = "require"
	}
	if !sslModes[d.SSLMode] {
		return Database{}, invalid("database.sslmode must be disable, require, verify-ca or verify-full")
	}
	d.Description = strings.TrimSpace(d.Description)
	if len([]rune(d.Description)) > maxDBDescription {
		return Database{}, invalid("database.description is too long (max %d)", maxDBDescription)
	}
	d.Schemas = dedupe(d.Schemas)
	if len(d.Schemas) > maxDatabaseSchemas {
		return Database{}, invalid("database.schemas has at most %d entries", maxDatabaseSchemas)
	}
	for _, s := range d.Schemas {
		if !dbSchemaRe.MatchString(s) {
			return Database{}, invalid("database.schemas: %q is not a schema name", s)
		}
	}
	return d, nil
}

// Account is what the UI and the agents may know of the login: user@host:port/db (never the password).
func (d Database) Account() string {
	return d.User + "@" + d.Host + ":" + strconv.Itoa(d.Port) + "/" + d.Name
}

// ParseConnString splits a postgres:// (or postgresql://) connection URL into the non-secret configuration and
// the password. The caller stores the password in the vault and drops the string: the whole URL is never kept.
func ParseConnString(raw string) (Database, string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return Database{}, "", invalid("connection_string must look like postgresql://user:password@host:5432/database")
	}
	d := Database{Engine: EnginePostgres, Host: u.Hostname(), Name: strings.TrimPrefix(u.Path, "/")}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Database{}, "", invalid("connection_string has an invalid port")
		}
		d.Port = n
	}
	pass := ""
	if u.User != nil {
		d.User = u.User.Username()
		pass, _ = u.User.Password()
	}
	q := u.Query()
	d.SSLMode = q.Get("sslmode")
	if v := q.Get("password"); v != "" && pass == "" {
		pass = v
	}
	if v := q.Get("user"); v != "" && d.User == "" {
		d.User = v
	}
	n, err := d.Normalize()
	return n, pass, err
}

// SchemaFilter narrows a schema listing to one schema and/or one table (exact names).
type SchemaFilter struct {
	Schema string `json:"schema,omitempty"`
	Table  string `json:"table,omitempty"`
}

// DBSchema is what a database user may read: tables and views with their columns and comments. Names and comments
// are written in the database, so for agents they are untrusted text (they go through the sanitizer).
type DBSchema struct {
	Tables    []DBTable `json:"tables"`
	Truncated bool      `json:"truncated"`
}

// DBTable is one table or view.
type DBTable struct {
	Schema       string     `json:"schema"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"` // table | view | materialized_view | foreign_table
	Comment      string     `json:"comment,omitempty"`
	RowsEstimate int64      `json:"rows_estimate"` // planner estimate, -1 when unknown
	Columns      []DBColumn `json:"columns"`
}

// DBColumn is one column.
type DBColumn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"not_null"`
	Comment string `json:"comment,omitempty"`
}

// DBField is one result column.
type DBField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// DBResult is the bounded answer of one read query. Values are the server's text form; nil is NULL.
type DBResult struct {
	Columns     []DBField   `json:"columns"`
	Rows        [][]*string `json:"rows"`
	RowCount    int         `json:"row_count"`
	Truncated   bool        `json:"truncated"`
	TruncatedBy string      `json:"truncated_by,omitempty"` // rows | bytes
	ElapsedMS   int         `json:"elapsed_ms"`
	// Error is a mistake in the statement the author can fix (syntax, unknown table...). Never a password.
	Error *DBError `json:"error,omitempty"`
}

// DBError is a statement error reported by the database (SQLSTATE and its message, scrubbed).
type DBError struct {
	SQLState string `json:"sqlstate"`
	Message  string `json:"message"`
}

// DatabaseExplorer is implemented by the database provider for the admin screens (schema and a test query).
type DatabaseExplorer interface {
	Schema(ctx context.Context, h Handle, f SchemaFilter) (DBSchema, error)
	Query(ctx context.Context, h Handle, sql string) (DBResult, error)
}
