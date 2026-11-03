import type { Agent } from "../types";
import type { AgentPermissions, Exception, FloorLift, PermissionsOverview } from "../permissionsApi";
import { CATEGORIES, FLOOR, type Mode, type PlainLevel } from "../permissionsLogic";

/** Simulation of /permissions and /agents/{id}/permissions (mirrors backend/internal/application/permissions.go). The demo
 *  user is the owner: every change is allowed. Ceilings mirror roles.AutonomyCeiling (see trust-mock.ts). */
const CEILING: Record<string, string> = {
  assistant: "autonomous", analyst: "autonomous", data_analyst: "autonomous", education: "autonomous", internal_auditor: "autonomous", project_manager: "autonomous",
};
const TO_AUTONOMY: Record<string, Agent["autonomy"]> = { ask_all: "approve_each", important: "rules", alone: "autonomous", suggest: "suggest" };
const TO_PLAIN: Record<string, PlainLevel> = { approve_each: "ask_all", rules: "important", autonomous: "alone", suggest: "suggest" };
const RANK: Record<string, number> = { suggest: 1, approve_each: 2, rules: 3, autonomous: 4 };

interface Entry { exceptions: Exception[]; floor_lifts: FloorLift[]; updated_at: string }

export class MockPermissions {
  private entries = new Map<string, Entry>();
  private def: PlainLevel | "" = "";

  matches(seg: string[]): boolean {
    return seg[0] === "permissions" || (seg[0] === "agents" && !!seg[1] && seg[2] === "permissions");
  }

  private ceiling(a: Agent) { return CEILING[a.role] ?? "rules"; }

  private view(a: Agent): AgentPermissions {
    const e = this.entries.get(a.id);
    const factory = !!e, ceiling = this.ceiling(a);
    return {
      agent_id: a.id, name: a.name, autonomy: a.autonomy, level: TO_PLAIN[a.autonomy] ?? "", factory, ceiling,
      options: (["ask_all", "important", "alone", "suggest"] as PlainLevel[]).map((l) => {
        const au = TO_AUTONOMY[l];
        return RANK[au] > RANK[ceiling] ? { level: l, autonomy: au, allowed: false, reason: "role_ceiling" } : { level: l, autonomy: au, allowed: true };
      }),
      categories: CATEGORIES.map((c) => {
        const ex = e?.exceptions.find((x) => x.category === c);
        const lifted = e?.floor_lifts.find((x) => x.category === c);
        let eff: Mode = "ask";
        if (factory && FLOOR.includes(c) && !lifted) eff = "ask";
        else if (a.autonomy === "approve_each" || a.autonomy === "suggest") eff = "ask";
        else if (ex && factory) eff = ex.mode;
        else if (a.autonomy === "autonomous") eff = "never";
        else eff = factory || c === "contracts" ? "ask" : "never";
        return { category: c, floor: FLOOR.includes(c), effective: eff, ...(ex ? { exception: ex } : {}), ...(lifted ? { lifted } : {}) };
      }),
      can_edit: true, can_lift_floor: true, ...(e ? { updated_by: "demo", updated_at: e.updated_at } : {}),
    };
  }

  handle(method: string, seg: string[], body: any, agents: Agent[], setAutonomy: (id: string, a: Agent["autonomy"]) => void): unknown {
    const now = new Date().toISOString();
    if (seg[0] === "permissions" && seg[1] === "scopes") return method === "GET" ? [] : {};
    if (seg[0] === "permissions") {
      if (method === "PUT" && seg[1] === "default") this.def = body?.level ?? "";
      const out: PermissionsOverview = {
        default_level: this.def, categories: [...CATEGORIES], floor: [...FLOOR], can_edit_default: true,
        agents: agents.map((a) => ({ agent_id: a.id, name: a.name, level: TO_PLAIN[a.autonomy] ?? "", factory: this.entries.has(a.id),
          exceptions: this.entries.get(a.id)?.exceptions ?? [], floor_lifts: this.entries.get(a.id)?.floor_lifts ?? [] })),
      };
      return out;
    }
    const a = agents.find((x) => x.id === seg[1]);
    if (!a) throw new Error(`${method} /agents/${seg[1]}/permissions -> 404`);
    const sub = seg[3], cat = seg[4];
    const entry = () => this.entries.get(a.id);
    if (method === "PUT" && sub === "level") {
      const au = TO_AUTONOMY[body?.level];
      if (!au) throw new Error("PUT level -> 400");
      if (RANK[au] > RANK[this.ceiling(a)]) throw new Error("PUT level -> 403");
      setAutonomy(a.id, au);
      if (!entry()) this.entries.set(a.id, { exceptions: [], floor_lifts: [], updated_at: now });
    } else if (sub === "exceptions" && cat) {
      const e = entry();
      if (!e) throw new Error("PUT exception -> 409");
      e.exceptions = e.exceptions.filter((x) => x.category !== cat);
      if (method === "PUT") e.exceptions.push({ category: cat, mode: body?.mode, ...(body?.mode === "ask_if" ? { if: body?.if } : {}), created_by: "demo", created_at: now, source: body?.source ?? "panel" });
      e.updated_at = now;
    } else if (sub === "floor" && cat) {
      const e = entry();
      if (!e) throw new Error("POST floor -> 409");
      e.floor_lifts = e.floor_lifts.filter((x) => x.category !== cat);
      if (method === "POST") e.floor_lifts.push({ category: cat, by: "demo", at: now });
      e.updated_at = now;
    }
    return this.view(agents.find((x) => x.id === a.id) ?? a);
  }
}
