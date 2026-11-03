"use client";
import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { batchable, groupQuestions } from "@/lib/questionsLogic";
import { Btn } from "../ui";
import { QuestionCard } from "./QuestionCard";

/** Pendientes: the questions agents asked (needs your help), grouped by batch / project / request. Several can be
 * selected and answered at once with the same text or a shared option ("3 customers have no phone: use the e-mail").
 * Each question keeps its own history: who answered and what. */
export function QuestionsSection() {
  const { t } = useT();
  const byId = useStore((s) => s.questions);
  const load = useStore((s) => s.loadQuestions);
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  useEffect(() => {
    void load();
    const iv = setInterval(() => void load(), 60_000); // waiting times and reminders stay fresh
    return () => clearInterval(iv);
  }, [load]);
  const groups = useMemo(() => groupQuestions(Object.values(byId)), [byId]);
  const open = groups.flatMap((g) => g.items);
  const chosen = open.filter((q) => sel[q.id]);
  const shared = batchable(chosen);
  if (!open.length) return null;

  const answerAll = async (body: { kind: "text" | "option"; text?: string; option?: string }) => {
    setBusy(true); setMsg("");
    try {
      const r = await api.answerQuestions(chosen.map((q) => q.id), body);
      const failed = Object.keys(r.failed || {}).length;
      setMsg(failed ? t("questions.batch.partial", { count: r.answered.length, failed }) : t("questions.batch.done", { count: r.answered.length }));
      setSel({}); setText("");
      void load();
    } catch { setMsg(t("questions.answerError")); } finally { setBusy(false); }
  };

  return (
    <section data-testid="questions-section" aria-labelledby="questions-title" className="space-y-2">
      <h2 id="questions-title" className="text-[15px] font-semibold text-ink">{t("questions.sectionTitle", { count: open.length })}</h2>
      <p className="text-[12px] text-mute">{t("questions.sectionHint")}</p>
      {groups.map((g) => {
        const all = g.items.every((q) => sel[q.id]);
        return (
          <div key={g.key} data-testid={`questions-group-${g.key}`} className="space-y-1.5 rounded-[10px] border border-line bg-panel p-2">
            <div className="flex items-center justify-between gap-2 px-1">
              <span className="truncate text-[12px] font-semibold text-ink2">{g.label || t(`questions.group.${g.kind}`)} · {g.items.length}</span>
              {g.items.length > 1 && (
                <button type="button" className="text-[11px] font-medium text-accent" onClick={() => setSel((s) => ({ ...s, ...Object.fromEntries(g.items.map((q) => [q.id, !all])) }))}>
                  {all ? t("questions.unselectAll") : t("questions.selectAll")}
                </button>
              )}
            </div>
            {g.items.map((q) => <QuestionCard key={q.id} q={q} selectable selected={!!sel[q.id]} onSelect={(v) => setSel((s) => ({ ...s, [q.id]: v }))} />)}
          </div>
        );
      })}
      {chosen.length > 1 && (
        <div data-testid="questions-batch" className="sticky bottom-3 space-y-1.5 rounded-[10px] border border-accent/40 bg-white p-3 shadow-soft">
          <div className="text-[12px] font-semibold text-ink">{t("questions.batch.title", { count: chosen.length })}</div>
          {!shared.ok ? <p className="text-[11px] text-mute">{t("questions.batch.notAllowed")}</p> : (
            <>
              {shared.options.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                  {shared.options.map((o) => <Btn key={o} disabled={busy} onClick={() => void answerAll({ kind: "option", option: o })}>{o}</Btn>)}
                </div>
              )}
              <form className="flex gap-1.5" onSubmit={(e) => { e.preventDefault(); if (text.trim()) void answerAll({ kind: "text", text: text.trim() }); }}>
                <input value={text} onChange={(e) => setText(e.target.value)} maxLength={2000} placeholder={t("questions.placeholder")} aria-label={t("questions.placeholder")}
                  data-testid="questions-batch-text" className="min-w-0 flex-1 rounded-md border border-line px-2 py-1.5 text-[12px] outline-none focus:border-accent" />
                <Btn kind="primary" type="submit" disabled={busy || !text.trim()} data-testid="questions-batch-send">{t("questions.batch.send", { count: chosen.length })}</Btn>
              </form>
            </>
          )}
        </div>
      )}
      {msg && <p role="status" className="text-[12px] text-ink2">{msg}</p>}
    </section>
  );
}
