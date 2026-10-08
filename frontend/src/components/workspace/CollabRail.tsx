"use client";
import { useCallback, useEffect, useState } from "react";
import { artifactApi, useArtifacts, type ArtifactComment, type ArtifactProposal, type StoredArtifact } from "@/lib/artifacts";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useAgentName } from "@/components/ui";

/** Comments and edit proposals of an artifact. Comment and proposal text is plain data (rendered as text, never as markup). */
export function CollabRail({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const name = useAgentName();
  const [comments, setComments] = useState<ArtifactComment[]>([]);
  const [proposals, setProposals] = useState<ArtifactProposal[]>([]);
  const [text, setText] = useState("");
  const [error, setError] = useState<"" | "conflict" | "failed">("");
  const who = (a: { kind: string; id: string }) => (a.kind === "agent" ? name(a.id) : t("common.you"));
  const pending = art.pending_proposals;

  const load = useCallback(() => {
    artifactApi.comments(art.id).then(setComments).catch(() => setComments([]));
    artifactApi.proposals(art.id).then(setProposals).catch(() => setProposals([]));
  }, [art.id]);
  useEffect(load, [load, pending, art.head_version]);

  const decide = async (p: ArtifactProposal, decision: "accept" | "reject") => {
    setError("");
    try {
      await artifactApi.decideProposal(art.id, p.id, decision);
      if (decision === "accept") await useArtifacts.getState().ensureContent(art.id, true);
      load();
    } catch (e) { setError(e instanceof Error && e.message.includes("409") ? "conflict" : "failed"); }
  };
  const send = async () => {
    const body = text.trim();
    if (!body) return;
    try { await artifactApi.addComment(art.id, { body }); setText(""); load(); } catch { setError("failed"); }
  };
  const open = proposals.filter((p) => p.status === "pending");
  const btn = "rounded-md border border-line bg-panel px-2 py-0.5 text-[11px] font-semibold hover:border-accent";

  return (
    <aside data-testid="art-collab" className="w-[260px] shrink-0 space-y-3 overflow-y-auto border-l border-line bg-panel2/40 p-2">
      {error && <div role="alert" data-testid="collab-error" className="rounded-lg border border-red-400/70 bg-red-100/70 px-2 py-1 text-[11px] text-ink">{t(error === "conflict" ? "collab.conflict" : "collab.failed")}</div>}
      <section>
        <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("collab.proposals")}</h4>
        {open.length === 0 && <p className="text-[11px] text-mute">{t("collab.noProposals")}</p>}
        <ul className="space-y-1.5">
          {open.map((p) => (
            <li key={p.id} data-testid={`proposal-${p.id}`} className="rounded-lg border border-dashed border-amber-400 bg-panel px-2 py-1 text-[11px]">
              <div className="flex items-center justify-between"><b>{who(p.author)}</b><span className="font-mono text-mute">v{p.base_version}</span></div>
              <div className="text-ink">{p.summary || t("collab.noSummary")}</div>
              <div className="text-[10px] text-mute">{fmtDateTime(p.created_at)}</div>
              <div className="mt-1 flex gap-1.5">
                <button type="button" data-testid={`proposal-accept-${p.id}`} disabled={art.locked} onClick={() => decide(p, "accept")} className={`${btn} text-emerald-700 disabled:opacity-40`}>{t("collab.accept")}</button>
                <button type="button" data-testid={`proposal-reject-${p.id}`} onClick={() => decide(p, "reject")} className={btn}>{t("collab.reject")}</button>
              </div>
            </li>
          ))}
        </ul>
      </section>
      <section>
        <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("collab.comments")}</h4>
        {comments.length === 0 && <p className="text-[11px] text-mute">{t("collab.noComments")}</p>}
        <ul className="space-y-1.5">
          {comments.map((c) => (
            <li key={c.id} data-testid="comment" className={`rounded-lg border border-line bg-panel px-2 py-1 text-[11px] ${c.parent_id ? "ml-3" : ""} ${c.resolved ? "opacity-60" : ""}`}>
              <div className="flex items-center justify-between"><b>{who(c.author)}</b><span className="text-[10px] text-mute">{fmtDateTime(c.created_at)}</span></div>
              <div className="whitespace-pre-wrap break-words text-ink">{c.body}</div>
              {!c.parent_id && <button type="button" data-testid="comment-resolve" onClick={() => artifactApi.resolveComment(art.id, c.id, !c.resolved).then(load).catch(() => setError("failed"))} className="mt-0.5 font-semibold text-accent hover:underline">{t(c.resolved ? "collab.reopen" : "collab.resolve")}</button>}
            </li>
          ))}
        </ul>
        <div className="mt-2 flex gap-1.5">
          <input data-testid="comment-input" aria-label={t("collab.placeholder")} placeholder={t("collab.placeholder")} value={text} maxLength={4000} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") send(); }} className="min-w-0 flex-1 rounded-md border border-line bg-panel px-2 py-1 text-[11px] outline-none focus:border-accent" />
          <button type="button" data-testid="comment-send" onClick={send} className={btn}>{t("collab.send")}</button>
        </div>
      </section>
    </aside>
  );
}
