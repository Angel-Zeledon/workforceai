"use client";
import { useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { skillPayload, skillsApi, SkillError, type SkillDetail } from "@/lib/skills";
import {
  NAME_RE, emptyDraft, isDraftDirty, isScript, joinFiles, splitFiles, validateDraft, slugify, LIMITS, type Example, type SkillDraft,
} from "@/lib/skillsLogic";
import { Btn, Card } from "../ui";
import { Icon } from "../icons";
import { field, issueText, label } from "./shared";

function draftOf(d: SkillDetail): SkillDraft {
  return { name: d.name, description: d.description, instructions: d.instructions, allowed_tools: d.allowed_tools ?? [], parts: splitFiles(d.files ?? []), changelog: "" };
}

/** Create or edit a skill: description (when to use it), instructions (markdown), resource files,
 * suggested tools (informational), examples and quality criteria. Every save is a new version. */
export function SkillEditor({ detail, prefill, canWrite, onSaved }: {
  detail?: SkillDetail; prefill?: SkillDraft | null; canWrite: boolean; onSaved: (d: SkillDetail, changed: boolean) => void;
}) {
  const { t } = useT();
  const base = useMemo(() => (detail ? draftOf(detail) : prefill ?? emptyDraft()), [detail, prefill]);
  const [draft, setDraft] = useState<SkillDraft>(base);
  const [toolInput, setToolInput] = useState("");
  const [preview, setPreview] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const upload = useRef<HTMLInputElement>(null);
  const editing = !!detail;
  const issues = validateDraft(draft, editing);
  const dirty = isDraftDirty(base, draft) || (!editing);
  const set = (p: Partial<SkillDraft>) => setDraft((d) => ({ ...d, ...p }));
  const setParts = (p: Partial<SkillDraft["parts"]>) => setDraft((d) => ({ ...d, parts: { ...d.parts, ...p } }));
  const ro = !canWrite;

  const save = async () => {
    setBusy(true); setErr("");
    try {
      const files = joinFiles(draft.parts);
      if (editing) {
        const r = await skillsApi.update(detail!.id, skillPayload(draft, files));
        onSaved(r, r.changed);
      } else {
        onSaved(await skillsApi.create(skillPayload(draft, files)), true);
      }
    } catch (e) {
      const code = e instanceof SkillError ? e.code : "";
      setErr(code && t(`skills.error.${code}`) !== `skills.error.${code}` ? t(`skills.error.${code}`) : t("skills.error.generic"));
    } finally { setBusy(false); }
  };

  const addTool = () => {
    const v = toolInput.trim();
    if (v && !draft.allowed_tools.includes(v)) set({ allowed_tools: [...draft.allowed_tools, v] });
    setToolInput("");
  };
  const readUpload = async (files: FileList | null) => {
    if (!files) return;
    const added = [];
    for (const f of Array.from(files)) added.push({ path: f.name, content: await f.text() });
    setParts({ files: [...draft.parts.files, ...added] });
  };
  const setFile = (i: number, patch: Partial<{ path: string; content: string }>) => setParts({ files: draft.parts.files.map((f, j) => (j === i ? { ...f, ...patch } : f)) });
  const setExample = (i: number, patch: Partial<Example>) => setParts({ examples: draft.parts.examples.map((e, j) => (j === i ? { ...e, ...patch } : e)) });

  return (
    <div data-testid="skill-editor" className="space-y-4">
      {draft.source === "from_work" && !editing && <div data-testid="skill-from-work-note" className="rounded-lg bg-accent-soft px-3 py-2 text-xs text-accent">{t("skills.fromWork.note")}</div>}
      <Card title={t("skills.editor.basics")}>
        <div className="grid gap-3 md:grid-cols-[minmax(0,260px)_1fr]">
          <div>
            <label className={label} htmlFor="skill-name">{t("skills.editor.name")}</label>
            <input id="skill-name" data-testid="skill-name" value={draft.name} readOnly={editing || ro} onChange={(e) => set({ name: e.target.value })}
              onBlur={() => { if (!editing && draft.name && !NAME_RE.test(draft.name)) set({ name: slugify(draft.name) }); }}
              placeholder="bank-reconciliation" className={`${field} font-mono ${editing ? "opacity-70" : ""}`} />
            <p className="mt-1 text-[11px] text-mute">{editing ? t("skills.editor.nameLocked") : t("skills.editor.nameHint")}</p>
          </div>
          <div>
            <label className={label} htmlFor="skill-description">{t("skills.editor.description")}</label>
            <textarea id="skill-description" data-testid="skill-description" rows={3} value={draft.description} readOnly={ro} onChange={(e) => set({ description: e.target.value })}
              placeholder={t("skills.editor.descriptionPh")} className={field} />
            <p className="mt-1 text-[11px] text-mute">{t("skills.editor.descriptionHint")} <span className="font-mono">{[...draft.description].length}/{LIMITS.description}</span></p>
          </div>
        </div>
      </Card>

      <Card title={t("skills.editor.instructions")} right={
        <button type="button" data-testid="skill-preview-toggle" onClick={() => setPreview(!preview)} className="text-[11px] font-medium text-accent">{preview ? t("skills.editor.edit") : t("skills.editor.preview")}</button>}>
        {preview ? (
          <pre data-testid="skill-instructions-preview" className="max-h-[420px] overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-panel2 p-3 font-mono text-[12px] text-ink2">{draft.instructions}</pre>
        ) : (
          <textarea data-testid="skill-instructions" rows={14} value={draft.instructions} readOnly={ro} onChange={(e) => set({ instructions: e.target.value })}
            placeholder={t("skills.editor.instructionsPh")} className={`${field} font-mono text-[12.5px] leading-relaxed`} />
        )}
        <p className="mt-1 text-[11px] text-mute">{t("skills.editor.instructionsHint")}</p>
      </Card>

      <Card title={t("skills.editor.files")} right={!ro && (
        <div className="flex gap-2">
          <input ref={upload} type="file" multiple data-testid="skill-file-upload" className="hidden" accept=".md,.txt,.csv,.json,.yaml,.yml,.py,.js,.sh" onChange={(e) => { readUpload(e.target.files); e.target.value = ""; }} />
          <Btn data-testid="skill-upload-file" onClick={() => upload.current?.click()}>{t("skills.editor.uploadFile")}</Btn>
          <Btn data-testid="skill-add-file" onClick={() => setParts({ files: [...draft.parts.files, { path: "", content: "" }] })}><Icon name="plus" size={12} className="mr-1 inline-block" />{t("skills.editor.addFile")}</Btn>
        </div>)}>
        <p className="mb-2 text-[11px] text-mute">{t("skills.editor.filesHint")}</p>
        {draft.parts.files.length === 0 && <div className="text-xs text-mute">{t("skills.editor.noFiles")}</div>}
        <div className="space-y-3">
          {draft.parts.files.map((f, i) => (
            <div key={i} data-testid="skill-file-row" className="rounded-lg border border-line p-2.5">
              <div className="mb-1.5 flex items-center gap-2">
                <input data-testid="skill-file-path" aria-label={t("skills.editor.filePath")} value={f.path} readOnly={ro} onChange={(e) => setFile(i, { path: e.target.value })}
                  placeholder="templates/plantilla.md" className={`${field} py-1.5 font-mono text-[12px]`} />
                {isScript(f.path) && <span data-testid="skill-script-badge" className="shrink-0 rounded bg-warn/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-warn">{t("skills.script")}</span>}
                {!ro && <button type="button" aria-label={t("skills.editor.removeFile")} onClick={() => setParts({ files: draft.parts.files.filter((_, j) => j !== i) })} className="rounded p-1 text-mute hover:bg-mute/10 hover:text-err"><Icon name="x" size={14} /></button>}
              </div>
              <textarea data-testid="skill-file-content" aria-label={t("skills.editor.fileContent")} rows={5} value={f.content} readOnly={ro} onChange={(e) => setFile(i, { content: e.target.value })} className={`${field} font-mono text-[12px]`} />
            </div>
          ))}
        </div>
        {draft.parts.files.some((f) => isScript(f.path)) && <p className="mt-2 text-[11px] text-warn">{t("skills.editor.scriptNote")}</p>}
      </Card>

      <Card title={t("skills.editor.examples")} right={!ro && <Btn data-testid="skill-add-example" onClick={() => setParts({ examples: [...draft.parts.examples, { input: "", output: "" }] })}><Icon name="plus" size={12} className="mr-1 inline-block" />{t("skills.editor.addExample")}</Btn>}>
        <p className="mb-2 text-[11px] text-mute">{t("skills.editor.examplesHint")}</p>
        {draft.parts.examples.length === 0 && <div className="text-xs text-mute">{t("skills.editor.noExamples")}</div>}
        <div className="space-y-3">
          {draft.parts.examples.map((ex, i) => (
            <div key={i} data-testid="skill-example-row" className="grid gap-2 rounded-lg border border-line p-2.5 md:grid-cols-2">
              <div><label className={label}>{t("skills.editor.exampleInput")}</label><textarea data-testid="skill-example-input" rows={4} value={ex.input} readOnly={ro} onChange={(e) => setExample(i, { input: e.target.value })} className={`${field} text-[12px]`} /></div>
              <div><label className={label}>{t("skills.editor.exampleOutput")}</label><textarea data-testid="skill-example-output" rows={4} value={ex.output} readOnly={ro} onChange={(e) => setExample(i, { output: e.target.value })} className={`${field} text-[12px]`} /></div>
              {!ro && <button type="button" onClick={() => setParts({ examples: draft.parts.examples.filter((_, j) => j !== i) })} className="justify-self-start text-[11px] text-mute hover:text-err md:col-span-2">{t("skills.editor.removeExample")}</button>}
            </div>
          ))}
        </div>
      </Card>

      <div className="grid gap-4 md:grid-cols-2">
        <Card title={t("skills.editor.quality")}>
          <textarea data-testid="skill-quality" rows={5} value={draft.parts.quality.join("\n")} readOnly={ro} onChange={(e) => setParts({ quality: e.target.value.split("\n") })} placeholder={t("skills.editor.qualityPh")} className={`${field} text-[12.5px]`} />
          <p className="mt-1 text-[11px] text-mute">{t("skills.editor.qualityHint")}</p>
        </Card>
        <Card title={t("skills.editor.tools")}>
          <div className="mb-2 flex flex-wrap gap-1">
            {draft.allowed_tools.map((x) => (
              <span key={x} data-testid="skill-tool-chip" className="inline-flex items-center gap-1 rounded bg-mute/[0.08] px-1.5 py-0.5 font-mono text-[11px] text-ink2">{x}
                {!ro && <button type="button" aria-label={t("common.close")} onClick={() => set({ allowed_tools: draft.allowed_tools.filter((y) => y !== x) })} className="text-mute hover:text-err"><Icon name="x" size={10} /></button>}</span>
            ))}
          </div>
          {!ro && <input data-testid="skill-tool-input" value={toolInput} onChange={(e) => setToolInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addTool(); } }} onBlur={addTool}
            placeholder={t("skills.editor.toolsPh")} className={`${field} py-1.5 font-mono text-[12px]`} />}
          <p className="mt-1 text-[11px] text-mute">{t("skills.editor.toolsHint")}</p>
        </Card>
      </div>

      {canWrite && (
        <Card>
          {editing && (
            <div className="mb-3">
              <label className={label} htmlFor="skill-changelog">{t("skills.editor.changelog")}</label>
              <input id="skill-changelog" data-testid="skill-changelog" value={draft.changelog} maxLength={400} onChange={(e) => set({ changelog: e.target.value })} placeholder={t("skills.editor.changelogPh")} className={field} />
            </div>
          )}
          {issues.length > 0 && dirty && (
            <ul data-testid="skill-issues" className="mb-3 list-disc space-y-0.5 pl-5 text-xs text-err">{issues.map((i, n) => <li key={n}>{issueText(t, i)}</li>)}</ul>
          )}
          {err && <div data-testid="skill-save-error" className="mb-3 rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{err}</div>}
          <div className="flex items-center gap-3">
            <Btn kind="primary" data-testid="skill-save" disabled={busy || issues.length > 0 || !dirty} onClick={save}>{busy ? t("skills.saving") : editing ? t("skills.editor.saveVersion") : t("skills.editor.create")}</Btn>
            <span className="text-[11px] text-mute">{editing ? t("skills.editor.saveNote") : t("skills.editor.createNote")}</span>
          </div>
        </Card>
      )}
    </div>
  );
}
