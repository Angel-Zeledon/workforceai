"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { fromBlocks, newBid, plainText, segments, toBlocks, type DocBlock, type DocContent } from "../adapters/docAdapter";
import { ArtifactEmbed } from "../ArtifactBody";
import { KindIcon } from "../kindMeta";

function AutoText({ value, onChange, onBlur, className }: { value: string; onChange: (v: string) => void; onBlur: () => void; className: string }) {
  const ref = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => { const el = ref.current; if (el) { el.style.height = "auto"; el.style.height = `${el.scrollHeight}px`; el.focus(); } }, [value]);
  return <textarea ref={ref} rows={1} value={value} onChange={(e) => onChange(e.target.value)} onBlur={onBlur} className={`w-full resize-none rounded-lg border border-accent/50 bg-panel px-2 py-1 outline-none ${className}`} />;
}

export function DocEditor({ art, readOnly, compact, depth = 0 }: { art: StoredArtifact; readOnly?: boolean; compact?: boolean; depth?: number }) {
  const { t } = useT();
  const content = art.content as DocContent | undefined;
  const edit = useArtifacts((s) => s.edit);
  const anchor = useArtifacts((s) => s.anchor);
  const deskId = useArtifacts((s) => s.deskId);
  const openTab = useArtifacts((s) => s.openTab);
  const artifacts = useArtifacts((s) => s.artifacts);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [linkPick, setLinkPick] = useState("");
  const locked = !!readOnly || art.locked;
  const blocks = useMemo(() => toBlocks(content), [content]);
  const refs = useRef<Record<string, HTMLDivElement | null>>({});

  // scroll to the block a link points at
  useEffect(() => {
    if (!anchor || anchor.artifactId !== art.id || !anchor.anchor?.bid) return;
    refs.current[anchor.anchor.bid]?.scrollIntoView({ behavior: "smooth", block: "center" });
  }, [anchor?.n, art.id]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!content) return <div className="p-4 text-xs text-mute">…</div>;
  const save = (next: DocBlock[]) => edit(art.id, fromBlocks(next, content));
  const commitBlock = (b: DocBlock) => {
    setEditing(null);
    if (draft === b.text) return;
    save(blocks.map((x) => (x.bid === b.bid ? { ...x, text: draft, edited: true } : x)));
  };
  const addBlock = (type: "paragraph" | "heading", after?: string) => {
    const node = type === "heading" ? { type: "heading", attrs: { bid: newBid(), level: 2 }, content: [] } : { type: "paragraph", attrs: { bid: newBid() }, content: [] };
    const nb: DocBlock = { bid: node.attrs.bid, type, level: (node.attrs as any).level, text: "", links: {}, raw: node, editable: true, edited: true };
    const i = after ? blocks.findIndex((b) => b.bid === after) : blocks.length - 1;
    const next = [...blocks.slice(0, i + 1), nb, ...blocks.slice(i + 1)];
    save(next); setEditing(nb.bid); setDraft("");
  };
  const removeBlock = (bid: string) => save(blocks.filter((b) => b.bid !== bid));
  const insertEmbed = (target: string) => {
    const node = { type: "embed", attrs: { bid: newBid(), artifact_id: target, view: { height: 220 }, pinned_version: null } };
    save([...blocks, { bid: node.attrs.bid, type: "embed", text: "", links: {}, raw: node, editable: false, edited: false }]);
  };
  const insertLink = (target: string) => {
    if (!editing || !target) return;
    const b = blocks.find((x) => x.bid === editing); if (!b) return;
    const id = `l${Object.keys(b.links).length + 100}`;
    const label = artifacts[target]?.title ?? target;
    save(blocks.map((x) => (x.bid === b.bid ? { ...x, text: `${draft}[[${id}|${label}]]`, links: { ...x.links, [id]: { artifact_id: target, anchor: null, pinned_version: null } }, edited: true } : x)));
    setDraft(`${draft}[[${id}|${label}]]`); setLinkPick("");
  };
  const others = Object.values(artifacts).filter((a) => a.id !== art.id);

  return (
    <div data-testid="art-doc" className="flex h-full min-h-0 flex-col">
      {!compact && !locked && (
        <div className="flex flex-wrap items-center gap-1.5 border-b border-line bg-panel2/60 px-3 py-1.5">
          <button type="button" onClick={() => addBlock("heading")} className="rounded-md border border-line bg-panel px-3 py-0.5 text-[11px] font-semibold text-ink hover:border-accent">{t("doc.addHeading")}</button>
          <button type="button" onClick={() => addBlock("paragraph")} className="rounded-md border border-line bg-panel px-3 py-0.5 text-[11px] font-semibold text-ink hover:border-accent">{t("doc.addParagraph")}</button>
          <select aria-label={t("doc.insertEmbed")} value="" onChange={(e) => e.target.value && insertEmbed(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-0.5 text-[11px] font-semibold text-ink">
            <option value="">{t("doc.insertEmbed")}</option>
            {others.map((a) => <option key={a.id} value={a.id}>{t(`artifact.kind.${a.kind}`)}: {a.title}</option>)}
          </select>
          <select aria-label={t("doc.insertLink")} disabled={!editing} value={linkPick} onChange={(e) => insertLink(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-0.5 text-[11px] font-semibold text-ink disabled:opacity-40">
            <option value="">{t("doc.insertLink")}</option>
            {others.map((a) => <option key={a.id} value={a.id}>{t(`artifact.kind.${a.kind}`)}: {a.title}</option>)}
          </select>
        </div>
      )}
      <div className={`min-h-0 flex-1 overflow-y-auto bg-panel ${compact ? "p-3" : "px-8 py-6"}`}>
        <div className="mx-auto max-w-[760px] space-y-2">
          {blocks.map((b) => {
            const cls = b.type === "heading" ? (b.level === 1 ? "font-display text-2xl font-semibold" : "font-display text-lg font-semibold") : b.type === "blockquote" ? "border-l-4 border-line pl-3 italic text-mute" : "text-[13.5px] leading-relaxed";
            return (
              <div key={b.bid} ref={(el) => { refs.current[b.bid] = el; }} data-bid={b.bid} className={`group relative rounded-lg ${anchor?.artifactId === art.id && anchor.anchor?.bid === b.bid ? "bg-amber-200/60" : ""}`}>
                {b.type === "embed" ? (
                  <div data-testid={`art-embed-${b.bid}`} data-target-id={b.raw.attrs.artifact_id} data-target-kind={artifacts[b.raw.attrs.artifact_id]?.kind ?? ""}>
                    <ArtifactEmbed id={b.raw.attrs.artifact_id} height={b.raw.attrs.view?.height} depth={depth + 1} />
                  </div>
                ) : b.editable ? (
                  editing === b.bid && !locked ? (
                    <AutoText value={draft} onChange={setDraft} onBlur={() => commitBlock(b)} className={`text-ink ${cls}`} />
                  ) : (
                    <div onClick={() => { if (!locked) { setEditing(b.bid); setDraft(b.text); } }} className={`min-h-[1.5rem] whitespace-pre-wrap px-2 py-1 text-ink ${cls} ${locked ? "" : "cursor-text hover:bg-accent/5"}`}>
                      {b.text === "" && <span className="text-mute">{t("doc.empty")}</span>}
                      {segments(b.text, b.links).map((s, i) => s.t === "text" ? <span key={i}>{s.text}</span> : (
                        <button key={i} type="button" data-testid={`art-doclink-${b.bid}-${s.id}`} onClick={(e) => { e.stopPropagation(); if (s.attrs && deskId) openTab(deskId, s.attrs.artifact_id, s.attrs.anchor ?? null); }}
                          className="mx-0.5 rounded-md border border-accent/50 bg-accent/10 px-2 text-[12px] font-semibold text-accent hover:bg-accent/20">&#8599; {s.label}</button>
                      ))}
                    </div>
                  )
                ) : (
                  <div className="whitespace-pre-wrap rounded-lg border border-dashed border-line px-3 py-2 text-[13px] text-ink">{plainText(b.raw)}</div>
                )}
                {!compact && !locked && (
                  <button type="button" aria-label={t("doc.deleteBlock")} onClick={() => removeBlock(b.bid)} className="absolute -right-1 top-0 hidden rounded-full bg-panel2 px-1.5 text-xs text-mute shadow-pop hover:text-red-600 group-hover:block">&#10005;</button>
                )}
              </div>
            );
          })}
          {blocks.length === 0 && <div className="text-center text-xs text-mute"><KindIcon kind="doc" /> {t("doc.empty")}</div>}
        </div>
      </div>
    </div>
  );
}
