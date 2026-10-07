/**
 * Adapter between the canonical aiw.doc/1 (ProseMirror JSON, sec. 5.1) and the simple block model the DocEditor edits.
 * Only paragraph/heading/blockquote text is editable; every other node (clauses, lists, embeds, tables) is kept untouched.
 * Links to other artifacts (`artifact_link` marks) are edited as `[[id|label]]` tokens.
 */
import type { Anchor } from "@/lib/artifacts";

export interface LinkAttrs { artifact_id: string; anchor?: Anchor | null; pinned_version?: number | null }
export interface DocBlock {
  bid: string;
  type: string;
  level?: number;
  /** editable plain text with `[[linkId|label]]` tokens (only for editable types) */
  text: string;
  links: Record<string, LinkAttrs>;
  /** original node, preserved when the block is not edited (keeps marks, suggestions, comments) */
  raw: any;
  editable: boolean;
  edited: boolean;
}
export interface DocContent { schema: "aiw.doc/1"; doc: { type: "doc"; content: any[] }; comments?: unknown[] }

const EDITABLE = new Set(["paragraph", "heading", "blockquote"]);
const TOKEN = /\[\[(\w+)\|([^\]]*)\]\]/g;
let counter = 0;
export const newBid = () => `b${Date.now().toString(36)}${(counter++).toString(36)}`;

export function plainText(node: any): string {
  if (!node) return "";
  if (node.type === "text") return node.text ?? "";
  return (node.content ?? []).map(plainText).join(node.type === "paragraph" || node.type === "heading" ? "" : " ");
}

export function toBlocks(content: DocContent | undefined): DocBlock[] {
  const nodes: any[] = content?.doc?.content ?? [];
  return nodes.map((n, i) => {
    const bid: string = n.attrs?.bid ?? `auto${i}`;
    const editable = EDITABLE.has(n.type);
    const links: Record<string, LinkAttrs> = {};
    let text = "";
    if (editable) {
      let k = 0;
      for (const child of n.content ?? []) {
        const mark = (child.marks ?? []).find((m: any) => m.type === "artifact_link");
        if (child.type === "text" && mark) { const id = `l${k++}`; links[id] = mark.attrs; text += `[[${id}|${child.text}]]`; }
        else text += child.type === "text" ? child.text : "";
      }
    }
    return { bid, type: n.type, level: n.attrs?.level, text, links, raw: n, editable, edited: false };
  });
}

export function fromBlocks(blocks: DocBlock[], base?: DocContent): DocContent {
  const content = blocks.map((b) => {
    if (!b.edited || !b.editable) return b.raw;
    const nodes: any[] = [];
    let last = 0; let m: RegExpExecArray | null;
    TOKEN.lastIndex = 0;
    while ((m = TOKEN.exec(b.text))) {
      if (m.index > last) nodes.push({ type: "text", text: b.text.slice(last, m.index) });
      const attrs = b.links[m[1]];
      nodes.push(attrs ? { type: "text", text: m[2], marks: [{ type: "artifact_link", attrs }] } : { type: "text", text: m[2] });
      last = m.index + m[0].length;
    }
    if (last < b.text.length) nodes.push({ type: "text", text: b.text.slice(last) });
    return { ...b.raw, attrs: { ...(b.raw.attrs ?? {}), bid: b.bid }, content: nodes };
  });
  return { ...(base ?? { schema: "aiw.doc/1" as const, doc: { type: "doc" as const, content: [] } }), doc: { type: "doc", content } } as DocContent;
}

/** Splits editable text into plain and link segments for display. */
export function segments(text: string, links: Record<string, LinkAttrs>): ({ t: "text"; text: string } | { t: "link"; id: string; label: string; attrs: LinkAttrs | undefined })[] {
  const out: ({ t: "text"; text: string } | { t: "link"; id: string; label: string; attrs: LinkAttrs | undefined })[] = [];
  let last = 0; let m: RegExpExecArray | null;
  const re = new RegExp(TOKEN.source, "g");
  while ((m = re.exec(text))) {
    if (m.index > last) out.push({ t: "text", text: text.slice(last, m.index) });
    out.push({ t: "link", id: m[1], label: m[2], attrs: links[m[1]] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ t: "text", text: text.slice(last) });
  return out;
}
