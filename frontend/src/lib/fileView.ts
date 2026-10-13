/**
 * Files in the conversation: the viewer side panel (like Claude artifacts / ChatGPT canvas) and the type of a file.
 * Content is untrusted: nothing here renders HTML from a file, everything is shown as text, table or canvas.
 */
import { create } from "zustand";
import type { FileRef } from "./types";

export type FileKind = "markdown" | "csv" | "sheet" | "doc" | "json" | "code" | "text" | "pdf" | "image" | "other";

const CODE = /\.(py|go|js|ts|tsx|jsx|sql|sh|css|java|rb|rs|c|h|cpp|cs|php|yaml|yml|xml|toml|ini|tex|jsonl|log)$/i;

export function fileKind(name: string, mime = ""): FileKind {
  const n = name.toLowerCase();
  if (/\.(md|markdown)$/.test(n) || mime === "text/markdown") return "markdown";
  if (/\.(csv|tsv)$/.test(n) || mime === "text/csv" || mime === "text/tab-separated-values") return "csv";
  if (n.endsWith(".xlsx")) return "sheet";
  if (n.endsWith(".docx")) return "doc";
  if (n.endsWith(".json") || mime === "application/json") return "json";
  if (n.endsWith(".pdf") || mime === "application/pdf") return "pdf";
  if (/\.(png|jpe?g|gif|webp)$/.test(n) || /^image\/(png|jpeg|gif|webp)$/.test(mime)) return "image";
  if (CODE.test(n)) return "code";
  if (n.endsWith(".txt") || mime.startsWith("text/")) return "text";
  return "other";
}

/** Kinds whose content is text the user can edit in the panel. */
export const EDITABLE: FileKind[] = ["markdown", "csv", "json", "text", "code"];
/** Kinds that have a text form (viewer, diff, version compare). */
export const TEXTUAL: FileKind[] = ["markdown", "csv", "sheet", "json", "code", "text"];

/** Kinds shown from the server preview (/drive/files/:id/preview) instead of raw text or bytes. */
export const PREVIEWED: FileKind[] = ["sheet", "doc"];

/** A cell the table shows right-aligned: a number, optionally with a currency symbol, separators or a percent sign. */
export const isNumericCell = (s: string) => /^[-+(]?[$€£¥]?\s?[\d][\d.,\s]*\)?\s?%?$/.test(s.trim());

/** The active sheet index, kept inside the sheets that exist (a new version may have fewer). */
export const clampSheet = (i: number, n: number) => (n <= 0 ? 0 : Math.min(Math.max(i | 0, 0), n - 1));

export const fmtBytes = (n = 0) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);

interface ViewerState {
  ref: FileRef | null;
  /** "Edit this file in chat": messages sent from the chat carry the open file as context */
  editInChat: boolean;
  /** bumped when something changed the open file from outside the panel (Undo in a chat card) */
  rev: number;
  open: (r: FileRef) => void;
  close: () => void;
  setEditInChat: (on: boolean) => void;
  touch: () => void;
}
export const useFileViewer = create<ViewerState>((set) => ({
  ref: null,
  editInChat: false,
  rev: 0,
  open: (ref) => set((s) => ({ ref, editInChat: s.ref?.path === ref.path ? s.editInChat : false })),
  close: () => set({ ref: null, editInChat: false }),
  setEditInChat: (editInChat) => set({ editInChat }),
  touch: () => set((s) => ({ rev: s.rev + 1 })),
}));
