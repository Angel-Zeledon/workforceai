import type { FieldChange, RestorePreview, RestoreResult, RestoreStep, VersionDetail, VersionList, VersionSummary } from "../agentHistoryApi";

/**
 * Agent configuration history in mock mode (NEXT_PUBLIC_MOCK=true), F4. Valeria (sales) has a believable timeline:
 * the baseline, Carla's change of the instructions, an automatic demotion after an incident, a skill added and a cap.
 * Restoring follows the backend rules on the fields the mock knows: the autonomy never goes up, a skill that is gone
 * is not assigned again, and the restore is a NEW version.
 */

type Cfg = Record<string, unknown>;
interface Row extends VersionSummary { config: Cfg }

const ago = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString();
const rank: Record<string, number> = { suggest: 1, approve_each: 2, rules: 3, autonomous: 4 };
const RESTORABLE = ["instructions", "creativity", "tone", "skills", "budget_usd"];
const SHOWN_ONLY: Record<string, string> = { paused: "control", read_only: "control", rehearsal: "control", department: "structural", name: "structural", title: "structural" };

function baseCfg(name: string, autonomy: string): Cfg {
  return { name, instructions: "", creativity: "medium", tags: [], tone: "", autonomy, permissions: null, skills: [], budget_usd: 0, paused: false, read_only: false, rehearsal: false };
}

function diff(a: Cfg, b: Cfg): FieldChange[] {
  const out: FieldChange[] = [];
  for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
    if (JSON.stringify(a[k]) !== JSON.stringify(b[k])) out.push({ field: k, from: a[k], to: b[k] });
  }
  return out;
}

export class MockAgentHistory {
  private rows = new Map<string, Row[]>();
  private gone = new Set<string>(["sk-old"]); // skills deleted since

  reset() { this.rows.clear(); }

  matches(seg: string[]) { return seg[0] === "agents" && seg[2] === "versions"; }

  private seed(id: string, name: string, autonomy: string): Row[] {
    const have = this.rows.get(id);
    if (have) return have;
    const b = baseCfg(name, id === "sales" ? "rules" : autonomy);
    const rows: Row[] = [{ version: 1, created_at: ago(24 * 9), actor: "system", actor_kind: "system", source: "baseline", fields: [], config: b }];
    if (id === "sales") {
      const v2 = { ...b, instructions: "Responde siempre con una propuesta y un precio cerrado.", skills: [{ id: "sk-old", name: "Cotizar con la lista 2025" }] };
      const v3 = { ...v2, instructions: "Responde en menos de 5 líneas y pregunta antes de dar descuentos." };
      const v4 = { ...v3, autonomy: "approve_each" };
      const v5 = { ...v4, skills: [{ id: "sk-quote", name: "Cotizar" }], budget_usd: 40 };
      rows.push(
        { version: 2, created_at: ago(24 * 6), actor: "u-carla", actor_kind: "person", actor_name: "Carla", source: "PUT /agents/{id}/profile", fields: ["instructions", "skills"], config: v2 },
        { version: 3, created_at: ago(26), actor: "u-carla", actor_kind: "person", actor_name: "Carla", source: "PUT /agents/{id}/profile", fields: ["instructions"], config: v3 },
        { version: 4, created_at: ago(20), actor: "system", actor_kind: "system", source: "trust.auto_demotion", fields: ["autonomy"], config: v4 },
        { version: 5, created_at: ago(3), actor: "u-ana", actor_kind: "person", actor_name: "Ana", source: "PUT /agents/{id}/skills/{skillId}", fields: ["skills", "budget_usd"], config: v5 },
      );
    }
    this.rows.set(id, rows);
    return rows;
  }

  private plan(old: Cfg, cur: Cfg): { apply: RestoreStep[]; skip: RestoreStep[]; next: Cfg } {
    const apply: RestoreStep[] = [], skip: RestoreStep[] = [];
    const next: Cfg = { ...cur };
    for (const c of diff(old, cur)) {
      if (c.field === "autonomy") {
        if ((rank[String(old.autonomy)] ?? 0) < (rank[String(cur.autonomy)] ?? 0)) { apply.push({ field: "autonomy", op: "lower_autonomy", autonomy: String(old.autonomy) }); next.autonomy = old.autonomy; }
        else skip.push({ field: "autonomy", reason: "autonomy_earned" });
      } else if (c.field === "skills") {
        const keep = (old.skills as { id: string; name: string }[]).filter((s) => {
          if (this.gone.has(s.id)) { skip.push({ field: "skills", skill: s, reason: "skill_unavailable" }); return false; }
          return true;
        });
        next.skills = keep;
        if (JSON.stringify(keep) !== JSON.stringify(cur.skills)) apply.push({ field: "skills", op: "assign_skill" });
      } else if (RESTORABLE.includes(c.field)) {
        apply.push({ field: c.field, op: c.field === "budget_usd" ? "set_budget" : "set_profile" });
        next[c.field] = old[c.field];
      } else if (SHOWN_ONLY[c.field]) {
        skip.push({ field: c.field, reason: SHOWN_ONLY[c.field] });
      }
    }
    return { apply, skip, next };
  }

  handle(method: string, seg: string[], agents: { id: string; name: string; autonomy: string }[], setAutonomy: (id: string, to: string) => void): unknown {
    const id = seg[1];
    const ag = agents.find((a) => a.id === id);
    if (!ag) throw new Error(`GET /agents/${id}/versions -> 404`);
    const rows = this.seed(id, ag.name, ag.autonomy);
    const cur = rows[rows.length - 1];
    if (seg.length === 3 && method === "GET") {
      const list: VersionList = { agent_id: id, current: cur.version, keep: 100, can_restore: true,
        versions: [...rows].reverse().map(({ config: _c, ...v }) => v) };
      return list;
    }
    const n = Number(seg[3]);
    const row = rows.find((r) => r.version === n);
    if (!row) throw new Error(`GET /agents/${id}/versions/${seg[3]} -> 404`);
    if (seg.length === 4 && method === "GET") {
      const prev = rows.find((r) => r.version === n - 1);
      const d: VersionDetail = { version: row, previous: prev?.version, changes: prev ? diff(prev.config, row.config) : [] };
      return d;
    }
    if (seg[4] === "restore") {
      const p = this.plan(row.config, cur.config);
      if (method === "GET") { const out: RestorePreview = { version: n, apply: p.apply, skip: p.skip, can_restore: true }; return out; }
      const fields = diff(cur.config, p.next).map((c) => c.field);
      const res: RestoreResult = { from: n, applied: p.apply, skipped: p.skip, unchanged: fields.length === 0 };
      if (fields.length) {
        const v: Row = { version: cur.version + 1, created_at: new Date().toISOString(), actor: "u-me", actor_kind: "person", actor_name: "Tú",
          source: "restore", fields, restored_from: n, config: p.next };
        rows.push(v);
        if (p.next.autonomy !== cur.config.autonomy) setAutonomy(id, String(p.next.autonomy));
        const { config: _c, ...summary } = v;
        res.version = summary;
      }
      return res;
    }
    throw new Error(`${method} /agents/${id}/versions -> 404`);
  }
}
