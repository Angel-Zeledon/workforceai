/** Pure helpers of the "database" provider form (read-only database source): draft <-> DatabaseConfig and the same
 *  validation rules as the server (backend/internal/connections/database_source.go). No React, no network, and never
 *  the password or the connection string: those stay in uncontrolled inputs and are sent once. */
import type { DatabaseConfig, DbEngine, DbSslMode } from "./types";

export const DATABASE = "database";
export const DB_LIMITS = { description: 500, schemas: 20, maxRows: 500 } as const;
export const SSL_MODES: DbSslMode[] = ["require", "verify-full", "verify-ca", "disable"];
const HOST_RE = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$|^\[?[0-9A-Fa-f:.]{2,45}\]?$/;
const SCHEMA_RE = /^[A-Za-z_][A-Za-z0-9_$]{0,62}$/;

/** "fields": host, port, database, user (+ password apart); "url": one pasted connection string (secret). */
export type DbInputMode = "fields" | "url";
export interface DbDraft {
  mode: DbInputMode;
  engine: DbEngine;
  host: string;
  port: string;
  database: string;
  user: string;
  sslmode: DbSslMode;
  description: string;
  /** comma-separated */
  schemas: string;
}
export type DbErrCode = "host_required" | "host_invalid" | "port_invalid" | "user_required" | "schemas_invalid" | "schemas_too_many"
  | "description_too_long" | "engine_soon";
export interface DbErrors { host?: DbErrCode; port?: DbErrCode; user?: DbErrCode; schemas?: DbErrCode; description?: DbErrCode; engine?: DbErrCode }

export const emptyDbDraft = (): DbDraft => ({ mode: "fields", engine: "postgres", host: "", port: "5432", database: "postgres", user: "", sslmode: "require", description: "", schemas: "" });

/** "public, sales,,public" -> ["public", "sales"]. */
export function parseSchemas(text: string): string[] {
  const out: string[] = [];
  for (const p of text.split(",")) { const v = p.trim(); if (v && !out.includes(v)) out.push(v); }
  return out;
}

/** Field errors. In "url" mode only what a connection string cannot say is checked (description, schemas). */
export function validateDb(d: DbDraft): DbErrors {
  const e: DbErrors = {};
  if (d.engine !== "postgres") e.engine = "engine_soon";
  if (d.mode === "fields") {
    const host = d.host.trim();
    if (!host) e.host = "host_required"; else if (!HOST_RE.test(host)) e.host = "host_invalid";
    const raw = d.port.trim() || "5432";
    const port = Number(raw);
    if (!/^\d+$/.test(raw) || port < 1 || port > 65535) e.port = "port_invalid";
    if (!d.user.trim()) e.user = "user_required";
  }
  const schemas = parseSchemas(d.schemas);
  if (schemas.length > DB_LIMITS.schemas) e.schemas = "schemas_too_many";
  else if (schemas.some((s) => !SCHEMA_RE.test(s))) e.schemas = "schemas_invalid";
  if ([...d.description].length > DB_LIMITS.description) e.description = "description_too_long";
  return e;
}
export const hasDbErrors = (e: DbErrors) => Object.values(e).some(Boolean);

/** The configuration sent to the server (never the password). In "url" mode only description and schemas travel
 *  here; the server reads the rest from the connection string. */
export function draftToDatabase(d: DbDraft): Partial<DatabaseConfig> {
  const extra = { ...(d.description.trim() ? { description: d.description.trim() } : {}), schemas: parseSchemas(d.schemas) };
  if (d.mode === "url") return extra;
  return { engine: d.engine, host: d.host.trim(), port: Number(d.port.trim() || "5432"), database: d.database.trim() || "postgres",
    user: d.user.trim(), sslmode: d.sslmode, ...extra };
}

export function dbToDraft(c?: DatabaseConfig): DbDraft {
  if (!c) return emptyDbDraft();
  return { mode: "fields", engine: c.engine ?? "postgres", host: c.host ?? "", port: String(c.port ?? 5432), database: c.database ?? "postgres",
    user: c.user ?? "", sslmode: c.sslmode ?? "require", description: c.description ?? "", schemas: (c.schemas ?? []).join(", ") };
}

/** A pasted connection string looks right (the server parses it; this only catches an obvious mistake early). */
export const looksLikeConnString = (s: string) => /^postgres(ql)?:\/\/\S+$/i.test(s.trim());

/** Codes the server may answer for a database call (each has a conn.db.err.<code> text). */
export const DB_ERROR_CODES = ["sql_empty", "sql_too_long", "sql_invalid", "sql_multiple_statements", "sql_not_read_only", "sql_forbidden_function",
  "sql_schema_not_allowed", "sql_timeout", "db_read_only_not_enforced", "db_unreachable", "db_address_not_allowed", "db_host_not_found", "db_tls_failed",
  "db_not_found", "db_not_configured", "reauth_required", "rate_limit", "engine_not_supported"] as const;
export const isDbErrorCode = (c: string | undefined): c is (typeof DB_ERROR_CODES)[number] => !!c && (DB_ERROR_CODES as readonly string[]).includes(c);
