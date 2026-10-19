"""Planner fast path: a simple request is ONE task for the best-fit agent, with no planning LLM call.

Live finding: "compute gross margin" was decomposed into several tasks and each one cost a full model call.
The heuristic is deliberately conservative: when in doubt the request goes to the normal planner.
A request is simple when ALL of these hold:
  - short (<= FAST_PATH_MAX_CHARS, default 280) and at most two sentences/lines;
  - no list, no multi-step connector (then, after, also...), no deliverable-sized word (proposal, project,
    campaign, "everything"...), no slash-command marker;
  - it touches at most one area of the office (several areas = several agents = the planner's job), and
    does not name two or more agents.
Switch: PLANNER_FAST_PATH (default on; "false" restores the old behaviour).
"""
from __future__ import annotations

import os
import re

from .cost_control import fast_path_enabled
from .models import PlanAgent, PlanRequest, PlanResponse, PlanTask, Usage
from .routing import norm, topic_scores

DEFAULT_MAX_CHARS = 280

_MULTI_STEP = re.compile(
    r"\b(despues|luego|posteriormente|ademas|tambien|al mismo tiempo|paso a paso|en paralelo|por ultimo|finalmente|"
    r"then|afterwards|after that|also|as well|additionally|step by step|in parallel|finally|and then)\b")
_BIG = re.compile(
    r"\b(propuesta|proyecto|campana|estrategia|plan de|plan para|hoja de ruta|roadmap|lanzamiento|onboarding|"
    r"contrato|auditoria completa|informe completo|reporte completo|todo|toda la|todos los|de punta a punta|"
    r"proposal|project|campaign|strategy|launch|everything|end to end|full report|complete report|"
    r"business plan|plan de negocio)\b")
_LIST = re.compile(r"(^|\n)\s*(?:[-*•]|\d+[.)])\s+\S")
_SENTENCE_END = re.compile(r"[.!?;]+(?:\s+|$)|\n+")


def max_chars() -> int:
    try:
        v = int(os.getenv("FAST_PATH_MAX_CHARS", ""))
        return v if v > 0 else DEFAULT_MAX_CHARS
    except ValueError:
        return DEFAULT_MAX_CHARS


def _names_two_agents(n: str, agents: list[PlanAgent]) -> bool:
    hits = 0
    for a in agents:
        names = {norm(a.role or ""), norm(a.id or ""), norm(a.title or "")}
        if any(x and len(x) > 3 and re.search(r"\b" + re.escape(x) + r"\b", n) for x in names):
            hits += 1
    return hits >= 2


def is_simple_request(text: str, agents: list[PlanAgent]) -> bool:
    """True when the request is a single question/calculation/short text for one agent."""
    raw = (text or "").strip()
    if not raw or len(raw) > max_chars() or "[[" in raw:
        return False
    if _LIST.search(raw):
        return False
    sentences = [s for s in _SENTENCE_END.split(raw) if s.strip()]
    if len(sentences) > 2:
        return False
    n = norm(raw)
    if _MULTI_STEP.search(n) or _BIG.search(n):
        return False
    scores = topic_scores(n, agents)
    scores.pop("assistant", None)
    if len(scores) > 1:
        return False
    if _names_two_agents(n, agents):
        return False
    return True


def best_fit_agent(text: str, agents: list[PlanAgent]) -> PlanAgent | None:
    """The owner of the request's area; the assistant (or the first agent) when it names no area."""
    from .routing import owner_role

    if not agents:
        return None
    role = owner_role(text, agents)
    if role:
        for a in agents:
            if (a.role or a.id) == role or a.id == role:
                return a
    for a in agents:
        if (a.role or a.id) == "assistant" or a.id == "assistant":
            return a
    return agents[0]


_TEXT = {
    "es": ("Pedido directo", "Resolver la solicitud con un solo responsable.",
           "Solicitud simple: un solo agente la resuelve, sin descomponer en varias tareas."),
    "en": ("Direct request", "Resolve the request with a single owner.",
           "Simple request: one agent handles it, no decomposition into several tasks."),
}


def try_fast_plan(req: PlanRequest) -> PlanResponse | None:
    """A one-task plan for a simple request, or None (use the planner). Never calls a model."""
    if not fast_path_enabled() or not req.agents:
        return None
    if not is_simple_request(req.request_text, req.agents):
        return None
    agent = best_fit_agent(req.request_text, req.agents)
    if agent is None:
        return None
    title_fallback, objective, reason = _TEXT.get(req.locale, _TEXT["en"])
    text = req.request_text.strip()
    title = text if len(text) <= 80 else text[:77].rstrip() + "..."
    task = PlanTask(key="t1", title=title or title_fallback, description=text, agent_id=agent.id, depends_on=[],
                    reason=reason)
    plan = PlanResponse(objectives=[objective], tasks=[task], clarifying_questions=[])
    plan.usage = Usage(model="fast-path", input_tokens=0, output_tokens=0, cost_usd=0.0, duration_ms=0)
    return plan
