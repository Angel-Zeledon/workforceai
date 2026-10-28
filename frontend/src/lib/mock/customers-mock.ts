/**
 * Mock of the customer district (B12, NEXT_PUBLIC_MOCK=true): four fictional Spanish-speaking small businesses of an
 * accounting firm, a few per-customer clones and seeded projects so every building status shows up. Same shapes as
 * backend/internal/api/customers.go. Mutations live in memory until the page reloads.
 */
import type { Agent, Request } from "../types";
import type { Assignment, Customer, CustomerDetail, CustomerInput, CustomerProject, CustomerRequest, District } from "../customersApi";
import { defaultCloneName, normalizeStatus, worstStatus, type CustomerStatus } from "../customersLogic";

const iso = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();

const SEED: Customer[] = [
  { id: "cus_lopez", name: "Ferretería López", tax_id: "LOFE800101AB1", color: "#b7791f", notes: "Cierre mensual el día 5. Contacto: Don Ramón.", folder_path: "/Clients/Ferretería López", archived: false, created_at: iso(60 * 24 * 40), updated_at: iso(60 * 24 * 2) },
  { id: "cus_sanjose", name: "Panadería San José", tax_id: "", color: "#c05621", notes: "Dos sucursales; ventas en efectivo.", folder_path: "/Clients/Panadería San José", archived: false, created_at: iso(60 * 24 * 30), updated_at: iso(60 * 24 * 3) },
  { id: "cus_sonrisa", name: "Clínica Dental Sonrisa", tax_id: "CDS120304XY9", color: "#2c7a7b", notes: "", folder_path: "/Clients/Clínica Dental Sonrisa", archived: false, created_at: iso(60 * 24 * 20), updated_at: iso(60 * 24) },
  { id: "cus_ruiz", name: "Transportes Ruiz", tax_id: "", color: "#4c51bf", notes: "Flota de 6 camiones.", folder_path: "/Clients/Transportes Ruiz", archived: false, created_at: iso(60 * 24 * 10), updated_at: iso(60 * 24 * 10) },
];

/** Seeded demo projects (fictional): one per status so the district shows every light. */
const PROJECTS: Record<string, CustomerProject[]> = {
  cus_lopez: [{ id: "prj-lopez-close", name: "Cierre de septiembre", status: "running", light: "green", tasks_done: 3, tasks_total: 7, awaiting: 0, failed: 0 }],
  cus_sanjose: [{ id: "prj-sanjose-close", name: "Cierre de septiembre", status: "waiting_human", light: "amber", tasks_done: 5, tasks_total: 7, awaiting: 1, failed: 0 }],
  cus_sonrisa: [{ id: "prj-sonrisa-close", name: "Conciliación bancaria", status: "running", light: "red", tasks_done: 2, tasks_total: 6, awaiting: 0, failed: 1 }],
  cus_ruiz: [],
};

const ASSIGN: Assignment[] = [
  { id: "asg_lopez_acc", customer_id: "cus_lopez", agent_id: "accounting", display_name: "Contador · Ferretería López", created_at: iso(60 * 24 * 39) },
  { id: "asg_lopez_an", customer_id: "cus_lopez", agent_id: "analyst", display_name: "Analista · Ferretería López", created_at: iso(60 * 24 * 39) },
  { id: "asg_sanjose_acc", customer_id: "cus_sanjose", agent_id: "accounting", display_name: "Contador · Panadería San José", created_at: iso(60 * 24 * 29) },
  { id: "asg_sonrisa_acc", customer_id: "cus_sonrisa", agent_id: "accounting", display_name: "Contador · Clínica Dental Sonrisa", created_at: iso(60 * 24 * 19) },
  { id: "asg_sonrisa_legal", customer_id: "cus_sonrisa", agent_id: "legal", display_name: "Abogada · Clínica Dental Sonrisa", created_at: iso(60 * 24 * 19) },
];

const SEED_REQUESTS: Record<string, CustomerRequest[]> = {
  cus_lopez: [{ id: "req-lopez-1", text: "Concilia el banco de septiembre de la ferretería", status: "running", created_at: iso(90) }],
  cus_sanjose: [{ id: "req-sanjose-1", text: "Prepara la declaración mensual de la panadería", status: "awaiting_approval", created_at: iso(240) }],
  cus_sonrisa: [{ id: "req-sonrisa-1", text: "Revisa las facturas de proveedores de agosto", status: "failed", created_at: iso(60 * 20) }],
  cus_ruiz: [{ id: "req-ruiz-1", text: "Resumen de gastos de combustible del trimestre", status: "done", created_at: iso(60 * 24 * 6) }],
};

const PROJECT_STATUS: Record<string, CustomerStatus> = { waiting_human: "waiting_approval", failed: "blocked", paused: "blocked", running: "on_track" };
const REQUEST_STATUS: Record<string, CustomerStatus> = { planning: "on_track", running: "on_track", awaiting_approval: "waiting_approval", awaiting_confirmation: "waiting_approval", failed: "blocked", paused: "blocked" };

function projectStatus(p: CustomerProject): CustomerStatus {
  if (p.failed > 0) return "blocked";
  if (p.awaiting > 0) return "waiting_approval";
  return PROJECT_STATUS[p.status] ?? "idle";
}

const notFound = () => { throw new Error("customer not found"); };

export class MockCustomers {
  private customers: Customer[] = SEED.map((c) => ({ ...c }));
  private assignments: Assignment[] = ASSIGN.map((a) => ({ ...a }));
  private requestLinks: Record<string, string> = {}; // request id -> customer id (requests sent from the district)
  private seq = 0;
  constructor(private agents: () => Agent[], private requests: () => Request[]) {}

  matches(seg: string[]): boolean { return seg[0] === "customers"; }

  /** POST /requests with customer_id: the engine starts the request and the district remembers its customer. */
  linkRequest(requestId: string, customerId: string) {
    if (!this.customers.some((c) => c.id === customerId && !c.archived)) notFound();
    this.requestLinks[requestId] = customerId;
  }
  hasCustomer(customerId: string): boolean { return this.customers.some((c) => c.id === customerId && !c.archived); }

  private liveRequests(cid: string): CustomerRequest[] {
    const live = this.requests().filter((r) => this.requestLinks[r.id] === cid).map((r) => ({ id: r.id, text: r.text, status: r.status, created_at: r.created_at }));
    return [...live, ...(SEED_REQUESTS[cid] ?? [])].sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)).slice(0, 20);
  }

  private statusOf(cid: string): CustomerStatus {
    const st: (string | undefined)[] = (PROJECTS[cid] ?? []).map(projectStatus);
    for (const r of this.liveRequests(cid)) st.push(REQUEST_STATUS[r.status]);
    return worstStatus(st);
  }

  private get(cid: string): Customer { return this.customers.find((c) => c.id === cid) ?? notFound(); }

  private detail(cid: string): CustomerDetail {
    const c = this.get(cid);
    return {
      customer: c, status: normalizeStatus(this.statusOf(cid)), assignments: this.assignments.filter((a) => a.customer_id === cid),
      projects: PROJECTS[cid] ?? [], requests: this.liveRequests(cid), folder_path: c.folder_path,
    };
  }

  private district(): District {
    return {
      generated_at: new Date().toISOString(),
      customers: this.customers.filter((c) => !c.archived).map((c) => {
        const ps = PROJECTS[c.id] ?? [];
        return {
          customer: c, status: this.statusOf(c.id),
          projects_active: ps.filter((p) => !["done", "cancelled", "draft"].includes(p.status)).length,
          approvals_waiting: ps.reduce((n, p) => n + p.awaiting, 0) + this.liveRequests(c.id).filter((r) => r.status === "awaiting_approval").length,
          failed: ps.reduce((n, p) => n + p.failed, 0),
          assignments: this.assignments.filter((a) => a.customer_id === c.id).length,
        };
      }),
    };
  }

  private clean(body: CustomerInput | undefined, prev?: Customer): Customer {
    const name = String(body?.name ?? prev?.name ?? "").trim();
    if (!name) throw new Error("name is required");
    if (this.customers.some((c) => c.id !== prev?.id && !c.archived && c.name.toLowerCase() === name.toLowerCase())) throw new Error("a customer with that name already exists");
    const color = String(body?.color ?? prev?.color ?? "");
    const now = new Date().toISOString();
    const id = prev?.id ?? `cus_${Date.now().toString(36)}${++this.seq}`;
    return {
      id, name, tax_id: String(body?.tax_id ?? prev?.tax_id ?? "").trim(), notes: String(body?.notes ?? prev?.notes ?? "").trim(),
      color: /^#[0-9a-fA-F]{6}$/.test(color) ? color : "#4a5568", folder_path: prev?.folder_path ?? `/Clients/${name}`, archived: prev?.archived ?? false,
      created_at: prev?.created_at ?? now, updated_at: now,
    };
  }

  /** Earlier work without a customer (B12 history): finished requests the district has not linked yet. */
  private history(method: string, sub: string | undefined, body: any): unknown {
    const linked = new Set([...Object.keys(this.requestLinks), ...Object.values(SEED_REQUESTS).flat().map((r) => r.id)]);
    if (method === "GET" && sub === "candidates") {
      const requests = this.requests().filter((r) => !linked.has(r.id) && (r.status === "done" || r.status === "failed")).slice(0, 30)
        .map((r) => ({ id: r.id, text: r.text, status: r.status, created_at: r.created_at }));
      return { projects: [], requests };
    }
    if (method === "POST" && !sub) {
      const kind = String(body?.kind ?? ""), ref = String(body?.ref_id ?? ""), to = String(body?.customer_id ?? "");
      if (kind !== "request") throw new Error("mock: only requests can be assigned in the demo");
      const from = this.requestLinks[ref] ?? "";
      if (to && !this.hasCustomer(to)) notFound();
      if (from && to && from !== to) throw new Error("the request already belongs to another customer: remove that customer first");
      if (to) this.requestLinks[ref] = to; else delete this.requestLinks[ref];
      return { kind, ref_id: ref, from, to, memories: 0, changed: from !== to };
    }
    throw new Error(`mock: unsupported ${method} /customers/history`);
  }

  handle(method: string, seg: string[], q: URLSearchParams, body: any): unknown {
    const [, cid, sub, subId] = seg;
    if (cid === "history") return this.history(method, sub, body);
    if (method === "GET") {
      if (!cid) return this.customers.filter((c) => q.get("include_archived") === "1" || !c.archived);
      if (cid === "district") return this.district();
      return this.detail(cid);
    }
    if (method === "POST" && !cid) { const c = this.clean(body); this.customers.push(c); return c; }
    if (method === "PUT" && cid && !sub) { const prev = this.get(cid); const c = this.clean(body, prev); Object.assign(prev, c); return prev; }
    if (method === "DELETE" && cid && !sub) { const c = this.get(cid); c.archived = true; c.updated_at = new Date().toISOString(); return null; }
    if (sub === "assignments") {
      const c = this.get(cid);
      if (method === "POST") {
        const agent = this.agents().find((a) => a.id === String(body?.agent_id ?? ""));
        if (!agent) throw new Error("agent not found");
        if (this.assignments.some((a) => a.customer_id === cid && a.agent_id === agent.id)) throw new Error("that agent already has a clone for this customer");
        const a: Assignment = { id: `asg_${Date.now().toString(36)}${++this.seq}`, customer_id: cid, agent_id: agent.id,
          display_name: String(body?.display_name ?? "").trim() || defaultCloneName(agent.title, c.name), created_at: new Date().toISOString() };
        this.assignments.push(a);
        return a;
      }
      if (method === "DELETE" && subId) { this.assignments = this.assignments.filter((a) => !(a.customer_id === cid && a.id === subId)); return null; }
    }
    if (sub === "folders") { this.get(cid); return method === "POST" ? { customer_id: cid, kind: "drive_folder", ref_id: String(body?.folder_id ?? "") } : null; }
    throw new Error(`mock: unsupported ${method} /${seg.join("/")}`);
  }
}
