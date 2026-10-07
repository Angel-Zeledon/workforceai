import type { ProjectTemplate } from "./types";

/**
 * Built-in project templates (spec 2.8): keyed definitions, every user-visible string is an i18n key.
 * `deps` reference node keys across the whole template (a workflow may depend on another one's nodes).
 */
export const BUILTIN_TEMPLATES: ProjectTemplate[] = [
  {
    id: "tpl-financial-close", key: "financial_close", version: 1, builtin: true,
    name: "", name_key: "proj.fc.name", description: "", description_key: "proj.fc.desc", params: [],
    objectives: [
      { key: "o1", title_key: "proj.fc.o1", workflows: [
        { key: "w1", title_key: "proj.fc.w1", nodes: [
          { key: "n1", title_key: "proj.fc.n1", agent: "accounting", complexity: "M" },
          { key: "n2", title_key: "proj.fc.n2", agent: "accounting", complexity: "M", deps: ["n1"] },
        ] },
      ] },
      { key: "o2", title_key: "proj.fc.o2", workflows: [
        // Balance sheet and income statement run in parallel; the equity step needs the net income (cross-workflow dependency).
        { key: "w2", title_key: "proj.fc.w2", nodes: [
          { key: "a1", title_key: "proj.fc.a1", agent: "accounting", complexity: "M", deps: ["n1"] },
          { key: "a2", title_key: "proj.fc.a2", agent: "accounting", complexity: "M", deps: ["a1", "b3"] },
          { key: "a3", title_key: "proj.fc.a3", agent: "accounting", complexity: "L", deps: ["a2", "n2"],
            delegate: { agent: "operations", title_key: "proj.fc.a3s", complexity: "S" },
            approval: { action: "publish_statement", risk: "medium" } },
        ] },
        { key: "w3", title_key: "proj.fc.w3", nodes: [
          { key: "b1", title_key: "proj.fc.b1", agent: "accounting", complexity: "M", deps: ["n1"] },
          { key: "b2", title_key: "proj.fc.b2", agent: "accounting", complexity: "M", deps: ["n1"], fail_once: true },
          { key: "b3", title_key: "proj.fc.b3", agent: "accounting", complexity: "M", deps: ["b1", "b2"] },
          { key: "b4", title_key: "proj.fc.b4", agent: "accounting", complexity: "L", deps: ["b3"],
            approval: { action: "publish_statement", risk: "medium" } },
        ] },
      ] },
      { key: "o3", title_key: "proj.fc.o3", workflows: [
        { key: "w4", title_key: "proj.fc.w4", nodes: [
          { key: "c1", title_key: "proj.fc.c1", agent: "analyst", complexity: "M", deps: ["a3", "b4"] },
          { key: "c2", title_key: "proj.fc.c2", agent: "legal", complexity: "M", deps: ["a3", "b4"] },
          { key: "g1", kind: "gate", title_key: "proj.fc.g1", deps: ["c1", "c2"], secs: 8, approval: { action: "approve_close", risk: "high" } },
          { key: "c3", title_key: "proj.fc.c3", agent: "assistant", complexity: "M", deps: ["g1"] },
        ] },
      ] },
    ],
  },
  {
    id: "tpl-branch-opening", key: "branch_opening", version: 1, builtin: true,
    name: "", name_key: "proj.bo.name", description: "", description_key: "proj.bo.desc",
    params: [{ key: "city", label_key: "proj.bo.param.city", default: "Norte" }],
    objectives: [
      { key: "o1", title_key: "proj.bo.o1", workflows: [
        { key: "w1", title_key: "proj.bo.w1", nodes: [
          { key: "n1", title_key: "proj.bo.n1", agent: "legal", complexity: "M" },
          { key: "n2", title_key: "proj.bo.n2", agent: "legal", complexity: "L", deps: ["n1"], approval: { action: "send_contract", risk: "high" } },
          { key: "n3", kind: "wait", title_key: "proj.bo.n3", deps: ["n2"], secs: 6 },
        ] },
        { key: "w2", title_key: "proj.bo.w2", nodes: [
          { key: "p1", title_key: "proj.bo.p1", agent: "operations", complexity: "M" },
          { key: "p2", title_key: "proj.bo.p2", agent: "accounting", complexity: "S", deps: ["p1"] },
        ] },
      ] },
      { key: "o2", title_key: "proj.bo.o2", workflows: [
        { key: "w3", title_key: "proj.bo.w3", nodes: [
          { key: "h1", title_key: "proj.bo.h1", agent: "hr", complexity: "M", deps: ["n1"] },
          { key: "h2", title_key: "proj.bo.h2", agent: "hr", complexity: "M", deps: ["h1"] },
        ] },
      ] },
      { key: "o3", title_key: "proj.bo.o3", workflows: [
        { key: "w4", title_key: "proj.bo.w4", nodes: [
          { key: "m1", title_key: "proj.bo.m1", agent: "sales", complexity: "L", deps: ["n3", "p2"] },
          { key: "m2", kind: "milestone", title_key: "proj.bo.m2", deps: ["m1", "h2"] },
          { key: "m3", title_key: "proj.bo.m3", agent: "assistant", complexity: "S", deps: ["m2"] },
        ] },
      ] },
    ],
  },
  {
    id: "tpl-generic", key: "generic", version: 1, builtin: true,
    name: "", name_key: "proj.gen.name", description: "", description_key: "proj.gen.desc", params: [],
    objectives: [
      { key: "o1", title_key: "proj.gen.o1", workflows: [
        { key: "w1", title_key: "proj.gen.w1", nodes: [
          { key: "r1", title_key: "proj.gen.r1", agent: "analyst", complexity: "M" },
          { key: "r2", title_key: "proj.gen.r2", agent: "operations", complexity: "M" },
        ] },
      ] },
      { key: "o2", title_key: "proj.gen.o2", workflows: [
        { key: "w2", title_key: "proj.gen.w2", nodes: [
          { key: "e1", title_key: "proj.gen.e1", agent: "sales", complexity: "L", deps: ["r1", "r2"] },
          { key: "e2", title_key: "proj.gen.e2", agent: "accounting", complexity: "M", deps: ["r1"] },
        ] },
      ] },
      { key: "o3", title_key: "proj.gen.o3", workflows: [
        { key: "w3", title_key: "proj.gen.w3", nodes: [
          { key: "v1", kind: "gate", title_key: "proj.gen.v1", deps: ["e1", "e2"], secs: 8, approval: { action: "approve_plan", risk: "medium" } },
          { key: "v2", title_key: "proj.gen.v2", agent: "assistant", complexity: "M", deps: ["v1"] },
        ] },
      ] },
    ],
  },
];
