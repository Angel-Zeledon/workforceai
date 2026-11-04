"use client";
import { useState, type HTMLAttributes, type ReactNode } from "react";
import { AnswerError, api } from "@/lib/api";
import { projectsApi } from "@/lib/projects/api";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { useStore } from "@/lib/store";
import { skipVerdict, waitingParts, type AnswerKind, type QuestionItem, type SkipImpact } from "@/lib/questionsLogic";
import type { Approval } from "@/lib/types";
import { Btn, RiskBadge, useAgentColor, useAgentName } from "../ui";
import { CodeRunView } from "../code/CodeRunView";
import { ActionName } from "./ActionName";
import { ApprovalReason } from "./ApprovalReason";
import { DevilsAdvocate } from "./DevilsAdvocate";
import { RememberApproval } from "./RememberApproval";

/**
 * The ONE pending card (proposal 5.4 #3): a question or an approval looks the same in the office tray, in "Mientras no
 * estabas", in a project, in the chat and in Pendientes. Always: who asks (the agent, with its avatar), what (the action
 * by its name in words), why (the rule that asked) and the buttons (Aprobar / Rechazar / "Aprobar y no volver a
 * preguntar", or Responder). Two sizes: `compact` for the office tray and small panels, `full` for Pendientes.
 * This file is presentation; every screen keeps its own decision logic (immediate, held for undo, project store).
 */
export type PendingVariant = "compact" | "full";
export type PendingKind = "approval" | "question";

/** The agent's avatar: its initial on its color (the same mark as in the chat). */
export function PendingAvatar({ agentId, name, size = 30 }: { agentId: string | null | undefined; name?: string; size?: number }) {
  const agentName = useAgentName();
  const color = useAgentColor();
  const n = name || agentName(agentId);
  return (
    <span aria-hidden data-testid="pending-avatar" className="flex shrink-0 items-center justify-center rounded-md font-semibold text-white"
      style={{ width: size, height: size, background: color(agentId), fontSize: Math.round(size * 0.46) }}>
      {n.slice(0, 1).toUpperCase()}
    </span>
  );
}

/** How long a question has been waiting, in words ("hace 3 h"). */
export function WaitingLabel({ seconds }: { seconds: number }) {
  const { t } = useT();
  const w = waitingParts(seconds);
  return <span>{w.unit === "now" ? t("questions.waiting.now") : t(`questions.waiting.${w.unit}`, { count: w.n })}</span>;
}

type RootProps = Omit<HTMLAttributes<HTMLElement>, "title" | "className" | "children"> & { [k: `data-${string}`]: string | number | boolean | undefined };

/** The frame of the card. Screens fill `body` (details), `why` (the rule) and `actions` (their own buttons). */
export function PendingCard({
  kind, variant = "full", agentId, agentName, title, action, context, risk, lead, body, why, actions, children,
  as: Tag = "div", testId, className = "", border, rootProps,
}: {
  kind: PendingKind; variant?: PendingVariant;
  /** who asks */
  agentId: string | null | undefined; agentName?: string;
  /** what: the title of the request and the action code (shown by its name in words) */
  title: ReactNode; action?: string | null;
  /** where it comes from (task, project, waiting time) */
  context?: ReactNode;
  risk?: string | null;
  /** before the avatar (a selection checkbox) */
  lead?: ReactNode;
  body?: ReactNode;
  /** why: the rule that asked for it */
  why?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  as?: "div" | "li" | "article";
  testId?: string; className?: string;
  /** border color class, instead of the default one (the swipe of Pendientes tints it) */
  border?: string;
  rootProps?: RootProps;
}) {
  const { t } = useT();
  const name = useAgentName();
  const compact = variant === "compact";
  const who = agentName || name(agentId);
  return (
    <Tag data-testid={testId} data-pending-kind={kind} data-variant={variant} {...rootProps}
      className={`rounded-[10px] border bg-white shadow-sm ${compact ? "p-3" : "p-3.5 sm:p-4"} ${border ?? (kind === "question" ? "border-accent/30" : "border-line")} ${className}`}>
      <div className="flex items-start gap-2.5">
        {lead}
        <PendingAvatar agentId={agentId} name={who} size={compact ? 28 : 32} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 text-[12px] leading-snug">
            <span data-testid="pending-who" className="truncate font-semibold text-ink">{who}</span>
            <span className="text-mute">{t(`pendingCard.asks.${kind}`)}</span>
          </div>
          {context && <div className="mt-0.5 truncate text-[11px] text-mute">{context}</div>}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1 sm:flex-row sm:items-center">
          <span className={`whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide ${kind === "question" ? "bg-accent/10 text-accent" : "bg-warn/10 text-warn"}`}>{t(`pendingCard.kind.${kind}`)}</span>
          {risk && <RiskBadge risk={risk} />}
        </div>
      </div>
      <div className="mt-2">
        {action && <div data-testid="pending-what" className="text-[10.5px] font-semibold uppercase tracking-[0.06em] text-accent"><ActionName code={action} /></div>}
        <div className={`break-words font-semibold leading-snug text-ink [overflow-wrap:anywhere] ${compact ? "text-[13px]" : "text-[15px] sm:text-[16px]"}`}>{title}</div>
        {body}
      </div>
      {why}
      {children}
      {actions && <div className="mt-2.5">{actions}</div>}
    </Tag>
  );
}

/** An approval with the immediate decision (office tray, office desk, "Mientras no estabas", dashboard, chat).
 * `scope="office"` / `"away"` only change the test ids, so the tray's `approval-<id>` selectors stay unique. */
export function ApprovalPending({ a, variant = "full", scope = "tray", context, extraActions }: {
  a: Approval; variant?: PendingVariant; scope?: "tray" | "office" | "away"; context?: ReactNode; extraActions?: ReactNode;
}) {
  const { t } = useT();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const canDecide = useCan("approvals:decide");
  // "Always allow this kind of action in this project": only offered when the server marked the approval as an
  // internal, reversible action of a project. The server checks it again; it never covers external actions.
  // The three choices of the card: approve once (default) / approve and remember for this session or this project / deny.
  const [always, setAlways] = useState(false);
  const [remember, setRemember] = useState<"session" | "project">("project");
  const [anyAgent, setAnyAgent] = useState(false);
  const compact = variant === "compact";
  const decide = async (d: "approve" | "reject") => {
    setBusy(true); setErr("");
    try { await api.decide(a.id, d, undefined, d === "approve" && always ? { anyAgent, scope: remember } : undefined); } catch { setErr(t("approvals.decideError")); setBusy(false); }
  };
  const canRemember = !!a.standing_eligible && !!a.project_id;
  const approveLabel = !canRemember ? t("approvals.approve") : !always ? t("approvals.approveOnce") : remember === "session" ? t("approvals.approveSession") : t("approvals.approveProject");
  const pending = a.status === "pending";
  // Same rule as the server (IsHighRiskApproval): the risk the policy engine stored, or a double approval.
  const highRisk = a.risk === "high" || (a.required_approvals ?? 1) >= 2;
  const testId = pending ? (scope === "office" ? `office-approval-card-${a.id}` : scope === "away" ? `away-approval-card-${a.id}` : `approval-${a.id}`) : undefined;
  const body = a.action === "code.run" ? <CodeRunView approvalId={a.id} status={a.status} compact={compact} />
    : a.details ? <p className={`mt-1 break-words text-[12px] leading-snug text-ink2 [overflow-wrap:anywhere] ${compact ? "line-clamp-2" : "line-clamp-6"}`} title={a.details}>{a.details}</p> : null;

  let actions: ReactNode;
  if (pending && !canDecide) {
    actions = <div data-testid={scope === "office" ? "office-approval-no-permission" : "approval-no-permission"} className="text-[11px] text-mute">{t("approvals.noPermission")}</div>;
  } else if (pending) {
    actions = (
      <>
        {highRisk && <DevilsAdvocate approvalId={a.id} />}
        {canRemember && (
          <div className="mt-2 space-y-1 text-[11px] text-ink2">
            <label className="flex items-center gap-1.5 font-semibold">
              <input type="checkbox" data-testid={`approval-always-${a.id}`} checked={always} disabled={busy} onChange={(e) => setAlways(e.target.checked)} />
              {t("approvals.always.label")}
            </label>
            {always && (
              <div role="radiogroup" aria-label={t("approvals.scope.label")} className="ml-5 flex flex-wrap items-center gap-x-4 gap-y-1">
                {(["session", "project"] as const).map((sc) => (
                  <label key={sc} className="flex items-center gap-1.5">
                    <input type="radio" name={`approval-scope-${a.id}`} data-testid={`approval-scope-${a.id}-${sc}`} checked={remember === sc} disabled={busy} onChange={() => setRemember(sc)} />
                    {t(`approvals.scope.${sc}`)}
                  </label>
                ))}
              </div>
            )}
            {always && remember === "project" && (
              <label className="ml-5 flex items-center gap-1.5">
                <input type="checkbox" data-testid={`approval-always-any-${a.id}`} checked={anyAgent} disabled={busy} onChange={(e) => setAnyAgent(e.target.checked)} />
                {t("approvals.always.anyAgent")}
              </label>
            )}
            {always && <p className="ml-5 text-mute">{remember === "session" ? t("approvals.scope.sessionHint") : t("approvals.always.hint")}</p>}
          </div>
        )}
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Btn kind="ok" disabled={busy} data-testid={`approve-btn-${a.id}`} onClick={() => decide("approve")}>{approveLabel}</Btn>
          <Btn kind="danger" disabled={busy} data-testid={`reject-btn-${a.id}`} onClick={() => decide("reject")}>{t("approvals.reject")}</Btn>
          {!always && <RememberApproval a={a} busy={busy} setBusy={setBusy} />}
          {extraActions}
          {err && <span role="alert" className="text-[11px] text-red-700">{err}</span>}
        </div>
      </>
    );
  } else {
    actions = (
      <div className="flex flex-wrap items-center gap-2">
        <span className={`text-[11px] font-semibold ${a.status === "approved" ? "text-emerald-700" : "text-red-700"}`}>{a.status === "approved" ? t("approvals.approved") : t("approvals.rejected")}</span>
        {extraActions}
      </div>
    );
  }
  return (
    <PendingCard kind="approval" variant={variant} testId={testId} agentId={a.agent_id} title={a.title} action={a.action} risk={a.risk}
      context={context} body={body} why={pending ? <ApprovalReason a={a} /> : null} actions={actions} />
  );
}

type Panel = "" | "file" | "reassign" | "skip";
type Candidate = { id: string; name: string; role: string; title: string };

/** "Necesita tu ayuda": an agent stopped a task to ask a person. Answer with an option, text or a file, pass the task
 * to another agent, or skip it (the app first shows what skipping leaves undone, and refuses when nothing would be
 * delivered). Selection checkboxes are for answering several at once in Pendientes. In the compact size the text
 * answer opens with "Responder". */
export function QuestionPending({ q, variant = "full", selectable = false, selected = false, onSelect }: {
  q: QuestionItem; variant?: PendingVariant; selectable?: boolean; selected?: boolean; onSelect?: (v: boolean) => void;
}) {
  const { t } = useT();
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
  const [replying, setReplying] = useState(variant === "full");
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
  const where = [q.task_title, q.project_name].filter(Boolean).join(" · ");
  const lead = selectable && open
    ? <input type="checkbox" className="mt-2 shrink-0" data-testid={`question-select-${q.id}`} aria-label={t("questions.select")} checked={selected} onChange={(e) => onSelect?.(e.target.checked)} />
    : null;
  const why = (
    <div className="mt-1.5 rounded-md bg-panel px-2 py-1.5 text-[11px] leading-snug text-ink2">
      {q.what_happened && q.what_happened !== q.need && <div><span className="font-semibold text-ink">{t("perm.why.label")}: </span>{q.what_happened}</div>}
      <div className="text-mute">{t("questions.waitingFor")} <WaitingLabel seconds={q.waiting_seconds} />{q.reminders > 0 ? ` · ${t("questions.reminded", { count: q.reminders })}` : ""}</div>
    </div>
  );

  let actions: ReactNode;
  if (!open) {
    actions = <div data-testid={`question-done-${q.id}`} className="text-[11px] font-semibold text-emerald-700">{t(q.status === "open" ? "questions.sent" : `questions.status.${q.status}`)}</div>;
  } else if (!q.can_answer) {
    actions = <div className="text-[11px] text-mute">{t("questions.notForYou")}</div>;
  } else {
    actions = (
      <>
        {q.options.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {q.options.map((o) => (
              <button key={o} type="button" disabled={busy} data-testid={`question-option-${q.id}`} onClick={() => void send("option", { option: o })}
                className="min-h-10 rounded-md border border-accent/40 bg-accent/5 px-2.5 py-1 text-[12px] font-medium text-accent hover:bg-accent/10 disabled:opacity-40 sm:min-h-0">{o}</button>
            ))}
          </div>
        )}
        {replying ? (
          <form className={`${q.options.length ? "mt-2" : ""} flex gap-1.5`} onSubmit={(e) => { e.preventDefault(); if (text.trim()) void send("text", { text: text.trim() }); }}>
            <input value={text} onChange={(e) => setText(e.target.value)} maxLength={2000} disabled={busy} data-testid={`question-text-${q.id}`} autoFocus={variant === "compact"}
              placeholder={t("questions.placeholder")} aria-label={t("questions.placeholder")}
              className="min-h-10 min-w-0 flex-1 rounded-md border border-line bg-white px-2 py-1.5 text-[12px] text-ink outline-none focus:border-accent sm:min-h-0" />
            <Btn kind="primary" type="submit" disabled={busy || !text.trim()} data-testid={`question-send-${q.id}`}>{t("questions.send")}</Btn>
          </form>
        ) : (
          <div className={q.options.length ? "mt-2" : ""}>
            <Btn kind="primary" data-testid={`question-reply-${q.id}`} onClick={() => setReplying(true)}>{t("questions.send")}</Btn>
          </div>
        )}
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
          <button type="button" className="min-h-8 font-medium text-accent sm:min-h-0" onClick={() => setPanel(panel === "file" ? "" : "file")}>{t("questions.file")}</button>
          <button type="button" className="min-h-8 font-medium text-accent sm:min-h-0" data-testid={`question-reassign-${q.id}`} onClick={() => void loadDetail("reassign")}>{t("questions.reassign")}</button>
          <button type="button" className="min-h-8 font-medium text-ink2 sm:min-h-0" data-testid={`question-skip-${q.id}`} onClick={() => void loadDetail("skip")}>{t("questions.skip.button")}</button>
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
    );
  }
  return (
    <PendingCard kind="question" variant={variant} testId={`question-${q.id}`} agentId={q.agent_id} lead={lead}
      title={q.need} context={where || undefined} why={why} actions={actions} />
  );
}
