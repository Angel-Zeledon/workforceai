"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { useRituals } from "@/lib/rituals";
import { ritualsApi, type Proposal, type Ritual, type RitualSettings } from "@/lib/ritualsApi";
import { entryTotals, toggleDay, type RitualEntry, type RitualItem, type RitualKind } from "@/lib/ritualsLogic";
import { Btn, Card, Dot, Empty, Loading, timeAgo, useAgentColor, useAgentName } from "../ui";

const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0];
const SIGNALS = ["rejected_approvals", "reworks", "failed_tasks", "quality_score"] as const;

function errCode(e: unknown): string {
  const m = String((e as Error)?.message || "");
  if (m.includes("-> 409")) return "blocked";
  if (m.includes("-> 429")) return "limit";
  if (m.includes("-> 403")) return "forbidden";
  return "generic";
}

/**
 * Rituals (B4): the daily stand-up and the weekly retro. Each agent tells what it did, what it will do and what
 * blocks it (from its real tasks); the retro proposes up to two improvements to each agent's own work, which only a
 * person accepts (an instruction note is added to the agent's work profile as a new version) or dismisses.
 */
export function RitualsPanel() {
  const { t } = useT();
  const canRun = useCan("requests:create");
  const canDecide = useCan("agents:write");
  const seq = useRituals((s) => s.seq);
  const [list, setList] = useState<Ritual[] | null>(null);
  const [open, setOpen] = useState<Ritual | null>(null);
  const [pending, setPending] = useState<Proposal[]>([]);
  const [busy, setBusy] = useState<string>("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");

  const load = useCallback(async () => {
    try {
      const [ls, ps] = await Promise.all([ritualsApi.list("", 20), ritualsApi.proposals("pending").catch(() => [] as Proposal[])]);
      setList(ls); setPending(ps);
      setOpen((cur) => cur ?? ls[0] ?? null);
    } catch { setList((l) => l ?? []); }
  }, []);
  useEffect(() => { void load(); }, [load, seq]);

  const run = async (kind: RitualKind) => {
    setBusy(kind); setErr(""); setNote("");
    try { const r = await ritualsApi.run(kind); setOpen(r); setNote(t("rituals.held")); }
    catch (e) { setErr(t(`rituals.error.${errCode(e)}`)); }
    setBusy(""); void load();
  };
  const decide = async (p: Proposal, kind: "accept" | "dismiss") => {
    setBusy(p.id); setErr(""); setNote("");
    try {
      if (kind === "accept") {
        const res = await ritualsApi.accept(p.id);
        setNote(res.applied ? t("rituals.proposal.applied", { version: String(res.profile_version ?? "") }) : t(p.kind === "skill_suggestion" ? "rituals.proposal.acceptedSkill" : "rituals.proposal.accepted"));
      } else { await ritualsApi.dismiss(p.id); setNote(t("rituals.proposal.dismissed")); }
    } catch (e) { setErr(t(`rituals.error.${errCode(e)}`)); }
    setBusy(""); void load();
  };

  return (
    <div data-testid="rituals-panel" className="space-y-3">
      <Card title={t("rituals.title")} right={canRun ? (
        <div className="flex flex-wrap gap-2">
          <Btn kind="primary" data-testid="rituals-run-standup" disabled={!!busy} onClick={() => run("standup")}>{t("rituals.run.standup")}</Btn>
          <Btn data-testid="rituals-run-retro" disabled={!!busy} onClick={() => run("retro")}>{t("rituals.run.retro")}</Btn>
        </div>
      ) : undefined}>
        <p className="text-xs text-mute">{t("rituals.subtitle")}</p>
        {note && <div role="status" className="mt-2 text-[11px] font-semibold text-emerald-700">{note}</div>}
        {err && <div role="alert" className="mt-2 text-[11px] text-red-700">{err}</div>}
      </Card>

      {pending.length > 0 && (
        <Card title={t("rituals.proposals.title")}>
          <ul className="space-y-2">
            {pending.map((p) => <ProposalRow key={p.id} p={p} canDecide={canDecide} busy={busy === p.id} onDecide={decide} />)}
          </ul>
          <p className="mt-2 text-[11px] text-mute">{t("rituals.proposals.hint")}</p>
        </Card>
      )}

      {list === null ? <Loading rows={3} /> : list.length === 0 ? <Empty>{t("rituals.empty")}</Empty> : (
        <div className="grid gap-3 md:grid-cols-[220px_1fr]">
          <ul className="space-y-1" data-testid="rituals-list">
            {list.map((r) => (
              <li key={r.id}>
                <button type="button" data-testid={`ritual-${r.id}`} onClick={() => ritualsApi.get(r.id).then(setOpen).catch(() => setOpen(r))}
                  className={`w-full rounded-md border px-2.5 py-1.5 text-left text-xs ${open?.id === r.id ? "border-accent bg-accent-soft" : "border-line bg-panel hover:bg-panel2"}`}>
                  <div className="font-semibold text-ink">{t(`rituals.kind.${r.kind}`)}{r.status === "skipped" ? ` · ${t("rituals.skipped")}` : ""}</div>
                  <div className="text-[11px] text-mute">{timeAgo(r.held_at)} · {t(`rituals.origin.${r.origin}`)}</div>
                </button>
              </li>
            ))}
          </ul>
          {open ? <RitualView r={open} /> : null}
        </div>
      )}
      <RitualSettingsCard />
    </div>
  );
}

function ProposalRow({ p, canDecide, busy, onDecide }: { p: Proposal; canDecide: boolean; busy: boolean; onDecide: (p: Proposal, k: "accept" | "dismiss") => void }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const known = (SIGNALS as readonly string[]).includes(p.signal);
  return (
    <li data-testid={`proposal-${p.id}`} className="rounded-md border border-line bg-panel2 p-3">
      <div className="mb-1 flex items-center justify-between gap-2 text-[11px] text-mute">
        <span className="flex items-center gap-1.5"><Dot color={color(p.agent_id)} />{t("rituals.proposal.from", { agent: name(p.agent_id) })}</span>
        <span>{t(`rituals.proposal.kind.${p.kind}`)}</span>
      </div>
      <div className="text-[13px] font-semibold text-ink">{known ? t(`rituals.signal.${p.signal}.title`) : p.title}</div>
      <p className="mt-1 break-words text-[12px] leading-snug text-ink2 [overflow-wrap:anywhere]">{p.text}</p>
      {p.evidence.length > 0 && (
        <ul className="mt-1 list-disc pl-4 text-[11px] text-mute">{p.evidence.map((e, i) => <li key={i} className="break-words [overflow-wrap:anywhere]">{e}</li>)}</ul>
      )}
      {canDecide ? (
        <div className="mt-2.5 flex items-center gap-2">
          <Btn kind="primary" disabled={busy} data-testid={`proposal-accept-${p.id}`} onClick={() => onDecide(p, "accept")}>{t("rituals.proposal.accept")}</Btn>
          <Btn disabled={busy} data-testid={`proposal-dismiss-${p.id}`} onClick={() => onDecide(p, "dismiss")}>{t("rituals.proposal.dismiss")}</Btn>
        </div>
      ) : <div className="mt-2 text-[11px] text-mute">{t("rituals.proposal.noPermission")}</div>}
    </li>
  );
}

function Items({ label, items }: { label: string; items: RitualItem[] }) {
  const { t } = useT();
  if (!items.length) return null;
  return (
    <div className="text-[12px] text-ink2">
      <span className="font-semibold text-ink">{label}: </span>
      {items.map((it, i) => (
        <span key={i}>
          {i > 0 ? "; " : ""}
          {it.status === "more" ? t("rituals.item.more", { n: it.title.replace("+", "") }) : <>«{it.title}»{it.status !== "done" && it.status !== "pending" ? ` (${t(`rituals.item.${it.status}`)})` : ""}</>}
        </span>
      ))}
    </div>
  );
}

function EntryCard({ e, kind }: { e: RitualEntry; kind: RitualKind }) {
  const { t } = useT();
  const color = useAgentColor();
  return (
    <li data-testid={`ritual-entry-${e.agent_id}`} className="rounded-md border border-line bg-panel p-2.5">
      <div className="mb-1 flex items-center gap-1.5 text-[12px] font-semibold text-ink"><Dot color={color(e.agent_id)} />{e.name}</div>
      {kind === "standup" ? (
        <div className="space-y-0.5">
          <Items label={t("rituals.entry.done")} items={e.done} />
          {!e.done.length && <div className="text-[12px] text-mute">{t("rituals.entry.noneDone")}</div>}
          <Items label={t("rituals.entry.next")} items={e.next} />
          {!e.next.length && <div className="text-[12px] text-mute">{t("rituals.entry.noneNext")}</div>}
          <Items label={t("rituals.entry.blockers")} items={e.blockers} />
        </div>
      ) : e.retro ? (
        <div className="text-[12px] text-ink2">
          {t("rituals.entry.week", { done: String(e.retro.tasks_done), failed: String(e.retro.tasks_failed), rejected: String(e.retro.rejected), reworks: String(e.retro.reworks) })}
          {e.retro.score !== null && e.retro.score !== undefined ? ` · ${t("rituals.entry.score", { score: String(e.retro.score) })}` : ""}
        </div>
      ) : null}
    </li>
  );
}

function RitualView({ r }: { r: Ritual }) {
  const { t } = useT();
  const tot = entryTotals(r.entries);
  const busy = r.entries.filter((e) => !e.quiet);
  const quiet = r.entries.filter((e) => e.quiet);
  return (
    <div data-testid="ritual-view">
      <Card title={`${t(`rituals.kind.${r.kind}`)} · ${new Date(r.held_at).toLocaleString()}`}>
        {r.status === "skipped" ? (
          <p className="text-xs text-mute">{t("rituals.skippedBecause", { reason: t(`rituals.reason.${r.skip_reason === "kill_switch_active" || r.skip_reason === "outside_operating_hours" ? r.skip_reason : "other"}`) })}</p>
        ) : (
          <div className="space-y-2">
            <p className="text-xs text-ink2">
              {r.summary_source === "deterministic"
                ? (r.kind === "standup"
                  ? t("rituals.summary.standup", { present: String(tot.present), done: String(tot.done), next: String(tot.next), blockers: String(tot.blockers), quiet: String(tot.quiet) })
                  : t("rituals.summary.retro", { present: String(tot.present), proposals: String(r.proposals?.length ?? 0) }))
                : <><span className="font-semibold">{t("rituals.summary.model")}: </span>{r.summary}</>}
            </p>
            {busy.length === 0 ? <Empty>{t("rituals.nothingNew")}</Empty> : (
              <ul className="space-y-2">{busy.map((e) => <EntryCard key={e.agent_id} e={e} kind={r.kind} />)}</ul>
            )}
            {quiet.length > 0 && <p className="text-[11px] text-mute">{t("rituals.quiet", { names: quiet.map((e) => e.name).join(", ") })}</p>}
            {r.absent.length > 0 && <p className="text-[11px] text-mute">{t("rituals.absent", { names: r.absent.map((a) => a.name).join(", ") })}</p>}
            {r.kind === "retro" && (r.proposals?.length ?? 0) > 0 && (
              <ul className="space-y-1 text-[11px] text-mute">
                {r.proposals!.map((p) => <li key={p.id}>{(SIGNALS as readonly string[]).includes(p.signal) ? t(`rituals.signal.${p.signal}.title`) : p.title} · {t(`rituals.proposal.status.${p.status}`)}</li>)}
              </ul>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}

/** Schedule of the rituals (owner/admin). Both are off by default. */
export function RitualSettingsCard() {
  const { t } = useT();
  const canManage = useCan("org:manage");
  const [cfg, setCfg] = useState<RitualSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  useEffect(() => { if (canManage) ritualsApi.settings().then(setCfg).catch(() => setCfg(null)); }, [canManage]);
  if (!canManage || !cfg) return null;
  const save = async () => {
    setBusy(true); setMsg("");
    try { setCfg(await ritualsApi.putSettings(cfg)); setMsg(t("rituals.settings.saved")); } catch { setMsg(t("rituals.error.generic")); }
    setBusy(false);
  };
  const input = "rounded-md border border-line-strong bg-panel px-2 py-1 text-sm text-ink outline-none focus:border-accent";
  return (
    <div data-testid="rituals-settings">
      <Card title={t("rituals.settings.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("rituals.settings.help")}</p>
          <label className="flex flex-wrap items-center gap-2">{t("rituals.settings.timezone")}
            <input data-testid="rituals-tz" className={input} value={cfg.timezone} onChange={(e) => setCfg({ ...cfg, timezone: e.target.value })} placeholder="America/Mexico_City" />
            <Btn onClick={() => setCfg({ ...cfg, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" })}>{t("rituals.settings.useMine")}</Btn>
          </label>
          <div className="space-y-1.5">
            <label className="flex items-center gap-2 font-semibold">
              <input type="checkbox" data-testid="rituals-standup-enabled" checked={cfg.standup.enabled} onChange={(e) => setCfg({ ...cfg, standup: { ...cfg.standup, enabled: e.target.checked } })} />
              {t("rituals.settings.standup")}
            </label>
            <div className="flex flex-wrap items-center gap-1.5 pl-5">
              <input type="time" className={input} value={cfg.standup.time} onChange={(e) => setCfg({ ...cfg, standup: { ...cfg.standup, time: e.target.value } })} />
              {WEEKDAYS.map((d) => (
                <button key={d} type="button" aria-pressed={cfg.standup.weekdays.includes(d)} onClick={() => setCfg({ ...cfg, standup: { ...cfg.standup, weekdays: toggleDay(cfg.standup.weekdays, d) } })}
                  className={`rounded-full border px-2 py-0.5 text-[11px] ${cfg.standup.weekdays.includes(d) ? "border-accent bg-accent-soft text-accent" : "border-line-strong text-ink2"}`}>{t(`rituals.day.${d}`)}</button>
              ))}
            </div>
          </div>
          <div className="space-y-1.5">
            <label className="flex items-center gap-2 font-semibold">
              <input type="checkbox" data-testid="rituals-retro-enabled" checked={cfg.retro.enabled} onChange={(e) => setCfg({ ...cfg, retro: { ...cfg.retro, enabled: e.target.checked } })} />
              {t("rituals.settings.retro")}
            </label>
            <div className="flex flex-wrap items-center gap-1.5 pl-5">
              <select className={input} value={cfg.retro.weekdays[0] ?? 5} onChange={(e) => setCfg({ ...cfg, retro: { ...cfg.retro, weekdays: [Number(e.target.value)] } })}>
                {WEEKDAYS.map((d) => <option key={d} value={d}>{t(`rituals.day.${d}`)}</option>)}
              </select>
              <input type="time" className={input} value={cfg.retro.time} onChange={(e) => setCfg({ ...cfg, retro: { ...cfg.retro, time: e.target.value } })} />
            </div>
          </div>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" data-testid="rituals-use-model" checked={cfg.use_model} onChange={(e) => setCfg({ ...cfg, use_model: e.target.checked })} />
            <span>{t("rituals.settings.useModel")}<br /><span className="text-mute">{t("rituals.settings.useModelHelp")}</span></span>
          </label>
          <div className="flex items-center gap-2">
            <Btn kind="primary" disabled={busy} data-testid="rituals-save" onClick={save}>{t("rituals.settings.save")}</Btn>
            {msg && <span role="status">{msg}</span>}
          </div>
        </div>
      </Card>
    </div>
  );
}
