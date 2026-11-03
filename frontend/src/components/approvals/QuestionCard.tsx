"use client";
import { useState } from "react";
import { AnswerError, api } from "@/lib/api";
import { projectsApi } from "@/lib/projects/api";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { skipVerdict, waitingParts, type AnswerKind, type QuestionItem, type SkipImpact } from "@/lib/questionsLogic";
import { Btn, Dot, useAgentColor, useAgentName } from "../ui";

type Panel = "" | "file" | "reassign" | "skip";
type Candidate = { id: string; name: string; role: string; title: string };

/** How long a question has been waiting, in words ("hace 3 h"). */
export function WaitingLabel({ seconds }: { seconds: number }) {
  const { t } = useT();
  const w = waitingParts(seconds);
  return <span>{w.unit === "now" ? t("questions.waiting.now") : t(`questions.waiting.${w.unit}`, { count: w.n })}</span>;
}

/** "Necesita tu ayuda": an agent stopped a task to ask a person. Answer with an option, text or a file, pass the task
 * to another agent, or skip it (the app first shows what skipping leaves undone, and refuses when nothing would be
 * delivered). Selection checkboxes are for answering several at once in the inbox. */
export function QuestionCard({ q, selectable = false, selected = false, onSelect }: {
  q: QuestionItem; selectable?: boolean; selected?: boolean; onSelect?: (v: boolean) => void;
}) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const agents = useStore((s) => s.agents);
  const [text, setText] = useState("");
  const [filePath, setFilePath] = useState("");
  const [panel, setPanel] = useState<Panel>("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [impact, setImpact] = useState<SkipImpact | null>(null);
  const [cands, setCands] = useState<Candidate[] | null>(null);
  const [agentId, setAgentId] = useState("");
  const [done, setDone] = useState(false);
  const open = q.status === "open" && !done;

  const send = async (kind: AnswerKind, extra: { text?: string; option?: string; file_path?: string; agent_id?: string; confirm?: boolean } = {}) => {
    setBusy(true); setErr("");
    try {
      await api.answerQuestion(q.id, { kind, ...extra });
      setDone(true);
    } catch (e) {
      if (e instanceof AnswerError && e.impact) { setImpact(e.impact); setPanel("skip"); }
      setErr(e instanceof AnswerError && e.status === 409 && !e.impact ? t("questions.alreadyAnswered") : t("questions.answerError"));
    } finally { setBusy(false); }
  };
  const loadDetail = async (next: Panel) => {
    setPanel(next === panel ? "" : next); setErr("");
    if ((next === "skip" && impact) || (next === "reassign" && cands)) return;
    try {
      const d = await api.question(q.id);
      setImpact(d.impact ?? null);
      setCands(d.reassign_candidates ?? []);
    } catch { setErr(t("questions.loadError")); }
  };
  const cancelProject = async () => {
    if (!q.project_id || !window.confirm(t("questions.skip.cancelConfirm"))) return;
    setBusy(true);
    try { await projectsApi.control(q.project_id, "cancel"); setDone(true); } catch { setErr(t("questions.answerError")); } finally { setBusy(false); }
  };
  const verdict = skipVerdict(impact);

  return (
    <div data-testid={`question-${q.id}`} className="rounded-[10px] border border-accent/30 bg-white p-3 shadow-sm">
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1.5 text-[11px] text-mute">
          {selectable && open && (
            <input type="checkbox" data-testid={`question-select-${q.id}`} aria-label={t("questions.select")} checked={selected} onChange={(e) => onSelect?.(e.target.checked)} />
          )}
          <Dot color={color(q.agent_id)} /><span className="truncate">{name(q.agent_id)}{q.task_title ? ` · ${q.task_title}` : ""}</span>
        </span>
        <span className="shrink-0 rounded-md bg-accent/10 px-1.5 py-0.5 text-[10px] font-semibold text-accent">{t("questions.badge")}</span>
      </div>
      {q.project_name && <div className="text-[11px] text-mute">{q.project_name}</div>}
      <div className="mt-1 text-[13px] font-semibold text-ink">{q.need}</div>
      {q.what_happened && q.what_happened !== q.need && <p className="mt-0.5 text-[12px] leading-snug text-ink2">{q.what_happened}</p>}
      <div className="mt-1 text-[11px] text-mute">{t("questions.waitingFor")} <WaitingLabel seconds={q.waiting_seconds} />{q.reminders > 0 ? ` · ${t("questions.reminded", { count: q.reminders })}` : ""}</div>

      {!open ? (
        <div data-testid={`question-done-${q.id}`} className="mt-2 text-[11px] font-semibold text-emerald-700">{t(q.status === "open" ? "questions.sent" : `questions.status.${q.status}`)}</div>
      ) : !q.can_answer ? (
        <div className="mt-2 text-[11px] text-mute">{t("questions.notForYou")}</div>
      ) : (
        <>
          {q.options.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {q.options.map((o) => (
                <button key={o} type="button" disabled={busy} data-testid={`question-option-${q.id}`} onClick={() => void send("option", { option: o })}
                  className="rounded-md border border-accent/40 bg-accent/5 px-2.5 py-1 text-[12px] font-medium text-accent hover:bg-accent/10 disabled:opacity-40">{o}</button>
              ))}
            </div>
          )}
          <form className="mt-2 flex gap-1.5" onSubmit={(e) => { e.preventDefault(); if (text.trim()) void send("text", { text: text.trim() }); }}>
            <input value={text} onChange={(e) => setText(e.target.value)} maxLength={2000} disabled={busy} data-testid={`question-text-${q.id}`}
              placeholder={t("questions.placeholder")} aria-label={t("questions.placeholder")}
              className="min-w-0 flex-1 rounded-md border border-line bg-white px-2 py-1.5 text-[12px] text-ink outline-none focus:border-accent" />
            <Btn kind="primary" type="submit" disabled={busy || !text.trim()} data-testid={`question-send-${q.id}`}>{t("questions.send")}</Btn>
          </form>
          <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
            <button type="button" className="font-medium text-accent" onClick={() => setPanel(panel === "file" ? "" : "file")}>{t("questions.file")}</button>
            <button type="button" className="font-medium text-accent" data-testid={`question-reassign-${q.id}`} onClick={() => void loadDetail("reassign")}>{t("questions.reassign")}</button>
            <button type="button" className="font-medium text-ink2" data-testid={`question-skip-${q.id}`} onClick={() => void loadDetail("skip")}>{t("questions.skip.button")}</button>
          </div>

          {panel === "file" && (
            <form className="mt-2 space-y-1.5" onSubmit={(e) => { e.preventDefault(); if (filePath.trim()) void send("file", { file_path: filePath.trim(), text: text.trim() || undefined }); }}>
              <p className="text-[11px] text-mute">{t("questions.fileHint")}</p>
              <div className="flex gap-1.5">
                <input value={filePath} onChange={(e) => setFilePath(e.target.value)} placeholder="/Clients/…/archivo.xlsx" aria-label={t("questions.file")}
                  className="min-w-0 flex-1 rounded-md border border-line bg-white px-2 py-1.5 font-mono text-[11px] outline-none focus:border-accent" />
                <Btn kind="primary" type="submit" disabled={busy || !filePath.trim().startsWith("/")}>{t("questions.send")}</Btn>
              </div>
            </form>
          )}

          {panel === "reassign" && (
            <div className="mt-2 flex gap-1.5">
              <select value={agentId} onChange={(e) => setAgentId(e.target.value)} aria-label={t("questions.reassign")} data-testid={`question-agent-${q.id}`}
                className="min-w-0 flex-1 rounded-md border border-line bg-white px-2 py-1.5 text-[12px]">
                <option value="">{t("questions.pickAgent")}</option>
                {(cands ?? []).map((c) => <option key={c.id} value={c.id}>{agents[c.id]?.name || c.name} · {c.title || c.role}</option>)}
              </select>
              <Btn kind="primary" disabled={busy || !agentId} onClick={() => void send("reassign", { agent_id: agentId })}>{t("questions.reassignGo")}</Btn>
            </div>
          )}

          {panel === "skip" && impact && (
            <div data-testid={`question-skip-panel-${q.id}`} data-verdict={verdict} className={`mt-2 rounded-md border p-2 text-[12px] ${verdict === "refuse" ? "border-err/40 bg-err/5 text-ink" : "border-line bg-panel2 text-ink2"}`}>
              {verdict === "refuse" ? (
                <>
                  <p className="font-semibold text-err">{t("questions.skip.refuse", { count: impact.skipped.length })}</p>
                  <p className="mt-1">{t("questions.skip.refuseHint")}</p>
                </>
              ) : impact.skipped.length === 0 ? (
                <p>{t("questions.skip.nothing")}</p>
              ) : (
                <p>{t("questions.skip.willSkip", { count: impact.skipped.length })}</p>
              )}
              {impact.skipped.length > 0 && (
                <ul className="mt-1 list-disc pl-4">{impact.skipped.slice(0, 8).map((s) => <li key={s.id}>{s.title}</li>)}{impact.skipped.length > 8 && <li>…</li>}</ul>
              )}
              {verdict === "partial" && (
                <p className="mt-1 font-medium text-amber-800">{t("questions.skip.partial", { list: impact.objectives_lost.map((o) => o.title).filter(Boolean).join(", ") || String(impact.objectives_lost.length) })}</p>
              )}
              <div className="mt-2 flex flex-wrap gap-1.5">
                {verdict === "refuse" ? (
                  q.project_id && <Btn kind="danger" disabled={busy} onClick={() => void cancelProject()}>{t("questions.skip.cancelProject")}</Btn>
                ) : (
                  <Btn kind="danger" disabled={busy} data-testid={`question-skip-confirm-${q.id}`} onClick={() => void send("skip", { confirm: true, text: text.trim() || undefined })}>{t("questions.skip.confirm")}</Btn>
                )}
                <Btn disabled={busy} onClick={() => setPanel("")}>{t("questions.skip.back")}</Btn>
              </div>
            </div>
          )}
          {err && <p role="alert" className="mt-1.5 text-[11px] text-red-700">{err}</p>}
        </>
      )}
    </div>
  );
}
