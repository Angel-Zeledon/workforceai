"use client";
import { Fragment, type ReactNode } from "react";

/**
 * Small, safe Markdown renderer for files written by agents or other people (untrusted).
 * It builds React elements only: raw HTML is shown as text, links keep http(s)/mailto and open with
 * rel="noopener noreferrer nofollow", and images are never fetched (the alt text is shown instead).
 */
const SAFE_URL = /^(https?:\/\/|mailto:)/i;

function inline(src: string, key = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  // order matters: code, image, link, bold, italic, strikethrough
  const rx = /`([^`]+)`|!\[([^\]]*)\]\(([^)\s]+)[^)]*\)|\[([^\]]+)\]\(([^)\s]+)[^)]*\)|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*\s][^*]*)\*|~~([^~]+)~~/g;
  let last = 0, n = 0, m: RegExpExecArray | null;
  while ((m = rx.exec(src))) {
    if (m.index > last) out.push(src.slice(last, m.index));
    const k = `${key}${n++}`;
    if (m[1] !== undefined) out.push(<code key={k} className="rounded bg-mute/10 px-1 py-[1px] font-mono text-[0.9em]">{m[1]}</code>);
    else if (m[3] !== undefined) out.push(<span key={k} data-md-image className="italic text-mute">[{m[2] || "image"}]</span>);
    else if (m[5] !== undefined) {
      out.push(SAFE_URL.test(m[5])
        ? <a key={k} href={m[5]} target="_blank" rel="noopener noreferrer nofollow" className="text-accent underline">{inline(m[4], k)}</a>
        : <span key={k}>{m[4]}</span>);
    } else if (m[6] !== undefined || m[7] !== undefined) out.push(<strong key={k}>{inline(m[6] ?? m[7], k)}</strong>);
    else if (m[8] !== undefined) out.push(<em key={k}>{inline(m[8], k)}</em>);
    else if (m[9] !== undefined) out.push(<del key={k}>{inline(m[9], k)}</del>);
    last = m.index + m[0].length;
  }
  if (last < src.length) out.push(src.slice(last));
  return out;
}

const cells = (l: string) => l.trim().replace(/^\||\|$/g, "").split("|").map((c) => c.trim());
const isTableSep = (l: string) => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(l) && l.includes("-");

export function Markdown({ text }: { text: string }) {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let i = 0, k = 0;
  while (i < lines.length) {
    const line = lines[i];
    const key = `b${k++}`;
    if (!line.trim()) { i++; continue; }
    const fence = /^\s*(```|~~~)\s*([\w-]*)/.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++;
      blocks.push(<pre key={key} className="overflow-x-auto rounded-lg border border-line bg-panel2 p-3 font-mono text-[12px] leading-relaxed"><code>{body.join("\n")}</code></pre>);
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const lvl = h[1].length;
      const cls = ["text-xl font-semibold mt-1", "text-lg font-semibold mt-4", "text-base font-semibold mt-3", "text-sm font-semibold mt-3", "text-sm font-semibold mt-2", "text-xs font-semibold mt-2"][lvl - 1];
      blocks.push(<div key={key} role="heading" aria-level={lvl} className={`${cls} text-ink`}>{inline(h[2], key)}</div>);
      i++; continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) { blocks.push(<hr key={key} className="my-3 border-line" />); i++; continue; }
    if (line.includes("|") && i + 1 < lines.length && isTableSep(lines[i + 1])) {
      const head = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim()) rows.push(cells(lines[i++]));
      blocks.push(
        <div key={key} className="overflow-x-auto"><table className="w-full border-collapse text-[12.5px]">
          <thead><tr>{head.map((c, j) => <th key={j} className="border-b border-line-strong px-2 py-1 text-left font-semibold">{inline(c, `${key}h${j}`)}</th>)}</tr></thead>
          <tbody>{rows.map((r, a) => <tr key={a} className="border-b border-line">{r.map((c, j) => <td key={j} className="px-2 py-1">{inline(c, `${key}${a}c${j}`)}</td>)}</tr>)}</tbody>
        </table></div>,
      );
      continue;
    }
    if (/^\s*>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
      blocks.push(<blockquote key={key} className="border-l-2 border-line-strong pl-3 text-ink2">{inline(body.join(" "), key)}</blockquote>);
      continue;
    }
    const li = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/.exec(line);
    if (li) {
      const ordered = /\d/.test(li[2]);
      const items: { depth: number; text: string }[] = [];
      while (i < lines.length) {
        const m = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/.exec(lines[i]);
        if (!m) break;
        items.push({ depth: Math.min(3, Math.floor(m[1].length / 2)), text: m[3] });
        i++;
      }
      const Tag = ordered ? "ol" : "ul";
      blocks.push(
        <Tag key={key} className={`${ordered ? "list-decimal" : "list-disc"} space-y-0.5 pl-5`}>
          {items.map((it, a) => <li key={a} style={{ marginLeft: it.depth * 16 }}>{inline(it.text, `${key}${a}`)}</li>)}
        </Tag>,
      );
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^\s*(#{1,6}\s|```|~~~|>|([-*+]|\d+[.)])\s)/.test(lines[i]) && !(lines[i].includes("|") && i + 1 < lines.length && isTableSep(lines[i + 1]))) para.push(lines[i++]);
    if (!para.length) { para.push(lines[i++]); }
    blocks.push(<p key={key}>{para.map((p, a) => <Fragment key={a}>{a > 0 && <br />}{inline(p, `${key}${a}`)}</Fragment>)}</p>);
  }
  return <div data-testid="viewer-markdown" className="space-y-2 break-words text-[13px] leading-relaxed text-ink">{blocks}</div>;
}
