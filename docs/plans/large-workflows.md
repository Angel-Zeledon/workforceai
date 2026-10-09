# Large workflows

This document collects the work that makes agents productive on large projects
(dozens to hundreds of tasks, several phases). Each package adds its own section.

## W1 - Scheduler throughput

- **Per-project parallelism.** `projects` passes its `max_parallel` (1..64, default 4) to `SubmitPlan` through `application.WithRunParallel(ctx, n)`; `Orchestrator.execute` uses it instead of `MAX_PARALLEL` for that request. It stays bounded by the per-organization queue (`MAX_PARALLEL_PER_ORG`, default 8, unchanged): a project with `max_parallel=16` only reaches 16 concurrent runtime calls if the org cap is raised (env). Not persisted in the run meta: a request resumed by restart recovery uses `MAX_PARALLEL`.
- **Waiting tasks release their slot.** `application.YieldSlot(ctx)` gives the scheduler slot back while a task waits for a human or a timer (project gate hold, gate approval, tool approval, guard pause/kill switch, budget-cap pause) and takes one again before the task continues. Resuming tasks are served before new ready nodes; on cancel they are granted regardless of the limit so everything winds down. The org-queue slot was already only held during a runtime call (`orgqueue.go`), so waits never held it.
- **Ready queue.** Ready nodes are launched by longest remaining downstream path (`Node.Weight`, default 1 per node; ties keep list order). Indegree counters and a dependents index make completion O(dependents); the scheduler no longer rescans the node list.
- **Not done:** per-agent concurrency cap (AGENT_MAX_CONCURRENT).

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

## W5 monitoring and budget alerts

Goal: keep monitoring cost flat as projects grow (hundreds of tasks, several large projects at once) and warn before the budget cap pauses the work.

**Project monitor (`backend/internal/projects/monitor.go`).** Each launched project has one watcher goroutine.
- Approvals are loaded per request, not per org: `application.RequestApprovalLister.ListApprovalsByRequest(org, requestID, extraTaskIDs)` (memory and Postgres stores; migration `400_approvals_task_idx` adds `approvals(org_id, task_id)`). Stores that do not implement it fall back to the old full scan.
- Change detection: a SHA-1 fingerprint of record status/control/budget/decisions, request status/cost, each task (status, cost, start, finish) and each approval (status, resolution). Unchanged fingerprint means no `build`, no diff, no publish.
- Backoff: 8 unchanged ticks at the active period (`Poll`, 250 ms), then the period doubles up to `PollIdleMax` (2 s). Any change, or a human action (`Control`, gate decision, `SetBudget` call `liveProject.wake()`), resets it. A full rebuild always runs at least every `PollIdleMax` because the view also depends on state outside the snapshot (execution guard, wait nodes).
- Events keep their shape: `project.delta` / `project.status_changed` are emitted exactly as before, just not re-evaluated when nothing changed. Worst case latency to notice a task transition is now `PollIdleMax` after a long idle (it is 250 ms while work is moving).
- Env (all optional): `PROJECT_POLL_MS`, `PROJECT_POLL_IDLE_MAX_MS`, `PROJECT_BUDGET_WARN_PCT` (1-100).

**Org spend counter (`application/spend.go`).** `Orchestrator.call()`, `Budget.Reserve` and the 80% warning check read `Budget.OrgSpent` instead of running `SUM(cost_usd)` every time (gap G11). The counter is seeded from the store, incremented by `Budget.Record` right after `AddCost` (before the reservation is released, so caps stay exact: spent + reserved + estimate <= cap), re-synced every 15 s, and reset with the budget. A re-seed that races with a Record is discarded instead of risking a double count. Limitation: with several backend replicas writing to the same database, spend from another replica is only seen at the next re-sync (up to 15 s).

**Budget early warning.** When the spend of a project reaches a threshold of its approved budget the monitor persists the crossing (`Record.budget_warned`), then emits `project.budget_warning` (see `docs/architecture/03-eventos-realtime.md`) and audits `project.budget_warning`. Once per crossing; a restart does not repeat it; if the budget is extended and the spend drops under the threshold it re-arms. Thresholds: a project with the default policy (`warn_at` 0.5/0.8/0.95) alerts once at `PROJECT_BUDGET_WARN_PCT` (default 80); a customized `budget.warn_at` is used as is. `Summary.budget_warn_pct` (additive) feeds an amber banner in the project view. The existing per-request `budget.warning` (80%) still fires too. Not done: Web Push for this alert (push is wired for approvals only and the service worker needs matching labels).

**Progress and ETA.** The project view shows done/total, percentage and a status-count line (running, awaiting approval, failed, pending). The ETA (`health.schedule.eta_p50/p90`) is the simulated remaining makespan from the complexity estimates, multiplied by a calibration factor: the median of observed/estimated duration over finished agent nodes (clamped to 0.2-5, used from 3 samples). `calibration_factor` and `calibration_samples` are returned (additive) and shown under the ETA. The ETA is an estimate: human gates and approval waits are not predicted from history.
