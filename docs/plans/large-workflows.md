# Large workflows

This document collects the work that makes agents productive on large projects
(dozens to hundreds of tasks, several phases). Each package adds its own section.

## W4 hierarchical planning and limits

Self-contained description of what W4 changed: how a project goal becomes a large
plan, the size limits that are now enforced, how planner failures surface, and
how to configure all of it.

### 1. Hierarchical planning of a goal

A project created from a free goal (no template) is planned in two steps instead
of one flat call (`backend/internal/projects/planner.go`):

1. **Phases.** `POST /v1/plan-phases` returns the roadmap: `phases[{key, title,
   goal, size S|M|L|XL, depends_on[]}]`. Independent phases stay parallel.
2. **Tasks per phase.** Each phase is expanded by its own bounded call,
   `POST /v1/plan-phase` (several in parallel, `PLANNER_CONCURRENCY`, default 3).
   A task has `key` (local to the phase), `title`, `description`, `agent_id` (an
   agent of the organization), `depends_on` (tasks of the same phase),
   `complexity S|M|L|XL` and an optional `reason`. The target number of tasks
   follows the phase size (S 6, M 12, L 16, XL 22) and is hard-capped by
   `PLANNER_MAX_TASKS_PER_PHASE` (default 40).

Mapping onto the project structure (so the map, level of detail and estimates work
unchanged): phase = **objective**; its tasks are grouped in **workflows** (groups)
of at most `PROJECT_MAX_CHILDREN_PER_GROUP`; node keys are `<phase>_<task>`
(`p3_t7`). Cross-phase dependencies are resolved by Go: the tasks of a phase that
depend on nothing inside it wait for the *exit tasks* (tasks nothing else in their
phase needs) of every phase it depends on. The phase graph is checked to be acyclic.

Validation in Go (the runtime is not trusted with the DAG): keys remapped, unknown
agents replaced by `assistant` (or the first agent), unknown/self/duplicate
dependencies dropped, intra-phase cycles broken (only dependencies on earlier
tasks are kept), size caps applied, complexity normalized (anything unknown is
`M`). The runtime validates the same way before answering.

Estimates: `projects/calc.go` already prices each node by its complexity prior
(`priorCost`/`priorSeconds`); the planner now supplies the real complexity per
node instead of the flat `M`, so the estimate, the simulated schedule and the
critical path reflect it. Not done: pricing by each agent's configured model
(priors still use deepseek-chat rates; `estimateModel`).

Fallbacks, all visible (see section 3): hierarchical -> flat planner (one call,
the previous behavior; all tasks `M`) -> generic template. A phase whose expansion
fails keeps one placeholder task (its title/goal, complexity = phase size) so the
DAG stays connected; the project is `degraded`, not lost.

Slow planners: `CreateDraft` waits up to `PLANNER_SYNC_WAIT` (default 40 s, below
the 60 s REST write timeout). If the planner is still working it answers with a
draft in `planning` state (`planner.status = "running"`, `planning {done,total}`
counts phases) and the plan is filled in the background (event
`project.planned`). Launch is refused with `planning_in_progress` meanwhile. If the
server restarts mid-planning the draft reports `planner_interrupted`.

Simulation (no API key): the runtime has a deterministic simulated hierarchical
planner (`agent-runtime/app/hier_plan.py`): 5 phases (a diamond: p3 and p4 both
after p2, p5 after both), about 78 tasks by default, tasks in waves of 4 with
cross-wave dependencies, mixed complexities, agents round-robin over the org's
agents. `SIM_PROJECT_TASKS` (default 72, 8-400) scales the phase sizes.

### 2. Size limits (docs/architecture/workflow-visualization.md 2.9)

| Limit | Env | Default | Enforced |
|---|---|---|---|
| Nodes per project (groups + tasks + subtasks) | `PROJECT_MAX_NODES` | 20000 | draft creation, launch |
| Children of one group | `PROJECT_MAX_CHILDREN_PER_GROUP` | 200 | draft creation, launch |
| Active (launched, unfinished) projects per org | `PROJECT_MAX_ACTIVE` | 20 | launch |

Errors: HTTP 400 (nodes, children) or 409 (active projects) with a message
`... limit_exceeded: <code> (limit N): <detail>` where `<code>` is
`max_nodes_per_project`, `max_children_per_group` or `max_active_projects`. The UI
maps the code to a translated text (`pv.limit.<code>`). The active-projects check
and the launch are serialized so two simultaneous launches cannot both pass.
Planner bounds: `PLANNER_MAX_PHASES` (12), `PLANNER_MAX_TASKS_PER_PHASE` (40),
`PLANNER_CONCURRENCY` (3), `PLANNER_PHASES_TIMEOUT` (45s), `PLANNER_PHASE_TIMEOUT`
(75s), `PLANNER_SYNC_WAIT` (40s).

Free-form request depth: the longest dependency chain of a non-project request was
5; it is now `MAX_PLAN_DEPTH` (default 30). Cycle and dangling-dependency
validation is unchanged. Project launches still raise it per plan. The delegation
depth of agent consults (`MaxDepth`, 5) is unchanged.

### 3. Failures are visible, and planner calls cost money

`GET /projects/{id}` carries `planner` (additive): `{mode: hierarchical|flat|generic,
status: ok|running|degraded|failed, code, message, fell_back_from, phases, tasks,
failed_phases, calls, cost_usd}`. Codes: `planner_unavailable`, `planner_timeout`,
`planner_invalid`, `budget_exceeded`, `no_runtime`, `no_agents`,
`planner_interrupted`, `phases_failed`. The draft editor shows a notice for
`failed` and `degraded`; an audit entry (`project.planner_failed|degraded`) and an
event (`project.planner_notice`) are written too.

Spend: every planner call reserves budget first (`Orchestrator.ReservePlanner`:
organization budget and agent cap apply, like chat) and is recorded in the cost
ledger as kind `plan` with no request (`RecordPlannerUsage`), so it counts toward
the organization total, metrics and the audit trail. A refused reservation ends the
planning with `budget_exceeded`. The runtime's `/v1/plan` response may also carry
`usage` (additive); the orchestrator records it for free-form requests.

### 4. Contracts (all additive)

* Runtime: new `POST /v1/plan-phases` and `POST /v1/plan-phase`; `PlanResponse`
  gains optional `usage`. Old requests and responses still work (the Go client
  tolerates a missing `usage`; a runtime without the new endpoints makes the
  backend use the flat planner after a visible fallback notice).
* Backend: `Detail.planner`, `Detail.planning` (now phases done/total),
  `application.HierarchicalPlanner` (optional interface of the runtime port),
  `domain.UsagePlan`.
* Not verified: a live LLM run of the new prompts (`CrewAIEngine.plan_phases/plan_phase`
  compile and share the tested validators, but crewai is not installed in the test
  environment); the Postgres store with the new `planner` field (the record is
  stored as JSON, no migration needed; not run against a real database here).
