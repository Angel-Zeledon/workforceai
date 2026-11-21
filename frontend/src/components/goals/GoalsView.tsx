"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { toast } from "@/lib/toast";
import { go } from "@/lib/navBridge";
import { goalsApi, type Goal, type GoalDetail, type GoalPatch } from "@/lib/goalsApi";
import { GOAL_LIMITS, activeCount, goalCreateBody, goalErrorKind, goalStatusKey, goalWhen, isOverdue, normalizeGoalStatus, sortGoals, validateGoal } from "@/lib/goalsLogic";
import { officesApi } from "@/lib/officesApi";
import { Btn, Card, Empty, Loading } from "../ui";

const field = "w-full rounded-md border border-line bg-panel2 px-2.5 py-1.5 text-[16px] text-ink md:text-[13px]";

/** Same message for every failed write: who may do it, the limit of active goals, or a text the server refused. */
function useErrorText() {
  const { t } = useT();
  return (e: unknown, fallback: string) => {
    const k = goalErrorKind(e);
    return k === "forbidden" ? t("goals.error.forbidden") : k === "limit" ? t("goals.error.limit") : k === "invalid" ? t("goals.error.invalid") : fallback;
  };
}

function StatusChip({ status }: { status: string }) {
  const { t } = useT();
  const s = normalizeGoalStatus(status);
  const cls = s === "active" ? "border-accent/30 bg-accent/10 text-accent" : s === "achieved" ? "border-ok/40 bg-ok/10 text-ok" : "border-line bg-panel2 text-mute";
  return <span data-testid="goal-status" data-status={s} className={`shrink-0 rounded-full border px-2 py-0.5 text-[10.5px] font-semibold ${cls}`}>{t(goalStatusKey(s))}</span>;
}

/** Period, deadline and department of a goal, one quiet line. */
function GoalMeta({ g }: { g: Goal }) {
  const { t, locale } = useT();
  const { period, due } = goalWhen(g, locale);
  const late = isOverdue(g);
  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-mute">
      <span data-testid="goal-scope">{g.office_name ? t("goals.scope.office", { name: g.office_name }) : t("goals.scope.company")}</span>
      {period && <span>{t("goals.period.line", { period })}</span>}
      {due && <span className={late ? "font-semibold text-warn" : ""}>{late ? t("goals.due.late", { date: due }) : t("goals.due.line", { date: due })}</span>}
    </div>
  );
}

/** Company goals (F1): what the business wants to achieve. Requests and projects can point to one; the final report then says it contributes to it. */
export function GoalsView() {
  const { t } = useT();
  const [goals, setGoals] = useState<Goal[] | null>(null);
  const [max, setMax] = useState(10);
  const [err, setErr] = useState(false);
  const [archived, setArchived] = useState(false);
  const [creating, setCreating] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const load = useCallback(() => {
    setErr(false);
    goalsApi.list(archived).then((r) => { setGoals(r.goals ?? []); setMax(r.max_active || 10); }).catch(() => setErr(true));
  }, [archived]);
  useEffect(load, [load]);

  const list = goals ? sortGoals(goals) : [];
  const active = goals ? activeCount(goals) : 0;
  const full = active >= max;

  if (open) return <GoalDetailView id={open} max={max} activeNow={active} onBack={() => { setOpen(null); load(); }} />;

  return (
    <div data-testid="goals-view" className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="font-display text-lg font-semibold text-ink">{t("goals.title")}</h1>
          <p className="mt-0.5 max-w-2xl text-xs text-mute">{t("goals.help")}</p>
        </div>
        <Btn data-testid="goals-new" kind="primary" onClick={() => setCreating((v) => !v)}>{t("goals.new")}</Btn>
      </div>
      {creating && (
        <Card title={t("goals.new")}>
          <GoalForm full={full} max={max} onCancel={() => setCreating(false)} onCreated={() => { setCreating(false); toast(t("goals.toast.created")); load(); }} />
        </Card>
      )}
      <label className="flex w-fit cursor-pointer items-center gap-2 text-[12px] text-ink2">
        <input data-testid="goals-show-archived" type="checkbox" checked={archived} onChange={(e) => { setGoals(null); setArchived(e.target.checked); }} />
        {t("goals.showArchived")}
      </label>
      {err ? <Empty icon="alert" action={<Btn onClick={load}>{t("goals.retry")}</Btn>}>{t("goals.error.load")}</Empty>
        : !goals ? <Loading rows={3} />
        : !list.length ? (
          <Empty testid="goals-empty" icon="checkbox" title={t("goals.empty.title")} action={<Btn kind="primary" onClick={() => setCreating(true)}>{t("goals.new")}</Btn>}>
            {t("goals.empty.body")}
          </Empty>
        ) : (
          <ul className="space-y-2">
            {list.map((g) => (
              <li key={g.id}>
                <button type="button" data-testid={`goal-row-${g.id}`} onClick={() => setOpen(g.id)}
                  className={`w-full rounded-[10px] border border-line bg-panel p-3 text-left transition hover:border-line-strong sm:p-4 ${normalizeGoalStatus(g.status) === "active" ? "" : "opacity-75"}`}>
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0 text-[14px] font-semibold text-ink">{g.title}</div>
                    <StatusChip status={g.status} />
                  </div>
                  {g.description && <p className="mt-1 line-clamp-2 text-[12px] text-ink2">{g.description}</p>}
                  <GoalMeta g={g} />
                </button>
              </li>
            ))}
          </ul>
        )}
      <p className="text-[11px] text-mute">{t("goals.limitNote", { active, max })}</p>
    </div>
  );
}

/** Create form: title, short description, period, optional deadline and who the goal is for. */
function GoalForm({ full, max, onCancel, onCreated }: { full: boolean; max: number; onCancel: () => void; onCreated: () => void }) {
  const { t } = useT();
  const errText = useErrorText();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [period, setPeriod] = useState("");
  const [due, setDue] = useState("");
  const [office, setOffice] = useState("");
  const [offices, setOffices] = useState<{ id: string; name: string }[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let alive = true;
    officesApi.list().then((r) => { if (alive) setOffices(r.offices.filter((o) => !o.general).map(({ id, name }) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name))); }).catch(() => { /* company-wide only */ });
    return () => { alive = false; };
  }, []);
  const submit = async () => {
    const issue = validateGoal({ title, description, period, due_date: due });
    if (issue) { setErr(t(`goals.form.error.${issue}`)); return; }
    setBusy(true); setErr("");
    try { await goalsApi.create(goalCreateBody({ title, description, period, due_date: due, office_id: office })); onCreated(); }
    catch (e) { setErr(errText(e, t("goals.error.save"))); }
    finally { setBusy(false); }
  };
  return (
    <form data-testid="goals-form" className="space-y-3" onSubmit={(e) => { e.preventDefault(); void submit(); }}>
      {full && <p role="status" className="rounded-md border border-warn/40 bg-warn/5 px-2.5 py-1.5 text-[12px] text-ink2">{t("goals.error.limit", { max })}</p>}
      <label className="block text-[11px] font-medium text-mute">{t("goals.form.title")}
        <input data-testid="goals-form-title" className={`${field} mt-1`} value={title} maxLength={GOAL_LIMITS.title} onChange={(e) => setTitle(e.target.value)} placeholder={t("goals.form.titlePlaceholder")} />
      </label>
      <label className="block text-[11px] font-medium text-mute">{t("goals.form.description")}
        <textarea data-testid="goals-form-description" className={`${field} mt-1 min-h-[64px]`} value={description} maxLength={GOAL_LIMITS.description} onChange={(e) => setDescription(e.target.value)} placeholder={t("goals.form.optional")} />
      </label>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block text-[11px] font-medium text-mute">{t("goals.form.period")}
          <input data-testid="goals-form-period" className={`${field} mt-1`} value={period} maxLength={GOAL_LIMITS.period} onChange={(e) => setPeriod(e.target.value)} placeholder={t("goals.form.periodPlaceholder")} />
        </label>
        <label className="block text-[11px] font-medium text-mute">{t("goals.form.due")}
          <input data-testid="goals-form-due" type="date" className={`${field} mt-1`} value={due} onChange={(e) => setDue(e.target.value)} />
        </label>
      </div>
      <label className="block text-[11px] font-medium text-mute">{t("goals.form.for")}
        <select data-testid="goals-form-office" className={`${field} mt-1`} value={office} onChange={(e) => setOffice(e.target.value)}>
          <option value="">{t("goals.form.forCompany")}</option>
          {offices.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
        </select>
      </label>
      {err && <p role="alert" data-testid="goals-form-error" className="text-[12px] text-err">{err}</p>}
      <div className="flex justify-end gap-2">
        <Btn onClick={onCancel}>{t("goals.form.cancel")}</Btn>
        <Btn data-testid="goals-form-save" kind="primary" type="submit" disabled={busy}>{t("goals.form.create")}</Btn>
      </div>
    </form>
  );
}

/** One goal: its data, the actions that change its state and the work that points to it. */
function GoalDetailView({ id, max, activeNow, onBack }: { id: string; max: number; activeNow: number; onBack: () => void }) {
  const { t } = useT();
  const errText = useErrorText();
  const [data, setData] = useState<GoalDetail | null>(null);
  const [err, setErr] = useState(false);
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => {
    setErr(false);
    goalsApi.get(id).then(setData).catch(() => setErr(true));
  }, [id]);
  useEffect(load, [load]);

  const change = async (patch: GoalPatch, done: string) => {
    setBusy(true);
    try { const g = await goalsApi.update(id, patch); setData((d) => (d ? { ...d, goal: g } : d)); toast(done); }
    catch (e) { toast(errText(e, t("goals.error.save")), "err"); }
    finally { setBusy(false); }
  };

  const g = data?.goal;
  const status = g ? normalizeGoalStatus(g.status) : "active";
  return (
    <div data-testid="goal-detail" className="space-y-4">
      <button type="button" data-testid="goal-back" onClick={onBack} className="text-[12px] font-semibold text-accent hover:underline">{t("goals.back")}</button>
      {err ? <Empty icon="alert" action={<Btn onClick={load}>{t("goals.retry")}</Btn>}>{t("goals.error.detail")}</Empty>
        : !data || !g ? <Loading rows={3} />
        : (
          <>
            <div className="rounded-[10px] border border-line bg-panel p-4">
              <div className="flex items-start justify-between gap-2">
                <h2 className="font-display text-lg font-semibold text-ink">{g.title}</h2>
                <StatusChip status={g.status} />
              </div>
              {g.description && <p className="mt-1.5 text-[13px] text-ink2">{g.description}</p>}
              <GoalMeta g={g} />
              <div className="mt-3 flex flex-wrap gap-2">
                {status === "active" && <Btn data-testid="goal-achieve" kind="primary" disabled={busy} onClick={() => change({ status: "achieved" }, t("goals.toast.achieved"))}>{t("goals.action.achieve")}</Btn>}
                {status === "active" && <Btn data-testid="goal-archive" disabled={busy} onClick={() => change({ status: "archived" }, t("goals.toast.archived"))}>{t("goals.action.archive")}</Btn>}
                {status !== "active" && <Btn data-testid="goal-reactivate" disabled={busy || activeNow >= max} onClick={() => change({ status: "active" }, t("goals.toast.reactivated"))}>{t("goals.action.reactivate")}</Btn>}
                {status === "achieved" && <Btn data-testid="goal-archive" disabled={busy} onClick={() => change({ status: "archived" }, t("goals.toast.archived"))}>{t("goals.action.archive")}</Btn>}
              </div>
              {status !== "active" && activeNow >= max && <p className="mt-2 text-[11px] text-mute">{t("goals.error.limit", { max })}</p>}
            </div>
            <Card title={t("goals.linked.requests")}>
              {data.requests.length ? (
                <ul className="divide-y divide-line">
                  {data.requests.map((r) => (
                    <li key={r.id} data-testid={`goal-request-${r.id}`} className="flex items-center justify-between gap-3 py-2 text-[12.5px]">
                      <span className="min-w-0 truncate text-ink2">{r.text}</span>
                      {r.report_id
                        ? <button type="button" onClick={() => go("/dashboard?section=reports")} className="shrink-0 text-[11px] font-semibold text-accent hover:underline">{t("goals.linked.seeReport")}</button>
                        : <span className="shrink-0 text-[11px] text-mute">{t(`goals.request.status.${r.status}`)}</span>}
                    </li>
                  ))}
                </ul>
              ) : <p className="text-[12px] text-mute">{t("goals.linked.noRequests")}</p>}
            </Card>
            <Card title={t("goals.linked.projects")}>
              {data.projects.length ? (
                <ul className="divide-y divide-line">
                  {data.projects.map((p) => (
                    <li key={p.id} data-testid={`goal-project-${p.id}`} className="flex items-center justify-between gap-3 py-2 text-[12.5px]">
                      <span className="min-w-0 truncate text-ink2">{p.name}</span>
                      <button type="button" onClick={() => go(`/projects?p=${encodeURIComponent(p.id)}`)} className="shrink-0 text-[11px] font-semibold text-accent hover:underline">{t("goals.linked.openProject")}</button>
                    </li>
                  ))}
                </ul>
              ) : <p className="text-[12px] text-mute">{t("goals.linked.noProjects")}</p>}
            </Card>
          </>
        )}
    </div>
  );
}
