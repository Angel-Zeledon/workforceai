"""Replanning of a failed branch of a running project (Q2): POST /v1/replan.

The backend sends the failed node, its finished inputs and the nodes waiting for it; the runtime answers
with a small replacement sub-plan (tasks with local keys; roots inherit the dependencies of the failed
node, the last tasks take over its dependents - the backend does that wiring). The runtime never applies
anything: the backend validates the DAG again and a human approves the proposal.

The default planner is deterministic (no LLM): a short chain "diagnose -> redo with another approach ->
check", each step with the agent that owned the failed node. A live engine may override `replan`.
"""
from __future__ import annotations

from pydantic import Field, field_validator

from .engine import estimate_cost
from .hier_plan import SIM_MODEL, _loc, normalize_phase_tasks
from .models import PhaseTask, PlanAgent, Usage, _Base, _ProviderPolicyMixin, normalize_locale


class ReplanNode(_Base):
    id: str = ""
    title: str = ""
    description: str = ""
    agent_id: str = ""
    error: str = ""
    summary: str = ""


class ReplanRequest(_ProviderPolicyMixin):
    project_goal: str = ""
    failed: ReplanNode
    inputs: list[ReplanNode] = Field(default_factory=list)
    dependents: list[ReplanNode] = Field(default_factory=list)
    agents: list[PlanAgent] = Field(default_factory=list)
    max_tasks: int = Field(default=6, ge=1, le=20)
    budget_usd: float = 0.0
    locale: str = "es"

    _norm = field_validator("locale", mode="before")(normalize_locale)


class ReplanResponse(_Base):
    tasks: list[PhaseTask]
    reason: str = ""
    usage: Usage | None = None
    provider: str | None = None
    model: str | None = None


_STEPS = {
    "es": [
        ("Diagnosticar por que fallo: {t}", "Revisar los insumos y la causa probable del fallo de \"{t}\" y proponer otra via."),
        ("Rehacer con otro enfoque: {t}", "Completar el objetivo de \"{t}\" con el enfoque alternativo del diagnostico."),
        ("Verificar el resultado: {t}", "Comprobar que el resultado cubre lo que esperaban los pasos que dependen de \"{t}\"."),
    ],
    "en": [
        ("Diagnose why it failed: {t}", "Review the inputs and the likely cause of the failure of \"{t}\" and propose another way."),
        ("Redo with another approach: {t}", "Reach the goal of \"{t}\" with the alternative approach from the diagnosis."),
        ("Check the result: {t}", "Check that the result covers what the steps depending on \"{t}\" expect."),
    ],
}
_REASON = {
    "es": "Reemplazo propuesto para la rama fallida \"{t}\": diagnosticar, rehacer con otro enfoque y verificar.",
    "en": "Proposed replacement for the failed branch \"{t}\": diagnose, redo with another approach and check.",
}


def sim_replan(req: ReplanRequest) -> ReplanResponse:
    loc = _loc(req.locale)
    owner = req.failed.agent_id or (req.agents[0].id if req.agents else "assistant")
    title = (req.failed.title or "").strip()[:120] or "?"
    steps = _STEPS[loc][: max(1, req.max_tasks)]
    tasks = []
    for i, (t, d) in enumerate(steps):
        tasks.append(PhaseTask(key=f"r{i + 1}", title=t.format(t=title), description=d.format(t=title), agent_id=owner,
                               depends_on=[f"r{i}"] if i else [], complexity="S" if i != 1 else "M"))
    tasks = normalize_phase_tasks(tasks, req.agents, min(req.max_tasks, 6))
    i_tok = 400 + 60 * len(req.inputs) + 40 * len(req.dependents)
    usage = Usage(model=SIM_MODEL, input_tokens=i_tok, output_tokens=250, cost_usd=estimate_cost(i_tok, 250), duration_ms=0, provider="simulation")
    return ReplanResponse(tasks=tasks, reason=_REASON[loc].format(t=title), usage=usage, provider="simulation", model=SIM_MODEL)
