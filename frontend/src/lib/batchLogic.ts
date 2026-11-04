// Batches ("lotes": one template launched for many customers): pure rules, tested by batchLogic.test.mjs.
// No path-alias imports: node runs the test on this file directly.

export type ImportField = "name" | "email" | "phone" | "company" | "notes" | "tax_id" | "contact_name";
export type ParsedRow = Partial<Record<ImportField, string>>;

/** Header words (es/en/pt, accents and case ignored) -> field. */
const HEADERS: Record<string, ImportField> = {
  nombre: "name", name: "name", nome: "name", cliente: "name", customer: "name",
  correo: "email", email: "email", "e-mail": "email", mail: "email", "correo electronico": "email",
  telefono: "phone", phone: "phone", tel: "phone", celular: "phone", movil: "phone", whatsapp: "phone",
  empresa: "company", company: "company", compania: "company", "razon social": "company",
  notas: "notes", notes: "notes", nota: "notes", observaciones: "notes", notas_: "notes",
  nif: "tax_id", "tax id": "tax_id", tax_id: "tax_id", cedula: "tax_id", "cedula juridica": "tax_id", rfc: "tax_id", cuit: "tax_id", ruc: "tax_id", cnpj: "tax_id", "numero fiscal": "tax_id",
  contacto: "contact_name", contact: "contact_name", "persona de contacto": "contact_name", contato: "contact_name",
};

const fold = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().trim().replace(/\s+/g, " ");

function splitLine(line: string, sep: string): string[] {
  const out: string[] = [];
  let cur = "", quoted = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quoted) {
      if (c === '"' && line[i + 1] === '"') { cur += '"'; i++; }
      else if (c === '"') quoted = false;
      else cur += c;
    } else if (c === '"' && cur === "") quoted = true;
    else if (c === sep) { out.push(cur); cur = ""; }
    else cur += c;
  }
  out.push(cur);
  return out.map((v) => v.trim());
}

/** Parses a CSV file or rows pasted from a spreadsheet (tab, ";" or "," separated, first line = headers). */
export function parseList(text: string): { rows: ParsedRow[]; columns: ImportField[]; errors: string[] } {
  const lines = String(text || "").replace(/^﻿/, "").split(/\r?\n/).filter((l) => l.trim() !== "");
  if (!lines.length) return { rows: [], columns: [], errors: ["empty"] };
  const head = lines[0];
  const sep = head.includes("\t") ? "\t" : head.split(";").length > head.split(",").length ? ";" : ",";
  const cols = splitLine(head, sep).map((h) => HEADERS[fold(h)] ?? null);
  const columns = cols.filter((c): c is ImportField => c !== null);
  if (!columns.includes("name") && !columns.includes("company")) return { rows: [], columns, errors: ["no_name_column"] };
  const rows: ParsedRow[] = [];
  for (const line of lines.slice(1)) {
    const vals = splitLine(line, sep);
    const row: ParsedRow = {};
    cols.forEach((c, i) => { if (c && vals[i]) row[c] = vals[i]; });
    if (Object.keys(row).length) rows.push(row);
  }
  return { rows, columns, errors: [] };
}

export const ROW_STATUSES = ["needs_help", "running", "queued", "paused", "done", "partial", "failed", "cancelled"] as const;
export type RowStatus = (typeof ROW_STATUSES)[number];

export function filterRows<T extends { status: string }>(rows: T[], status: string): T[] {
  return status ? rows.filter((r) => r.status === status) : rows;
}

/** Color family of a row status in the table. */
// Same families as the single palette (lib/statusLogic.ts; statusLogic.test.mjs checks they never drift apart).
export function rowTone(status: string): "running" | "err" | "ok" | "warn" | "mute" {
  switch (status) {
    case "needs_help": return "warn"; // "Te necesita" is amber everywhere
    case "running": return "running";
    case "failed": return "err";
    case "done": case "partial": return "ok";
    default: return "mute";
  }
}

/** Inserts {{cliente.<field>}} at a cursor position. */
export function insertVar(text: string, cursor: number, field: string): { text: string; cursor: number } {
  const tok = `{{cliente.${field}}}`;
  const at = Math.max(0, Math.min(cursor, text.length));
  return { text: text.slice(0, at) + tok + text.slice(at), cursor: at + tok.length };
}
