/** F3 "Guardar un departamento como plantilla": pure logic of the export dialog, the import and the preview
 *  (tests: officeTemplatesLogic.test.mjs). Contract: backend/internal/api/office_templates.go and
 *  backend/internal/officetemplates (format aiw.office-template, format_version 1). */
import type { TeamPreviewAgent } from "./teamsCatalogLogic";

export const TEMPLATE_FORMAT = "aiw.office-template";
export const TEMPLATE_VERSION = 1;
/** the server refuses bigger files (413); checked here too so nobody waits for an upload that cannot work */
export const MAX_TEMPLATE_BYTES = 3 * 1024 * 1024;

export interface OfficeTemplateFile {
  format: string; format_version: number; kind: "department" | "company"; name: string; for?: string; solves?: string;
  departments: { key: string; name: string }[];
  agents: { department: string; role: string; name?: string; does?: string; instructions?: string; skills?: string[] }[];
  skills: { name: string }[]; workflows: { name: string }[]; schedules: { template: string }[]; goals: { title: string }[];
}

export interface OfficeTemplateExport { file_name: string; template: OfficeTemplateFile; left_out: string[] }

export interface OfficeTemplateItem { name: string; status: "new" | "existing" | "active" | "archived"; detail?: string }

export interface OfficeTemplateDept {
  key: string; name: string;
  destination: { office_id: string; office_name: string; matched: boolean };
  agents: TeamPreviewAgent[]; new_count: number; existing_count: number; skills: string[];
}

export interface OfficeTemplatePreview {
  id: string; name: string; kind: "department" | "company"; for: string; solves: string; format_version: number; office_id: string;
  departments: OfficeTemplateDept[]; skills: OfficeTemplateItem[]; workflows: OfficeTemplateItem[]; schedules: OfficeTemplateItem[];
  goals: OfficeTemplateItem[]; new_count: number; existing_count: number; can_install: boolean;
  blocked_reason?: "nothing_new" | "office_full" | "plan_limit" | "";
}

export interface OfficeTemplateCard {
  id: string; name: string; kind: "department" | "company"; for: string; solves: string; departments: string[];
  agents: number; skills: number; workflows: number; goals: number; new_count: number; existing_count: number;
}

export interface OfficeTemplateInstall {
  created: { id: string; name: string; role: string }[]; skills: string[]; workflows: string[]; schedules: number;
  goals: OfficeTemplateItem[]; skipped: string[]; preview: OfficeTemplatePreview;
}

/** What the file carries, counted, for "Esto incluye" (each line: an i18n key and its count; empty ones are left out). */
export function includesOf(t: Pick<OfficeTemplateFile, "departments" | "agents" | "skills" | "workflows" | "schedules" | "goals">): { key: string; count: number }[] {
  const custom = t.agents.filter((a) => (a.instructions ?? "") !== "").length;
  return [
    { key: "otpl.inc.departments", count: t.departments.length },
    { key: "otpl.inc.people", count: t.agents.length },
    { key: "otpl.inc.instructions", count: custom },
    { key: "otpl.inc.skills", count: t.skills.length },
    { key: "otpl.inc.workflows", count: t.workflows.length },
    { key: "otpl.inc.schedules", count: t.schedules.length },
    { key: "otpl.inc.goals", count: t.goals.length },
  ].filter((l) => l.count > 0);
}

/** What a template never carries, in plain words ("Esto NO incluye"). The same list the server's allow-list keeps out. */
export const EXCLUDED_KEYS = [
  "otpl.exc.connections", "otpl.exc.memory", "otpl.exc.files", "otpl.exc.people", "otpl.exc.customers",
  "otpl.exc.chats", "otpl.exc.audit", "otpl.exc.trust",
] as const;

/** The file to save: pretty JSON, so a person can open it and read what it carries. */
export function fileText(t: OfficeTemplateFile): string {
  return JSON.stringify(t, null, 2) + "\n";
}

/** A first look at a chosen file before it is uploaded: null = send it; otherwise the i18n key of why not. The server
 *  validates everything again (this only spares an upload that cannot work). */
export function checkFile(name: string, size: number, text?: string): string | null {
  if (size > MAX_TEMPLATE_BYTES) return "otpl.import.tooLarge";
  if (!/\.json$/i.test(name)) return "otpl.import.notJson";
  if (text === undefined) return null;
  let v: unknown;
  try { v = JSON.parse(text); } catch { return "otpl.import.notJson"; }
  const o = v as Partial<OfficeTemplateFile> | null;
  if (!o || typeof o !== "object" || o.format !== TEMPLATE_FORMAT) return "otpl.import.notTemplate";
  if (typeof o.format_version !== "number" || o.format_version > TEMPLATE_VERSION) return "otpl.import.newer";
  return null;
}

/** Totals of a preview over its departments: who arrives and who is already there. */
export function previewPeople(p: Pick<OfficeTemplatePreview, "departments">): { arriving: number; already: number } {
  let arriving = 0, already = 0;
  for (const d of p.departments) for (const a of d.agents) a.status === "new" ? arriving++ : already++;
  return { arriving, already };
}

/** The result lines after installing (each an i18n key with its count); empty when nothing was new. */
export function installLines(r: Pick<OfficeTemplateInstall, "created" | "skills" | "workflows" | "schedules" | "goals">): { key: string; count: number }[] {
  const archived = r.goals.filter((g) => g.status === "archived").length;
  return [
    { key: "otpl.done.people", count: r.created.length },
    { key: "otpl.done.skills", count: r.skills.length },
    { key: "otpl.done.workflows", count: r.workflows.length },
    { key: "otpl.done.schedules", count: r.schedules },
    { key: "otpl.done.goals", count: r.goals.length - archived },
    { key: "otpl.done.goalsArchived", count: archived },
  ].filter((l) => l.count > 0);
}

/** The i18n key of the status chip of an item of the preview. */
export function itemStatusKey(i: Pick<OfficeTemplateItem, "status">): string {
  return `otpl.status.${i.status}`;
}
