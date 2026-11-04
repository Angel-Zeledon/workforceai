"use client";
// The panel of one pedido, beside the chat (docs/superpowers/specs/2026-11-03-chats-y-pedidos-design.md): everything about a
// piece of work in one place (summary, steps, files, report, how they worked on it) without leaving the chat. Desktop: a
// drawer on the right; phone: full screen with "Back". Closing (X, Esc, Back of the browser) returns to where the person was.
import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, call } from "@/lib/api";
import { chatsApi, useChats, type ConvRow } from "@/lib/chats";
import { fmtDateTime, fmtUsd, trOr, useT } from "@/lib/i18n";
import { useFileViewer } from "@/lib/fileView";
import { needsAttention, pedidoInfo, type PedidoCtx } from "@/lib/pedidosLogic";
import { useProjects } from "@/lib/projects/store";
import { useStore } from "@/lib/store";
import type { ChatMessage, FileRef, Report, Request, Task } from "@/lib/types";
import { Avatar, Bubble } from "../chat/ChatThread";
import { FileCards } from "../files/FileCard";
import { ReportView } from "../ReportView";
import { useAgentName } from "../ui";
import { Glyph, StatusPill } from "./chatsUi";

export type PedidoTab = "summary" | "steps" | "files" | "report" | "work";
const TABS: PedidoTab[] = ["summary", "steps", "files", "report", "work"];
const NO_MSGS: ChatMessage[] = [];

/** What the pure pedido logic needs, out of the store (memoized on the store slices it reads). */
export function usePedidoCtx(): PedidoCtx {
  const requests = useStore((s) => s.requests);
  const tasks = useStore((s) => s.tasks);
  const questions = useStore((s) => s.questions);
  const approvals = useStore((s) => s.approvals);
  return useMemo(() => ({
    requests, tasks: Object.values(tasks), questions: Object.values(questions ?? {}), approvals: Object.values(approvals),
  }), [requests, tasks, questions, approvals]);
}

/** The internal conversation of a pedido: the chat history row (archive / rename) or, failing that, the store one. */
export function usePedidoConv(requestId: string): { row?: ConvRow; convId: string; title: string; participants: string[]; projectId: string | null } {
  const row = useChats((s) => s.convs.find((c) => c.request_id === requestId));
  const conv = useStore((s) => (row ? undefined : Object.values(s.conversations).find((c) => c.request_id === requestId)));
  return { row, convId: row?.id ?? conv?.id ?? "", title: row?.title ?? conv?.title ?? "", participants: row?.participants ?? conv?.participants ?? [], projectId: row?.project_id ?? null };
}

/** Files the pedido produced: the files of every message that belongs to it (latest version of each path). */
function usePedidoFiles(requestId: string, convId: string): FileRef[] {
  const hist = useChats((s) => s.hist);
  const live = useStore((s) => s.chat);
  return useMemo(() => {
    const by = new Map<string, FileRef>();
    const take = (m: ChatMessage, all = false) => {
      if (!(all || m.request_id === requestId)) return;
      for (const f of m.files ?? []) if (f.action !== "attach") { const prev = by.get(f.path); if (!prev || (f.version ?? 0) >= (prev.version ?? 0)) by.set(f.path, f); }
    };
    for (const [k, h] of Object.entries(hist)) for (const m of h.items) take(m, !!convId && k === `conv:${convId}`);
    for (const list of Object.values(live)) for (const m of list) take(m);
    return [...by.values()];
  }, [hist, live, requestId, convId]);
}

function openProject(router: ReturnType<typeof useRouter>, id: string) {
  const p = useProjects.getState();
  p.setOpen(true);
  void p.openProject(id);
  router.push("/");
}

function ConvActions({ row, onGone }: { row: ConvRow; onGone: () => void }) {
  const { t } = useT();
  const patch = useChats((s) => s.patchConv);
  const drop = useChats((s) => s.dropConv);
  const [mode, setMode] = useState<"" | "rename" | "delete">("");
  const [title, setTitle] = useState(row.title);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  useEffect(() => { setMode(""); setTitle(row.title); setErr(false); }, [row.id, row.title]);
  const run = async (f: () => Promise<void>) => { setBusy(true); setErr(false); try { await f(); } catch { setErr(true); } setBusy(false); };
  const b = "chats-t inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11.5px] font-medium text-ink2 transition-colors hover:border-accent hover:text-ink disabled:opacity-50";
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {mode === "rename" ? (
        <form className="flex flex-wrap items-center gap-1" onSubmit={(e) => { e.preventDefault(); void run(async () => { const r = await chatsApi.rename(row.id, title); patch({ ...row, ...r, title: r.title ?? title }); setMode(""); }); }}>
          <input autoFocus data-testid="chats-rename-input" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} aria-label={t("chats.rename")}
            className="w-52 rounded-md border border-line-strong bg-panel2 px-2 py-1 text-[12px] text-ink outline-none focus:border-accent" />
          <button type="submit" data-testid="chats-rename-save" disabled={busy || !title.trim()} className={b}>{t("chats.save")}</button>
          <button type="button" className={b} onClick={() => { setMode(""); setTitle(row.title); }}>{t("chats.cancel")}</button>
        </form>
      ) : mode === "delete" ? (
        <div role="alertdialog" aria-label={t("pedido.delete.title")} className="flex flex-wrap items-center gap-1.5 rounded-md border border-err/40 bg-err/10 px-2 py-1">
          <span className="text-[11.5px] text-err">{t("pedido.delete.confirm")}</span>
          <button type="button" data-testid="chats-delete-confirm" disabled={busy} className="rounded-md border border-err/50 bg-err px-2 py-0.5 text-[11.5px] font-semibold text-white disabled:opacity-50"
            onClick={() => void run(async () => { await chatsApi.remove(row.id); drop(row.id); onGone(); })}>{t("chats.delete.yes")}</button>
          <button type="button" className={b} onClick={() => setMode("")}>{t("chats.cancel")}</button>
        </div>
      ) : (
        <>
          <button type="button" data-testid="chats-rename" className={b} onClick={() => setMode("rename")}><Glyph name="edit" size={12} />{t("chats.rename")}</button>
          <button type="button" data-testid="chats-archive" disabled={busy} className={b}
            onClick={() => void run(async () => { const r = await chatsApi.archive(row.id, !row.archived); patch({ ...row, ...r, archived: !row.archived }); })}>
            <Glyph name={row.archived ? "restore" : "archive"} size={12} />{row.archived ? t("chats.unarchive") : t("chats.archive")}
          </button>
          <button type="button" data-testid="chats-delete" className={`${b} hover:border-err hover:text-err`} onClick={() => setMode("delete")}><Glyph name="trash" size={12} />{t("chats.delete")}</button>
        </>
      )}
      {err && <span role="alert" className="text-[11px] text-err">{t("chats.actionError")}</span>}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="border-b border-line py-3 last:border-b-0">
      <dt className="mb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{label}</dt>
      <dd className="text-[12.5px] leading-relaxed text-ink">{children}</dd>
    </div>
  );
}

function Who({ ids }: { ids: string[] }) {
  const { t } = useT();
  const name = useAgentName();
  if (!ids.length) return <span className="text-mute">{t("pedido.nobody")}</span>;
  return (
    <span className="flex flex-wrap gap-1.5">
      {ids.map((id) => (
        <span key={id} className="inline-flex items-center gap-1.5 rounded-full border border-line bg-panel py-0.5 pl-0.5 pr-2 text-[11.5px] font-medium text-ink2"><Avatar id={id} size={18} />{name(id)}</span>
      ))}
    </span>
  );
}

const STEP_CLS: Record<string, string> = {
  running: "text-accent-hover", needs_input: "text-warn", awaiting_approval: "text-warn", done: "text-ok", failed: "text-err", blocked: "text-err",
};

function Steps({ tasks }: { tasks: Task[] }) {
  const { t } = useT();
  const name = useAgentName();
  if (!tasks.length) return <p data-testid="pedido-steps-empty" className="py-8 text-center text-xs leading-relaxed text-mute">{t("pedido.steps.empty")}</p>;
  return (
    <ol data-testid="pedido-steps" className="space-y-2">
      {tasks.map((x, i) => (
        <li key={x.id} data-testid="pedido-step" data-status={x.status} className="flex gap-2.5 rounded-xl border border-line bg-panel px-3 py-2.5">
          <span aria-hidden className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-mute/10 font-mono text-[10px] text-ink2">{i + 1}</span>
          <span className="min-w-0 flex-1">
            <span className="block text-[12.5px] font-semibold text-ink">{x.title}</span>
            <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-mute">
              <span className="inline-flex items-center gap-1"><Avatar id={x.agent_id} size={16} />{name(x.agent_id)}</span>
              <span className={`font-semibold ${STEP_CLS[x.status] ?? "text-ink2"}`}>{trOr(`pedido.step.${x.status}`, x.status)}</span>
            </span>
            {x.assigned_reason && <span className="mt-1 block text-[11px] leading-snug text-mute">{x.assigned_reason}</span>}
          </span>
        </li>
      ))}
    </ol>
  );
}

export function PedidoPanel({ requestId, onClose }: { requestId: string; onClose: () => void }) {
  const { t } = useT();
  const router = useRouter();
  const [tab, setTab] = useState<PedidoTab>("summary");
  const ctx = usePedidoCtx();
  const req: Request | undefined = useStore((s) => s.requests[requestId]);
  const tasksAll = useStore((s) => s.tasks);
  const report: Report | undefined = useStore((s) => Object.values(s.reports).find((r) => r.request_id === requestId) ?? (req?.report_id ? s.reports[req.report_id] : undefined));
  const loaded = useStore((s) => s.loaded);
  const conv = usePedidoConv(requestId);
  const loadRequestConv = useChats((s) => s.loadRequestConv);
  const work = useChats((s) => (conv.convId ? s.hist[`conv:${conv.convId}`] : undefined));
  const files = usePedidoFiles(requestId, conv.convId);
  const info = useMemo(() => pedidoInfo(requestId, ctx, conv.participants), [requestId, ctx, conv.participants]);
  const tasks = useMemo(() => Object.values(tasksAll).filter((x) => x.request_id === requestId).sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at)), [tasksAll, requestId]);
  const [missing, setMissing] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  // back to the first tab when another pedido opens
  useEffect(() => { setTab("summary"); box.current?.scrollTo(0, 0); }, [requestId]);

  // the detail (its steps) and, if the store does not have it yet, the request itself
  useEffect(() => {
    let live = true;
    setMissing(false);
    call<Request & { tasks?: Task[] }>("GET", `/requests/${encodeURIComponent(requestId)}`).then((d) => {
      if (!live || !d?.id) return;
      useStore.setState((s) => ({
        requests: { ...s.requests, [d.id]: { ...s.requests[d.id], id: d.id, text: d.text, status: d.status, created_at: d.created_at, report_id: d.report_id, cost_usd: d.cost_usd } },
        tasks: { ...s.tasks, ...Object.fromEntries((d.tasks ?? []).map((x) => [x.id, { ...s.tasks[x.id], ...x }])) },
      }));
    }).catch(() => { if (live && !useStore.getState().requests[requestId]) setMissing(true); });
    return () => { live = false; };
  }, [requestId]);

  // the report, when the request points to one the store has not loaded
  useEffect(() => {
    if (!req?.report_id || report) return;
    api.report(req.report_id).then((r) => { if (r?.id) useStore.setState((s) => ({ reports: { ...s.reports, [r.id]: r } })); }).catch(() => {});
  }, [req?.report_id, report]);

  // how they worked on it: the internal conversation (loaded once per opening, it is read-only)
  useEffect(() => { if (conv.convId) void loadRequestConv(conv.convId); }, [conv.convId, loadRequestConv]);

  // Esc closes (the file viewer, when open on top, takes its own Esc first)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape" && !useFileViewer.getState().ref && !e.defaultPrevented) onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const title = (conv.row?.title_custom ? conv.title : "") || req?.text || conv.title || t("pedido.label"); // a renamed pedido keeps its name
  const finished = report?.created_at || (info.status === "done" || info.status === "failed" ? tasks.map((x) => x.finished_at || "").sort().pop() : "") || "";
  const attention = needsAttention(info.status);
  const msgs = work?.items ?? NO_MSGS;

  return (
    <aside
      role="dialog" aria-modal="false" aria-label={t("pedido.aria", { title })} data-testid="pedido-panel" data-request={requestId} data-status={info.status}
      className="chats-fade absolute inset-0 z-20 flex min-h-0 flex-col bg-panel md:left-auto md:w-[460px] md:border-l md:border-line md:shadow-float xl:static xl:z-auto xl:shrink-0 xl:shadow-none"
    >
      <header className="border-b border-line px-4 pb-3 pt-2.5">
        <div className="flex items-center gap-2">
          <button type="button" data-testid="pedido-back" onClick={onClose} className="inline-flex min-h-10 items-center gap-1 rounded-md pr-2 text-[12px] font-semibold text-accent-hover hover:bg-panel2 md:hidden">
            <Glyph name="back" size={15} />{t("pedido.back")}
          </button>
          <span className="hidden text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute md:inline">{t("pedido.label")}</span>
          <span className="flex-1" />
          <button type="button" data-testid="pedido-close" onClick={onClose} aria-label={t("pedido.close")} title={t("pedido.close")}
            className="hidden min-h-9 min-w-9 items-center justify-center rounded-md text-mute hover:bg-panel2 hover:text-ink md:flex"><Glyph name="x" size={15} /></button>
        </div>
        <h2 data-testid="pedido-title" className="mt-1 line-clamp-2 text-[15px] font-semibold leading-snug tracking-tight text-ink">{title}</h2>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11.5px] text-mute">
          <StatusPill status={info.status} />
          {req?.created_at && <span>{t("pedido.created", { date: fmtDateTime(req.created_at) })}</span>}
          {conv.row?.archived && <span className="rounded bg-mute/10 px-1.5 py-[1px] text-[10.5px] font-medium text-ink2">{t("chats.archived.badge")}</span>}
        </div>
      </header>

      {attention && (
        <div data-testid="pedido-attention" role="status" className="flex flex-wrap items-center gap-2 border-b border-warn/30 bg-warn/10 px-4 py-2">
          <Glyph name="alert" size={14} className="shrink-0 text-warn" />
          <p className="min-w-0 flex-1 text-[12px] leading-snug text-ink">{t(info.status === "needs_help" ? "pedido.attention.help" : "pedido.attention.approval")}</p>
          <Link href="/approvals/inbox" data-testid="pedido-to-pending" className="shrink-0 rounded-md bg-accent px-2.5 py-1 text-[11.5px] font-semibold text-white hover:bg-accent-hover">{t("pedido.toPending")}</Link>
        </div>
      )}

      <div role="tablist" aria-label={t("pedido.label")} className="flex shrink-0 gap-1 overflow-x-auto border-b border-line px-3 py-1.5 wf-tabs">
        {TABS.map((k) => (
          <button key={k} type="button" role="tab" id={`pedido-tab-${k}`} aria-selected={tab === k} aria-controls="pedido-tabpanel" data-testid={`pedido-tab-${k}`} onClick={() => setTab(k)}
            className={`chats-t shrink-0 rounded-md px-2.5 py-1.5 text-[12px] font-medium transition-colors ${tab === k ? "bg-accent-soft text-accent-hover" : "text-ink2 hover:bg-panel2 hover:text-ink"}`}>
            {t(`pedido.tab.${k}`)}
            {k === "steps" && tasks.length > 0 && <span className="ml-1 font-mono text-[10px] text-mute">{tasks.length}</span>}
            {k === "files" && files.length > 0 && <span className="ml-1 font-mono text-[10px] text-mute">{files.length}</span>}
          </button>
        ))}
      </div>

      <div ref={box} id="pedido-tabpanel" role="tabpanel" aria-labelledby={`pedido-tab-${tab}`} data-testid={`pedido-body-${tab}`} className="min-h-0 flex-1 overflow-y-auto bg-bg px-4 py-3">
        {missing && !req && loaded ? (
          <p data-testid="pedido-missing" className="py-10 text-center text-xs text-mute">{t("pedido.notFound")}</p>
        ) : tab === "summary" ? (
          <dl className="rounded-xl border border-line bg-panel px-3.5">
            <Field label={t("pedido.asked")}><span className="whitespace-pre-line break-words">{req?.text || title}</span></Field>
            <Field label={t("pedido.state")}>
              <span className="flex flex-col items-start gap-1"><StatusPill status={info.status} testId="pedido-summary-status" /><span className="text-[12px] text-ink2">{t(`pedido.statusHint.${info.status}`)}</span></span>
            </Field>
            <Field label={t("pedido.who")}><Who ids={info.agents} /></Field>
            <Field label={t("pedido.cost")}><span className="font-mono text-[12px]">{fmtUsd(req?.cost_usd ?? 0)}</span></Field>
            <Field label={t("pedido.dates")}>
              <span className="block">{req?.created_at ? t("pedido.created", { date: fmtDateTime(req.created_at) }) : "—"}</span>
              {finished && <span className="block text-ink2">{t(info.status === "failed" ? "pedido.stopped" : "pedido.finished", { date: fmtDateTime(finished) })}</span>}
            </Field>
            {conv.projectId && (
              <Field label={t("pedido.project")}>
                <button type="button" data-testid="pedido-open-project" onClick={() => openProject(router, conv.projectId!)}
                  className="chats-t inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11.5px] font-medium text-accent-hover transition-colors hover:border-accent">
                  <Glyph name="folder" size={12} />{t("pedido.openProject")}
                </button>
              </Field>
            )}
            {conv.row && <Field label={t("pedido.manage")}><ConvActions row={conv.row} onGone={onClose} /></Field>}
          </dl>
        ) : tab === "steps" ? (
          <Steps tasks={tasks} />
        ) : tab === "files" ? (
          files.length ? <div data-testid="pedido-files" className="[&_[data-testid=file-card]]:max-w-none"><FileCards files={files} /></div>
            : <p data-testid="pedido-files-empty" className="py-8 text-center text-xs leading-relaxed text-mute">{t("pedido.files.empty")}</p>
        ) : tab === "report" ? (
          report ? <div data-testid="pedido-report" className="rounded-xl border border-line bg-panel p-4"><ReportView r={report} /></div>
            : <p data-testid="pedido-report-empty" className="py-8 text-center text-xs leading-relaxed text-mute">{t(info.status === "failed" ? "pedido.report.failed" : req?.report_id ? "pedido.report.loading" : "pedido.report.pending")}</p>
        ) : (
          <div data-testid="pedido-work">
            <p className="mb-2 rounded-lg bg-accent-soft/60 px-3 py-1.5 text-[11.5px] leading-snug text-ink2">{t("pedido.work.hint")}</p>
            {!conv.convId || (work?.loaded && msgs.length === 0) ? <p className="py-8 text-center text-xs text-mute">{t("pedido.work.empty")}</p>
              : !work?.loaded ? <p className="py-8 text-center text-xs text-mute">{t("chats.loadingOlder")}</p>
              : work.error && !msgs.length ? <p role="alert" className="py-8 text-center text-xs text-err">{t("chats.error.title")}</p>
              : <div className="space-y-1.5" role="log" aria-label={t("pedido.tab.work")}>{msgs.map((m, i) => <Bubble key={m.id} m={m} grouped={i > 0 && msgs[i - 1].from === m.from && m.from !== "user"} showRoute={false} />)}</div>}
          </div>
        )}
      </div>
    </aside>
  );
}
