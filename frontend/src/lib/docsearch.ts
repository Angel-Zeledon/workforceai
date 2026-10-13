/**
 * Passage search over the drive with citations (capability 6, docs/architecture/agent-search.md).
 * Contract: backend/internal/api/docsearch.go. In mock mode the file-level mock search answers
 * (no pages, keyword only) so demos keep working.
 */
import { call } from "./api";
import { MOCK } from "./config";
import { drive } from "./drive";

export type SearchMode = "keyword" | "semantic" | "hybrid";
export interface Passage {
  file_id: string; path: string; name: string; version: number;
  page?: number; line?: number; start: number; end: number; heading?: string;
  /** <= 300 chars; the matched words are wrapped in « » */
  snippet: string; score: number;
}
export interface PassageResult { enabled: boolean; mode: SearchMode | "off"; note?: string; hits: Passage[] }
export interface IndexCounts { pending: number; indexing: number; indexed: number; skipped: number; failed: number; unindexed: number; chunks: number; embedded: number; too_large: number }
export interface EmbeddingsInfo { available: boolean; provider?: string; model?: string; reason?: string; note?: string; tokens_today: number; daily_token_cap: number }
export interface IndexStatus { enabled: boolean; status?: IndexCounts; embeddings: EmbeddingsInfo }
export interface DocIndexSettings { settings: { enabled: boolean; updated_by?: string; updated_at?: string }; embeddings: EmbeddingsInfo }

const q = encodeURIComponent;
let mockEnabled = true;
const mockEmb: EmbeddingsInfo = { available: false, reason: "embeddings_off", tokens_today: 0, daily_token_cap: 0 };

export const docSearch = {
  passages: async (query: string, path = "", mode?: SearchMode, limit = 20): Promise<PassageResult> => {
    if (MOCK) {
      if (!mockEnabled) return { enabled: false, mode: "off", hits: [] };
      const hits = await drive.search(query, path).catch(() => []);
      return { enabled: true, mode: "keyword", hits: hits.map((h) => ({ file_id: h.file_id, path: h.path, name: h.name, version: 1, start: 0, end: 0, snippet: h.snippet, score: 0 })) };
    }
    const r = await call<PassageResult>("GET", `/drive/passages?q=${q(query)}${path && path !== "/" ? `&path=${q(path)}` : ""}${mode ? `&mode=${mode}` : ""}&limit=${limit}`);
    return { ...r, hits: r.hits ?? [] };
  },
  status: (path = "/"): Promise<IndexStatus | null> =>
    MOCK ? Promise.resolve(null) : call<IndexStatus>("GET", `/drive/index/status?path=${q(path)}`).catch(() => null),
  settings: (): Promise<DocIndexSettings> =>
    MOCK ? Promise.resolve({ settings: { enabled: mockEnabled }, embeddings: mockEmb }) : call<DocIndexSettings>("GET", "/settings/doc-index"),
  /** Extracted text from a character offset (the context of a passage; works for docx/xlsx/pdf too). */
  textAt: async (id: string, offset: number): Promise<string> => {
    if (MOCK) return (await drive.text(id)).text;
    const r = await call<{ text: string }>("GET", `/drive/files/${id}/text?offset=${Math.max(0, Math.floor(offset))}&max_chars=6000`);
    return r.text ?? "";
  },
  putSettings: (enabled: boolean): Promise<DocIndexSettings> => {
    if (MOCK) { mockEnabled = enabled; return Promise.resolve({ settings: { enabled }, embeddings: mockEmb }); }
    return call<DocIndexSettings>("PUT", "/settings/doc-index", { enabled });
  },
};

/** Files still waiting for the index under a folder (pending + indexing + not reached yet). */
export const indexBacklog = (s: IndexCounts | undefined) => (s ? s.pending + s.indexing + s.unindexed : 0);

/** Splits a snippet on the « » markers: odd parts are the matched words. */
export function snippetParts(s: string): { text: string; hit: boolean }[] {
  const out: { text: string; hit: boolean }[] = [];
  const re = /«([^»]*)»/g;
  let last = 0;
  for (let m = re.exec(s); m; m = re.exec(s)) {
    if (m.index > last) out.push({ text: s.slice(last, m.index), hit: false });
    out.push({ text: m[1], hit: true });
    last = m.index + m[0].length;
  }
  if (last < s.length) out.push({ text: s.slice(last), hit: false });
  return out;
}

/** "p. 12" / "l. 40" / "" for a passage. */
export const citeOf = (p: Pick<Passage, "page" | "line">, pageWord: string, lineWord: string) =>
  p.page ? `${pageWord} ${p.page}` : p.line ? `${lineWord} ${p.line}` : "";
