/**
 * Demo of the shares between departments (D4): an in-memory list with the rules of the backend (same errors, same
 * statuses). The person of the demo is a lead of Contabilidad, so what Contabilidad shares is born active; the demo
 * also has a request from a colleague waiting for the person, and a folder Legal shares with Contabilidad.
 */
import type { DriveShare, DriveSharesApi } from "../drive";

const ME = "demo";
const OWNER_OF = (path: string) => /^\/offices\/([^/]+)\/./i.exec(path)?.[1].toLowerCase() ?? "";
const nameOf = (p: string) => p.replace(/\/+$/, "").split("/").pop() || p;
const kindOf = (p: string): "file" | "folder" => (/\.[a-z0-9]{1,5}$/i.test(nameOf(p)) ? "file" : "folder");

/** `fail(status, code)` builds the drive's error (the caller owns the class). `leads`: the offices the demo person leads. */
export function mockShares(fail: (status: number, code: string) => Error, leads: string[] = ["contabilidad"]): DriveSharesApi {
  const at = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();
  let seq = 0;
  const base = (over: Partial<DriveShare>): DriveShare => ({
    id: `shr_mock_${++seq}`, path: "", kind: "file", name: "", owner_office_id: "", target_office_id: "", status: "pending",
    requested_by: ME, requested_at: at(0), can_decide: false, can_revoke: false, ...over,
  });
  const list: DriveShare[] = [
    base({ path: "/Offices/Legal/Contratos", kind: "folder", name: "Contratos", owner_office_id: "legal", target_office_id: "contabilidad", status: "active",
      requested_by: "u_legal", requested_at: at(72), decided_by: "u_legal", decided_at: at(71) }),
    base({ path: "/Offices/Contabilidad/Bancos y conciliaciones/conciliacion-septiembre.md", kind: "file", name: "conciliacion-septiembre.md",
      owner_office_id: "contabilidad", target_office_id: "legal", status: "pending", requested_by: "u_laura", requested_at: at(2) }),
  ];
  // what the person may do with each one: decided on every read, as the backend does
  const view = (s: DriveShare): DriveShare => {
    const lead = leads.includes(s.owner_office_id);
    return { ...s, can_decide: s.status === "pending" && lead, can_revoke: (s.status === "pending" || s.status === "active") && (lead || s.requested_by === ME) };
  };
  const find = (id: string) => { const s = list.find((x) => x.id === id); if (!s) throw fail(404, "not_found"); return s; };
  return {
    create: async (path, target) => {
      const owner = OWNER_OF(path), p = path.replace(/\/+$/, "");
      if (!owner) throw fail(400, "not_in_office");
      if (target === "general") throw fail(400, "general_office");
      if (target === owner) throw fail(400, "same_office");
      if (list.some((s) => s.path.toLowerCase() === p.toLowerCase() && s.target_office_id === target && (s.status === "pending" || s.status === "active"))) throw fail(409, "already_shared");
      const born = leads.includes(owner);
      const s = base({ path: p, kind: kindOf(p), name: nameOf(p), owner_office_id: owner, target_office_id: target, status: born ? "active" : "pending",
        ...(born ? { decided_by: ME, decided_at: at(0) } : {}) });
      list.push(s);
      return view(s);
    },
    out: async (office) => list.filter((s) => s.owner_office_id === office).map(view),
    inbox: async (office) => list.filter((s) => s.target_office_id === office && s.status === "active").map(view),
    forPath: async (path) => list.filter((s) => s.path.toLowerCase() === path.replace(/\/+$/, "").toLowerCase() && (s.status === "pending" || s.status === "active")).map(view),
    decide: async (id, decision) => {
      const s = find(id);
      if (!leads.includes(s.owner_office_id)) throw fail(403, "not_decider");
      if (s.status !== "pending") throw fail(409, "not_pending");
      Object.assign(s, { status: decision === "approve" ? "active" : "rejected", decided_by: ME, decided_at: at(0) });
      return view(s);
    },
    revoke: async (id) => { const s = find(id); if (s.status === "pending" || s.status === "active") { s.status = "revoked"; s.decided_by = ME; s.decided_at = at(0); } },
  };
}
