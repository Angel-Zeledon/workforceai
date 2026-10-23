"use client";
import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { fmtDateTime, useT } from "@/lib/i18n";
import { portalApi, type CreatedPortalLink, type PortalCandidate, type PortalLink } from "@/lib/portalApi";
import { linkStatus, portalUrl } from "@/lib/portalLogic";
import { Btn } from "../ui";
import { Modal } from "./shared";
import { inputCls, labelCls } from "./templateUi";

const EXPIRY_DAYS = [7, 30, 90, 365, 0] as const;
const STATUS_CLS: Record<string, string> = {
  active: "border-ok/30 bg-ok/10 text-ok", expired: "border-line bg-panel2 text-mute", revoked: "border-err/30 bg-err/10 text-err",
};

function Deliverables({ candidates, picked, onToggle, testPrefix }: { candidates: PortalCandidate[]; picked: string[]; onToggle: (id: string) => void; testPrefix: string }) {
  const { t } = useT();
  const done = candidates.filter((c) => c.completed);
  const pending = candidates.length - done.length;
  return (
    <div>
      <div className={labelCls}>{t("portal.share.deliverables")}</div>
      <p className="mt-0.5 text-[11px] text-mute">{t("portal.share.deliverablesHint")}</p>
      {done.length === 0 ? <p data-testid={`${testPrefix}-no-deliverables`} className="mt-1.5 text-[11.5px] text-mute">{t("portal.share.noDeliverables")}</p> : (
        <ul className="mt-1.5 max-h-40 space-y-1 overflow-y-auto">
          {done.map((c) => (
            <li key={c.id}>
              <label className="flex cursor-pointer items-center gap-2 rounded-md px-1 py-1 text-xs text-ink hover:bg-panel2">
                <input type="checkbox" data-testid={`${testPrefix}-deliverable`} checked={picked.includes(c.id)} onChange={() => onToggle(c.id)} className="h-4 w-4 accent-[rgb(var(--accent))]" />
                <span className="min-w-0 flex-1 truncate">{c.title}</span>
                <span className="text-[10px] uppercase text-mute">{t(`portal.kind.${c.kind}`)}</span>
              </label>
            </li>
          ))}
        </ul>
      )}
      {pending > 0 && <p className="mt-1 text-[10.5px] text-mute">{t("portal.share.pendingDeliverables", { count: pending })}</p>}
    </div>
  );
}

function LinkRow({ link, projectId, candidates, onChanged }: { link: PortalLink; projectId: string; candidates: PortalCandidate[]; onChanged: () => void }) {
  const { t } = useT();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(link.title);
  const [label, setLabel] = useState(link.label);
  const [picked, setPicked] = useState<string[]>(link.deliverable_ids);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const status = link.revoked_at ? "revoked" : linkStatus(link);
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr("");
    try { await fn(); onChanged(); setEditing(false); } catch { setErr(t("portal.share.error")); }
    setBusy(false);
  };
  return (
    <li data-testid="portal-link-row" data-status={status} className="rounded-lg border border-line bg-panel p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{link.label || link.title || t("portal.share.untitled")}</span>
        <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold ${STATUS_CLS[status]}`}>{t(`portal.link.${status}`)}</span>
      </div>
      <div className="mt-1 space-y-0.5 text-[11px] text-mute">
        <div>{t("portal.share.createdAt", { when: fmtDateTime(link.created_at) })} · {link.expires_at ? t("portal.share.expiresAt", { when: fmtDateTime(link.expires_at) }) : t("portal.share.noExpiry")}</div>
        <div>{link.first_viewed_at ? t("portal.share.viewed", { first: fmtDateTime(link.first_viewed_at), last: fmtDateTime(link.last_viewed_at) }) : t("portal.share.notViewed")}</div>
        <div>{t("portal.share.sharedCount", { count: link.deliverable_ids.length })}</div>
      </div>
      {editing && (
        <div className="mt-3 space-y-2.5 border-t border-line pt-3">
          <label className={labelCls}>{t("portal.share.title")}<input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} className={`${inputCls} mt-1`} /></label>
          <label className={labelCls}>{t("portal.share.label")}<input value={label} onChange={(e) => setLabel(e.target.value)} maxLength={80} className={`${inputCls} mt-1`} /></label>
          <Deliverables candidates={candidates} picked={picked} onToggle={(id) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]))} testPrefix="portal-edit" />
        </div>
      )}
      {err && <p role="alert" className="mt-2 text-[11px] text-err">{err}</p>}
      {status === "active" && (
        <div className="mt-2 flex flex-wrap justify-end gap-2">
          {editing ? (
            <>
              <Btn onClick={() => setEditing(false)}>{t("portal.share.cancel")}</Btn>
              <Btn kind="primary" data-testid="portal-link-save" disabled={busy} onClick={() => run(() => portalApi.updateLink(projectId, link.id, { title, label, deliverable_ids: picked }))}>{t("portal.share.save")}</Btn>
            </>
          ) : (
            <>
              <Btn data-testid="portal-link-edit" onClick={() => setEditing(true)}>{t("portal.share.edit")}</Btn>
              <Btn kind="danger" data-testid="portal-link-revoke" disabled={busy} onClick={() => { if (window.confirm(t("portal.share.revokeConfirm"))) run(() => portalApi.revokeLink(projectId, link.id)); }}>{t("portal.share.revoke")}</Btn>
            </>
          )}
        </div>
      )}
    </li>
  );
}

/** "Share with the client": read-only links of this project with the firm's branding (B13). */
export function ClientPortalModal({ projectId, defaultTitle, onClose }: { projectId: string; defaultTitle: string; onClose: () => void }) {
  const { t } = useT();
  const router = useRouter();
  const [links, setLinks] = useState<PortalLink[] | null>(null);
  const [candidates, setCandidates] = useState<PortalCandidate[]>([]);
  const [title, setTitle] = useState(defaultTitle);
  const [label, setLabel] = useState("");
  const [days, setDays] = useState<number>(30);
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [created, setCreated] = useState<CreatedPortalLink | null>(null);
  const [copied, setCopied] = useState(false);

  const reload = useCallback(async () => {
    try {
      const [ls, cs] = await Promise.all([portalApi.links(projectId), portalApi.candidates(projectId)]);
      setLinks(ls); setCandidates(cs);
    } catch { setLinks([]); setErr(t("portal.share.loadError")); }
  }, [projectId, t]);
  useEffect(() => { reload(); }, [reload]);

  const create = async () => {
    if (busy) return;
    setBusy(true); setErr(""); setCopied(false);
    try {
      setCreated(await portalApi.createLink(projectId, { title: title.trim(), label: label.trim(), expires_in_days: days, deliverable_ids: picked }));
      setLabel(""); setPicked([]);
      await reload();
    } catch { setErr(t("portal.share.error")); }
    setBusy(false);
  };
  const url = created ? portalUrl(window.location.origin, created.token) : "";
  const copy = async () => {
    try { await navigator.clipboard.writeText(url); setCopied(true); } catch { setCopied(false); }
  };

  return (
    <Modal onClose={onClose} testId="client-portal-modal" wide>
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-lg font-semibold text-ink">{t("portal.share.heading")}</h2>
          <p className="mt-0.5 text-[11.5px] text-mute">{t("portal.share.subtitle")}</p>
        </div>
        <Btn data-testid="portal-branding-link" onClick={() => { onClose(); router.push("/settings?s=portal"); }}>{t("portal.share.branding")}</Btn>
      </div>

      <div className="mt-3 rounded-lg border border-line bg-panel2 p-3 text-[11px] leading-relaxed text-mute">
        <b className="text-ink">{t("portal.share.showsTitle")}</b> {t("portal.share.shows")}<br />
        <b className="text-ink">{t("portal.share.neverTitle")}</b> {t("portal.share.never")}
      </div>

      {created && (
        <div data-testid="portal-created" className="mt-3 rounded-lg border border-accent/30 bg-accent-soft p-3">
          <div className="text-[12px] font-semibold text-ink">{t("portal.share.createdTitle")}</div>
          <p className="mt-0.5 text-[11px] text-mute">{t("portal.share.createdHint")}</p>
          <div className="mt-2 flex flex-col gap-2 sm:flex-row">
            <input data-testid="portal-created-url" readOnly value={url} onFocus={(e) => e.currentTarget.select()} className={`${inputCls} font-mono text-base sm:text-xs`} />
            <div className="flex gap-2">
              <Btn kind="primary" data-testid="portal-copy" onClick={copy}>{copied ? t("portal.share.copied") : t("portal.share.copy")}</Btn>
              <a data-testid="portal-preview" href={url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center whitespace-nowrap rounded-md border border-line bg-panel px-3 py-1.5 text-xs font-semibold text-ink hover:border-line-strong">{t("portal.share.preview")}</a>
            </div>
          </div>
        </div>
      )}

      <div className="mt-4 space-y-3">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("portal.share.new")}</h3>
        <label className={labelCls}>{t("portal.share.title")}
          <input data-testid="portal-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} className={`${inputCls} mt-1 text-base sm:text-xs`} />
          <span className="mt-0.5 block font-normal text-mute">{t("portal.share.titleHint")}</span>
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className={labelCls}>{t("portal.share.label")}
            <input data-testid="portal-label" value={label} onChange={(e) => setLabel(e.target.value)} maxLength={80} placeholder={t("portal.share.labelPh")} className={`${inputCls} mt-1 text-base sm:text-xs`} />
            <span className="mt-0.5 block font-normal text-mute">{t("portal.share.labelHint")}</span>
          </label>
          <label className={labelCls}>{t("portal.share.expiry")}
            <select data-testid="portal-expiry" value={days} onChange={(e) => setDays(Number(e.target.value))} className={`${inputCls} mt-1 text-base sm:text-xs`}>
              {EXPIRY_DAYS.map((d) => <option key={d} value={d}>{d ? t("portal.share.days", { count: d }) : t("portal.share.noExpiryOption")}</option>)}
            </select>
          </label>
        </div>
        <Deliverables candidates={candidates} picked={picked} onToggle={(id) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]))} testPrefix="portal-new" />
        {err && <p role="alert" className="text-[11px] text-err">{err}</p>}
        <div className="flex justify-end gap-2">
          <Btn onClick={onClose}>{t("common.close")}</Btn>
          <Btn kind="primary" data-testid="portal-create" disabled={busy} onClick={create}>{t("portal.share.create")}</Btn>
        </div>
      </div>

      <div className="mt-5">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("portal.share.links")}</h3>
        {links === null ? <p className="mt-2 text-xs text-mute">…</p> : links.length === 0 ? (
          <p data-testid="portal-no-links" className="mt-2 text-xs text-mute">{t("portal.share.noLinks")}</p>
        ) : (
          <ul className="mt-2 space-y-2">
            {links.map((l) => <LinkRow key={`${l.id}:${l.updated_at}:${l.revoked_at ?? ""}`} link={l} projectId={projectId} candidates={candidates} onChanged={reload} />)}
          </ul>
        )}
      </div>
    </Modal>
  );
}
