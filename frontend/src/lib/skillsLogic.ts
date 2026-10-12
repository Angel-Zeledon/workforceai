// Pure logic of the skill library UI (no React, no path aliases: unit-testable with node --test).
// Skills follow the open Agent Skills format: SKILL.md (frontmatter + instructions) plus text resource
// files. The editor shows examples and quality criteria as friendly fields; they are stored as ordinary
// resource files (examples/example-N.md and quality-criteria.md), so an exported zip works in other tools.

export const NAME_RE = /^[a-z0-9-]{1,64}$/;
export const LIMITS = { description: 1024, files: 40, fileBytes: 512 * 1024, totalBytes: 2 * 1024 * 1024, tools: 20 };
export const ALLOWED_EXT = ["md", "txt", "csv", "json", "yaml", "yml", "py", "js", "sh"];
export const SCRIPT_EXT = ["py", "js", "sh"];
export const QUALITY_PATH = "quality-criteria.md";

export interface SkillFileEntry { path: string; content: string }
export interface Example { input: string; output: string }

const ext = (p: string) => (p.includes(".") ? p.split(".").pop()!.toLowerCase() : "");

/** Lowercase, no accents, [a-z0-9-], at most 64 characters. */
export function slugify(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 64).replace(/-+$/, "");
}

export type PathIssue = "empty" | "invalid" | "ext";
const SEG = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
export function pathIssue(p: string): PathIssue | null {
  if (!p.trim()) return "empty";
  if (p.length > 160 || p.startsWith("/") || p.includes("\\")) return "invalid";
  const segs = p.split("/");
  if (segs.length > 4 || segs.some((s) => s === "." || s === ".." || !SEG.test(s))) return "invalid";
  if (p.toLowerCase() === "skill.md") return "invalid";
  if (!ALLOWED_EXT.includes(ext(p))) return "ext";
  return null;
}
export const isScript = (p: string) => SCRIPT_EXT.includes(ext(p));
export const byteLength = (s: string) => new TextEncoder().encode(s).length;

export const exampleFile = (i: number, ex: Example): SkillFileEntry => ({
  path: `examples/example-${i + 1}.md`,
  content: `## Input\n\n${ex.input.trim()}\n\n## Output\n\n${ex.output.trim()}\n`,
});
export function parseExample(content: string): Example {
  const m = content.match(/^\s*##\s*Input\s*\n([\s\S]*?)\n##\s*Output\s*\n([\s\S]*)$/i);
  return m ? { input: m[1].trim(), output: m[2].trim() } : { input: "", output: content.trim() };
}
export const isExamplePath = (p: string) => /^examples\/example-\d+\.md$/.test(p);

export const qualityFile = (items: string[]): SkillFileEntry | null => {
  const rows = items.map((x) => x.trim()).filter(Boolean);
  return rows.length ? { path: QUALITY_PATH, content: `# Quality criteria\n\n${rows.map((r) => `- ${r}`).join("\n")}\n` } : null;
};
export const parseQuality = (content: string): string[] =>
  content.split("\n").map((l) => l.replace(/^\s*[-*]\s+/, "").trim()).filter((l) => l && !l.startsWith("#"));

export interface Parts { files: SkillFileEntry[]; examples: Example[]; quality: string[] }
/** Splits the resource files of a skill into the editor's fields. */
export function splitFiles(all: SkillFileEntry[]): Parts {
  const files: SkillFileEntry[] = [], examples: Example[] = [];
  let quality: string[] = [];
  for (const f of [...all].sort((a, b) => a.path.localeCompare(b.path, undefined, { numeric: true }))) {
    if (isExamplePath(f.path)) examples.push(parseExample(f.content));
    else if (f.path === QUALITY_PATH) quality = parseQuality(f.content);
    else files.push({ ...f });
  }
  return { files, examples, quality };
}
/** The inverse: what is sent to the backend as resource files. */
export function joinFiles(p: Parts): SkillFileEntry[] {
  const out = [...p.files];
  p.examples.filter((e) => e.input.trim() || e.output.trim()).forEach((e, i) => out.push(exampleFile(i, e)));
  const q = qualityFile(p.quality);
  if (q) out.push(q);
  return out;
}

export interface SkillDraft {
  name: string; description: string; instructions: string; allowed_tools: string[]; parts: Parts; changelog: string; source?: "created" | "from_work";
}
export const emptyDraft = (): SkillDraft => ({ name: "", description: "", instructions: "", allowed_tools: [], parts: { files: [], examples: [], quality: [] }, changelog: "" });

export type SkillIssue =
  | { code: "name_invalid" } | { code: "description_required" } | { code: "description_too_long"; max: number } | { code: "instructions_required" }
  | { code: "too_many_files"; max: number } | { code: "file_path"; path: string; why: PathIssue } | { code: "file_duplicate"; path: string }
  | { code: "file_empty"; path: string } | { code: "file_too_large"; path: string } | { code: "package_too_large" } | { code: "too_many_tools"; max: number };

export function validateDraft(d: SkillDraft, editing = false): SkillIssue[] {
  const out: SkillIssue[] = [];
  if (!editing && !NAME_RE.test(d.name)) out.push({ code: "name_invalid" });
  if (!d.description.trim()) out.push({ code: "description_required" });
  else if ([...d.description].length > LIMITS.description) out.push({ code: "description_too_long", max: LIMITS.description });
  if (!d.instructions.trim()) out.push({ code: "instructions_required" });
  if (d.allowed_tools.length > LIMITS.tools) out.push({ code: "too_many_tools", max: LIMITS.tools });
  const files = joinFiles(d.parts);
  if (files.length + 1 > LIMITS.files) out.push({ code: "too_many_files", max: LIMITS.files });
  const seen = new Set<string>();
  let total = byteLength(d.instructions) + byteLength(d.description) + 200;
  for (const f of files) {
    const why = pathIssue(f.path);
    if (why) out.push({ code: "file_path", path: f.path, why });
    if (seen.has(f.path.toLowerCase())) out.push({ code: "file_duplicate", path: f.path });
    seen.add(f.path.toLowerCase());
    if (!f.content.trim()) out.push({ code: "file_empty", path: f.path });
    const n = byteLength(f.content);
    total += n;
    if (n > LIMITS.fileBytes) out.push({ code: "file_too_large", path: f.path });
  }
  if (total > LIMITS.totalBytes) out.push({ code: "package_too_large" });
  return out;
}

export function isDraftDirty(a: SkillDraft, b: SkillDraft): boolean {
  return JSON.stringify([a.name, a.description, a.instructions, a.allowed_tools, joinFiles(a.parts)]) !== JSON.stringify([b.name, b.description, b.instructions, b.allowed_tools, joinFiles(b.parts)]);
}

export interface TaskLike { title: string; description?: string; output?: { summary: string; findings?: string[]; recommendations?: string[]; evidence?: string[] } | null }
export interface FromTaskLabels {
  whenToUse: string; context: string; approach: string; steps: string; quality: string; descriptionPrefix: string; example: string;
}
/** "Save as skill" from a finished task: instructions from the task and its output, the deliverable as an example. */
export function fromTask(task: TaskLike, L: FromTaskLabels): SkillDraft {
  const out = task.output;
  const findings = out?.findings ?? [], recs = out?.recommendations ?? [];
  const lines = [`# ${task.title}`, "", `## ${L.whenToUse}`, task.description?.trim() || task.title, ""];
  if (out?.summary) lines.push(`## ${L.approach}`, out.summary.trim(), "", ...findings.map((f) => `- ${f}`), "");
  if (recs.length) lines.push(`## ${L.steps}`, ...recs.map((r, i) => `${i + 1}. ${r}`), "");
  const exOut = [out?.summary ?? "", ...findings.map((f) => `- ${f}`), ...recs.map((r) => `- ${r}`)].filter(Boolean).join("\n");
  const desc = `${L.descriptionPrefix} ${(task.title || "").trim()}`.trim().slice(0, LIMITS.description);
  return {
    name: slugify(task.title), description: desc, instructions: lines.join("\n").trim() + "\n", allowed_tools: [],
    parts: { files: [], examples: exOut ? [{ input: `${task.title}\n${task.description ?? ""}`.trim(), output: exOut }] : [], quality: findings.length ? [] : [] },
    changelog: L.example, source: "from_work",
  };
}

export type SkillStatus = "draft" | "in_review" | "active" | "disabled";
export const STATUS_ORDER: SkillStatus[] = ["active", "in_review", "draft", "disabled"];
export const hasPending = (s: { status: string; current_version: number; active_version: number }) =>
  s.status === "in_review" || (s.active_version > 0 && s.current_version > s.active_version);
export const needsReview = (s: { status: string }) => s.status === "in_review";

export type SkillFilter = "all" | SkillStatus;
export function filterSkills<T extends { name: string; description: string; status: string }>(list: T[], f: SkillFilter, q: string): T[] {
  const n = q.trim().toLowerCase();
  return list.filter((s) => (f === "all" || s.status === f) && (!n || s.name.includes(n) || s.description.toLowerCase().includes(n)));
}
export function countByStatus(list: { status: string }[]): Record<SkillFilter, number> {
  const c: Record<SkillFilter, number> = { all: list.length, draft: 0, in_review: 0, active: 0, disabled: 0 };
  for (const s of list) if (s.status in c) c[s.status as SkillStatus]++;
  return c;
}
export const errorCode = (e: unknown): string => (e && typeof e === "object" && "code" in e ? String((e as { code: unknown }).code) : "");
