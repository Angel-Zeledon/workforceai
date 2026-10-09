"""Capa de abstraccion propia sobre el framework de agentes."""
from __future__ import annotations

import abc
import json
import logging
import os
from pathlib import Path
from typing import TYPE_CHECKING

from .providers import configured_providers
from .models import (
    ChatReplyRequest,
    ChatReplyResponse,
    ConsultRequest,
    ConsultResponse,
    PlanPhaseRequest,
    PlanPhaseResponse,
    PlanPhasesRequest,
    PlanPhasesResponse,
    PlanRequest,
    PlanResponse,
    ReviewRequest,
    ReviewResponse,
    RouteRequest,
    RouteResponse,
    RunTaskRequest,
    RunTaskResponse,
    SynthesizeRequest,
    SynthesizeResponse,
)

if TYPE_CHECKING:  # avoid a circular import (estimate.py imports engine.py)
    from .estimate import EstimateRequest, EstimateResponse

log = logging.getLogger("agent_runtime")

# USD per million tokens (input, output), keyed by model name without the "provider/" prefix.
# Loaded from model_prices.json, the single table shared with the backend
# (backend/internal/application/model_prices.json must be byte-identical; tests check it).
# Peak rates so the budget is never underestimated; force values with PRICE_IN_PER_M / PRICE_OUT_PER_M.
PRICES_FILE = Path(__file__).with_name("model_prices.json")


def _load_prices() -> tuple[dict[str, tuple[float, float]], tuple[float, float]]:
    data = json.loads(PRICES_FILE.read_text(encoding="utf-8"))
    models = {k.lower(): (float(v["input"]), float(v["output"])) for k, v in data["models"].items()}
    return models, (float(data["default"]["input"]), float(data["default"]["output"]))


MODEL_PRICES, (PRICE_IN_PER_M, PRICE_OUT_PER_M) = _load_prices()
_warned_models: set[str] = set()


def _env_price(name_in: str, name_out: str) -> tuple[float, float] | None:
    env_in, env_out = os.getenv(name_in), os.getenv(name_out)
    if env_in and env_out:
        try:
            return float(env_in), float(env_out)
        except ValueError:
            log.warning("invalid %s/%s; ignoring", name_in, name_out)
    return None


def _price_for(model: str | None, provider: str | None = None) -> tuple[float, float]:
    forced = _env_price("PRICE_IN_PER_M", "PRICE_OUT_PER_M")  # global override wins (legacy)
    if forced:
        return forced
    if provider == "custom":  # unknown model names on a custom endpoint: own rate or the default one
        return _env_price("CUSTOM_PRICE_IN_PER_M", "CUSTOM_PRICE_OUT_PER_M") or (PRICE_IN_PER_M, PRICE_OUT_PER_M)
    key = (model or "").split("/")[-1].lower()
    if key in MODEL_PRICES:
        return MODEL_PRICES[key]
    if key and "simulat" not in key and key not in _warned_models:
        _warned_models.add(key)
        log.warning("model %s not in the price table: billed at the default rate %s/%s per M tokens",
                    model, PRICE_IN_PER_M, PRICE_OUT_PER_M)
    return PRICE_IN_PER_M, PRICE_OUT_PER_M


def estimate_cost(input_tokens: int, output_tokens: int, model: str | None = None,
                  provider: str | None = None) -> float:
    p_in, p_out = _price_for(model, provider)
    return round(input_tokens * p_in / 1e6 + output_tokens * p_out / 1e6, 5)


class AgentEngine(abc.ABC):
    """Interfaz que consume la API. El runtime NUNCA ejecuta herramientas:
    solo devuelve tool_requests para que el backend decida."""

    mode: str  # "simulation" | "live"
    provider: str = "simulation"  # simulation | deepseek | anthropic | custom

    @abc.abstractmethod
    async def plan(self, req: PlanRequest) -> PlanResponse: ...

    @abc.abstractmethod
    async def run_task(self, req: RunTaskRequest) -> RunTaskResponse: ...

    @abc.abstractmethod
    async def consult(self, req: ConsultRequest) -> ConsultResponse: ...

    @abc.abstractmethod
    async def synthesize(self, req: SynthesizeRequest) -> SynthesizeResponse: ...

    model: str | None = None  # model used for pricing (None = default rate)

    async def plan_phases(self, req: PlanPhasesRequest) -> PlanPhasesResponse:
        """Hierarchical planning, step 1: the goal as phases. Default: the deterministic simulated planner."""
        from .hier_plan import sim_plan_phases

        return sim_plan_phases(req)

    async def plan_phase(self, req: PlanPhaseRequest) -> PlanPhaseResponse:
        """Hierarchical planning, step 2: ONE phase as tasks. Default: the deterministic simulated planner."""
        from .hier_plan import sim_plan_phase

        return sim_plan_phase(req)

    async def review(self, req: ReviewRequest) -> ReviewResponse:
        """Quality review of ONE task output (Q1). Default: the deterministic simulated reviewer."""
        from .review import sim_review

        return sim_review(req)

    async def replan(self, req):
        """Replacement sub-plan for a failed node (Q2). Default: the deterministic simulated replanner."""
        from .replan import sim_replan

        return sim_replan(req)

    async def route(self, req: RouteRequest) -> RouteResponse:
        """Who should answer a chat message (docs/architecture/chat-routing.md). Default: the rules."""
        from .routing import rules_route

        return rules_route(req)

    async def chat_reply(self, req: ChatReplyRequest) -> ChatReplyResponse:
        """One short, human reply of ONE agent. Default: scripted (no LLM, tiny simulated usage)."""
        from .routing import compose_reply
        from .models import Usage

        text, kind, consult = compose_reply(req)
        return ChatReplyResponse(text=text, kind=kind, consult=consult,
                                 usage=Usage(model="simulation-claude-sonnet", input_tokens=0, output_tokens=0,
                                             cost_usd=0.0, duration_ms=0))

    async def estimate(self, req: "EstimateRequest") -> "EstimateResponse":
        """Cost range for a plan, before running it. Never calls the LLM."""
        from .estimate import estimate_plan

        return estimate_plan(req, mode=self.mode, model=self.model)


def _truthy(v: str | None) -> bool:
    return (v or "").strip().lower() in {"1", "true", "yes", "on", "si"}


def select_provider() -> str:
    """simulation | <first configured provider>. SIMULATION=true wins; then MODEL_PROVIDER_ORDER
    (default deepseek, anthropic, custom) over the providers that have credentials."""
    if _truthy(os.getenv("SIMULATION")):
        return "simulation"
    configs = configured_providers()
    return configs[0].id if configs else "simulation"


def select_engine() -> AgentEngine:
    from .simulation import SimulationEngine

    if select_provider() == "simulation":
        return SimulationEngine()
    try:
        from .crewai_engine import CrewAIEngine

        return CrewAIEngine(providers=configured_providers())
    except Exception as exc:  # CrewAI not installed / incompatible
        log.warning("CrewAI unavailable (%s); using SimulationEngine", exc)
        return SimulationEngine()
