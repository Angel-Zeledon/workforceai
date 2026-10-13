package tenantdata

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Redacted is the value written in place of a secret column.
const Redacted = "[REDACTED]"

// Manifest is MANIFEST.json of an export.
type Manifest struct {
	FormatVersion int                `json:"format_version"`
	Organization  ManifestOrg        `json:"organization"`
	GeneratedAt   time.Time          `json:"generated_at"`
	GeneratedBy   string             `json:"generated_by,omitempty"`
	Tables        []ManifestTable    `json:"tables"`
	Files         int                `json:"files"`
	FileBytes     int64              `json:"file_bytes"`
	MissingFiles  []string           `json:"missing_files,omitempty"`
	Excluded      map[string]string  `json:"excluded_tables"`
	Redaction     ManifestRedactions `json:"redaction_policy"`
}

// ManifestOrg identifies the exported organization.
type ManifestOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ManifestTable describes one exported table.
type ManifestTable struct {
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Kind     string   `json:"kind"`
	Rows     int64    `json:"rows"`
	Redacted []string `json:"redacted_columns,omitempty"`
	Blobs    []string `json:"file_columns,omitempty"`
	Derived  []string `json:"omitted_derived_columns,omitempty"`
}

// ManifestRedactions documents what was never exported.
type ManifestRedactions struct {
	Value       string            `json:"value"`
	NamePattern string            `json:"column_name_pattern"`
	Denylist    map[string]string `json:"denylist"`
	Bytea       string            `json:"bytea"`
}

// Export is an export in progress: a read-only, repeatable-read transaction
// bound to the organization (RLS) on the application pool. Close it.
type Export struct {
	tx     pgx.Tx
	ctx    context.Context
	org    ManifestOrg
	schema *Schema
	svc    *Service
	by     string
}

// BeginExport opens the snapshot and checks that the organization exists. The
// HTTP layer calls it before sending any byte, so failures still get a JSON error.
func (s *Service) BeginExport(ctx context.Context, orgID, actor string) (*Export, error) {
	tx, err := s.App.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	e := &Export{tx: tx, ctx: ctx, svc: s, by: actor}
	fail := func(err error) (*Export, error) {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := auth.SetOrgID(ctx, tx, orgID); err != nil {
		return fail(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id, name, slug FROM organizations WHERE id = $1`, orgID).Scan(&e.org.ID, &e.org.Name, &e.org.Slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(domain.ErrNotFound)
		}
		return fail(err)
	}
	if e.schema, err = Discover(ctx, tx); err != nil {
		return fail(err)
	}
	return e, nil
}

// Org is the exported organization.
func (e *Export) Org() ManifestOrg { return e.org }

// Close ends the snapshot.
func (e *Export) Close() { _ = e.tx.Rollback(context.WithoutCancel(e.ctx)) }

// WriteZip streams the ZIP: one <table>.jsonl per table carrying the
// organization's data, files/ with the stored file bytes, README.txt and
// MANIFEST.json. Secret columns are removed inside the database (they never
// reach this process) and replaced by "[REDACTED]".
func (e *Export) WriteZip(w io.Writer) (Manifest, error) {
	ctx := e.ctx
	m := Manifest{FormatVersion: 1, Organization: e.org, GeneratedAt: time.Now().UTC(), GeneratedBy: e.by,
		Excluded: map[string]string{}, Redaction: ManifestRedactions{Value: Redacted, NamePattern: secretName.String(), Denylist: RedactedColumns,
			Bytea: "every bytea column is redacted except the file contents listed in BlobColumns, exported under files/"}}
	for n, why := range ExcludedFromExport {
		m.Excluded[n] = why
	}
	for n, why := range GlobalTables {
		m.Excluded[n] = "not organization data: " + why
	}
	for _, n := range e.schema.OfKind(KindAccount) {
		if n != AccountTable {
			m.Excluded[n] = "account security data (purged with the account, not exported)"
		}
	}
	zw := zip.NewWriter(w)
	names := map[string]bool{}
	for _, t := range e.schema.ExportPlan() {
		mt, err := e.writeTable(ctx, zw, t, names, &m)
		if err != nil {
			return m, err
		}
		m.Tables = append(m.Tables, mt)
	}
	if e.svc.Objects != nil {
		if err := e.writeObjects(ctx, zw, names, &m); err != nil {
			return m, err
		}
	}
	if err := writeEntry(zw, "MANIFEST.json", func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}); err != nil {
		return m, err
	}
	if err := writeEntry(zw, "README.txt", func(w io.Writer) error { _, err := io.WriteString(w, readme(m)); return err }); err != nil {
		return m, err
	}
	return m, zw.Close()
}

func writeEntry(zw *zip.Writer, name string, fn func(io.Writer) error) error {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now().UTC()})
	if err != nil {
		return err
	}
	return fn(f)
}

func (e *Export) writeTable(ctx context.Context, zw *zip.Writer, t ExportTable, names map[string]bool, m *Manifest) (ManifestTable, error) {
	mt := ManifestTable{Name: t.Name, File: t.Name + ".jsonl", Kind: t.Kind.String(), Redacted: t.Redacted, Blobs: t.Blobs, Derived: t.Derived}
	drop := append(append(append([]string{}, t.Redacted...), t.Blobs...), t.Derived...)
	from := ` FROM ` + ident(t.Name) + ` AS t WHERE ` + t.Scope + ` ORDER BY t.ctid`
	// Pass 1: the JSON lines (blob columns replaced by their file reference).
	// The snapshot is REPEATABLE READ, so ORDER BY ctid is the same in pass 2.
	sel := `SELECT to_jsonb(t) - $2::text[]`
	for _, b := range t.Blobs {
		sel += `, octet_length(t.` + ident(b) + `)`
	}
	rows, err := e.tx.Query(ctx, sel+from, e.org.ID, drop)
	if err != nil {
		return mt, fmt.Errorf("tenantdata: export %s: %w", t.Name, err)
	}
	defer rows.Close()
	f, err := zw.CreateHeader(&zip.FileHeader{Name: mt.File, Method: zip.Deflate, Modified: time.Now().UTC()})
	if err != nil {
		return mt, err
	}
	bw := bufio.NewWriterSize(f, 64<<10)
	var paths [][]string // per row, per blob column ("" = NULL)
	for rows.Next() {
		var doc []byte
		dst := []any{&doc}
		sizes := make([]*int64, len(t.Blobs))
		for i := range sizes {
			dst = append(dst, &sizes[i])
		}
		if err := rows.Scan(dst...); err != nil {
			return mt, err
		}
		obj := map[string]json.RawMessage{}
		if err := json.Unmarshal(doc, &obj); err != nil {
			return mt, err
		}
		for _, c := range t.Redacted {
			obj[c] = json.RawMessage(strconv.Quote(Redacted))
		}
		var rp []string
		for i, c := range t.Blobs {
			if sizes[i] == nil {
				obj[c] = json.RawMessage("null")
				rp = append(rp, "")
				continue
			}
			p := blobPath(t.Name, c, len(t.Blobs) > 1, obj, mt.Rows, names)
			ref, _ := json.Marshal(map[string]any{"file": p, "size": *sizes[i]})
			obj[c] = ref
			rp = append(rp, p)
		}
		if len(t.Blobs) > 0 {
			paths = append(paths, rp)
		}
		line, err := json.Marshal(obj)
		if err != nil {
			return mt, err
		}
		if _, err := bw.Write(append(line, '\n')); err != nil {
			return mt, err
		}
		mt.Rows++
	}
	if err := rows.Err(); err != nil {
		return mt, fmt.Errorf("tenantdata: export %s: %w", t.Name, err)
	}
	rows.Close()
	if err := bw.Flush(); err != nil {
		return mt, err
	}
	if len(t.Blobs) == 0 || len(paths) == 0 {
		return mt, nil
	}
	// Pass 2: the file bytes, one row in memory at a time.
	sel = `SELECT`
	for i, b := range t.Blobs {
		if i > 0 {
			sel += `,`
		}
		sel += ` t.` + ident(b)
	}
	rows, err = e.tx.Query(ctx, sel+from, e.org.ID)
	if err != nil {
		return mt, fmt.Errorf("tenantdata: export files of %s: %w", t.Name, err)
	}
	defer rows.Close()
	for i := 0; rows.Next(); i++ {
		if i >= len(paths) {
			return mt, fmt.Errorf("tenantdata: export files of %s: snapshot changed", t.Name)
		}
		raw := make([][]byte, len(t.Blobs))
		dst := make([]any, len(raw))
		for j := range raw {
			dst[j] = &raw[j]
		}
		if err := rows.Scan(dst...); err != nil {
			return mt, err
		}
		for j, data := range raw {
			if paths[i][j] == "" {
				continue
			}
			if err := writeEntry(zw, paths[i][j], func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
				return mt, err
			}
			m.Files++
			m.FileBytes += int64(len(data))
		}
	}
	return mt, rows.Err()
}

// writeObjects adds the objects of an external object store referenced by the
// organization's object_key columns, under files/objects/<key>.
func (e *Export) writeObjects(ctx context.Context, zw *zip.Writer, names map[string]bool, m *Manifest) error {
	keys, err := objectKeys(ctx, e.tx, e.schema, e.org.ID)
	if err != nil {
		return err
	}
	sort.Strings(keys)
	for _, k := range keys {
		rc, _, err := e.svc.Objects.Get(ctx, k)
		if err != nil {
			m.MissingFiles = append(m.MissingFiles, k)
			continue
		}
		p := uniquePath("files/objects/"+safePath(k), names)
		var n int64
		err = writeEntry(zw, p, func(w io.Writer) error { var err error; n, err = io.Copy(w, rc); return err })
		rc.Close()
		if err != nil {
			return err
		}
		m.Files++
		m.FileBytes += n
	}
	return nil
}

var unsafePath = regexp.MustCompile(`[^A-Za-z0-9._=@/-]`)

// safePath turns an identifier into a relative path without "..", empty or absolute segments.
func safePath(s string) string {
	s = unsafePath.ReplaceAllString(s, "_")
	var segs []string
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return "_"
	}
	return strings.Join(segs, "/")
}

func uniquePath(p string, names map[string]bool) string {
	out := p
	for i := 2; names[out]; i++ {
		out = fmt.Sprintf("%s~%d", p, i)
	}
	names[out] = true
	return out
}

// blobPath names a file entry after the row's key or id when it has one.
func blobPath(table, col string, multi bool, obj map[string]json.RawMessage, idx int64, names map[string]bool) string {
	name := strconv.FormatInt(idx+1, 10)
	for _, k := range []string{"key", "id"} {
		var v string
		if raw, ok := obj[k]; ok && json.Unmarshal(raw, &v) == nil && v != "" {
			name = v
			break
		}
	}
	p := "files/" + table + "/" + safePath(name)
	if multi {
		p += "." + col
	}
	return uniquePath(p, names)
}

func readme(m Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AI Workforce OS - organization data export / Exportación de datos de la organización\n\n")
	fmt.Fprintf(&b, "Organization: %s (%s)\nGenerated at: %s\n\n", m.Organization.Name, m.Organization.ID, m.GeneratedAt.Format(time.RFC3339))
	b.WriteString(`SCOPE
Every table of the database that carries this organization's data: the tables with an org_id
column, the tables linked to them through foreign keys, and the accounts (users) that are members
of the organization. Each table is one JSON Lines file (<table>.jsonl, one JSON object per row,
column names as keys, empty when the organization has no rows there). File contents stored in the
database (Drive files, PDF blobs) are under files/ and referenced from their row as {"file": ...}.

SECRETS ARE NEVER EXPORTED
Password hashes, session/invitation/verification token hashes, vault ciphertext and keys, sealed
API keys and OAuth secrets, Web Push keys: those columns are removed inside the database and appear
as "[REDACTED]". MANIFEST.json lists the redacted columns of every table and the full policy.

ALCANCE (es)
Todas las tablas con datos de esta organización, un archivo JSON Lines por tabla, los archivos
del Drive en files/. Los secretos (contraseñas, tokens, claves cifradas) nunca se exportan y
aparecen como "[REDACTED]".

TABLES / TABLAS (rows)
`)
	for _, t := range m.Tables {
		fmt.Fprintf(&b, "  %-32s %8d\n", t.Name, t.Rows)
	}
	fmt.Fprintf(&b, "\nFiles / Archivos: %d (%d bytes)\n", m.Files, m.FileBytes)
	if len(m.MissingFiles) > 0 {
		fmt.Fprintf(&b, "Missing objects (could not be read from the object store): %d\n", len(m.MissingFiles))
	}
	if len(m.Excluded) > 0 {
		b.WriteString("\nNOT EXPORTED / NO EXPORTADO\n")
		var ks []string
		for k := range m.Excluded {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Fprintf(&b, "  %s: %s\n", k, m.Excluded[k])
		}
	}
	return b.String()
}
