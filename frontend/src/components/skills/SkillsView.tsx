"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { skillsApi, SkillError, useSkillsNav, type Skill } from "@/lib/skills";
import { countByStatus, filterSkills, needsReview, type SkillFilter } from "@/lib/skillsLogic";
import { useCan } from "@/lib/session";
import { Btn, Empty, Loading, timeAgo } from "../ui";
import { Icon } from "../icons";
import { SkillDetailView } from "./SkillDetailView";
import { SourceChip, StatusBadge } from "./shared";

const FILTERS: SkillFilter[] = ["all", "active", "in_review", "draft", "disabled"];

/** "Skills" screen: the library of reusable know-how (Agent Skills format) with import, export and review. */
export function SkillsView() {
  const { t } = useT();
  const nav = useSkillsNav();
  const canWrite = useCan("skills:write");
  const [list, setList] = useState<Skill[] | null>(null);
  const [filter, setFilter] = useState<SkillFilter>("all");
  const [q, setQ] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState<{ text: string; notes: string[] } | null>(null);
  const [rename, setRename] = useState<{ file: File; name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try { setList(await skillsApi.list()); setError(""); } catch { setError(t("skills.error.load")); }
  }, [t]);
  useEffect(() => { load(); }, [load]);

  const doImport = async (file: File, name = "") => {
    setBusy(true); setError(""); setNotice(null);
    try {
      const r = await skillsApi.importFile(file, file.name, name);
      setRename(null);
      setNotice({ text: t("skills.import.done", { name: r.name }), notes: r.notes ?? [] });
      await load();
      nav.select(r.id, "review");
    } catch (e) {
      if (e instanceof SkillError && e.code === "name_taken") setRename({ file, name: name || "" });
      else setError(e instanceof SkillError && t(`skills.error.${e.code}`) !== `skills.error.${e.code}` ? t(`skills.error.${e.code}`) : t("skills.error.generic"));
    } finally { setBusy(false); }
  };

  if (nav.selected) {
    return (
      <div data-testid="skills-view" className="h-full min-h-0 overflow-y-auto bg-bg">
        {notice && <div className="mx-auto mt-3 max-w-4xl px-5"><div data-testid="skills-import-notice" className="rounded-lg bg-ok/10 px-3 py-2 text-xs text-ok">{notice.text}{notice.notes.length > 0 && <ul className="mt-1 list-disc pl-5 text-ink2">{notice.notes.map((n, i) => <li key={i}>{n}</li>)}</ul>}</div></div>}
        <SkillDetailView id={nav.selected} prefill={nav.prefill} tab={nav.tab} onBack={() => { nav.select(null); setNotice(null); load(); }} onChanged={load} />
      </div>
    );
  }

  const counts = countByStatus(list ?? []);
  const shown = filterSkills(list ?? [], filter, q);
  return (
    <div data-testid="skills-view" className="flex h-full min-h-0 flex-col bg-bg">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-panel px-5 py-3">
        <div className="mr-3">
          <h1 className="font-display text-xl font-semibold text-ink">{t("skills.title")}</h1>
          <p className="text-[11px] text-mute">{t("skills.subtitle")}</p>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <input data-testid="skills-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("skills.searchPlaceholder")} aria-label={t("skills.searchPlaceholder")} className="w-56 rounded-lg border border-line bg-panel2 px-3 py-1.5 text-xs outline-none focus:border-accent" />
          <input ref={input} type="file" accept=".zip,.md,application/zip,text/markdown" data-testid="skills-import-input" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) doImport(f); e.target.value = ""; }} />
          {canWrite && <Btn data-testid="skills-import" disabled={busy} onClick={() => input.current?.click()}>{busy ? t("skills.import.busy") : t("skills.import.button")}</Btn>}
          {canWrite && <Btn kind="primary" data-testid="skills-new" className="inline-flex items-center gap-1" onClick={() => nav.openNew(null)}><Icon name="plus" size={13} />{t("skills.new")}</Btn>}
        </div>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-5">
        {error && <div data-testid="skills-error" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{error}</div>}
        {rename && (
          <div data-testid="skills-import-rename" className="flex flex-wrap items-center gap-2 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
            <span>{t("skills.import.nameTaken")}</span>
            <input data-testid="skills-import-rename-input" value={rename.name} onChange={(e) => setRename({ ...rename, name: e.target.value })} placeholder="nombre-nuevo" className="w-56 rounded border border-line bg-panel px-2 py-1 font-mono text-ink" />
            <Btn kind="primary" data-testid="skills-import-rename-go" disabled={busy || !/^[a-z0-9-]{1,64}$/.test(rename.name)} onClick={() => doImport(rename.file, rename.name)}>{t("skills.import.retry")}</Btn>
            <button type="button" className="text-mute underline" onClick={() => setRename(null)}>{t("common.close")}</button>
          </div>
        )}
        <div role="tablist" aria-label={t("skills.filter.label")} className="flex flex-wrap gap-1.5">
          {FILTERS.map((f) => (
            <button key={f} role="tab" aria-selected={filter === f} data-testid={`skills-filter-${f}`} onClick={() => setFilter(f)}
              className={`rounded-full border px-3 py-1 text-xs font-medium ${filter === f ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-ink2 hover:bg-panel2"} ${f === "in_review" && counts.in_review ? "ring-1 ring-warn/50" : ""}`}>
              {t(`skills.filter.${f}`)} <span className="font-mono text-[10px] opacity-70">{counts[f]}</span>
            </button>
          ))}
        </div>
        {list === null ? <Loading rows={4} /> : shown.length === 0 ? <Empty>{t("skills.empty")}</Empty> : (
          <div data-testid="skills-grid" className="ac-stagger grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {shown.map((s) => (
              <button key={s.id} type="button" data-testid={`skill-card-${s.name}`} data-status={s.status} onClick={() => nav.select(s.id, needsReview(s) ? "review" : "content")}
                className="ac-card-hover flex flex-col rounded-xl border border-line bg-panel p-4 text-left shadow-soft hover:border-accent">
                <div className="flex items-start justify-between gap-2">
                  <span className="min-w-0 break-words font-mono text-[13px] font-semibold text-ink">{s.name}</span>
                  <StatusBadge status={s.status} />
                </div>
                <p className="mt-1.5 line-clamp-3 text-xs text-ink2">{s.description}</p>
                <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-3 text-[10.5px] text-mute">
                  <SourceChip source={s.source} builtin={s.builtin} />
                  <span className="font-mono">v{s.current_version}</span>
                  {s.active_version > 0 && s.active_version !== s.current_version && <span className="text-warn">{t("skills.activeVersion", { v: s.active_version })}</span>}
                  {!!s.agents && <span>{t("skills.agentsCount", { n: s.agents })}</span>}
                  <span className="ml-auto">{timeAgo(s.updated_at)}</span>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
