"""Hierarchical planning of large projects (W4).

Step 1 (`plan_phases`): the goal becomes phases (roadmap) with a rough size and dependencies.
Step 2 (`plan_phase`): ONE phase becomes tasks (bounded call, the backend runs several in parallel),
each with complexity S/M/L/XL, an agent of the organization and dependencies inside the phase.

This module holds what is shared by the simulation and the live engine: the deterministic simulated
planner (no LLM, no cost surprises, used by tests/demos) and the validators that make any answer a
valid DAG (unknown agents dropped, unknown/self/cyclic dependencies removed, caps applied).
The runtime never calls external systems: the backend validates again and owns the final plan.
"""
from __future__ import annotations

import hashlib
import os
import random

from .engine import estimate_cost
from .models import (
    PhaseSpec,
    PhaseTask,
    PlanAgent,
    PlanPhaseRequest,
    PlanPhaseResponse,
    PlanPhasesRequest,
    PlanPhasesResponse,
    Usage,
)

SIM_MODEL = "simulation-claude-sonnet"

# Target tasks per phase size (a goal for the planner; the backend enforces the hard limits).
TASKS_BY_SIZE = {"S": 6, "M": 12, "L": 16, "XL": 22}

# (key, title, goal, size, depends_on) of the simulated roadmap, per locale.
_PHASES = {
    "es": [
        ("p1", "Descubrimiento y alcance", "Entender el objetivo, los actores y el alcance", "M", []),
        ("p2", "Diseno y planificacion", "Definir la solucion, los entregables y el plan de trabajo", "L", ["p1"]),
        ("p3", "Construccion", "Producir los entregables principales del proyecto", "XL", ["p2"]),
        ("p4", "Soporte y cumplimiento", "Preparar documentos, controles y comunicaciones en paralelo", "L", ["p2"]),
        ("p5", "Validacion y cierre", "Revisar, ajustar y cerrar el proyecto con un informe final", "M", ["p3", "p4"]),
    ],
    "en": [
        ("p1", "Discovery and scope", "Understand the goal, the stakeholders and the scope", "M", []),
        ("p2", "Design and planning", "Define the solution, the deliverables and the work plan", "L", ["p1"]),
        ("p3", "Build", "Produce the main deliverables of the project", "XL", ["p2"]),
        ("p4", "Support and compliance", "Prepare documents, controls and communications in parallel", "L", ["p2"]),
        ("p5", "Validation and closure", "Review, adjust and close the project with a final report", "M", ["p3", "p4"]),
    ],
}
_VERBS = {
    "es": ["Investigar", "Redactar", "Analizar", "Preparar", "Revisar", "Coordinar", "Documentar", "Validar"],
    "en": ["Research", "Draft", "Analyze", "Prepare", "Review", "Coordinate", "Document", "Validate"],
}
_TASK_DESC = {
    "es": "Paso {n} de la fase \"{phase}\" del proyecto: {goal}. Objetivo de la fase: {pgoal}",
    "en": "Step {n} of the \"{phase}\" phase of the project: {goal}. Phase goal: {pgoal}",
}
_TASK_TITLE = {"es": "{verb} - {phase} ({n})", "en": "{verb} - {phase} ({n})"}
_REASON = {
    "es": "Es la responsable natural de esta parte de la fase.",
    "en": "This agent owns this part of the phase.",
}
_ACCEPT = {
    "es": ["Entrega un resumen claro de «{title}»", "Cita los datos o la evidencia que respaldan el resultado"],
    "en": ["Delivers a clear summary of \"{title}\"", "Cites the data or evidence backing the result"],
}
_CX_CYCLE = ["S", "M", "M", "L", "S", "M", "XL", "M", "L", "S"]


def _loc(locale: str) -> str:
    return "en" if locale == "en" else "es"


def _rng(*parts: str) -> random.Random:
    return random.Random(int(hashlib.sha256("|".join(parts).encode()).hexdigest()[:12], 16))


def _usage(seed: str, in_range: tuple[int, int], out_range: tuple[int, int]) -> Usage:
    r = _rng(seed)
    i, o = r.randint(*in_range), r.randint(*out_range)
    return Usage(model=SIM_MODEL, input_tokens=i, output_tokens=o, cost_usd=estimate_cost(i, o),
                 duration_ms=0, provider="simulation")


def sim_target_tasks() -> int:
    """SIM_PROJECT_TASKS: approximate size of the simulated project (default 72, bounds 8-400)."""
    try:
        return max(8, min(400, int(os.getenv("SIM_PROJECT_TASKS", "72"))))
    except ValueError:
        return 72


# ---------------------------------------------------------------- validators
def normalize_phases(phases: list[PhaseSpec], max_phases: int) -> list[PhaseSpec]:
    """Unique non-empty keys, at most max_phases, dependencies only on other known phases and acyclic."""
    out: list[PhaseSpec] = []
    seen: set[str] = set()
    for ph in phases:
        key = (ph.key or "").strip()
        if not key or key in seen or not (ph.title or "").strip():
            continue
        seen.add(key)
        out.append(ph.model_copy(update={"key": key, "title": ph.title.strip()}))
        if len(out) >= max_phases:
            break
    deps = {p.key: list(p.depends_on) for p in out}
    clean = _acyclic({k: [d for d in v if d in deps and d != k] for k, v in deps.items()}, [p.key for p in out])
    return [p.model_copy(update={"depends_on": clean[p.key]}) for p in out]


def _acyclic(deps: dict[str, list[str]], order: list[str]) -> dict[str, list[str]]:
    """Adds the edges one by one and drops those that would close a cycle."""
    kept: dict[str, list[str]] = {k: [] for k in order}

    def reaches(src: str, dst: str) -> bool:  # does src depend (transitively) on dst?
        stack, seen = [src], set()
        while stack:
            cur = stack.pop()
            if cur == dst:
                return True
            if cur in seen:
                continue
            seen.add(cur)
            stack.extend(kept.get(cur, []))
        return False

    for k in order:
        for d in dict.fromkeys(deps.get(k, [])):
            if not reaches(d, k):
                kept[k].append(d)
    return kept


def normalize_phase_tasks(tasks: list[PhaseTask], agents: list[PlanAgent], max_tasks: int) -> list[PhaseTask]:
    """Valid task list of ONE phase: unique keys, known agents (unknown -> the first agent), acyclic deps."""
    valid = {a.id for a in agents}
    fallback = agents[0].id if agents else ""
    out: list[PhaseTask] = []
    seen: set[str] = set()
    for t in tasks:
        key = (t.key or "").strip()
        if not key or key in seen or not (t.title or "").strip():
            continue
        seen.add(key)
        aid = t.agent_id if (not valid or t.agent_id in valid) else fallback
        out.append(t.model_copy(update={"key": key, "title": t.title.strip(), "agent_id": aid}))
        if len(out) >= max_tasks:
            break
    deps = {t.key: [d for d in t.depends_on if d in {x.key for x in out} and d != t.key] for t in out}
    clean = _acyclic(deps, [t.key for t in out])
    return [t.model_copy(update={"depends_on": clean[t.key]}) for t in out]


# ---------------------------------------------------------- simulated planner
def sim_plan_phases(req: PlanPhasesRequest) -> PlanPhasesResponse:
    loc = _loc(req.locale)
    total = sim_target_tasks()
    base = sum(TASKS_BY_SIZE[p[3]] for p in _PHASES[loc])
    phases = []
    for key, title, goal, size, deps in _PHASES[loc][: req.max_phases]:
        # scale the size class so the whole project is about `total` tasks
        want = TASKS_BY_SIZE[size] * total / base
        best = min(TASKS_BY_SIZE, key=lambda s: abs(TASKS_BY_SIZE[s] - want))
        phases.append(PhaseSpec(key=key, title=title, goal=goal, size=best,
                                depends_on=[d for d in deps if d in {q[0] for q in _PHASES[loc][: req.max_phases]}]))
    phases = normalize_phases(phases, req.max_phases)
    return PlanPhasesResponse(objectives=[req.request_text[:200]], phases=phases, clarifying_questions=[],
                              usage=_usage("phases|" + req.request_text, (900, 1500), (300, 700)),
                              provider="simulation", model=SIM_MODEL)


def _pick_agents(agents: list[PlanAgent]) -> list[str]:
    ids = sorted(a.id for a in agents)
    workers = [i for i in ids if i != "assistant"]
    return workers or ids or ["assistant"]


def sim_plan_phase(req: PlanPhaseRequest) -> PlanPhaseResponse:
    """Deterministic: tasks in waves of 4 parallel tasks; each task depends on 1-2 tasks of the previous wave."""
    loc = _loc(req.locale)
    n = min(req.target_tasks, req.max_tasks)
    roles = _pick_agents(req.agents)
    width = 4
    r = _rng("phase", req.phase.key, req.request_text)
    off = r.randrange(len(roles))
    tasks: list[PhaseTask] = []
    for i in range(n):
        wave, col = divmod(i, width)
        deps: list[str] = []
        if wave > 0:
            deps.append(f"t{(wave - 1) * width + col + 1}")
            if i % 3 == 0:
                deps.append(f"t{(wave - 1) * width + (col + 1) % width + 1}")
        verb = _VERBS[loc][(i + r.randrange(len(_VERBS[loc]))) % len(_VERBS[loc])]
        tasks.append(PhaseTask(
            key=f"t{i + 1}",
            title=_TASK_TITLE[loc].format(verb=verb, phase=req.phase.title, n=i + 1),
            description=_TASK_DESC[loc].format(n=i + 1, phase=req.phase.title, goal=req.request_text[:300],
                                               pgoal=req.phase.goal or req.phase.title),
            agent_id=roles[(off + i) % len(roles)], depends_on=deps,
            complexity=_CX_CYCLE[(i + off) % len(_CX_CYCLE)], reason=_REASON[loc],
            acceptance=[c.format(title=_TASK_TITLE[loc].format(verb=verb, phase=req.phase.title, n=i + 1)) for c in _ACCEPT[loc]]))
    tasks = normalize_phase_tasks(tasks, req.agents, req.max_tasks)
    return PlanPhaseResponse(tasks=tasks, usage=_usage("phase|" + req.phase.key + req.request_text, (1200, 2200), (900, 1800)),
                             provider="simulation", model=SIM_MODEL)
