import { call } from "./api";
import type { CustomerStatus } from "./customersLogic";

/** Customer district (B12). Contract: backend/internal/api/customers.go and docs/architecture/customer-district.md (/api/v1). */

export interface Customer {
  id: string; name: string; tax_id: string; color: string; notes: string;
  /** The customer's drive folder (/Clients/<name>), "" when the drive is off. */
  folder_path: string;
  archived: boolean; created_at: string; updated_at: string;
}
export interface CustomerInput { name: string; tax_id?: string; color?: string; notes?: string }

/** A per-customer clone of an agent: same role and tools, memory and files isolated to this customer. */
export interface Assignment { id: string; customer_id: string; agent_id: string; display_name: string; created_at: string }

export interface DistrictItem {
  customer: Customer; status: CustomerStatus;
  projects_active: number; approvals_waiting: number; failed: number; assignments: number;
}
export interface District { generated_at: string; customers: DistrictItem[] }

export interface CustomerProject { id: string; name: string; status: string; light: string; tasks_done: number; tasks_total: number; awaiting: number; failed: number }
export interface CustomerRequest { id: string; text: string; status: string; created_at: string }
export interface CustomerDetail {
  customer: Customer; status: CustomerStatus; assignments: Assignment[];
  projects: CustomerProject[]; requests: CustomerRequest[]; folder_path: string;
}

const id = (s: string) => encodeURIComponent(s);

export const customersApi = {
  list: (includeArchived = false) => call<Customer[]>("GET", `/customers${includeArchived ? "?include_archived=1" : ""}`),
  district: () => call<District>("GET", "/customers/district"),
  get: (cid: string) => call<CustomerDetail>("GET", `/customers/${id(cid)}`),
  create: (body: CustomerInput) => call<Customer>("POST", "/customers", body),
  update: (cid: string, body: CustomerInput) => call<Customer>("PUT", `/customers/${id(cid)}`, body),
  /** Archives (the history stays; no new work can be scoped to it). */
  archive: (cid: string) => call<unknown>("DELETE", `/customers/${id(cid)}`),
  addAssignment: (cid: string, agentId: string, displayName?: string) =>
    call<Assignment>("POST", `/customers/${id(cid)}/assignments`, { agent_id: agentId, ...(displayName ? { display_name: displayName } : {}) }),
  removeAssignment: (cid: string, assignmentId: string) => call<unknown>("DELETE", `/customers/${id(cid)}/assignments/${id(assignmentId)}`),
  linkFolder: (cid: string, folderId: string) => call<unknown>("POST", `/customers/${id(cid)}/folders`, { folder_id: folderId }),
  unlinkFolder: (cid: string, folderId: string) => call<unknown>("DELETE", `/customers/${id(cid)}/folders/${id(folderId)}`),
  /** A request scoped to the customer: its tasks only recall this customer's memory and files. */
  request: (cid: string, text: string) => call<{ request_id: string }>("POST", "/requests", { text, customer_id: cid }),
};
