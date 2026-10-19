# Corporate organization model (departments, org chart, job descriptions, delegation, industry templates)

A company is more than a flat list of agents: it has departments with heads and cost centers, people and agents that
report to someone, working hours, budgets, rules about who may assign work to whom, and a typical shape per industry.
Code: `backend/internal/orgchart/` (model, rules, service, gate), `backend/internal/catalog/industry.go` and
`data/industries/*.json` (industry templates), `backend/internal/demodata/industry.go` (sample data),
`backend/internal/api/orgchart.go` (routes), `backend/internal/infrastructure/postgres/orgchart_store.go`
(migrations `750`-`754`), `cmd/server/orgchart.go` (wiring), and in the frontend `components/org/**`,
`components/templates/IndustryTemplates.tsx`, `components/onboarding/IndustryField.tsx`, `lib/orgChartApi.ts`,
`lib/orgChartLogic.ts`.

Principle: **the model never grants power**. A job description can only narrow the tools of the agent's role; a schedule
only delays work (queued, never dropped); a delegation rule only adds a check; an industry template builds agents from
existing role templates and creates nothing the owner could not create by hand. With nothing configured, the product
behaves exactly as before.

## 1. Departments and the org chart

* `departments` (750): a tree per organization. Fields: `name` (1-80, unique case-insensitive), `parent_id`, `head`
  (`{kind: "person"|"agent", id}`, empty = none; a person head is a member id or the alias `owner`), `cost_center`
  (`[A-Za-z0-9._-]`, max 32). Limits: 200 departments, 8 levels.
* **Cycle detection** (`orgchart.DetectCycle`, `ValidateDepartments`): a parent change that would make a department its own
  ancestor is rejected (HTTP 400, "form a cycle"). The UI also hides those parents (`parentOptions`).
  Deleting a department with sub-departments is a 409; deleting one without them leaves its members unassigned.
* **People**: the members of the organization (auth). `org_people` (752) only places them: department, title, boss.
  Reporting lines of people and agents are validated together (an agent cannot report to the person who reports to it).
* **`agents.department`** (nullable text, added idempotently by 750): a plain string **tag** holding the department **name**
  (the admin console treats it as a label). The service keeps it in sync through the department id: assigning an agent to
  a department writes the name, renaming the department rewrites the tag of every agent in it, deleting the department
  clears it. `GET /agents` returns it as `department` (string or null).
* `GET /api/v1/org/chart` returns the tree: each node has the department fields, `head_name`, `members` (people and agents
  with title, role, `reports_to`, `reports_to_name`, `escalation`, for agents `open`/`next_open` and `budget_usd`),
  `children`, counts, plus an `unassigned` lane and the industry templates already applied.

## 2. Agent job descriptions

`agent_jobs` (751), one per agent, `GET/PUT /api/v1/agents/{id}/job` (PUT: owner/admin, audited `agent.job_updated`):

| Field | Meaning and rule |
|---|---|
| `department_id` | department of the agent; its **name** is written to `agents.department` |
| `reports_to`, `escalation` | person or agent; not itself; the reporting lines must stay acyclic; `escalation` is who a human or agent should go to when the agent is stuck |
| `responsibilities` | up to 20 lines of 300 characters; copied to the agent record the planner reads |
| `allowed_tools` | must be a **subset of the tools of the agent's role template**, so it can only narrow; written to `agents.tools` |
| `schedule` | `{enabled, timezone (IANA), days (0=Sunday..6), start, end}` see below |
| `budget_usd` | monthly cap for the agent (null = organization default); mirrored into `budget_caps` scope `agent`, which the existing budget guard enforces |

`GET /agents/{id}/job` without a saved description returns one derived from the agent (current tools and responsibilities).

### Working schedule (queue, not act)

Evaluated on the **wall clock of the schedule's timezone**, so 09:00-17:00 in `America/New_York` means 09:00-17:00 local on
the day daylight saving changes too (tests cover the 2026 US and EU changes). Rules: `start` inclusive, `end` exclusive,
`end` may be `24:00`, `end < start` is an overnight window that belongs to the day it starts (Friday 22:00-06:00 covers
Saturday's small hours). A disabled or broken schedule means "always available": a bad configuration must never
silently stop an agent.

Enforcement: `orgchart.TaskGate` wraps the projects task gate (`application.TaskGate`). For a task of an agent outside its
hours it answers `GateHold` with the reason and the next opening, so the task waits in the queue (the agent shows as paused)
and runs when the window opens; the inner gate (project pause/cancel, human gates) keeps priority. Jobs are cached for 15 s
and the cache is invalidated by every save. `GET /agents/{id}/availability?at=<RFC3339>` answers `{open, next_open, timezone}`.

**Holds (760).** A held task with a known opening (`GateDecision.Until`) sleeps on a timer until then (it does not poll the gate) and wakes
early when a schedule or rule is edited (`WakeHeld`). Each hold is persisted in `schedule_holds` (`org_id, task_id, request_id, agent_id,
reason, wake_at`; `postgres.HoldStore`, memory without a database: then holds do not survive a restart), so a restart loses nothing:
* On start `Orchestrator.Recover` resumes the request; the gate holds the task again until the same opening (or runs it if the
  window is open) and the stale row is replaced or removed. A shutdown keeps the hold; a finished or cancelled task removes it.
* `StartHoldWatcher` (every 30 s, started in `cmd/server/main.go`, stopped with the server context) resumes requests whose
  `wake_at` passed with no goroutine running them and drops holds whose task or request is no longer live.

## 3. Delegation rules

`delegation_rules` (753); `GET/POST /org/delegation/rules`, `DELETE /org/delegation/rules/{id}`,
`POST /org/delegation/check {from, to}` (any member can ask, owner/admin changes rules; audited).

A rule is `from` -> `to` with `effect` `allow|deny`, optional `require_approval` and `approver`. Selectors: `any`,
`any_person`, `any_agent`, `person:<id>`, `agent:<id>`, `department:<id>` (the department and its sub-departments).
Resolution (`orgchart.Check`): the **most specific** matching rule wins (person/agent 3, department 2, any_person/any_agent 1,
any 0, summed for from and to); ties: deny beats approval beats allow. No matching rule = allowed, no approval (today's behaviour);
a rule `any` -> `any` is the organization default (for example deny, for a strict company). Working for yourself is never a delegation.
Approver when approval is required: the rule's approver, else the head of the target's department, else the target's boss, else the owner.

## 4. Industry templates

Five templates in `data/industries/`: `accounting_firm`, `law_firm`, `ecommerce_store`, `marketing_agency`,
`multinational_group` (consulting / SME with 3 subsidiaries: Mexico, Brazil, Spain, each with its own timezone and local controller).
Schema `aiw.industry_template/1`, **es, en and pt** (all three mandatory, same keys), validated at load by
`catalog.ValidateIndustry`: unknown roles, tools outside the role, autonomy above the role ceiling, department or reporting cycles,
unknown timezones, step dependency cycles and any missing translation fail the build's tests.

Each template defines departments (with cost centers and head agents), a starting team of 9-10 agents (role template,
title, responsibilities, tools, boss, escalation, schedule, budget, autonomy), 5-7 delegation rules (an approval rule and a
deny rule at least), 3 sample projects and the name of a demo dataset, plus a disclaimer (agents assist; a licensed professional reviews).

`POST /org/industries/{key}/apply {locale, skip_projects, demo_data}` (owner/admin; audited `industry.applied`) is **idempotent**:

1. refuses (409) if the office would exceed 20 agents, changing nothing;
2. creates departments (parents first; ids `ind_<key>_<dept>`) and agents (ids `<prefix>_<agent>`, prefix = first word of the key) that
   do not exist yet, existing ones are kept as the owner left them; agents start at the template's autonomy and still need connections and approvals;
3. writes job descriptions, department tags, budget caps and department heads;
4. rewrites the delegation rules with deterministic ids (`ind_<key>_<n>`);
5. creates the sample projects as **organization project templates** (they appear in the Templates screen) once per template; they are validated by the
   existing `projects.ValidateTemplate` like any template;
6. with `demo_data`, writes the sample files into `/Shared/demo-<key>/` of the drive (`demodata.GenerateIndustry`: fictional, deterministic, arithmetically
   consistent CSVs in es/en/pt). A failure there is reported as `demo_error` and does not undo the rest.

`GET /org/industries?locale=` lists them resolved for a language. From the UI: **onboarding** (optional "company template" in the last step) and
**Settings -> Templates -> Industries**.

## 5. Permissions and data

Reads need `agents:read`; every change needs `org:manage`. All tables have Row-Level Security by `org_id` (`app_current_org()`), like the rest.
Migrations 750-754 and 760 are idempotent and have rollbacks in `backend/migrations/down/` (the rollback of 750 keeps the `agents.department` column).

## 6. What is not enforced yet

* **Delegation rules are decided, not yet consulted by the orchestrator.** `POST /org/delegation/check` and `orgchart.Check` answer the question; the
  places that assign work (chat routing, plan assignment, reassignment) do not call it yet, so today a rule is a documented policy and a preview.
  The hook belongs next to `Orchestrator.announceAssignments` / task creation: deny -> reassign or block, approval -> create an approval for `approver`.
* The escalation contact is data (shown in the chart and available to prompts); no automatic notification flow uses it yet.
* The schedule gate uses the project task gate path of the orchestrator: every task goes through it, but a task that waits days for its window
  occupies an in-memory poll (100 ms, cached lookups) until it opens; a long weekend gap keeps the request open that long, so very long closures
  are better modelled as a pause of the agent than as a schedule.
