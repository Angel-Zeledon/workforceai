import { create } from "zustand";
import { go } from "@/lib/navBridge";
import { MOCK } from "../config";
import { useStore } from "../store";
import { projectsApi } from "./api";
import type {
  ProjectNode, ProjectObjective,
  ControlAction, LaunchBody, NewProjectBody, PlanChange, PlanChangeOp, PlanOp, ProjectDetail, ProjectFrame, ProjectHealth, ProjectSummary, ProjectTemplate, SaveTemplateOpts,
} from "./types";

export const PROJECT_TABS = ["health", "map", "timeline", "kanban", "lanes", "delegation", "approvals", "changes", "costs"] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];
/** what the Projects screen shows when no project is open */
export type ProjectsScreen = "list" | "library" | "builder";

/** What the office needs of a project being planned: phase titles (objectives / workflow groups) and agents seen so far. */
export interface PlanningInfo { titles: string[]; agents: string[]; updated: number }

function mergePlanning(prev: PlanningInfo | undefined, nodes: ProjectNode[] | undefined, objectives: ProjectObjective[] | undefined): PlanningInfo {
  const titles = prev ? prev.titles.slice() : [];
  const agents = prev ? prev.agents.slice() : [];
  for (const o of objectives || []) if (o.title && !titles.includes(o.title)) titles.push(o.title);
  for (const n of nodes || []) {
    if (n.kind === "group" && n.title && !titles.includes(n.title)) titles.push(n.title);
    if (n.agent_id && !agents.includes(n.agent_id)) agents.push(n.agent_id);
  }
  return { titles: titles.slice(-24), agents: agents.slice(-12), updated: Date.now() };
}

interface PState {
  /** Office: projects whose planner is running (project id -> real phase titles / agents known so far). Additive. */
  planningPhases: Record<string, PlanningInfo>;
  /** Office: feeds planningPhases from a full project detail (polling, live mode). */
  notePlanningDetail: (d: ProjectDetail) => void;
  /** the Projects screen is shown in place of office/dashboard */
  open: boolean;
  loaded: boolean;
  projects: Record<string, ProjectSummary>;
  activeId: string | null;
  detail: ProjectDetail | null;
  health: ProjectHealth | null;
  tab: ProjectTab;
  selectedNodeId: string | null;
  templates: ProjectTemplate[];
  /** template library / builder (inside the Projects screen) */
  view: ProjectsScreen;
  /** template being edited in the builder (null = a new blank one) */
  editingId: string | null;
  /** new-project wizard (open over the list or the library) */
  wizard: { templateId: string } | null;
  setView: (v: ProjectsScreen) => void;
  openLibrary: () => void;
  openBuilder: (templateId: string | null) => void;
  openWizard: (templateId: string) => void;
  closeWizard: () => void;
  duplicateTemplate: (id: string) => Promise<ProjectTemplate>;
  deleteTemplate: (id: string) => Promise<void>;
  /** Q2: plan changes of the open project */
  changes: PlanChange[];
  error: string | null;
  setOpen: (o: boolean) => void;
  setTab: (t: ProjectTab) => void;
  selectNode: (id: string | null) => void;
  loadList: () => Promise<void>;
  loadTemplates: () => Promise<void>;
  openProject: (id: string) => Promise<void>;
  closeProject: () => void;
  apply: (f: ProjectFrame) => void;
  createDraft: (b: NewProjectBody) => Promise<string>;
  patchPlan: (ops: PlanOp[]) => Promise<void>;
  launch: (b: LaunchBody) => Promise<void>;
  control: (a: ControlAction) => Promise<void>;
  retryNode: (nodeId: string) => Promise<void>;
  skipNode: (nodeId: string, reason: string) => Promise<void>;
  decide: (approvalId: string, d: "approve" | "reject") => Promise<void>;
  decideBatch: (b: { action: string; decision: "approve" | "reject"; expected_count: number; include_high?: boolean }) => Promise<void>;
  setBudget: (usd: number) => Promise<void>;
  saveAsTemplate: (opts?: SaveTemplateOpts) => Promise<ProjectTemplate | null>;
  loadChanges: () => Promise<void>;
  proposeChange: (reason: string, ops: PlanChangeOp[]) => Promise<void>;
  decideChange: (changeId: string, d: "approve" | "reject") => Promise<void>;
  replanNode: (nodeId: string) => Promise<void>;
}

let healthTimer: ReturnType<typeof setTimeout> | null = null;
function scheduleHealth(get: () => PState, set: (p: Partial<PState>) => void) {
  if (healthTimer) return;
  healthTimer = setTimeout(async () => {
    healthTimer = null;
    const id = get().activeId;
    if (!id) return;
    try { const h = await projectsApi.health(id); if (get().activeId === id) set({ health: h }); } catch { /* backend without /health */ }
  }, 700);
}

export const useProjects = create<PState>((set, get) => ({
  planningPhases: {},
  notePlanningDetail: (d) => set((s) => {
    const id = d.project.id;
    if (d.project.status !== "planning" && !d.planning) { if (!s.planningPhases[id]) return {}; const { [id]: _g, ...rest } = s.planningPhases; return { planningPhases: rest }; }
    return { planningPhases: { ...s.planningPhases, [id]: mergePlanning(undefined, d.nodes, d.objectives) } };
  }),
  open: false, loaded: false, projects: {}, activeId: null, detail: null, health: null, tab: "health", selectedNodeId: null,
  templates: [], changes: [], error: null,
  view: "list", editingId: null, wizard: null,
  setView: (view) => set({ view }),
  openLibrary: () => set({ view: "library", activeId: null, detail: null }),
  openBuilder: (editingId) => set({ view: "builder", editingId, activeId: null, detail: null }),
  openWizard: (templateId) => set({ wizard: { templateId } }),
  closeWizard: () => set({ wizard: null }),
  duplicateTemplate: async (id) => { const t = await projectsApi.duplicateTemplate(id); await get().loadTemplates(); return t; },
  deleteTemplate: async (id) => { await projectsApi.deleteTemplate(id); await get().loadTemplates(); },

  setOpen: (open) => { set({ open }); if (open) go("/projects"); },
  setTab: (tab) => set({ tab }),
  selectNode: (selectedNodeId) => set({ selectedNodeId }),

  loadList: async () => {
    try {
      const list = await projectsApi.list();
      set({ loaded: true, error: null, projects: Object.fromEntries(list.map((p) => [p.id, p])) });
    } catch { set({ loaded: true, error: "unavailable" }); }
  },
  loadTemplates: async () => { try { set({ templates: await projectsApi.templates() }); } catch { /* ignore */ } },

  openProject: async (id) => {
    set({ activeId: id, detail: null, health: null, changes: [], tab: "health", selectedNodeId: null, error: null, view: "list" });
    try {
      const detail = await projectsApi.get(id);
      if (get().activeId !== id) return;
      set({ detail });
      projectsApi.health(id).then((h) => { if (get().activeId === id) set({ health: h }); }).catch(() => {});
      get().loadChanges();
    } catch { set({ error: "unavailable" }); }
  },
  closeProject: () => set({ activeId: null, detail: null, health: null, changes: [], selectedNodeId: null }),

  apply: (f) => {
    const { project } = f.payload;
    const s = get();
    const next: Partial<PState> = { projects: { ...s.projects, [project.id]: project } };
    if (project.status === "planning" || f.payload.planning) next.planningPhases = { ...s.planningPhases, [project.id]: mergePlanning(s.planningPhases[project.id], f.payload.nodes, f.payload.objectives) };
    else if (s.planningPhases[project.id]) { const { [project.id]: _g, ...rest } = s.planningPhases; next.planningPhases = rest; }
    if (s.detail && s.activeId === project.id) {
      const d = s.detail;
      let nodes = d.nodes;
      if (f.payload.nodes?.length) {
        const idx = new Map(nodes.map((n, i) => [n.id, i]));
        nodes = nodes.slice();
        for (const n of f.payload.nodes) { const i = idx.get(n.id); if (i === undefined) { idx.set(n.id, nodes.length); nodes.push(n); } else nodes[i] = n; }
      }
      let objectives = d.objectives;
      if (f.payload.objectives?.length) {
        const have = new Set(objectives.map((o) => o.id));
        objectives = [...objectives, ...f.payload.objectives.filter((o) => !have.has(o.id))];
      }
      next.detail = {
        ...d, project, nodes, objectives,
        approvals: f.payload.approvals ?? d.approvals,
        planning: f.payload.planning !== undefined ? f.payload.planning : d.planning,
        estimate: f.payload.estimate !== undefined ? f.payload.estimate : d.estimate,
        structure_version: f.payload.structure_version ?? d.structure_version,
      };
      scheduleHealth(get, (p) => set(p));
    }
    set(next);
  },

  createDraft: async (b) => {
    const { project_id } = await projectsApi.createDraft(b);
    await get().loadList();
    await get().openProject(project_id);
    return project_id;
  },
  patchPlan: async (ops) => {
    const id = get().activeId; if (!id) return;
    await projectsApi.patchPlan(id, ops);
    if (!MOCK) set({ detail: await projectsApi.get(id) });
  },
  launch: async (b) => { const id = get().activeId; if (!id) return; await projectsApi.launch(id, b); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  control: async (a) => { const id = get().activeId; if (!id) return; await projectsApi.control(id, a); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  retryNode: async (nodeId) => { const id = get().activeId; if (!id) return; await projectsApi.retryNode(id, nodeId); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  skipNode: async (nodeId, reason) => { const id = get().activeId; if (!id) return; await projectsApi.skipNode(id, nodeId, reason); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  decide: async (approvalId, d) => { const id = get().activeId; if (!id) return; await projectsApi.decide(id, approvalId, d); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  decideBatch: async (b) => { const id = get().activeId; if (!id) return; await projectsApi.decideBatch(id, b); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  setBudget: async (usd) => { const id = get().activeId; if (!id) return; await projectsApi.setBudget(id, usd); if (!MOCK) set({ detail: await projectsApi.get(id) }); },
  loadChanges: async () => {
    const id = get().activeId; if (!id) return;
    try { const changes = await projectsApi.listChanges(id); if (get().activeId === id) set({ changes }); } catch { /* backend without plan changes */ }
  },
  proposeChange: async (reason, ops) => { const id = get().activeId; if (!id) return; await projectsApi.proposeChange(id, reason, ops); if (!MOCK) { set({ detail: await projectsApi.get(id) }); await get().loadChanges(); } },
  decideChange: async (changeId, d) => { const id = get().activeId; if (!id) return; await projectsApi.decideChange(id, changeId, d); if (!MOCK) { set({ detail: await projectsApi.get(id) }); await get().loadChanges(); } },
  replanNode: async (nodeId) => { const id = get().activeId; if (!id) return; await projectsApi.replanNode(id, nodeId); if (!MOCK) await get().loadChanges(); },
  saveAsTemplate: async (opts) => { const id = get().activeId; if (!id) return null; const t = await projectsApi.saveAsTemplate(id, opts); await get().loadTemplates(); return t; },
}));


let users = 0;
let stopFn: (() => void) | null = null;
/** Reference-counted realtime: mock frames in mock mode, light polling otherwise (WS subscribe/watch is not available yet). */
export function startProjectsRealtime() {
  users++;
  if (users === 1) {
    const { apply, loadList } = useProjects.getState();
    loadList();
    if (MOCK) {
      let off = () => {}; let cancelled = false;
      import("./mock").then(({ projectsMock }) => { if (!cancelled) off = projectsMock.connect(apply); });
      stopFn = () => { cancelled = true; off(); };
    } else {
      let tick = 0;
      const h = setInterval(() => {
        const s = useProjects.getState();
        // Backend without /projects yet: back off to one retry every ~30 s instead of a 404 every 3 s.
        if (s.error === "unavailable" && tick++ % 10 !== 0) return;
        s.loadList();
        // Office: the phases of a plan being drafted (at most 2 projects) so the meeting room can show them
        Object.values(s.projects).filter((p) => p.status === "planning").slice(0, 2).forEach((p) => {
          projectsApi.get(p.id).then((d) => useProjects.getState().notePlanningDetail(d)).catch(() => {});
        });
        if (s.activeId) s.loadChanges();
        if (s.activeId) projectsApi.get(s.activeId).then((d) => { if (useProjects.getState().activeId === d.project.id) useProjects.setState({ detail: d }); }).catch(() => {});
      }, 3000);
      stopFn = () => clearInterval(h);
    }
  }
  return () => {
    users = Math.max(0, users - 1);
    if (users === 0 && stopFn) { stopFn(); stopFn = null; }
  };
}
