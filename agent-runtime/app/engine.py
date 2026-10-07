"""Capa de abstraccion propia sobre el framework de agentes."""
from __future__ import annotations

import abc
import logging
import os
from typing import TYPE_CHECKING

from .providers import configured_providers
from .models import (
    ConsultRequest,
    ConsultResponse,
    PlanRequest,
    PlanResponse,
    RunTaskRequest,
    RunTaskResponse,
    SynthesizeRequest,
    SynthesizeResponse,
)

if TYPE_CHECKING:  # avoid a circular import (estimate.py imports engine.py)
    from .estimate import EstimateRequest, EstimateResponse

log = logging.getLogger("agent_runtime")

# USD per million tokens (input, output), keyed by model name without the "provider/" prefix.
# DeepSeek (https://api-docs.deepseek.com/quick_start/pricing, checked 2026-10): the page no longer lists
# deepseek-chat/deepseek-reasoner; their aliases bill as Flash. We use the PEAK rate (highest, cache miss)
# so the budget is never underestimated: Flash 0.30 in / 1.20 out; V4 Pro 1.32 in / 3.96 out.
# Verify and adjust if they change, or force values with PRICE_IN_PER_M / PRICE_OUT_PER_M.
MODEL_PRICES: dict[str, tuple[float, float]] = {
    "deepseek-chat": (0.30, 1.20),
    "deepseek-reasoner": (0.30, 1.20),
    "deepseek-flash": (0.30, 1.20),
    "deepseek-v4-flash": (0.30, 1.20),
    "deepseek-v4-pro": (1.32, 3.96),
}
# Default rate (Claude Sonnet): unknown model or simulation.
PRICE_IN_PER_M = 3.0
PRICE_OUT_PER_M = 15.0


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
    return MODEL_PRICES.get(key, (PRICE_IN_PER_M, PRICE_OUT_PER_M))


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
