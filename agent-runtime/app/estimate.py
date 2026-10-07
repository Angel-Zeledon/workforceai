"""Pre-execution cost estimation (POST /v1/estimate).

An LLM bill cannot be known before the call, so this module returns a RANGE
(min..max) per task and for the whole plan, never a single number. Rates come
from engine.py (`_price_for` / MODEL_PRICES), the same table that prices real
usage, so an estimate and the final bill always use the same tariff.

Two bases:
- "simulation": the simulation engine draws tokens from fixed ranges, so the
  estimate uses exactly those ranges (the real simulated cost is guaranteed to
  fall inside [min, max]; covered by tests).
- "token_heuristic": live mode. Input is approximated from text length and the
  number of dependencies; output is bounded by the configured max_tokens. The
  max also assumes one JSON-repair retry (the live engine retries once on
  invalid JSON) and up to LIVE_MAX_CONSULTS consultations.
"""
from __future__ import annotations

from typing import Literal

from pydantic import Field, field_validator

from .engine import _price_for, estimate_cost
from .models import Locale, _Base, normalize_locale

Basis = Literal["simulation", "token_heuristic"]

# --- simulation ranges: keep in sync with SimulationEngine (run_task/consult/synthesize) ---
SIM_RUN_IN = (1400, 3600)  # plus SIM_RUN_IN_PER_DEP per dependency output
SIM_RUN_IN_PER_DEP = (600, 900)
SIM_RUN_OUT = (450, 1300)
SIM_CONSULT_IN = (500, 1200)
SIM_CONSULT_OUT = (120, 380)
SIM_MAX_CONSULTS = 1  # the scripted content has at most one consult per task
SIM_SYNTH_IN = (1400, 3600)  # synthesize uses the default _usage ranges
SIM_SYNTH_OUT = (450, 1300)

# --- live heuristics ---
LIVE_PROMPT_OVERHEAD_TOKENS = 700  # system rules + persona + schema instructions
LIVE_DEP_TOKENS = (300, 1500)  # per dependency output (the backend truncates to 6000 in total)
LIVE_DEP_TOKENS_CAP = 6000
LIVE_MEMORY_TOKENS = (0, 2000)
LIVE_OUT_MIN = 300
LIVE_MAX_TOKENS = 4096  # LLM max_tokens configured in CrewAIEngine
LIVE_MAX_CONSULTS = 3
LIVE_MAX_ATTEMPTS = 2  # one retry on invalid JSON
LIVE_CONSULT_IN = (400, 2000)
LIVE_CONSULT_OUT = (100, 800)
CHARS_PER_TOKEN = 3.5


class EstimateTaskIn(_Base):
    id: str = ""
    key: str = ""
    title: str = ""
    description: str = ""
    agent_id: str = ""
    depends_on: list[str] = Field(default_factory=list)


class EstimateRequest(_Base):
    request_text: str = ""
    tasks: list[EstimateTaskIn] = Field(default_factory=list)
    locale: Locale = "es"

    _norm = field_validator("locale", mode="before")(normalize_locale)


class CostRange(_Base):
    min_usd: float
    max_usd: float


class TaskEstimate(_Base):
    id: str
    title: str
    agent_id: str
    min_usd: float
    max_usd: float
    input_tokens_min: int
    input_tokens_max: int
    output_tokens_min: int
    output_tokens_max: int


class Rates(_Base):
    input_per_m: float
    output_per_m: float


class EstimateResponse(_Base):
    mode: Literal["simulation", "live"]
    model: str
    basis: Basis
    currency: str = "USD"
    rates: Rates
    tasks: list[TaskEstimate]
    synthesis: CostRange
    total: CostRange


def _cost(model: str | None, tin: int, tout: int) -> float:
    return estimate_cost(tin, tout, model)


def _task_range(model: str | None, mode: str, t: EstimateTaskIn) -> TaskEstimate:
    n = len(t.depends_on)
    if mode == "simulation":
        in_min = SIM_RUN_IN[0] + SIM_RUN_IN_PER_DEP[0] * n
        in_max = SIM_RUN_IN[1] + SIM_RUN_IN_PER_DEP[1] * n
        out_min, out_max = SIM_RUN_OUT
        lo = _cost(model, in_min, out_min)
        hi = _cost(model, in_max, out_max) + SIM_MAX_CONSULTS * _cost(model, SIM_CONSULT_IN[1], SIM_CONSULT_OUT[1])
        return TaskEstimate(
            id=t.id, title=t.title, agent_id=t.agent_id, min_usd=round(lo, 5), max_usd=round(hi, 5),
            input_tokens_min=in_min, input_tokens_max=in_max + SIM_MAX_CONSULTS * SIM_CONSULT_IN[1],
            output_tokens_min=out_min, output_tokens_max=out_max + SIM_MAX_CONSULTS * SIM_CONSULT_OUT[1])
    text_tokens = int((len(t.title) + len(t.description)) / CHARS_PER_TOKEN) + 1
    deps_min = min(LIVE_DEP_TOKENS[0] * n, LIVE_DEP_TOKENS_CAP)
    deps_max = min(LIVE_DEP_TOKENS[1] * n, LIVE_DEP_TOKENS_CAP)
    in_min = LIVE_PROMPT_OVERHEAD_TOKENS + text_tokens + deps_min + LIVE_MEMORY_TOKENS[0]
    in_max = LIVE_PROMPT_OVERHEAD_TOKENS + text_tokens + deps_max + LIVE_MEMORY_TOKENS[1]
    lo = _cost(model, in_min, LIVE_OUT_MIN)
    one_call_hi = _cost(model, in_max, LIVE_MAX_TOKENS)
    consult_hi = LIVE_MAX_CONSULTS * _cost(model, LIVE_CONSULT_IN[1], LIVE_CONSULT_OUT[1])
    hi = LIVE_MAX_ATTEMPTS * one_call_hi + consult_hi
    return TaskEstimate(
        id=t.id, title=t.title, agent_id=t.agent_id, min_usd=round(lo, 5), max_usd=round(hi, 5),
        input_tokens_min=in_min,
        input_tokens_max=LIVE_MAX_ATTEMPTS * in_max + LIVE_MAX_CONSULTS * LIVE_CONSULT_IN[1],
        output_tokens_min=LIVE_OUT_MIN,
        output_tokens_max=LIVE_MAX_ATTEMPTS * LIVE_MAX_TOKENS + LIVE_MAX_CONSULTS * LIVE_CONSULT_OUT[1])


def _synthesis_range(model: str | None, mode: str, n_tasks: int) -> CostRange:
    if mode == "simulation":
        return CostRange(min_usd=round(_cost(model, SIM_SYNTH_IN[0], SIM_SYNTH_OUT[0]), 5),
                         max_usd=round(_cost(model, SIM_SYNTH_IN[1], SIM_SYNTH_OUT[1]), 5))
    tin_min = LIVE_PROMPT_OVERHEAD_TOKENS + 300 * n_tasks
    tin_max = LIVE_PROMPT_OVERHEAD_TOKENS + 1500 * n_tasks
    return CostRange(min_usd=round(_cost(model, tin_min, LIVE_OUT_MIN), 5),
                     max_usd=round(LIVE_MAX_ATTEMPTS * _cost(model, tin_max, LIVE_MAX_TOKENS), 5))


def estimate_plan(req: EstimateRequest, *, mode: str, model: str | None) -> EstimateResponse:
    """Range estimate for the tasks of a plan plus the final synthesis."""
    tasks = [_task_range(model, mode, t) for t in req.tasks]
    synth = _synthesis_range(model, mode, len(tasks))
    p_in, p_out = _price_for(model)
    return EstimateResponse(
        mode="simulation" if mode == "simulation" else "live",
        model=model or "default",
        basis="simulation" if mode == "simulation" else "token_heuristic",
        rates=Rates(input_per_m=p_in, output_per_m=p_out),
        tasks=tasks,
        synthesis=synth,
        total=CostRange(min_usd=round(sum(t.min_usd for t in tasks) + synth.min_usd, 5),
                        max_usd=round(sum(t.max_usd for t in tasks) + synth.max_usd, 5)),
    )
