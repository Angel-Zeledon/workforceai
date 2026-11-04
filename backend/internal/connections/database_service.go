package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"aiworkforce/backend/internal/sanitize"
)

// explorer returns an active database connection and its provider (admin screens).
func (s *Service) explorer(ctx context.Context, org, id string) (Connection, DatabaseExplorer, error) {
	c, err := s.Store.GetConnection(ctx, org, id)
	if err != nil {
		return Connection{}, nil, err
	}
	if c.Provider != DatabaseProvider || c.Database == nil {
		return Connection{}, nil, invalid("not a database connection")
	}
	if err := s.usable(c); err != nil {
		return Connection{}, nil, err
	}
	if c.Status != StatusActive {
		return Connection{}, nil, fmt.Errorf("%w: connection is %s", ErrConflict, c.Status)
	}
	ex, ok := s.Providers[c.Provider].(DatabaseExplorer)
	if !ok {
		return Connection{}, nil, ErrUnknownProvider
	}
	return c, ex, nil
}

// DatabaseSchema lists the tables and columns the database user may read (Settings > Connections > View tables).
func (s *Service) DatabaseSchema(ctx context.Context, org, id, actor string, f SchemaFilter) (DBSchema, error) {
	c, ex, err := s.explorer(ctx, org, id)
	if err != nil {
		return DBSchema{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	sc, err := ex.Schema(cctx, s.Handle(c), f)
	s.Audit(ctx, org, actor, "connection.db_schema_viewed", "connection", id, map[string]any{"tables": len(sc.Tables), "code": errCode(err)})
	return sc, err
}

// DatabaseQuery runs one test query by hand, under exactly the rules the agents get (read only, one statement,
// timeouts, caps). The audit keeps a hash and the length of the SQL, never its text nor any value.
func (s *Service) DatabaseQuery(ctx context.Context, org, id, actor, sql string) (DBResult, error) {
	c, ex, err := s.explorer(ctx, org, id)
	if err != nil {
		return DBResult{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := ex.Query(cctx, s.Handle(c), sql)
	if res.Error != nil {
		res.Error.Message = sanitize.RedactSecrets(res.Error.Message, s.Suspects)
	}
	sum := sha256.Sum256([]byte(sql))
	d := map[string]any{"sql_sha256": hex.EncodeToString(sum[:8]), "sql_chars": len(sql), "rows": res.RowCount, "truncated": res.Truncated, "code": errCode(err)}
	if res.Error != nil {
		d["sqlstate"] = res.Error.SQLState
	}
	s.Audit(ctx, org, actor, "connection.db_test_query", "connection", id, d)
	return res, err
}

func errCode(err error) string {
	if err == nil {
		return ""
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return CodeProviderError
}
