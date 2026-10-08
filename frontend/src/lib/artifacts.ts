/**
 * Agent workspaces: artifact types, REST layer (docs/architecture/agent-workspaces.md sec. 5), and the Zustand
 * slice that holds artifacts, desks (tabs per agent) and applies `artifact.*` WebSocket events.
 * Kept separate from lib/store.ts; store.apply() only forwards `artifact.*` frames here.
 */
import { useEffect } from "react";
import { create } from "zustand";
import { call } from "./api";
import { API_URL, MOCK } from "./config";
import type { WsFrame } from "./types";

// ---- types (sec. 5.1) -----------------------------------------------------------------------------
export type ArtifactKind = "sheet" | "doc" | "table" | "board" | "chart" | "pdf" | "form" | "inbox" | "agenda";
export const ARTIFACT_KINDS: ArtifactKind[] = ["sheet", "doc", "table", "board", "chart", "pdf", "form", "inbox", "agenda"];
/** Kinds with a full editor in the first version; the others are read-only views. */
export const EDITABLE_KINDS: ArtifactKind[] = ["sheet", "doc", "table"];

export type ArtifactStatus = "draft" | "in_review" | "approved" | "sent" | "archived";
export type BuildState = "queued" | "building" | "ready_for_review" | "done" | "blocked";
/** Attachment modes an agent can be granted. There is deliberately no "approve": agents never approve. */
export type AttachMode = "none" | "read" | "propose" | "edit";
export type ActorKind = "agent" | "user" | "system";
export interface ActorRef { kind: ActorKind; id: string }
export interface Attachment { agent_id: string; mode: Exclude<AttachMode, "none"> }

export interface ArtifactMeta {
  id: string; kind: ArtifactKind; schema_version: string; title: string; status: ArtifactStatus;
  head_version: number; created_by: ActorRef; last_author: ActorRef;
  task_id?: string | null; request_id?: string | null; customer_id?: string | null;
  project_id?: string | null; deliverable_id?: string | null;
  progress: number; build_state: BuildState; locale: "es" | "en";
  depends_on_artifacts: string[]; pending_proposals: number; tainted: boolean; locked: boolean;
  size_bytes: number; attachments: Attachment[]; created_at: string; updated_at: string;
}
export interface Artifact extends ArtifactMeta { content: any; version: number }
export interface ArtifactVersionInfo { artifact_id: string; version: number; base_version: number | null; author: ActorRef; source: string; summary: string; created_at: string }
export interface ArtifactLink { id: string; from: string; to: string; relation: "embeds" | "source_of" | "derived_from" | "refers_to"; anchor?: Anchor | null; alias?: string | null; stale?: boolean }
export type Anchor = { sheet?: string; range?: string; bid?: string; card?: string; page?: number };
export interface Deliverable { deliverable_id: string; title: string; agent_id: string; status: string; progress: number; build_state: BuildState; artifacts: ArtifactMeta[] }
export interface ProjectWorkspace { project_id: string; name: string; status: string; deliverables: Deliverable[]; links: ArtifactLink[] }
export interface ArtifactTemplate { id: string; kind: ArtifactKind; title: { es: string; en: string }; suggested_for: string[] }
export interface KindInfo { kind: ArtifactKind; schema_version: string; suggested?: boolean }

// ---- capabilities (sec. 7.1) -----------------------------------------------------------------------
export type Capability = "read" | "create" | "write" | "comment" | "approve" | "export" | "delete";
/** Capabilities that can ever be granted to an agent: never approve, never delete. */
export const AGENT_GRANTABLE: Capability[] = ["read", "create", "write", "comment", "export"];
/** The frontend has no auth yet: the human operator holds every capability (the backend enforces the real RBAC). */
export const HUMAN_CAPS: Capability[] = ["read", "create", "write", "comment", "approve", "export", "delete"];
export const canAgent = (cap: Capability) => AGENT_GRANTABLE.includes(cap);

/** Suggested kinds per role (sec. 4): ordering for the "+" menu only, never a restriction. */
export const ROLE_SUGGESTED: Record<string, ArtifactKind[]> = {
  accounting: ["sheet", "doc", "chart", "table"],
  legal: ["doc", "table", "sheet", "pdf"],
  sales: ["doc", "board", "sheet", "chart"],
  analyst: ["chart", "sheet", "doc", "table"],
  operations: ["board", "table", "sheet", "agenda"],
  assistant: ["inbox", "agenda", "doc", "form"],
  hr: ["table", "board", "doc", "form"],
};

// ---- REST (sec. 5.5); missing backend endpoints are served by lib/mock/artifacts-mock.ts ---------------
export const artifactApi = {
  kinds: (agentId?: string) => call<{ items: KindInfo[] }>("GET", `/artifact-kinds${agentId ? `?agent_id=${agentId}` : ""}`),
  templates: (agentId?: string) => call<{ items: ArtifactTemplate[] }>("GET", `/artifact-templates${agentId ? `?agent_id=${agentId}` : ""}`),
  list: async () => { const r = await call<any>("GET", "/artifacts"); return (Array.isArray(r) ? r : r?.items ?? []) as ArtifactMeta[]; },
  get: (id: string) => call<Artifact>("GET", `/artifacts/${id}`),
  create: (body: { kind: ArtifactKind; title: string; content?: unknown; template_id?: string; agent_id?: string; locale?: string }) =>
    call<Artifact>("POST", "/artifacts", body),
  patch: (id: string, body: Partial<Pick<ArtifactMeta, "title" | "status">>) => call<ArtifactMeta>("PATCH", `/artifacts/${id}`, body),
  saveVersion: (id: string, body: { base_version: number; content: unknown; summary?: string }) =>
    call<{ version: number; merged: boolean; content?: unknown }>("POST", `/artifacts/${id}/versions`, body),
  versions: async (id: string) => { const r = await call<any>("GET", `/artifacts/${id}/versions`); return (Array.isArray(r) ? r : r?.items ?? []) as ArtifactVersionInfo[]; },
  restore: (id: string, version: number) => call<ArtifactMeta>("POST", `/artifacts/${id}/restore`, { version }),
  attach: (id: string, agentId: string, mode: AttachMode) => call<ArtifactMeta>("PUT", `/artifacts/${id}/attachments/${agentId}`, { mode }),
  links: async (id: string) => { const r = await call<any>("GET", `/artifacts/${id}/links?direction=both`); return (Array.isArray(r) ? r : r?.items ?? []) as ArtifactLink[]; },
  refreshDependencies: (id: string) => call<unknown>("POST", `/artifacts/${id}/refresh-dependencies`),
  ask: (id: string, body: { agent_id: string; text?: string; mode: "review_my_changes" | "free" }) => call<{ request_id: string }>("POST", `/artifacts/${id}/ask`, body),
  /** Server-side docx/xlsx export (GET /artifacts/{id}/export); resolves with the file to download. Not available in demo mode. */
  exportFile: async (id: string, format: "docx" | "xlsx", version?: number) => {
    if (MOCK) throw new Error("export unavailable in demo mode");
    const res = await fetch(`${API_URL}/artifacts/${encodeURIComponent(id)}/export?format=${format}${version ? `&version=${version}` : ""}`, { cache: "no-store" });
    if (!res.ok) throw new Error(`export -> ${res.status}`);
    const name = /filename="?([^";]+)"?/.exec(res.headers.get("Content-Disposition") ?? "")?.[1] ?? `artifact.${format}`;
    return { blob: await res.blob(), name };
  },
  project: (pid: string) => call<ProjectWorkspace>("GET", `/projects/${pid}/workspace`),
};

// ---- store -----------------------------------------------------------------------------------------
export type SaveState = "saved" | "saving" | "conflict" | "offline";
export type DeskSize = "dock" | "expanded" | "full";
export interface Desk { tabs: string[]; active: string | null; split: string | null; focusPane: 0 | 1 }
export const ME_DESK = "me";
export const projectTabId = (pid: string) => `project:${pid}`;
export const isProjectTab = (id: string) => id.startsWith("project:");

type Stored = Artifact | (ArtifactMeta & { content?: undefined; version?: undefined });
/** An artifact in the store: meta always, content once loaded. */
export type StoredArtifact = Stored;

interface ArtState {
  loaded: boolean;
  artifacts: Record<string, Stored>;
  desks: Record<string, Desk>;
  size: DeskSize;
  deskId: string | null;
  stale: Record<string, boolean>;
  focus: Record<string, { agent_id: string; region?: string; until: number }>;
  anchor: { artifactId: string; anchor: Anchor | null; n: number } | null;
  projects: Record<string, ProjectWorkspace>;
  saveState: Record<string, SaveState>;
  remoteAhead: Record<string, boolean>;
  pulse: Record<string, number>;
  load: () => Promise<void>;
  ensureContent: (id: string, force?: boolean) => Promise<void>;
  loadProject: (pid: string) => Promise<void>;
  setSize: (s: DeskSize) => void;
  openDesk: (deskId: string, size?: DeskSize) => void;
  ensureDesk: (deskId: string) => Desk;
  openTab: (deskId: string, id: string, anchor?: Anchor | null) => void;
  closeTab: (deskId: string, id: string) => void;
  setActive: (deskId: string, id: string) => void;
  toggleSplit: (deskId: string) => void;
  setFocusPane: (deskId: string, p: 0 | 1) => void;
  create: (deskId: string, body: Parameters<typeof artifactApi.create>[0]) => Promise<Artifact>;
  edit: (id: string, content: unknown, summary?: string) => void;
  resolveConflict: (id: string, keep: "mine" | "theirs") => Promise<void>;
  setStatus: (id: string, status: ArtifactStatus) => Promise<void>;
  rename: (id: string, title: string) => Promise<void>;
  setAttachment: (id: string, agentId: string, mode: AttachMode) => Promise<void>;
  applyFrame: (f: WsFrame) => void;
  reset: () => void;
}

const STORAGE_KEY = "aiw.desks.v1";
function readDesks(): Record<string, Desk> {
  try { const raw = sessionStorage.getItem(STORAGE_KEY); return raw ? JSON.parse(raw) : {}; } catch { return {}; }
}
function writeDesks(d: Record<string, Desk>) {
  try { sessionStorage.setItem(STORAGE_KEY, JSON.stringify(d)); } catch { /* storage unavailable */ }
}
const emptyDesk = (): Desk => ({ tabs: [], active: null, split: null, focusPane: 0 });

/** Pending human edits: debounced autosave per artifact (sec. 5.3 step 5). */
const dirty = new Map<string, { content: unknown; summary?: string }>();
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const focusTimers = new Map<string, ReturnType<typeof setTimeout>>();
let projectTimer: ReturnType<typeof setTimeout> | null = null;
const SAVE_DEBOUNCE_MS = 1500;

export const useArtifacts = create<ArtState>((set, get) => {
  const putDesk = (deskId: string, fn: (d: Desk) => Desk) => {
    const cur = get().desks[deskId] ?? emptyDesk();
    const desks = { ...get().desks, [deskId]: fn(cur) };
    writeDesks(desks);
    set({ desks });
  };
  const patchMeta = (id: string, patch: Partial<ArtifactMeta>) =>
    set((s) => (s.artifacts[id] ? { artifacts: { ...s.artifacts, [id]: { ...s.artifacts[id], ...patch } as Stored } } : {}));

  async function flush(id: string) {
    const pending = dirty.get(id);
    const art = get().artifacts[id];
    if (!pending || !art) return;
    dirty.delete(id);
    set((s) => ({ saveState: { ...s.saveState, [id]: "saving" } }));
    try {
      const r = await artifactApi.saveVersion(id, { base_version: art.version ?? art.head_version, content: pending.content, summary: pending.summary });
      const merged = r.merged && r.content !== undefined && !dirty.has(id);
      set((s) => {
        const cur = s.artifacts[id];
        if (!cur) return {};
        return { artifacts: { ...s.artifacts, [id]: { ...cur, head_version: Math.max(cur.head_version, r.version), version: r.version, last_author: { kind: "user", id: "me" }, ...(merged ? { content: r.content } : {}) } as Stored } };
      });
      if (!dirty.has(id)) set((s) => ({ saveState: { ...s.saveState, [id]: "saved" }, remoteAhead: { ...s.remoteAhead, [id]: false } }));
    } catch (e) {
      const msg = e instanceof Error ? e.message : "";
      dirty.set(id, pending);
      set((s) => ({ saveState: { ...s.saveState, [id]: msg.includes("409") ? "conflict" : "offline" } }));
    }
  }

  return {
    loaded: false, artifacts: {}, desks: {}, size: "dock", deskId: null, stale: {}, focus: {}, anchor: null,
    projects: {}, saveState: {}, remoteAhead: {}, pulse: {},

    load: async () => {
      try {
        const metas = await artifactApi.list();
        set((s) => {
          const artifacts: Record<string, Stored> = {};
          for (const m of metas) {
            const old = s.artifacts[m.id];
            artifacts[m.id] = (old && old.content !== undefined && old.version === m.head_version ? { ...old, ...m } : { ...m }) as Stored;
          }
          // drop tabs of artifacts that no longer exist (e.g. after a demo reset)
          const saved = { ...readDesks(), ...s.desks };
          const desks: Record<string, Desk> = {};
          for (const [k, d] of Object.entries(saved)) {
            const ok = (t: string) => isProjectTab(t) || !!artifacts[t];
            const tabs = d.tabs.filter(ok);
            desks[k] = { ...d, tabs, active: d.active && ok(d.active) ? d.active : tabs[0] ?? null, split: d.split && ok(d.split) ? d.split : null };
          }
          return { artifacts, desks, loaded: true };
        });
      } catch { set({ loaded: true, desks: { ...readDesks(), ...get().desks } }); }
    },
    ensureContent: async (id, force) => {
      const cur = get().artifacts[id];
      if (!force && cur && cur.content !== undefined) return;
      if (dirty.has(id)) return; // never overwrite unsaved human edits
      try {
        const a = await artifactApi.get(id);
        set((s) => ({ artifacts: { ...s.artifacts, [id]: a } }));
      } catch { /* not found / forbidden */ }
    },
    loadProject: async (pid) => {
      try { const p = await artifactApi.project(pid); set((s) => ({ projects: { ...s.projects, [pid]: p } })); } catch { /* ignore */ }
    },

    setSize: (size) => set({ size }),
    openDesk: (deskId, size = "expanded") => { get().ensureDesk(deskId); set({ deskId, size }); },
    ensureDesk: (deskId) => {
      const existing = get().desks[deskId];
      if (existing) return existing;
      // First visit: open the agent's own newest artifacts (suggested default, never a restriction).
      const mine = Object.values(get().artifacts)
        .filter((a) => deskId !== ME_DESK && (a.created_by.id === deskId || a.attachments.some((x) => x.agent_id === deskId)))
        .sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at))
        .slice(0, 5);
      const pids = Array.from(new Set(mine.map((m) => m.project_id).filter(Boolean) as string[]));
      const tabs = [...pids.map(projectTabId), ...mine.map((m) => m.id)];
      const desk: Desk = { tabs, active: tabs.find((t) => !isProjectTab(t)) ?? tabs[0] ?? null, split: null, focusPane: 0 };
      if (tabs.length) { const desks = { ...get().desks, [deskId]: desk }; writeDesks(desks); set({ desks }); }
      return desk;
    },
    openTab: (deskId, id, anchor) => {
      putDesk(deskId, (d) => {
        const tabs = d.tabs.includes(id) ? d.tabs : [...d.tabs, id];
        if (d.split && d.focusPane === 1) return { ...d, tabs, split: id };
        return { ...d, tabs, active: id };
      });
      if (!isProjectTab(id)) { get().ensureContent(id); if (anchor) set({ anchor: { artifactId: id, anchor, n: Date.now() } }); }
      else get().loadProject(id.slice(8));
    },
    closeTab: (deskId, id) => putDesk(deskId, (d) => {
      const tabs = d.tabs.filter((t) => t !== id);
      const active = d.active === id ? tabs[tabs.length - 1] ?? null : d.active;
      return { ...d, tabs, active, split: d.split === id ? null : d.split };
    }),
    setActive: (deskId, id) => putDesk(deskId, (d) => (d.split && d.focusPane === 1 ? { ...d, split: id } : { ...d, active: id })),
    toggleSplit: (deskId) => putDesk(deskId, (d) => {
      if (d.split) return { ...d, split: null, focusPane: 0 };
      const other = d.tabs.find((t) => t !== d.active && !isProjectTab(t)) ?? null;
      return other ? { ...d, split: other, focusPane: 1 } : d;
    }),
    setFocusPane: (deskId, focusPane) => putDesk(deskId, (d) => ({ ...d, focusPane })),

    create: async (deskId, body) => {
      const a = await artifactApi.create({ ...body, agent_id: deskId === ME_DESK ? undefined : deskId });
      set((s) => ({ artifacts: { ...s.artifacts, [a.id]: a } }));
      get().openTab(deskId, a.id);
      return a;
    },
    edit: (id, content, summary) => {
      const art = get().artifacts[id];
      if (!art) return;
      dirty.set(id, { content, summary });
      set((s) => ({ artifacts: { ...s.artifacts, [id]: { ...art, content } as Stored }, saveState: { ...s.saveState, [id]: "saving" } }));
      const t = timers.get(id); if (t) clearTimeout(t);
      timers.set(id, setTimeout(() => { timers.delete(id); flush(id); }, SAVE_DEBOUNCE_MS));
    },
    resolveConflict: async (id, keep) => {
      if (keep === "theirs") {
        dirty.delete(id);
        await get().ensureContent(id, true);
        set((s) => ({ saveState: { ...s.saveState, [id]: "saved" }, remoteAhead: { ...s.remoteAhead, [id]: false } }));
        return;
      }
      try {
        const head = await artifactApi.get(id); // rebase my content onto the current head
        patchMeta(id, { head_version: head.head_version, version: head.head_version } as Partial<ArtifactMeta>);
        set((s) => ({ remoteAhead: { ...s.remoteAhead, [id]: false } }));
        await flush(id);
      } catch { set((s) => ({ saveState: { ...s.saveState, [id]: "offline" } })); }
    },
    setStatus: async (id, status) => {
      const m = await artifactApi.patch(id, { status });
      patchMeta(id, m);
    },
    rename: async (id, title) => {
      patchMeta(id, { title });
      try { patchMeta(id, await artifactApi.patch(id, { title })); } catch { /* keep optimistic title */ }
    },
    setAttachment: async (id, agentId, mode) => {
      const art = get().artifacts[id];
      if (art) {
        const rest = art.attachments.filter((x) => x.agent_id !== agentId);
        patchMeta(id, { attachments: mode === "none" ? rest : [...rest, { agent_id: agentId, mode }] });
      }
      try { patchMeta(id, await artifactApi.attach(id, agentId, mode)); } catch { /* keep optimistic */ }
    },

    applyFrame: (f) => {
      const p = f.payload || {};
      switch (f.type) {
        case "artifact.created": {
          const m: ArtifactMeta = p.artifact;
          if (!m) break;
          set((s) => ({ artifacts: { ...s.artifacts, [m.id]: { ...s.artifacts[m.id], ...m } as Stored } }));
          const agent = f.agent_id || m.created_by?.id;
          if (agent && m.created_by?.kind === "agent") {
            // a new agent artifact becomes a tab of that agent's desk (desks not visited yet are built from the artifact list on first open)
            if (get().desks[agent]) putDesk(agent, (d) => (d.tabs.includes(m.id) ? d : { ...d, tabs: [...d.tabs, m.id], active: d.active ?? m.id }));
            if (p.open_in_workspace && get().deskId === agent && get().size !== "dock") get().setActive(agent, m.id);
          }
          if (m.project_id) get().loadProject(m.project_id);
          break;
        }
        case "artifact.updated": {
          const m: ArtifactMeta = p.artifact;
          if (m) patchMeta(m.id, m);
          break;
        }
        case "artifact.status_changed": patchMeta(p.artifact_id, { status: p.to }); break;
        case "artifact.version_created": {
          const id: string = p.artifact_id;
          if (!get().artifacts[id]) break;
          const mine = p.author?.kind === "user";
          patchMeta(id, { head_version: p.version, last_author: p.author, updated_at: f.ts });
          if (p.author?.kind === "agent") set((s) => ({ pulse: { ...s.pulse, [p.author.id]: Date.now() } }));
          if (mine) break;
          if (dirty.has(id)) set((s) => ({ remoteAhead: { ...s.remoteAhead, [id]: true } }));
          else get().ensureContent(id, true);
          break;
        }
        case "artifact.progress_changed": {
          patchMeta(p.artifact_id, { progress: p.progress, build_state: p.build_state });
          if (p.project_id) {
            const pid = p.project_id as string;
            if (projectTimer) clearTimeout(projectTimer);
            projectTimer = setTimeout(() => { projectTimer = null; get().loadProject(pid); }, 300);
          }
          break;
        }
        case "artifact.dependency_changed": set((s) => ({ stale: { ...s.stale, [p.artifact_id]: !!p.stale } })); break;
        case "artifact.recalculated":
          set((s) => ({ stale: { ...s.stale, [p.artifact_id]: false } }));
          patchMeta(p.artifact_id, { head_version: p.version });
          get().ensureContent(p.artifact_id, true);
          break;
        case "artifact.agent_focus": {
          const id: string = p.artifact_id;
          const until = Date.now() + (p.ttl_ms ?? 3000);
          set((s) => ({ focus: { ...s.focus, [id]: { agent_id: p.agent_id, region: p.region, until } } }));
          const old = focusTimers.get(id); if (old) clearTimeout(old);
          focusTimers.set(id, setTimeout(() => {
            focusTimers.delete(id);
            set((s) => { const next = { ...s.focus }; delete next[id]; return { focus: next }; });
          }, p.ttl_ms ?? 3000));
          break;
        }
        case "artifact.deleted":
          set((s) => { const a = { ...s.artifacts }; delete a[p.artifact_id]; return { artifacts: a }; });
          break;
      }
    },
    reset: () => { dirty.clear(); timers.forEach(clearTimeout); timers.clear(); set({ artifacts: {}, projects: {}, stale: {}, focus: {}, saveState: {}, remoteAhead: {}, desks: {}, loaded: false }); writeDesks({}); },
  };
});

// ---- helpers ---------------------------------------------------------------------------------------
export const agentArtifacts = (all: Record<string, Stored>, agentId: string) =>
  Object.values(all)
    .filter((a) => a.created_by.id === agentId || a.attachments.some((x) => x.agent_id === agentId))
    .sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
export const attachModeOf = (a: ArtifactMeta, agentId: string): AttachMode => a.attachments.find((x) => x.agent_id === agentId)?.mode ?? "none";

/** Order the "+" menu: kinds suggested for the role first (sec. 4); all kinds always present. */
export function orderedKinds(role?: string): { kind: ArtifactKind; suggested: boolean }[] {
  const sug = (role && ROLE_SUGGESTED[role]) || [];
  return [...sug, ...ARTIFACT_KINDS.filter((k) => !sug.includes(k))].map((kind) => ({ kind, suggested: sug.includes(kind) }));
}

/** React helper: subscribe to one artifact and make sure its content is loaded. */
export function useArtifact(id: string | null | undefined): Stored | undefined {
  const art = useArtifacts((s) => (id ? s.artifacts[id] : undefined));
  const ensure = useArtifacts((s) => s.ensureContent);
  useEffect(() => { if (id && art && art.content === undefined) ensure(id); }, [id, art, ensure]);
  return art;
}
