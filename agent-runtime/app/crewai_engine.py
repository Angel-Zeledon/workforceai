"""CrewAIEngine: un Agent/Task/Crew de CrewAI por rol.

CrewAI se importa de forma perezosa en __init__ para que el resto del runtime
(SimulationEngine, API) funcione aunque CrewAI no este instalado.

Seguridad: los agentes se crean SIN herramientas (tools=[]) y sin delegacion;
el runtime nunca ejecuta nada. Las solicitudes de herramienta salen del modelo
como datos (tool_requests) y las decide el backend.
"""
from __future__ import annotations

import asyncio
import contextvars
import json
import logging
import os
import re
import time
from typing import Any

from pydantic import BaseModel, Field

from .cost_control import (
    CallBudgetError,
    crew_max_iter,
    crew_max_retry_limit,
    direct_call_enabled,
    call_deadline_s,
    max_retry_wait_s,
    max_tokens_for,
    non_reasoning_model,
    output_cap,
    preflight,
    reasoning_kwargs,
)
from .engine import AgentEngine, estimate_cost
from . import rate_limit
from .usage_tokens import tokens_from_metrics, tokens_from_raw_list
from .fast_path import try_fast_plan
from .hier_plan import normalize_phase_tasks, normalize_phases
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
    ReplyConsult,
    ReviewRequest,
    ReviewResponse,
    RouteRequest,
    RouteResponse,
    RunTaskRequest,
    RunTaskResponse,
    Section,
    StructuredOutput,
    SynthesizeRequest,
    SynthesizeResponse,
    TaskResult,
    Usage,
)
from .providers import (
    _api_key,
    DEFAULT_MODELS,
    ERR_ALL_PROVIDERS_FAILED,
    ERR_NO_BALANCE,
    ERR_RATE_LIMITED,
    ProviderConfig,
    ProviderError,
    ProviderPolicy,
    candidates,
    classify_error,
    fixed_temperature_model,
    is_no_balance,
    is_overloaded,
    is_rate_limited,
    is_transient,
    retry_after_from_exc,
    policy_from_request,
    scrub,
    temperature_for,
    vision_candidates,
)
from .vision import (
    NOTICE_VISION_UNAVAILABLE,
    build_vision_messages,
    estimate_image_tokens,
    prepare_images,
    unavailable_note,
)
from .review import build_review_prompt, normalize_review
from .routing import compose_reply, detect_locale, rules_route, validate_route
from .security import (
    build_chat_prompt,
    build_chat_system_prompt,
    build_route_prompt,
    language_rule,
    system_rules,
    build_consult_prompt,
    build_synthesis_prompt,
    build_system_prompt,
    build_task_prompt,
    wrap_untrusted,
)

DEFAULT_MODEL = DEFAULT_MODELS["anthropic"]
log = logging.getLogger("agent_runtime")

# Same-provider retries for transient errors (timeout / 5xx / network) before failing over.
PROVIDER_RETRIES = max(0, int(os.getenv("PROVIDER_RETRIES", "1") or 1))
PROVIDER_RETRY_BACKOFF_S = float(os.getenv("PROVIDER_RETRY_BACKOFF_S", "1.0") or 1.0)
PROVIDER_TIMEOUT_S = float(os.getenv("PROVIDER_TIMEOUT_S", "120") or 120)
# Same-provider retries for a RATE LIMIT (429): each waits what the provider asked for (cost_control.max_retry_wait_s
# bounds it per kind of call). This is the only retry layer: the SDK's own retries are off (max_retries=0 below).
PROVIDER_RATE_LIMIT_RETRIES = max(0, int(os.getenv("PROVIDER_RATE_LIMIT_RETRIES", "2") or 0))
# A busy provider (Gemini 503 "high demand", Anthropic 529): retried with exponential backoff (2 s, 4 s, ...), each wait
# bounded like a rate limit's (short for chat). Google answers these often on the free tier; they pass in seconds.
PROVIDER_OVERLOAD_RETRIES = max(0, int(os.getenv("PROVIDER_OVERLOAD_RETRIES", "2") or 0))
PROVIDER_OVERLOAD_BACKOFF_S = float(os.getenv("PROVIDER_OVERLOAD_BACKOFF_S", "2.0") or 2.0)
# Per-request timeout for the call being made now (an interactive call's remaining deadline); None = PROVIDER_TIMEOUT_S.
# A contextvar so _llm_kwargs sees it without changing the signature of every `once` (set in the worker thread).
_CALL_TIMEOUT: contextvars.ContextVar[float | None] = contextvars.ContextVar("call_timeout", default=None)
MIN_CALL_S = 3.0  # no request is started with less time than this left before the deadline
# A JSON answer whose output used at least this share of its cap was cut by the cap: retrying with the same cap
# repeats the cut, so the one JSON retry doubles it instead.
TRUNCATION_SHARE = 0.97


class _UsageTap:
    """Per-request provider usage of ONE call, recorded as it happens.

    Two live findings (2026-11-02, CrewAI 1.x native OpenAI client, Gemini):
      * LLM.get_token_usage_summary() is cumulative for the life of the LLM object; with a cached LLM every call
        reported the tokens of all the earlier calls of the same kind (third run_task: 4999 in / 1424 out for a
        1802 / 506 call). The engine now builds one LLM per call.
      * CrewAI rebuilds total_tokens as prompt + completion, so Gemini's thinking tokens (billed as output, only
        visible in the provider's total_tokens) were dropped: a route that thought 286 tokens reported 10.
    The tap wraps the instance's _track_token_usage_internal and keeps every raw usage dict (provider total
    included). Falls back to the framework metrics when the LLM object cannot be tapped (test doubles)."""

    def __init__(self, llm: Any):
        self.raw: list[dict] = []
        self.ok = False
        orig = getattr(llm, "_track_token_usage_internal", None)
        if callable(orig):
            def track(usage_data: Any, _orig: Any = orig) -> Any:
                if isinstance(usage_data, dict):
                    self.raw.append(dict(usage_data))
                return _orig(usage_data)
            try:
                object.__setattr__(llm, "_track_token_usage_internal", track)
                self.ok = True
            except Exception:  # pragma: no cover - a frozen/slotted object: metrics fallback
                self.ok = False

    def tokens(self, cfg: ProviderConfig, metrics: Any = None) -> tuple[int, int, int]:
        if self.ok and self.raw:
            return tokens_from_raw_list(self.raw)
        return tokens_from_metrics(metrics, cfg.id)


def _truncated(output_tokens: int, cap: int | None) -> bool:
    return bool(cap) and output_tokens >= TRUNCATION_SHARE * int(cap)


def parse_json_output(raw: str, schema: type[BaseModel]) -> Any:
    """Tolerant parsing: strip ```json fences and extract the first {...} object if there is surrounding text.
    Raises ValueError (JSONDecodeError/ValidationError) when invalid."""
    text = (raw or "").strip()
    m = re.search(r"```(?:json)?\s*(.*?)```", text, re.DOTALL | re.IGNORECASE)
    if m:
        text = m.group(1).strip()
    try:
        return schema.model_validate(json.loads(text))
    except json.JSONDecodeError:
        i, j = text.find("{"), text.rfind("}")
        if i < 0 or j <= i:
            raise
        return schema.model_validate(json.loads(text[i:j + 1]))


def native_structured_output(cfg: ProviderConfig) -> bool:
    """Whether the provider accepts a JSON-schema response_format (structured output).
    Anthropic and OpenAI do. DeepSeek and Kimi (Moonshot) only support {"type": "json_object"};
    custom OpenAI-compatible endpoints vary, so they also get the schema in the prompt."""
    return cfg.id in ("anthropic", "openai")


MAX_SCHEMA_DESCRIPTION_CHARS = 320  # longer prose is dropped from the schema sent in the prompt


def _compact_schema(node: Any) -> Any:
    """The JSON Schema without prose: "title" and "description" repeat what the field names and the task rules
    already say (the ToolRequest.args description alone was ~900 chars). Keys named title/description that are
    real properties are kept."""
    if isinstance(node, dict):
        props = node.get("properties") if isinstance(node.get("properties"), dict) else None
        out = {}
        for k, v in node.items():
            if k == "title" and not isinstance(v, (dict, list)):
                continue
            if k == "description" and isinstance(v, str) and len(v) > MAX_SCHEMA_DESCRIPTION_CHARS:
                continue
            out[k] = {pk: _compact_schema(pv) for pk, pv in v.items()} if (k == "properties" and props is not None) \
                else _compact_schema(v)
        return out
    if isinstance(node, list):
        return [_compact_schema(x) for x in node]
    return node


def json_instructions(schema: type[BaseModel]) -> str:
    """Prompt-side contract for providers without structured output."""
    return ("\n\nResponde SOLO con un objeto JSON valido (sin texto antes ni despues, sin ```), "
            "que cumpla exactamente este JSON Schema:\n"
            + json.dumps(_compact_schema(schema.model_json_schema()), ensure_ascii=False, separators=(",", ":")))


class _ConsultAnswer(BaseModel):
    answer: str


class _RouteLLM(BaseModel):
    intent: str
    topic: str = "general"
    primary_agent_id: str
    contributor_agent_ids: list[str] = Field(default_factory=list)
    reasons: dict[str, str] = Field(default_factory=dict)


class _SummaryLLM(BaseModel):
    text: str


class _ChatLLM(BaseModel):
    text: str
    consult_to_agent_id: str | None = None
    consult_question: str | None = None


class _ReviewLLM(BaseModel):
    verdict: str
    reasons: list[str] = Field(default_factory=list)
    criteria: list[dict[str, Any]] = Field(default_factory=list)
    evidence: list[str] = Field(default_factory=list)


class _CounterargumentLLM(BaseModel):
    counterargument: str
    risks: list[str] = Field(default_factory=list)
    checks: list[str] = Field(default_factory=list)


class _DebateTurnLLM(BaseModel):
    agent_id: str
    kind: str
    text: str


class _DebateSynthLLM(BaseModel):
    choice: str = ""
    rationale: str = ""
    dissent: list[str] = Field(default_factory=list)


class _DebateLLM(BaseModel):
    options: list[str] = Field(default_factory=list)
    turns: list[_DebateTurnLLM] = Field(default_factory=list)
    synthesis: _DebateSynthLLM = Field(default_factory=_DebateSynthLLM)


class _Synth(BaseModel):
    title: str
    summary: str
    sections: list[Section] = Field(default_factory=list)


class CrewAIEngine(AgentEngine):
    mode = "live"

    def __init__(self, model: str | None = None, api_key: str | None = None, provider: str = "anthropic",
                 providers: list[ProviderConfig] | None = None, allow_empty: bool = False):
        from crewai import LLM, Agent, Crew, Process, Task  # import aislado

        self._LLM, self._Agent, self._Task, self._Crew, self._Process = LLM, Agent, Task, Crew, Process
        if providers is None:  # legacy single-provider construction
            name = model or os.getenv("MODEL") or DEFAULT_MODELS[provider]
            if "/" not in name:
                name = f"{provider}/{name}"
            env_name = "DEEPSEEK_API_KEY" if provider == "deepseek" else "ANTHROPIC_API_KEY"
            api_key = api_key or _api_key(provider)
            if not api_key:
                raise RuntimeError(f"{env_name} requerido para CrewAIEngine")
            providers = [ProviderConfig(id=provider, model=name, api_key=api_key)]
        if not providers and not allow_empty:
            raise RuntimeError("no model provider configured for CrewAIEngine")
        self.providers = list(providers)
        # allow_empty: an engine that only serves requests carrying the organization's own key
        self.provider = self.providers[0].id if self.providers else "byo"  # primary (reported by /healthz)
        self.model = self.providers[0].model if self.providers else ""
        self._llms: dict[str, Any] = {}

    def _llm_for(self, cfg: ProviderConfig, max_tokens: int | None = None, creativity: str = "") -> Any:
        """A NEW LLM object for every call. It used to be cached per (provider, cap, temperature), but CrewAI keeps
        lifetime token counters on the object: a cached LLM billed each call for every earlier call of its kind and
        mixed concurrent calls (see _UsageTap). Building one is cheap (no network); an organization's own key was
        never cached anyway. self._llms stays empty (kept for code that inspects it)."""
        return self._LLM(**self._llm_kwargs(cfg, max_tokens, temperature_for(cfg.id, creativity)))

    @staticmethod
    def _llm_kwargs(cfg: ProviderConfig, max_tokens: int | None, temperature: float | None) -> dict[str, Any]:
        # No reasoning/thinking unless LLM_REASONING is set (cost_control): a reasoning alias maps to the plain model.
        kw: dict[str, Any] = {"model": non_reasoning_model(cfg.model), "api_key": cfg.api_key, "temperature": temperature,
                              "max_tokens": max_tokens or max_tokens_for("default"),
                              "timeout": min(PROVIDER_TIMEOUT_S, _CALL_TIMEOUT.get() or PROVIDER_TIMEOUT_S),
                              # The OpenAI/Anthropic SDKs retry 429/5xx twice on their own, blind to Gemini's retryDelay
                              # and invisible to the budget: the runtime's _kickoff is the only retry layer.
                              "max_retries": 0}
        kw.update(reasoning_kwargs(cfg.id, cfg.model))
        if temperature is None or (cfg.id == "openai" and fixed_temperature_model(cfg.model)):
            kw.pop("temperature")  # reasoning models only accept their default
        if cfg.id in ("deepseek", "kimi", "gemini"):
            # Live finding (DeepSeek): long consolidations came back as invalid JSON (unescaped quotes
            # around char 13k). JSON mode guarantees syntactically valid JSON (the schema still goes in
            # the instructions). Moonshot's Kimi has the same json_object mode and no json_schema; Gemini's
            # OpenAI-compatible endpoint takes json_object as well (verified live 2026-10-27).
            kw["response_format"] = {"type": "json_object"}
            # (the old floor of 8192 output tokens is gone: every kind of call has its own env-configurable cap)
        if cfg.base_url:
            kw["base_url"] = cfg.base_url
        if cfg.id == "openai":
            if cfg.org_id:
                kw["organization"] = cfg.org_id
            if cfg.project_id:
                kw["project"] = cfg.project_id
            if fixed_temperature_model(cfg.model):
                # Live finding (production, gpt-5-mini, 2026-11-03): OpenAI's reasoning models (gpt-5, o-series)
                # reject max_tokens with a 400 and cap the answer with max_completion_tokens instead.
                kw["max_completion_tokens"] = kw.pop("max_tokens")
        return kw

    def test_provider(self, cfg: ProviderConfig) -> dict[str, Any]:
        """A tiny real call with the given credential. Never raises: returns a stable code
        (ok | invalid_key | quota | model_not_found | network | unknown) and a redacted message."""
        t0 = time.monotonic()
        try:
            kw = self._llm_kwargs(cfg, 64, None)
            kw["timeout"] = min(PROVIDER_TIMEOUT_S, 30)
            kw.pop("response_format", None)  # a plain text probe
            kw["max_completion_tokens" if "max_completion_tokens" in kw else "max_tokens"] = 64
            self._LLM(**kw).call([{"role": "user", "content": "Reply with the single word: OK"}])
            code, msg = "ok", "Connection works."
        except Exception as exc:  # classified, redacted, never propagated
            code = classify_error(exc)
            msg = scrub(f"{type(exc).__name__}: {exc}", [cfg])[:300]
            log.warning("provider test %s failed: %s", cfg.id, code)
        return {"ok": code == "ok", "code": code, "message": msg, "provider": cfg.id, "model": cfg.model,
                "latency_ms": int((time.monotonic() - t0) * 1000)}

    # ------------------------------------------------------------ nucleo
    @staticmethod
    def _usage(cfg: ProviderConfig, tokens: tuple[int, int, int], t0: float, **extra: Any) -> Usage:
        i, c, o = tokens
        return Usage(model=cfg.model, input_tokens=i, output_tokens=o, cost_usd=estimate_cost(i, o, cfg.model, cfg.id, c),
                     duration_ms=int((time.monotonic() - t0) * 1000), provider=cfg.id, cached_tokens=c or None, **extra)

    @classmethod
    def _spent(cls, exc: BaseException, cfg: ProviderConfig, tokens: tuple[int, int, int], t0: float) -> BaseException:
        """Attach to a failed attempt the tokens it already burned (the first answer of a JSON retry, say), so
        _kickoff can charge them against the call's allowance before it retries or falls back."""
        if tokens[0] > 0 or tokens[2] > 0:
            exc.usage = cls._usage(cfg, tokens, t0)  # type: ignore[attr-defined]
        return exc

    def _crew_for(self, cfg: ProviderConfig, cap: int | None, *, role: str, goal: str, backstory: str,
                  description: str, expected: str, schema: type[BaseModel], creativity: str) -> tuple[Any, _UsageTap]:
        llm = self._llm_for(cfg, cap, creativity)
        tap = _UsageTap(llm)
        agent = self._Agent(role=role, goal=goal, backstory=backstory, llm=llm,
                            tools=[], allow_delegation=False, verbose=False,
                            # A tool-less agent answers in one pass. The framework default (25 iterations) re-sends the
                            # whole growing conversation on every format retry: the main source of the ~76k input tokens.
                            max_iter=crew_max_iter(), max_retry_limit=crew_max_retry_limit(), reasoning=False)
        if native_structured_output(cfg):
            task = self._Task(description=description, expected_output=expected,
                              agent=agent, output_pydantic=schema)
        else:
            # Providers without JSON-schema response_format (DeepSeek answers 400 "This
            # response_format type is unavailable"): the schema goes into the instructions and
            # the raw answer is parsed and validated below (one retry on invalid JSON).
            task = self._Task(description=description, expected_output=expected + json_instructions(schema),
                              agent=agent)
        return self._Crew(agents=[agent], tasks=[task], process=self._Process.sequential, verbose=False), tap

    def _kickoff_once(self, cfg: ProviderConfig, *, role: str, goal: str, backstory: str, description: str,
                      expected: str, schema: type[BaseModel], max_tokens: int | None = None,
                      creativity: str = "") -> tuple[Any, Usage]:
        """One CrewAI call; on invalid JSON one retry: the same crew (its LLM counts both requests), or, when the
        answer used its whole output cap (cut, not malformed), a new crew with the cap doubled."""
        args = dict(role=role, goal=goal, backstory=backstory, description=description, expected=expected,
                    schema=schema, creativity=creativity)
        crew, tap = self._crew_for(cfg, max_tokens, **args)
        t0 = time.monotonic()
        done = (0, 0, 0)  # tokens of crews already finished with (a truncated first attempt)
        result = None

        def burned() -> tuple[int, int, int]:
            now = tap.tokens(cfg, getattr(result, "token_usage", None))
            return done[0] + now[0], done[1] + now[1], done[2] + now[2]

        try:
            result = crew.kickoff()
            parsed = getattr(result, "pydantic", None)
            if parsed is None:  # tolerant parse of the raw text + one retry if the JSON is invalid
                try:
                    parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
                except ValueError:
                    first = burned()
                    if _truncated(first[2], max_tokens):
                        log.warning("provider %s: answer cut at its %s-token cap; retrying with %s", cfg.id,
                                    max_tokens, 2 * int(max_tokens or 0))
                        done, result = first, None
                        crew, tap = self._crew_for(cfg, 2 * int(max_tokens or 0), **args)
                    else:  # the retry doubles the call's cost: never silent (live: one in 30 DeepSeek run_tasks)
                        log.warning("provider %s: invalid JSON for %s (%d output tokens); retrying once", cfg.id,
                                    schema.__name__, first[2])
                    result = crew.kickoff()
                    parsed = getattr(result, "pydantic", None)
                    if parsed is None:
                        parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
        except Exception as exc:
            self._spent(exc, cfg, burned(), t0)
            raise
        return parsed, self._usage(cfg, burned(), t0)

    def _direct_once(self, cfg: ProviderConfig, *, role: str, goal: str, backstory: str, description: str,
                     expected: str, schema: type[BaseModel], max_tokens: int | None = None,
                     creativity: str = "") -> tuple[Any, Usage]:
        """LLM_DIRECT_CALL=true: ONE plain chat completion (system + user), JSON parsed and validated here.
        No Agent/Task/Crew, no ReAct wrapper, no iterations. Same contract as _kickoff_once (and the same
        truncation-aware JSON retry)."""
        messages = [{"role": "system", "content": backstory},
                    {"role": "user", "content": f"{description}\n\n{expected}{json_instructions(schema)}"}]
        t0 = time.monotonic()
        state: dict[str, Any] = {"done": (0, 0, 0)}

        def new_llm(cap: int | None) -> None:
            llm = self._LLM(**self._llm_kwargs(cfg, cap, temperature_for(cfg.id, creativity)))
            state.update(llm=llm, tap=_UsageTap(llm), summary=getattr(llm, "get_token_usage_summary", None))

        def burned() -> tuple[int, int, int]:
            s = state["summary"]
            now = state["tap"].tokens(cfg, s() if callable(s) else None)
            d = state["done"]
            return d[0] + now[0], d[1] + now[1], d[2] + now[2]

        def ask() -> str:
            raw = state["llm"].call(messages)
            return raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")

        new_llm(max_tokens)
        text = ""
        try:
            text = ask()
            try:
                parsed = parse_json_output(text, schema)
            except ValueError:  # one retry on invalid JSON, like the crew path
                first = burned()
                if _truncated(first[2], max_tokens):
                    log.warning("provider %s: answer cut at its %s-token cap; retrying with %s", cfg.id,
                                max_tokens, 2 * int(max_tokens or 0))
                    state["done"] = first
                    new_llm(2 * int(max_tokens or 0))
                else:
                    log.warning("provider %s: invalid JSON for %s (%d output tokens); retrying once", cfg.id,
                                schema.__name__, first[2])
                text = ask()
                parsed = parse_json_output(text, schema)
        except Exception as exc:
            self._spent(exc, cfg, burned(), t0)
            raise
        i, c, o = burned()
        if i <= 0:
            i, c = (len(backstory) + len(description) + len(expected)) // 4, 0
        if o <= 0:
            o = max(1, len(text) // 4)
        return parsed, self._usage(cfg, (i, c, o), t0)

    @staticmethod
    def _prompt_texts(cfg: ProviderConfig, kw: dict[str, Any]) -> list[str]:
        """Every string the call will send, for the pre-call estimate (the schema goes in the prompt for
        providers without native structured output)."""
        texts = [str(kw.get(k) or "") for k in ("backstory", "description", "expected", "system", "user")]
        schema = kw.get("schema")
        if schema is not None and not native_structured_output(cfg):
            texts.append(json_instructions(schema))
        return texts

    def _kickoff(self, *, policy: ProviderPolicy | None = None, order: list[ProviderConfig] | None = None,
                 once: Any = None, **kw) -> tuple[Any, Usage]:
        """Failover wrapper: data policy first (before anything is sent), then providers in order.
        Transient errors are retried on the same provider; any failure then moves to the next one.
        order (already policy-filtered, e.g. vision_candidates) and once (the single call) are optional."""
        order = order if order is not None else candidates(self.providers, policy or ProviderPolicy())
        once = once or (self._direct_once if direct_call_enabled() else self._kickoff_once)
        kind = kw.pop("kind", "") or "default"
        allowance = kw.pop("max_cost_usd", 0.0) or 0.0
        visible_cap = kw.get("max_tokens") or max_tokens_for(kind)
        wait_cap = max_retry_wait_s(kind)
        budget_s = call_deadline_s(kind)  # interactive calls: total time, retries included (backend gives up at 30 s)
        deadline = time.monotonic() + budget_s if budget_s else None

        def time_left() -> float:
            return deadline - time.monotonic() if deadline is not None else float("inf")

        def can_wait(pause: float) -> bool:
            """A retry after `pause` still has time to finish before the deadline."""
            return time_left() - pause >= MIN_CALL_S

        over: CallBudgetError | None = None  # the pre-call guard refused every provider tried so far
        failed: list[str] = []
        broke: list[str] = []  # providers whose account has no balance left
        limited: list[str] = []  # providers still rate limiting after the retries (or further away than wait_cap)
        retry_hint: float | None = None  # the shortest wait a rate-limiting provider asked for
        last: BaseException | None = None
        spent = 0.0  # USD already burned by failed attempts of THIS call (exc.usage.cost_usd when the error carries it)
        burned: list[Usage] = []  # the same attempts as Usage, reported on the final error (never silently dropped)

        def note_limit(cfg: ProviderConfig, wait: float | None) -> None:
            nonlocal retry_hint
            limited.append(cfg.id)
            if wait is not None and (retry_hint is None or wait < retry_hint):
                retry_hint = wait

        for cfg in order:
            attempt = 0  # transient (timeout / 5xx) retries on this provider
            limit_attempt = 0  # rate-limit retries on this provider
            busy_attempt = 0  # overload (503 high demand / 529) retries on this provider
            cap = output_cap(cfg.id, visible_cap)  # + room for hidden thinking where the cap counts it (Gemini)
            while True:
                try:  # budget guard BEFORE every send, against what is left after the failed attempts so far
                    left = allowance - spent if allowance > 0 else 0.0
                    if allowance > 0 and left <= 0:
                        left = 1e-12  # nothing left: any estimate is above it
                    texts = self._prompt_texts(cfg, kw)
                    try:
                        preflight(kind=kind, model=cfg.model, provider=cfg.id, prompt_texts=texts, max_tokens=cap,
                                  allowance_usd=left)
                    except CallBudgetError:
                        if cap == visible_cap:
                            raise
                        # the thinking headroom alone crosses the allowance: send the plain cap (still guarded)
                        preflight(kind=kind, model=cfg.model, provider=cfg.id, prompt_texts=texts,
                                  max_tokens=visible_cap, allowance_usd=left)
                        cap = visible_cap
                except CallBudgetError as exc:
                    log.warning("provider %s skipped by the budget guard (spent $%.4f): %s", cfg.id, spent, exc)
                    over = over or exc
                    if attempt or limit_attempt:
                        failed.append(cfg.id)
                    break
                if time_left() < MIN_CALL_S:  # an interactive call out of time: nothing more is sent
                    log.warning("provider %s: %s call out of time (%.0fs deadline); not sending", cfg.id, kind,
                                budget_s or 0)
                    failed.append(cfg.id)
                    last = last or TimeoutError(f"{kind} deadline of {budget_s:.0f}s reached")
                    break
                still = rate_limit.acquire(cfg.id, max(0.0, min(wait_cap, time_left() - MIN_CALL_S)))
                if still > 0:  # it would have to wait longer than this kind of call can: nothing is sent
                    log.warning("provider %s rate limited for %.0fs more (local pacing); not sending", cfg.id, still)
                    failed.append(cfg.id)
                    note_limit(cfg, still)
                    last = RuntimeError(f"rate limited by local pacing for {still:.0f}s")
                    break
                try:
                    token = _CALL_TIMEOUT.set(time_left() if deadline is not None else None)
                    try:
                        parsed, usage = once(cfg, **{**kw, "max_tokens": cap})
                    finally:
                        _CALL_TIMEOUT.reset(token)
                    if failed:
                        log.warning("provider failover: %s failed, answered by %s (%s)",
                                    ",".join(failed), cfg.id, cfg.model)
                        usage.failed_providers = list(failed)
                    return parsed, usage
                except Exception as exc:
                    last = exc
                    u = getattr(exc, "usage", None)
                    spent += float(getattr(u, "cost_usd", 0) or 0)
                    if isinstance(u, Usage):
                        burned.append(u)
                    if is_no_balance(exc):
                        log.warning("provider %s has no balance left", cfg.id)
                        failed.append(cfg.id)
                        broke.append(cfg.id)
                        break
                    if is_rate_limited(exc):
                        wait = retry_after_from_exc(exc)
                        rate_limit.note_rate_limited(cfg.id, wait)
                        if (limit_attempt < PROVIDER_RATE_LIMIT_RETRIES and (wait is None or wait <= wait_cap)
                                and can_wait(min(wait_cap, (wait or 0) + 0.5))):
                            limit_attempt += 1
                            # the provider's own delay when it gave one (+ a little), else exponential backoff
                            pause = min(wait_cap, (wait + 0.5) if wait is not None
                                        else PROVIDER_RETRY_BACKOFF_S * 2 ** limit_attempt)
                            log.warning("provider %s rate limited (retry in %s); waiting %.1fs, retry %d", cfg.id,
                                        f"{wait:.1f}s" if wait is not None else "?", pause, limit_attempt)
                            time.sleep(pause)
                            continue
                        log.warning("provider %s still rate limited (asked to wait %s)", cfg.id,
                                    f"{wait:.0f}s" if wait is not None else "?")
                        failed.append(cfg.id)
                        note_limit(cfg, wait)
                        break
                    hint = retry_after_from_exc(exc) if is_overloaded(exc) else None
                    pause = min(wait_cap, hint if hint is not None
                                else PROVIDER_OVERLOAD_BACKOFF_S * 2 ** busy_attempt)
                    if is_overloaded(exc) and busy_attempt < PROVIDER_OVERLOAD_RETRIES and can_wait(pause):
                        busy_attempt += 1
                        log.warning("provider %s overloaded (%s); waiting %.1fs, retry %d", cfg.id,
                                    type(exc).__name__, pause, busy_attempt)
                        time.sleep(pause)
                        continue
                    if (is_transient(exc) and not is_overloaded(exc) and attempt < PROVIDER_RETRIES
                            and can_wait(PROVIDER_RETRY_BACKOFF_S * (attempt + 1))):
                        attempt += 1
                        log.warning("provider %s transient error (%s); retry %d", cfg.id,
                                    type(exc).__name__, attempt)
                        time.sleep(PROVIDER_RETRY_BACKOFF_S * attempt)
                        continue
                    log.warning("provider %s failed: %s: %s", cfg.id, type(exc).__name__,
                                scrub(str(exc), order)[:200])
                    failed.append(cfg.id)
                    break
        if over is not None and (not failed or spent > 0):
            raise over
        common = dict(tried=failed, allowed=[c.id for c in order], configured=[c.id for c in self.providers])
        if failed and len(broke) == len(failed):
            err = ProviderError(
                ERR_NO_BALANCE,
                f"The model provider account has no balance left ({', '.join(broke)}): top it up or choose another model.",
                **common)
        elif failed and len(limited) == len(failed):
            when = f" Try again in about {retry_hint:.0f}s." if retry_hint is not None else " Try again shortly."
            err = ProviderError(
                ERR_RATE_LIMITED,
                f"The model provider is rate limiting requests ({', '.join(limited)}).{when}",
                retry_after_s=retry_hint, **common)
        else:
            err = ProviderError(
                ERR_ALL_PROVIDERS_FAILED,
                f"All allowed model providers failed ({', '.join(failed)}). Last error: "
                f"{type(last).__name__}: {scrub(str(last), order)[:200]}",
                **common)
        if burned:  # what the failed attempts cost: the caller can still charge it
            err.usage = Usage(model=burned[-1].model, provider=burned[-1].provider,
                              input_tokens=sum(b.input_tokens for b in burned),
                              output_tokens=sum(b.output_tokens for b in burned),
                              cost_usd=round(sum(b.cost_usd for b in burned), 6),
                              duration_ms=sum(b.duration_ms for b in burned),
                              cached_tokens=sum(b.cached_tokens or 0 for b in burned) or None)
        raise err

    async def _run(self, **kw) -> tuple[Any, Usage]:
        return await asyncio.to_thread(self._kickoff, **kw)

    # -------------------------------------------------------------- API
    async def plan(self, req: PlanRequest) -> PlanResponse:
        if (fast := try_fast_plan(req)) is not None:  # simple request: one task, no planning call (PLANNER_FAST_PATH)
            return fast
        agents = [{"id": a.id, "role": a.role, "title": a.title, "responsibilities": a.responsibilities}
                  for a in req.agents]
        desc = (
            f"SOLICITUD DEL USUARIO: {req.request_text}\n\n"
            f"Agentes disponibles (usa SOLO estos agent_id): {json.dumps(agents, ensure_ascii=False)}\n"
            f"Presupuesto maximo USD: {req.budget_usd}\n\n{language_rule(req.locale, req.tone)}\n\n"
            "Descompone la solicitud en objetivos y tareas. Cada tarea tiene key unica, title, "
            "description, agent_id y depends_on (lista de keys). Maximiza el paralelismo, sin ciclos. "
            "Si falta informacion critica, agrega clarifying_questions. "
            "En cada tarea agrega reason: una frase corta y humana que explique por que ESE agente tiene esa tarea. "
            "Si la solicitud exige una decision de alto impacto entre alternativas (elegir proveedor, precio, contratar, "
            "invertir), marca decision=true SOLO en la tarea que toma esa decision (como maximo una); en las demas omitelo."
        )
        plan, usage = await self._run(
            role="Orquestadora / Asistente Ejecutiva",
            goal="Planificar el trabajo de la empresa asignando tareas a los agentes correctos",
            backstory=system_rules(req.locale, req.tone), description=desc,
            expected="JSON con objectives, tasks y clarifying_questions", schema=PlanResponse,
            policy=policy_from_request(req, "plan"), kind="plan", max_cost_usd=req.max_cost_usd)
        valid = {a.id for a in req.agents}
        if valid:
            plan.tasks = [t for t in plan.tasks if t.agent_id in valid]
        keys = {t.key for t in plan.tasks}
        for t in plan.tasks:
            t.depends_on = [d for d in t.depends_on if d in keys and d != t.key]
        if not plan.tasks:
            raise ValueError("el plan generado no contiene tareas validas")
        plan.provider, plan.model = usage.provider, usage.model
        plan.usage = usage
        return plan

    async def plan_phases(self, req: PlanPhasesRequest) -> PlanPhasesResponse:
        agents = [{"id": a.id, "role": a.role, "title": a.title} for a in req.agents]
        desc = (
            f"SOLICITUD DEL USUARIO (proyecto grande): {req.request_text}\n\n"
            f"Agentes disponibles: {json.dumps(agents, ensure_ascii=False)}\n"
            f"Presupuesto maximo USD: {req.budget_usd}\n\n{language_rule(req.locale, req.tone)}\n\n"
            f"Divide el proyecto en FASES (hoja de ruta de 3 a {req.max_phases} fases, en orden logico). "
            "Cada fase tiene key unica (p1, p2...), title, goal (1-2 frases), size (S, M, L o XL segun cuanto "
            "trabajo contiene: S~6 tareas, M~12, L~16, XL~22) y depends_on (keys de fases anteriores que deben "
            "terminar antes; deja en paralelo las fases independientes, sin ciclos). "
            "NO listes tareas todavia. Si falta informacion critica, agrega clarifying_questions."
        )
        res, usage = await self._run(
            role="Orquestadora / Asistente Ejecutiva",
            goal="Planificar por fases un proyecto grande de la empresa",
            backstory=system_rules(req.locale, req.tone), description=desc,
            expected="JSON con objectives, phases y clarifying_questions", schema=PlanPhasesResponse,
            policy=policy_from_request(req, "plan"), kind="plan_phases", max_cost_usd=req.max_cost_usd)
        res.phases = normalize_phases(res.phases, req.max_phases)
        if not res.phases:
            raise ValueError("el plan generado no contiene fases validas")
        res.usage, res.provider, res.model = usage, usage.provider, usage.model
        return res

    async def plan_phase(self, req: PlanPhaseRequest) -> PlanPhaseResponse:
        agents = [{"id": a.id, "role": a.role, "title": a.title, "responsibilities": a.responsibilities}
                  for a in req.agents]
        roadmap = [{"key": p.key, "title": p.title} for p in req.other_phases]
        desc = (
            f"SOLICITUD DEL USUARIO (proyecto grande): {req.request_text}\n\n"
            f"FASE A DESCOMPONER: {json.dumps(req.phase.model_dump(), ensure_ascii=False)}\n"
            f"Resto de la hoja de ruta (solo contexto, no planifiques estas fases): {json.dumps(roadmap, ensure_ascii=False)}\n"
            f"Agentes disponibles (usa SOLO estos agent_id): {json.dumps(agents, ensure_ascii=False)}\n\n"
            f"{language_rule(req.locale, req.tone)}\n\n"
            f"Descompone SOLO esta fase en unas {req.target_tasks} tareas (maximo {req.max_tasks}). Cada tarea: key unica "
            "dentro de la fase (t1, t2...), title, description, agent_id, complexity (S, M, L o XL), depends_on "
            "(keys de tareas de ESTA fase; maximiza el paralelismo, sin ciclos) y reason (una frase corta). "
            "Las tareas sin depends_on arrancan la fase; las dependencias con otras fases las resuelve el sistema."
        )
        res, usage = await self._run(
            role="Orquestadora / Asistente Ejecutiva",
            goal="Descomponer una fase de un proyecto grande en tareas asignadas a los agentes correctos",
            backstory=system_rules(req.locale, req.tone), description=desc,
            expected="JSON con tasks", schema=PlanPhaseResponse,
            policy=policy_from_request(req, "plan"), kind="plan_phase", max_cost_usd=req.max_cost_usd)
        res.tasks = normalize_phase_tasks(res.tasks, req.agents, req.max_tasks)
        if not res.tasks:
            raise ValueError("la fase generada no contiene tareas validas")
        res.usage, res.provider, res.model = usage, usage.provider, usage.model
        return res

    async def run_task(self, req: RunTaskRequest) -> RunTaskResponse:
        # El system prompt (persona/reglas) va en backstory; los datos no confiables solo en la descripcion.
        policy = policy_from_request(req, req.agent.role)
        description = build_task_prompt(req)
        images, notices = prepare_images(req.images)
        if images:
            # Vision: only providers the data policy allows AND that can see images (vision_candidates
            # raises no_allowed_provider exactly like a text call would).
            order = vision_candidates(self.providers, policy)
            if order:
                res, usage = await asyncio.to_thread(
                    self._kickoff, order=order, once=self._vision_once,
                    system=build_system_prompt(req.agent, req.locale, req.tone), user=description, images=images,
                    schema=TaskResult, creativity=req.agent.creativity, locale=req.locale, kind="run_task",
                    max_cost_usd=req.max_cost_usd)
                out = StructuredOutput.model_validate(res.output.model_dump())
                return RunTaskResponse(output=out, consults=res.consults, tool_requests=res.tool_requests, usage=usage,
                                       provider=usage.provider, model=usage.model, notices=notices or None)
            # Nobody allowed can see images: never send them elsewhere; say so and go on with the text.
            log.warning("vision: no allowed provider can see images; continuing with text only")
            notices.append(NOTICE_VISION_UNAVAILABLE)
            description += unavailable_note(images, req.locale)
        res, usage = await self._run(
            role=req.agent.title or req.agent.role,
            goal=f"Completar la tarea asignada como {req.agent.title or req.agent.role}",
            backstory=build_system_prompt(req.agent, req.locale, req.tone),
            description=description,
            expected="JSON con output (StructuredOutput), consults y tool_requests",
            schema=TaskResult, policy=policy, creativity=req.agent.creativity, kind="run_task",
            max_cost_usd=req.max_cost_usd)
        out = StructuredOutput.model_validate(res.output.model_dump())  # validacion estricta
        return RunTaskResponse(output=out, consults=res.consults,
                               tool_requests=res.tool_requests, usage=usage,
                               provider=usage.provider, model=usage.model, notices=notices or None)

    def _vision_once(self, cfg: ProviderConfig, *, system: str, user: str, images: list, schema: type[BaseModel],
                     max_tokens: int | None = None, creativity: str = "", locale: str = "es") -> tuple[Any, Usage]:
        """One multimodal call: the images go as content blocks (OpenAI-style data URLs; CrewAI's native
        Anthropic provider converts them), each preceded by its untrusted-data label. A fresh LLM per call
        (never cached: the images belong to one organization, and the usage counter is per call)."""
        llm = self._LLM(**self._llm_kwargs(cfg, max_tokens, temperature_for(cfg.id, creativity)))
        tap = _UsageTap(llm)  # raw provider usage (Gemini thinking included), per call
        messages = build_vision_messages(system, user + json_instructions(schema), images, locale)
        t0 = time.monotonic()
        summary = getattr(llm, "get_token_usage_summary", None)
        try:
            raw = llm.call(messages)
            text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
            try:
                parsed = parse_json_output(text, schema)
            except ValueError:  # one retry on invalid JSON, like the text path
                raw = llm.call(messages)
                text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
                parsed = parse_json_output(text, schema)
        except Exception as exc:  # the images were sent and billed even when the answer is unusable
            self._spent(exc, cfg, tap.tokens(cfg, summary() if callable(summary) else None), t0)
            raise
        img_tokens = sum(estimate_image_tokens(i) for i in images)
        i, c, o = tap.tokens(cfg, summary() if callable(summary) else None)
        if i <= 0:  # the provider gave no usage: estimate (text at ~4 chars/token plus the images)
            i, c = (len(system) + len(user)) // 4 + img_tokens, 0
        if o <= 0:
            o = max(1, len(text) // 4)
        return parsed, self._usage(cfg, (i, c, o), t0, image_count=len(images), image_tokens=img_tokens)

    async def review(self, req: ReviewRequest) -> ReviewResponse:
        """Quality review (Q1): the reviewer persona (internal auditor or neutral) judges ONE output."""
        persona = req.reviewer
        res, usage = await self._run(
            role=(persona.title or persona.role) if persona else "Revisor",
            goal="Revisar un entregable contra sus criterios de aceptacion con rigor y sin inventar datos",
            backstory=build_system_prompt(persona, req.locale, req.tone) if persona else system_rules(req.locale, req.tone),
            description=build_review_prompt(req),
            expected="JSON con verdict, reasons, criteria y evidence", schema=_ReviewLLM,
            policy=policy_from_request(req, "review"), kind="review", max_cost_usd=req.max_cost_usd)
        data = normalize_review(res.model_dump(), req)
        return ReviewResponse(**data, usage=usage, provider=usage.provider, model=usage.model)

    async def counterargument(self, req):
        """Devil's advocate (B8): argues against ONE high-risk approval. Advisory: the model never decides anything."""
        from .devils_advocate import CounterargumentResponse, build_counterargument_prompt, normalize_counterargument

        res, usage = await self._run(
            role="Abogado del diablo", goal="Exponer la postura contraria y lo que podria salir mal antes de aprobar, sin decidir",
            backstory=system_rules(req.locale, req.tone), description=build_counterargument_prompt(req),
            expected="JSON con counterargument, risks y checks", schema=_CounterargumentLLM,
            policy=policy_from_request(req, "counterargument"), kind="counterargument", max_cost_usd=req.max_cost_usd)
        data = normalize_counterargument(res.model_dump())
        if not data["counterargument"]:
            raise ValueError("empty counterargument")
        return CounterargumentResponse(**data, usage=usage, provider=usage.provider, model=usage.model)

    async def debate(self, req):
        """Team debate (B5): ONE bounded call that voices every participant and the moderator's synthesis. Advisory."""
        from .debate import DebateResponse, build_debate_prompt, normalize_debate

        res, usage = await self._run(
            role="Moderadora de un debate de equipo", goal="Que el equipo exponga posturas y objeciones breves antes de decidir, sin decidir",
            backstory=system_rules(req.locale, req.tone), description=build_debate_prompt(req),
            expected="JSON con options, turns y synthesis", schema=_DebateLLM,
            policy=policy_from_request(req, "debate"), kind="debate", max_cost_usd=req.max_cost_usd)
        data = normalize_debate(res.model_dump(), req)
        if not data["turns"]:
            raise ValueError("empty debate")
        return DebateResponse(**data, usage=usage, provider=usage.provider, model=usage.model)

    async def consult(self, req: ConsultRequest) -> ConsultResponse:
        res, usage = await self._run(
            role=req.to_agent_id, goal="Responder consultas de colegas con precision",
            backstory=system_rules(req.locale, req.tone),
            description=build_consult_prompt(req.from_agent_id, req.to_agent_id, req.question,
                                             req.context, req.locale, req.tone),
            expected="JSON con answer", schema=_ConsultAnswer,
            policy=policy_from_request(req, "consult"), kind="consult", max_cost_usd=req.max_cost_usd)
        return ConsultResponse(answer=res.answer, usage=usage, provider=usage.provider, model=usage.model)

    async def synthesize(self, req: SynthesizeRequest) -> SynthesizeResponse:
        res, usage = await self._run(
            role="Asistente Ejecutiva", goal="Consolidar los resultados en un reporte ejecutivo",
            backstory=system_rules(req.locale, req.tone),
            description=build_synthesis_prompt(req.request_text, req.outputs, req.locale, req.tone,
                                           req.stage, req.part, req.parts),
            expected="JSON con title, summary y sections", schema=_Synth,
            policy=policy_from_request(req, "synthesize"), kind="synthesize", max_cost_usd=req.max_cost_usd)
        return SynthesizeResponse(title=res.title, summary=res.summary, sections=res.sections,
                                  provider=usage.provider, model=usage.model, usage=usage)

    async def summarize(self, req):
        """Slash commands (compact / meeting / daily). Works without structured output: the JSON contract is in the
        instructions (_kickoff_once) and the answer is parsed tolerantly with one retry."""
        from .commands import build_summarize_prompt
        from .models import SummarizeResponse

        res, usage = await self._run(
            role="Asistente Ejecutiva", goal="Resumir con fidelidad, sin inventar datos",
            backstory=system_rules(req.locale, req.tone), description=build_summarize_prompt(req),
            expected="JSON con text", schema=_SummaryLLM, policy=policy_from_request(req, "summarize"), kind="summarize",
            max_cost_usd=req.max_cost_usd)
        text = (res.text or "").strip()
        if not text:
            raise ValueError("empty summary")
        return SummarizeResponse(text=text, usage=usage, provider=usage.provider, model=usage.model)

    # ------------------------------------------------------------ chat layer
    async def route(self, req: RouteRequest) -> RouteResponse:
        """LLM classification with validated JSON; ANY failure ends in the deterministic rules (never fails)."""
        try:
            res, usage = await self._run(
                role="Recepcionista del equipo", goal="Decidir quien responde un mensaje de chat",
                backstory=system_rules(req.locale, req.tone),
                description=build_route_prompt(req.text, req.conversation, req.agents, req.history, req.locale),
                expected="JSON con intent, topic, primary_agent_id, contributor_agent_ids y reasons",
                schema=_RouteLLM, policy=policy_from_request(req, "route"), kind="route")
            out = validate_route(res.model_dump(), req)
            rules = rules_route(req)
            if rules.consult is not None:  # wrong-area asks (1:1 or addressed by name) follow the deterministic rules
                rules.usage, rules.provider, rules.model = usage, usage.provider, usage.model
                return rules
            if out is not None:
                out.usage, out.provider, out.model = usage, usage.provider, usage.model
                return out
            log.warning("route: invalid LLM routing, using rules")
            spent = usage
        except Exception as exc:  # provider failure, data policy, bad JSON...: the rules are the safe path
            log.warning("route: LLM unavailable (%s), using rules", type(exc).__name__)
            spent = getattr(exc, "usage", None)
        fallback = rules_route(req)
        if isinstance(spent, Usage) and (spent.input_tokens or spent.output_tokens):
            # the rules decided, but the model calls that failed were real and billed: report them (source stays
            # "rules") so the backend's ledger records them instead of losing them
            fallback.usage, fallback.provider, fallback.model = spent, spent.provider, spent.model
        return fallback

    async def chat_reply(self, req: ChatReplyRequest) -> ChatReplyResponse:
        if req.limit or req.handoff:  # "I can't" lines and handoffs are scripted: no LLM call, no cost
            text, kind, consult = compose_reply(req)
            return ChatReplyResponse(text=text, kind=kind, consult=consult, usage=Usage(
                model="scripted", input_tokens=0, output_tokens=0, cost_usd=0.0, duration_ms=0))
        res, usage = await self._run(
            role=req.agent.title or req.agent.role, goal="Responder en el chat de la oficina como una persona",
            backstory=build_chat_system_prompt(req.agent, detect_locale(req.text, req.locale), req.tone),
            description=build_chat_prompt(req), expected="JSON con text y, si aplica, consult",
            schema=_ChatLLM, policy=policy_from_request(req, req.agent.role), kind="chat", max_cost_usd=req.max_cost_usd)
        text = (res.text or "").strip()
        if not text:
            raise ValueError("empty chat reply")
        consult = None
        valid = {a.id for a in req.agents}
        if (res.consult_to_agent_id and res.consult_question and res.consult_to_agent_id in valid
                and res.consult_to_agent_id != req.agent.id and req.consult is None
                and req.consult_to == res.consult_to_agent_id):  # only the colleague the router named, never chained
            consult = ReplyConsult(to_agent_id=res.consult_to_agent_id, question=res.consult_question.strip()[:300])
        kind = "answer" if req.consult is not None else "chat"
        return ChatReplyResponse(text=text[:1200], kind=kind, consult=consult, usage=usage,
                                 provider=usage.provider, model=usage.model)
