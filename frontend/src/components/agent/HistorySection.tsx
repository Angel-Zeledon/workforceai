"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { agentHistoryApi, type RestorePreview, type RestoreResult, type VersionDetail, type VersionList, type VersionSummary } from "@/lib/agentHistoryApi";
import { applyFields, canRestoreVersion, changeRows, headline, skipLines, whatChanged } from "@/lib/agentHistoryLogic";
import { statusOf } from "@/lib/permissionsApi";
import { Btn, Card, Loading } from "../ui";

/** "Historial" of one agent (F4, docs/architecture/agent-history.md): who changed what and when, in plain words; what
 *  changed in each version (before / after); and "go back to this version", which first says what will not come back
 *  and why. The server decides everything; the screen only explains it. */
export function HistorySection({ id }: { id: string }) {
  const { t } = useT();
  const tick = useStore((s) => s.trustTick[id] || 0); // a change of autonomy reloads it
  const [list, setList] = useState<VersionList | null>(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const [detail, setDetail] = useState<VersionDetail | null>(null);
  const [confirm, setConfirm] = useState<RestorePreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<RestoreResult | null>(null);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(async () => {
    try { setList(await agentHistoryApi.list(id)); setErr(""); } catch (e) { setErr(statusOf(e) === 404 ? "" : "hist.error.load"); }
  }, [id]);
  useEffect(() => { void load(); }, [load, tick]);

  const toggle = async (v: VersionSummary) => {
    setConfirm(null); setDone(null);
    if (open === v.version) { setOpen(null); setDetail(null); return; }
    setOpen(v.version); setDetail(null);
    try { setDetail(await agentHistoryApi.detail(id, v.version)); } catch { setErr("hist.error.detail"); }
  };

  const ask = async (n: number) => {
    setBusy(true); setErr(""); setDone(null);
    try { setConfirm(await agentHistoryApi.preview(id, n)); } catch { setErr("hist.error.preview"); } finally { setBusy(false); }
  };

  const restore = async (n: number) => {
    setBusy(true); setErr("");
    try {
      const r = await agentHistoryApi.restore(id, n);
      setDone(r); setConfirm(null); setOpen(null); setDetail(null);
      await load();
    } catch (e) {
      setErr(statusOf(e) === 403 ? "hist.error.forbidden" : "hist.error.restore");
    } finally { setBusy(false); }
  };

  if (!list) return <Card title={t("hist.title")}>{err ? <p className="text-xs text-err">{t(err)}</p> : <Loading rows={2} />}</Card>;
  const items = showAll ? list.versions : list.versions.slice(0, 8);

  return (
    <Card title={t("hist.title")}>
      <div data-testid="agent-history" className="space-y-3">
        <p className="text-xs text-ink2">{t("hist.intro")}</p>
        {err && <p role="alert" className="text-xs text-err">{t(err)}</p>}
        {done && <RestoreDone r={done} />}
        <ol className="relative space-y-1.5 border-l border-line pl-4">
          {items.map((v) => {
            const isOpen = open === v.version;
            const current = v.version === list.current;
            return (
              <li key={v.version} data-testid={`hist-v-${v.version}`} className="relative">
                <span aria-hidden className={`absolute -left-[21px] top-3 h-2.5 w-2.5 rounded-full border-2 border-panel ${v.actor_kind === "system" ? "bg-mute" : "bg-accent"}`} />
                <button type="button" aria-expanded={isOpen} onClick={() => toggle(v)}
                  className={`w-full rounded-lg border px-3 py-2 text-left transition ${isOpen ? "border-accent bg-accent-soft" : "border-transparent hover:border-line hover:bg-panel2"}`}>
                  <span className="block text-[12.5px] font-medium text-ink">{headline(v, t)}</span>
                  <span className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[11px] text-mute">
                    <span>{fmtDateTime(v.created_at)}</span>
                    <span className="font-mono">v{v.version}</span>
                    {current && <span className="rounded-full border border-line px-1.5 text-[10.5px] text-ink2">{t("hist.current")}</span>}
                  </span>
                </button>
                {isOpen && (
                  <div className="mt-1.5 space-y-2 rounded-lg border border-line bg-panel px-3 py-2.5">
                    {!detail ? <Loading rows={1} /> : <Changes d={detail} />}
                    {canRestoreVersion(v, list.current, list.can_restore) && !confirm && (
                      <Btn data-testid={`hist-restore-${v.version}`} disabled={busy} onClick={() => ask(v.version)}>{t("hist.restore.button")}</Btn>
                    )}
                    {confirm && confirm.version === v.version && (
                      <Confirm p={confirm} busy={busy} onYes={() => restore(v.version)} onNo={() => setConfirm(null)} />
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ol>
        {list.versions.length > items.length && <Btn onClick={() => setShowAll(true)}>{t("hist.more", { n: list.versions.length - items.length })}</Btn>}
        <p className="text-[11px] text-mute">{t("hist.keep", { n: list.keep })}</p>
      </div>
    </Card>
  );
}

function Changes({ d }: { d: VersionDetail }) {
  const { t } = useT();
  const rows = changeRows(d.changes, t);
  if (d.previous === undefined) return <p className="text-[11.5px] text-ink2">{t("hist.detail.first")}</p>;
  if (!rows.length) return <p className="text-[11.5px] text-ink2">{t("hist.detail.none")}</p>;
  return (
    <dl className="space-y-2">
      {rows.map((r) => (
        <div key={r.field} className="text-[11.5px]">
          <dt className="font-semibold text-ink">{r.label}</dt>
          {r.secret ? <dd className="text-ink2">{t("hist.detail.secret")}</dd> : (
            <dd className="mt-0.5 grid gap-1 sm:grid-cols-2">
              <span className="rounded-md border border-line bg-panel2 px-2 py-1 text-ink2"><span className="block text-[10.5px] uppercase tracking-wide text-mute">{t("hist.detail.before")}</span><span className="whitespace-pre-wrap break-words">{r.before}</span></span>
              <span className="rounded-md border border-line px-2 py-1 text-ink"><span className="block text-[10.5px] uppercase tracking-wide text-mute">{t("hist.detail.after")}</span><span className="whitespace-pre-wrap break-words">{r.after}</span></span>
            </dd>
          )}
        </div>
      ))}
    </dl>
  );
}

function Confirm({ p, busy, onYes, onNo }: { p: RestorePreview; busy: boolean; onYes: () => void; onNo: () => void }) {
  const { t } = useT();
  const fields = applyFields(p.apply);
  const skips = skipLines(p.skip, t);
  return (
    <div data-testid="hist-confirm" role="dialog" aria-label={t("hist.confirm.title")} className="space-y-2 rounded-lg border border-line-strong bg-panel2 px-3 py-2.5">
      <p className="text-[12.5px] font-semibold text-ink">{t("hist.confirm.title")}</p>
      <p className="text-[11.5px] text-ink2">{fields.length ? t("hist.confirm.will", { what: whatChanged(fields, t) }) : t("hist.confirm.nothing")}</p>
      {skips.length > 0 && (
        <div>
          <p className="text-[11.5px] font-medium text-ink">{t("hist.confirm.wont")}</p>
          <ul className="mt-1 list-disc space-y-0.5 pl-4 text-[11.5px] text-ink2">{skips.map((s) => <li key={s.key}>{s.text}</li>)}</ul>
        </div>
      )}
      <p className="text-[11px] text-mute">{t("hist.confirm.new_version")}</p>
      <div className="flex flex-wrap gap-2">
        <Btn kind="primary" data-testid="hist-confirm-yes" disabled={busy || !fields.length} onClick={onYes}>{t("hist.confirm.yes")}</Btn>
        <Btn disabled={busy} onClick={onNo}>{t("hist.confirm.no")}</Btn>
      </div>
    </div>
  );
}

function RestoreDone({ r }: { r: RestoreResult }) {
  const { t } = useT();
  const skips = skipLines(r.skipped, t);
  return (
    <div data-testid="hist-done" role="status" className="space-y-1 rounded-lg border border-line bg-panel2 px-3 py-2 text-[11.5px]">
      <p className="font-medium text-ink">{r.unchanged ? t("hist.done.unchanged") : t("hist.done.ok", { what: whatChanged(applyFields(r.applied), t) })}</p>
      {skips.length > 0 && <ul className="list-disc space-y-0.5 pl-4 text-ink2">{skips.map((s) => <li key={s.key}>{s.text}</li>)}</ul>}
    </div>
  );
}
