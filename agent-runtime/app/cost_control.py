"""Cost control for live LLM calls (docs/cost-reduction.md).

Four independent pieces, all env-configurable and all free of network access:

1. Output caps per call kind (`max_tokens_for`): plan, run_task, consult, synthesize, ...
2. Loop caps for the CrewAI agent (`crew_max_iter`, `crew_max_retry_limit`): an agent without tools must
   answer in one pass; the framework default of 25 iterations re-sends a growing conversation every time.
3. No reasoning/thinking mode unless asked (`reasoning_enabled`, `reasoning_kwargs`, `non_reasoning_model`).
4. A budget guard BEFORE each call (`preflight`): estimate = input tokens (from the prompt text) priced at the
   model's input rate + max_tokens priced at its output rate, using the same price table as the backend
   (model_prices.json, byte-identical in both services). If it would cross the allowance the backend sent
   (`max_cost_usd`), the call is refused with a stable error code and nothing is sent to the provider.
"""
from __future__ import annotations

import logging
import math
import os

log = logging.getLogger("agent_runtime")

# Output-token cap per kind of call. Env override: MAX_TOKENS_<KIND> (e.g. MAX_TOKENS_RUN_TASK=2500).
# Defaults are sized for the structured JSON each kind returns, not for the model's maximum.
DEFAULT_MAX_TOKENS: dict[str, int] = {
    "plan": 3000,
    "plan_phases": 2048,
    "plan_phase": 4096,
    "run_task": 4000,
    "consult": 800,
    "synthesize": 6000,
    "review": 1500,
    "summarize": 2000,
    "route": 300,
    "chat": 350,
    "replan": 3000,
    "counterargument": 1200,
    "debate": 1600,
    "default": 3000,
}

# Tokens the framework wrapper (CrewAI role/goal/backstory template, format instructions) adds around a prompt.
WRAPPER_TOKENS = 350
CHARS_PER_TOKEN = 3.5  # conservative (more tokens per char than the 4 used for trimming): the guard must not under-estimate

ERR_CALL_BUDGET = "call_budget_exceeded"


def _env_int(name: str, default: int, *, minimum: int = 1) -> int:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    try:
        v = int(raw)
    except ValueError:
        log.warning("invalid %s=%r; using %d", name, raw, default)
        return default
    return v if v >= minimum else default


def _truthy(name: str, default: bool) -> bool:
    raw = os.getenv(name, "").strip().lower()
    if not raw:
        return default
    return raw in {"1", "true", "yes", "on", "si"}


def max_tokens_for(kind: str) -> int:
    """Output cap for a kind of call (MAX_TOKENS_<KIND> env, else the table, else MAX_TOKENS_DEFAULT)."""
    kind = (kind or "default").lower()
    base = DEFAULT_MAX_TOKENS.get(kind, DEFAULT_MAX_TOKENS["default"])
    if kind not in DEFAULT_MAX_TOKENS:
        base = _env_int("MAX_TOKENS_DEFAULT", base)
    return _env_int(f"MAX_TOKENS_{kind.upper()}", base)


def crew_max_iter() -> int:
    """Iterations the CrewAI agent may spend on ONE task (framework default: 25). CREW_MAX_ITER."""
    return _env_int("CREW_MAX_ITER", 3)


def crew_max_retry_limit() -> int:
    """Framework-level retries after an error (framework default: 2). CREW_MAX_RETRY_LIMIT; 0 is valid."""
    return _env_int("CREW_MAX_RETRY_LIMIT", 1, minimum=0)


def direct_call_enabled() -> bool:
    """LLM_DIRECT_CALL: one plain chat completion instead of the CrewAI Agent/Task/Crew loop (no ReAct wrapper)."""
    return _truthy("LLM_DIRECT_CALL", False)


def fast_path_enabled() -> bool:
    """PLANNER_FAST_PATH (default on): simple requests skip plan decomposition."""
    return _truthy("PLANNER_FAST_PATH", True)


def preflight_enabled() -> bool:
    return _truthy("BUDGET_PREFLIGHT", True)


# ------------------------------------------------------------------ reasoning / thinking
def reasoning_enabled() -> bool:
    """LLM_REASONING=true asks the provider for its reasoning/thinking mode. Off by default: it bills
    thousands of hidden output tokens for tasks that need none."""
    return _truthy("LLM_REASONING", False)


_REASONING_ALIAS = {"deepseek-reasoner": "deepseek-chat"}


def non_reasoning_model(model: str) -> str:
    """The same provider's plain model when `model` is a reasoning-only alias and reasoning was not asked for."""
    if reasoning_enabled():
        return model
    prefix, sep, name = model.rpartition("/")
    return f"{prefix}{sep}{_REASONING_ALIAS.get(name.lower(), name)}"


def reasoning_kwargs(provider: str, model: str) -> dict[str, str]:
    """Extra LLM kwargs that keep reasoning minimal. Nothing is sent for providers without such a knob
    (Anthropic extended thinking is opt-in: simply never requested; DeepSeek chat does not think)."""
    if reasoning_enabled():
        return {}
    name = model.split("/")[-1].lower()
    if provider == "openai" and name.startswith("gpt-5"):
        return {"reasoning_effort": "minimal"}
    if provider == "openai" and (name.startswith("o1") or name.startswith("o3") or name.startswith("o4")):
        return {"reasoning_effort": "low"}
    return {}


# ------------------------------------------------------------------ pre-call estimate and guard
def estimate_tokens(*texts: str) -> int:
    """Input tokens of the text(s) as the guard sees them (chars / 3.5, rounded up) plus the framework wrapper."""
    chars = sum(len(t or "") for t in texts)
    return int(math.ceil(chars / CHARS_PER_TOKEN)) + WRAPPER_TOKENS


class CallBudgetError(Exception):
    """The estimated cost of ONE call is above the allowance the backend gave it. Nothing was sent."""

    code = ERR_CALL_BUDGET
    status_code = 402

    def __init__(self, *, kind: str, model: str, input_tokens: int, max_tokens: int, estimated_usd: float,
                 allowance_usd: float):
        self.kind, self.model = kind, model
        self.input_tokens, self.max_tokens = input_tokens, max_tokens
        self.estimated_usd, self.allowance_usd = estimated_usd, allowance_usd
        super().__init__(
            f"call budget exceeded before sending ({kind}, {model}): estimated ${estimated_usd:.4f} "
            f"(~{input_tokens} in + up to {max_tokens} out) > allowance ${allowance_usd:.4f}")

    def detail(self) -> dict:
        return {"code": self.code, "message": str(self), "kind": self.kind, "model": self.model,
                "estimated_input_tokens": self.input_tokens, "max_output_tokens": self.max_tokens,
                "estimated_usd": round(self.estimated_usd, 5), "allowance_usd": round(self.allowance_usd, 5)}


def call_cost_upper_bound(model: str | None, provider: str | None, input_tokens: int, max_tokens: int) -> float:
    """Worst-case USD of one call: the prompt plus the whole output cap, at the shared price table."""
    from .engine import estimate_cost

    return estimate_cost(input_tokens, max_tokens, model, provider)


def preflight(*, kind: str, model: str | None, provider: str | None, prompt_texts: list[str], max_tokens: int,
              allowance_usd: float | None) -> float:
    """Refuse a call that would cross the allowance. Returns the estimate (USD).

    allowance_usd None or <= 0 means "no cap sent": the estimate is returned and nothing is refused.
    """
    tin = estimate_tokens(*prompt_texts)
    est = call_cost_upper_bound(model, provider, tin, max_tokens)
    if preflight_enabled() and allowance_usd and allowance_usd > 0 and est > allowance_usd + 1e-9:
        raise CallBudgetError(kind=kind, model=model or "", input_tokens=tin, max_tokens=max_tokens,
                              estimated_usd=est, allowance_usd=allowance_usd)
    return est
