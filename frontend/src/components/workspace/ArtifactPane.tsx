"use client";
import { useCallback, useEffect, useState } from "react";
import {
  artifactApi, attachModeOf, useArtifact, useArtifacts, type ArtifactLink, type ArtifactVersionInfo, type AttachMode, type StoredArtifact,
} from "@/lib/artifacts";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { useAgentColor, useAgentName } from "@/components/ui";
import { ArtifactBody } from "./ArtifactBody";
import { KindIcon } from "./kindMeta";

const ATTACH_MODES: AttachMode[] = ["none", "read", "propose", "edit"];

function download(name: string, mime: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: mime }));
  const a = document.createElement("a"); a.href = url; a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
const csvCell = (v: unknown) => {
  let s = v === null || v === undefined ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`; // spreadsheet formula injection guard (sec. 7.2)
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};
function toCsv(art: StoredArtifact): string {
  const c = art.content;
  if (art.kind === "table") return [c.columns.map((x: any) => csvCell(x.label)).join(","), ...c.rows.map((r: any) => c.columns.map((x: any) => csvCell(r.cells[x.key])).join(","))].join("\n");
  const sheet = c.sheets[0]; const keys = Object.keys(sheet.cells);
  const max = keys.reduce((m, k) => { const mm = /^([A-Z]+)(\d+)$/.exec(k); return mm ? { r: Math.max(m.r, Number(mm[2])), c: Math.max(m.c, mm[1].charCodeAt(0) - 64) } : m; }, { r: 0, c: 0 });
  const lines: string[] = [];
  for (let r = 1; r <= max.r; r++) lines.push(Array.from({ length: max.c }, (_, ci) => csvCell(sheet.cells[`${String.fromCharCode(65 + ci)}${r}`]?.v)).join(","));
  return lines.join("\n");
}

export function ArtifactPane({ id, deskId, focused, onFocus }: { id: string; deskId: string; focused?: boolean; onFocus?: () => void }) {
  const { t } = useT();
  const art = useArtifact(id);
  const agents = useStore((s) => s.agents);
  const agentName = useAgentName();
  const agentColor = useAgentColor();
  const setStatus = useArtifacts((s) => s.setStatus);
  const rename = useArtifacts((s) => s.rename);
  const setAttachment = useArtifacts((s) => s.setAttachment);
  const resolveConflict = useArtifacts((s) => s.resolveConflict);
  const openTab = useArtifacts((s) => s.openTab);
  const saveState = useArtifacts((s) => s.saveState[id] ?? "saved");
  const remoteAhead = useArtifacts((s) => !!s.remoteAhead[id]);
  const stale = useArtifacts((s) => !!s.stale[id]);
  const focus = useArtifacts((s) => s.focus[id]);
  const [title, setTitle] = useState("");
  const [showHistory, setShowHistory] = useState(false);
  const [versions, setVersions] = useState<ArtifactVersionInfo[]>([]);
  const [links, setLinks] = useState<ArtifactLink[]>([]);
  const [askOpen, setAskOpen] = useState(false);
  const [askText, setAskText] = useState("");
  const [askSent, setAskSent] = useState(false);
  const head = art?.head_version ?? 0;

  useEffect(() => { if (art) setTitle(art.title); }, [art?.title]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { artifactApi.links(id).then(setLinks).catch(() => setLinks([])); }, [id, head]);
  useEffect(() => { if (showHistory) artifactApi.versions(id).then(setVersions).catch(() => setVersions([])); }, [showHistory, id, head]);
  const allArtifacts = useArtifacts((s) => s.artifacts);

  const confirmReopen = useCallback(() => (art?.locked ? window.confirm(t("art.reopenConfirm")) : true), [art?.locked, t]);
  if (!art) return <div className="flex h-full items-center justify-center text-xs text-mute">{t("art.notFound")}</div>;

  const isAgentDesk = deskId !== "me" && !!agents[deskId];
  const mode = isAgentDesk ? attachModeOf(art, deskId) : "none";
  const statusColor = { draft: "#66707f", in_review: "#a86208", approved: "#2f7d55", sent: "#2b6cb0", archived: "#5b6678" }[art.status];
  const exportable = art.content !== undefined;

  return (
    <section data-testid={`ws-pane-${id}`} onMouseDown={onFocus} className={`flex h-full min-h-0 min-w-0 flex-col overflow-hidden ${focused ? "" : "opacity-95"}`}>
      <header className="space-y-1.5 border-b border-line bg-panel2/40 px-3 py-2">
        <div className="flex flex-wrap items-center gap-2">
          <KindIcon kind={art.kind} />
          <input data-testid="art-title" aria-label={t("art.title")} value={title} disabled={art.locked} onChange={(e) => setTitle(e.target.value)}
            onBlur={() => { const v = title.trim(); if (v && v !== art.title) rename(id, v); else setTitle(art.title); }}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }}
            className="min-w-[8rem] flex-1 rounded-lg border border-transparent bg-transparent px-1.5 py-0.5 font-display text-[15px] font-semibold text-ink outline-none hover:border-line focus:border-accent" />
          <span data-testid="art-status" data-status={art.status} className="rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${statusColor}22`, color: statusColor }}>{t(`artifact.status.${art.status}`)}</span>
          <span data-testid="art-version" className="font-mono text-[11px] text-mute">v{art.head_version}</span>
          <span data-testid="art-save-state" data-state={saveState} className={`text-[11px] font-semibold ${saveState === "saved" ? "text-emerald-700" : saveState === "saving" ? "text-mute" : "text-red-600"}`}>{t(`art.save.${saveState}`)}</span>
          {focus && <span data-testid="art-agent-badge" data-agent={focus.agent_id} className="ac-pop rounded-full px-2 py-0.5 text-[10px] font-semibold text-white" style={{ background: agentColor(focus.agent_id) }}>{t("art.agentEditing", { name: agentName(focus.agent_id), region: focus.region ?? "" })}</span>}
        </div>
        <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
          {art.status === "draft" && <button type="button" onClick={() => setStatus(id, "in_review")} className="rounded-md border border-line bg-panel px-2.5 py-0.5 font-semibold text-ink hover:border-accent">{t("art.sendReview")}</button>}
          {art.status === "in_review" && (
            <>
              {/* approval is a human-only capability: agents are never granted it (sec. 7.1) */}
              <button type="button" data-testid="art-approve" onClick={() => setStatus(id, "approved")} className="rounded-md border border-emerald-500/60 bg-emerald-500/15 px-2.5 py-0.5 font-semibold text-emerald-700 hover:bg-emerald-500/25">{t("art.approve")}</button>
              <button type="button" onClick={() => setStatus(id, "draft")} className="rounded-md border border-line bg-panel px-2.5 py-0.5 font-semibold text-ink hover:border-accent">{t("art.backToDraft")}</button>
            </>
          )}
          {art.locked && <button type="button" onClick={() => { if (confirmReopen()) setStatus(id, "draft"); }} className="rounded-md border border-line bg-panel px-2.5 py-0.5 font-semibold text-ink hover:border-accent">{t("art.reopen")}</button>}
          {isAgentDesk && (
            <label className="flex items-center gap-1 text-mute">
              {t("art.visibleTo", { name: agentName(deskId) })}
              <select data-testid={`art-attach-${deskId}`} value={mode} onChange={(e) => setAttachment(id, deskId, e.target.value as AttachMode)} className="rounded-md border border-line bg-panel px-1.5 py-0.5 font-semibold text-ink">
                {ATTACH_MODES.map((m) => <option key={m} value={m}>{t(`art.attach.${m}`)}</option>)}
              </select>
            </label>
          )}
          {isAgentDesk && <button type="button" data-testid="art-ask-agent" onClick={() => { setAskOpen(!askOpen); setAskSent(false); }} className="rounded-md border border-accent bg-accent/10 px-2.5 py-0.5 font-semibold text-accent hover:bg-accent/20">{t("art.askAgent", { name: agentName(deskId) })}</button>}
          <span className="ml-auto flex items-center gap-1.5">
            {exportable && <button type="button" data-testid="art-export-json" onClick={() => download(`${art.title}.json`, "application/json", JSON.stringify(art.content, null, 2))} className="text-mute hover:text-ink">JSON</button>}
            {exportable && (art.kind === "sheet" || art.kind === "table") && <button type="button" data-testid="art-export-csv" onClick={() => download(`${art.title}.csv`, "text/csv", toCsv(art))} className="text-mute hover:text-ink">CSV</button>}
            <button type="button" data-testid="art-history-toggle" onClick={() => setShowHistory(!showHistory)} className={`font-semibold ${showHistory ? "text-accent" : "text-mute hover:text-ink"}`}>{t("art.history")}</button>
          </span>
        </div>
        {askOpen && (
          <div className="flex items-center gap-2">
            <input value={askText} onChange={(e) => setAskText(e.target.value)} placeholder={t("art.askPlaceholder")} className="min-w-0 flex-1 rounded-md border border-line bg-panel px-3 py-1 text-[11px] outline-none focus:border-accent" />
            <button type="button" data-testid="art-ask-submit" onClick={async () => { try { await artifactApi.ask(id, { agent_id: deskId, text: askText || undefined, mode: askText ? "free" : "review_my_changes" }); setAskSent(true); setAskText(""); } catch { /* endpoint not available */ } }} className="rounded-md border border-accent bg-accent px-3 py-1 text-[11px] font-semibold text-white">{t("art.askSend")}</button>
            {askSent && <span className="text-[11px] font-semibold text-emerald-700">{t("art.askSent")}</span>}
          </div>
        )}
        {stale && (
          <div data-testid={`ws-dependency-badge-${id}`} className="flex items-center gap-2 rounded-lg border border-amber-400/70 bg-amber-100/70 px-2.5 py-1 text-[11px] text-ink">
            <span className="font-semibold">{t("art.sourceChanged")}</span>
            <button type="button" onClick={() => artifactApi.refreshDependencies(id).catch(() => {})} className="ml-auto font-semibold text-accent hover:underline">{t("art.refresh")}</button>
          </div>
        )}
        {saveState === "conflict" && (
          <div data-testid="art-conflict" className="flex flex-wrap items-center gap-2 rounded-lg border border-red-400/70 bg-red-100/70 px-2.5 py-1 text-[11px] text-ink">
            <span className="font-semibold">{t("art.conflict")}</span>
            <button type="button" data-testid="art-conflict-mine" onClick={() => resolveConflict(id, "mine")} className="rounded-md border border-line bg-panel px-2.5 py-0.5 font-semibold hover:border-accent">{t("art.keepMine")}</button>
            <button type="button" data-testid="art-conflict-theirs" onClick={() => resolveConflict(id, "theirs")} className="rounded-md border border-line bg-panel px-2.5 py-0.5 font-semibold hover:border-accent">{t("art.keepTheirs")}</button>
          </div>
        )}
        {remoteAhead && saveState !== "conflict" && <div className="text-[11px] font-semibold text-amber-700">{t("art.remoteAhead")}</div>}
      </header>
      <div className="flex min-h-0 flex-1">
        <div className="min-h-0 min-w-0 flex-1">
          {art.content === undefined ? <div className="p-6 text-center text-xs text-mute">…</div> : <ArtifactBody art={art} />}
        </div>
        {showHistory && (
          <aside className="w-[220px] shrink-0 overflow-y-auto border-l border-line bg-panel2/40 p-2">
            <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("art.history")}</h4>
            <ul className="space-y-1.5">
              {versions.map((v) => (
                <li key={v.version} data-testid={`art-version-${v.version}`} className="rounded-lg border border-line bg-panel px-2 py-1 text-[11px]">
                  <div className="flex items-center justify-between"><b className="font-mono">v{v.version}</b><span className="text-mute">{agentName(v.author.id === "me" ? "user" : v.author.id)}</span></div>
                  <div className="text-ink">{v.summary}</div>
                  <div className="text-[10px] text-mute">{fmtDateTime(v.created_at)}</div>
                  {v.version !== art.head_version && !art.locked && <button type="button" data-testid={`art-restore-${v.version}`} title={t("art.restore.hint")} onClick={() => artifactApi.restore(id, v.version).then(() => useArtifacts.getState().ensureContent(id, true))} className="mt-0.5 font-semibold text-accent hover:underline">{t("art.restore")}</button>}
                </li>
              ))}
            </ul>
          </aside>
        )}
      </div>
      {links.length > 0 && (
        <footer data-testid="ws-linked-artifacts" className="flex flex-wrap items-center gap-1.5 border-t border-line bg-panel2/40 px-3 py-1.5 text-[11px]">
          <span className="font-semibold uppercase tracking-wide text-mute">{t("art.links")}</span>
          {links.map((l) => {
            const out = l.from === id; const other = out ? l.to : l.from; const o = allArtifacts[other];
            return (
              <button key={l.id} type="button" data-testid={`art-link-${l.id}`} onClick={() => openTab(deskId, other, l.anchor ?? null)} className="inline-flex items-center gap-1 rounded-md border border-line bg-panel px-2 py-0.5 font-semibold text-ink hover:border-accent">
                {o && <KindIcon kind={o.kind} size={12} />}{out ? "→" : "←"} {o?.title ?? other} <span className="font-normal text-mute">({t(`art.relation.${l.relation}`)})</span>
              </button>
            );
          })}
        </footer>
      )}
    </section>
  );
}
