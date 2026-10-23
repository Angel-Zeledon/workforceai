"""CrewAIEngine: un Agent/Task/Crew de CrewAI por rol.

CrewAI se importa de forma perezosa en __init__ para que el resto del runtime
(SimulationEngine, API) funcione aunque CrewAI no este instalado.

Seguridad: los agentes se crean SIN herramientas (tools=[]) y sin delegacion;
el runtime nunca ejecuta nada. Las solicitudes de herramienta salen del modelo
como datos (tool_requests) y las decide el backend.
"""
from __future__ import annotations

import asyncio
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
    max_tokens_for,
    non_reasoning_model,
    preflight,
    reasoning_kwargs,
)
from .engine import AgentEngine, estimate_cost
from .usage_tokens import tokens_from_metrics
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
    ProviderConfig,
    ProviderError,
    ProviderPolicy,
    candidates,
    classify_error,
    fixed_temperature_model,
    is_no_balance,
    is_transient,
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

# Same-provider retries for transient errors (timeout / rate limit / 5xx) before failing over.
PROVIDER_RETRIES = max(0, int(os.getenv("PROVIDER_RETRIES", "1") or 1))
PROVIDER_RETRY_BACKOFF_S = float(os.getenv("PROVIDER_RETRY_BACKOFF_S", "1.0") or 1.0)
PROVIDER_TIMEOUT_S = float(os.getenv("PROVIDER_TIMEOUT_S", "120") or 120)


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
        temperature = temperature_for(cfg.id, creativity)
        key = f"{cfg.id}:{max_tokens or 0}:{temperature}"
        # An organization's own key is never cached: the cache is shared by every tenant.
        if cfg.byo or key not in self._llms:
            llm = self._LLM(**self._llm_kwargs(cfg, max_tokens, temperature))
            if cfg.byo:
                return llm
            self._llms[key] = llm
        return self._llms[key]

    @staticmethod
    def _llm_kwargs(cfg: ProviderConfig, max_tokens: int | None, temperature: float | None) -> dict[str, Any]:
        # No reasoning/thinking unless LLM_REASONING is set (cost_control): a reasoning alias maps to the plain model.
        kw: dict[str, Any] = {"model": non_reasoning_model(cfg.model), "api_key": cfg.api_key, "temperature": temperature,
                              "max_tokens": max_tokens or max_tokens_for("default"), "timeout": PROVIDER_TIMEOUT_S}
        kw.update(reasoning_kwargs(cfg.id, cfg.model))
        if temperature is None or (cfg.id == "openai" and fixed_temperature_model(cfg.model)):
            kw.pop("temperature")  # reasoning models only accept their default
        if cfg.id in ("deepseek", "kimi"):
            # Live finding (DeepSeek): long consolidations came back as invalid JSON (unescaped quotes
            # around char 13k). JSON mode guarantees syntactically valid JSON (the schema still goes in
            # the instructions). Moonshot's Kimi has the same json_object mode and no json_schema.
            kw["response_format"] = {"type": "json_object"}
            # (the old floor of 8192 output tokens is gone: every kind of call has its own env-configurable cap)
        if cfg.base_url:
            kw["base_url"] = cfg.base_url
        if cfg.id == "openai":
            if cfg.org_id:
                kw["organization"] = cfg.org_id
            if cfg.project_id:
                kw["project"] = cfg.project_id
        return kw

    def test_provider(self, cfg: ProviderConfig) -> dict[str, Any]:
        """A tiny real call with the given credential. Never raises: returns a stable code
        (ok | invalid_key | quota | model_not_found | network | unknown) and a redacted message."""
        t0 = time.monotonic()
        try:
            kw = self._llm_kwargs(cfg, 64, None)
            kw["timeout"] = min(PROVIDER_TIMEOUT_S, 30)
            kw.pop("response_format", None)  # a plain text probe
            kw["max_tokens"] = 64
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
    def _spent(exc: BaseException, cfg: ProviderConfig, metrics: Any, t0: float) -> BaseException:
        """Attach to a failed attempt the tokens it already burned (the first answer of a JSON retry, say), so
        _kickoff can charge them against the call's allowance before it retries or falls back."""
        i, c, o = tokens_from_metrics(metrics, cfg.id)
        if i > 0 or o > 0:
            exc.usage = Usage(model=cfg.model, input_tokens=i, output_tokens=o,  # type: ignore[attr-defined]
                              cost_usd=estimate_cost(i, o, cfg.model, cfg.id, c),
                              duration_ms=int((time.monotonic() - t0) * 1000), provider=cfg.id,
                              cached_tokens=c or None)
        return exc

    def _kickoff_once(self, cfg: ProviderConfig, *, role: str, goal: str, backstory: str, description: str,
                      expected: str, schema: type[BaseModel], max_tokens: int | None = None,
                      creativity: str = "") -> tuple[Any, Usage]:
        agent = self._Agent(role=role, goal=goal, backstory=backstory, llm=self._llm_for(cfg, max_tokens, creativity),
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
        crew = self._Crew(agents=[agent], tasks=[task], process=self._Process.sequential, verbose=False)
        t0 = time.monotonic()
        result = crew.kickoff()
        parsed = getattr(result, "pydantic", None)
        if parsed is None:  # tolerant parse of the raw text + one retry if the JSON is invalid
            try:
                parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
            except ValueError:
                first = getattr(result, "token_usage", None)
                try:
                    result = crew.kickoff()
                    parsed = getattr(result, "pydantic", None)
                    if parsed is None:
                        parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
                except Exception as exc:
                    self._spent(exc, cfg, getattr(result, "token_usage", None) or first, t0)
                    raise
        ms = int((time.monotonic() - t0) * 1000)
        m = getattr(result, "token_usage", None)
        i, c, o = tokens_from_metrics(m, cfg.id)
        return parsed, Usage(model=cfg.model, input_tokens=i, output_tokens=o,
                             cost_usd=estimate_cost(i, o, cfg.model, cfg.id, c), duration_ms=ms,
                             provider=cfg.id, cached_tokens=c or None)

    def _direct_once(self, cfg: ProviderConfig, *, role: str, goal: str, backstory: str, description: str,
                     expected: str, schema: type[BaseModel], max_tokens: int | None = None,
                     creativity: str = "") -> tuple[Any, Usage]:
        """LLM_DIRECT_CALL=true: ONE plain chat completion (system + user), JSON parsed and validated here.
        No Agent/Task/Crew, no ReAct wrapper, no iterations. Same contract as _kickoff_once."""
        llm = self._LLM(**self._llm_kwargs(cfg, max_tokens, temperature_for(cfg.id, creativity)))
        messages = [{"role": "system", "content": backstory},
                    {"role": "user", "content": f"{description}\n\n{expected}{json_instructions(schema)}"}]
        t0 = time.monotonic()
        summary = getattr(llm, "get_token_usage_summary", None)
        try:
            raw = llm.call(messages)
            text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
            try:
                parsed = parse_json_output(text, schema)
            except ValueError:  # one retry on invalid JSON, like the crew path
                raw = llm.call(messages)
                text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
                parsed = parse_json_output(text, schema)
        except Exception as exc:
            self._spent(exc, cfg, summary() if callable(summary) else None, t0)
            raise
        ms = int((time.monotonic() - t0) * 1000)
        m = summary() if callable(summary) else None
        i, c, o = tokens_from_metrics(m, cfg.id)
        if i <= 0:
            i, c = (len(backstory) + len(description) + len(expected)) // 4, 0
        if o <= 0:
            o = max(1, len(text) // 4)
        return parsed, Usage(model=cfg.model, input_tokens=i, output_tokens=o,
                             cost_usd=estimate_cost(i, o, cfg.model, cfg.id, c), duration_ms=ms, provider=cfg.id,
                             cached_tokens=c or None)

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
        kw["max_tokens"] = kw.get("max_tokens") or max_tokens_for(kind)
        over: CallBudgetError | None = None  # the pre-call guard refused every provider tried so far
        failed: list[str] = []
        broke: list[str] = []  # providers whose account has no balance left
        last: BaseException | None = None
        spent = 0.0  # USD already burned by failed attempts of THIS call (exc.usage.cost_usd when the error carries it)
        for cfg in order:
            attempt = 0
            while True:
                try:  # budget guard BEFORE every send, against what is left after the failed attempts so far
                    left = allowance - spent if allowance > 0 else 0.0
                    if allowance > 0 and left <= 0:
                        left = 1e-12  # nothing left: any estimate is above it
                    preflight(kind=kind, model=cfg.model, provider=cfg.id,
                              prompt_texts=self._prompt_texts(cfg, kw), max_tokens=kw["max_tokens"],
                              allowance_usd=left)
                except CallBudgetError as exc:
                    log.warning("provider %s skipped by the budget guard (spent $%.4f): %s", cfg.id, spent, exc)
                    over = over or exc
                    if attempt:
                        failed.append(cfg.id)
                    break
                try:
                    parsed, usage = once(cfg, **kw)
                    if failed:
                        log.warning("provider failover: %s failed, answered by %s (%s)",
                                    ",".join(failed), cfg.id, cfg.model)
                        usage.failed_providers = list(failed)
                    return parsed, usage
                except Exception as exc:
                    last = exc
                    spent += float(getattr(getattr(exc, "usage", None), "cost_usd", 0) or 0)
                    if is_no_balance(exc):
                        log.warning("provider %s has no balance left", cfg.id)
                        failed.append(cfg.id)
                        broke.append(cfg.id)
                        break
                    if is_transient(exc) and attempt < PROVIDER_RETRIES:
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
        if failed and len(broke) == len(failed):
            raise ProviderError(
                ERR_NO_BALANCE,
                f"The model provider account has no balance left ({', '.join(broke)}): top it up or choose another model.",
                tried=failed, allowed=[c.id for c in order], configured=[c.id for c in self.providers])
        raise ProviderError(
            ERR_ALL_PROVIDERS_FAILED,
            f"All allowed model providers failed ({', '.join(failed)}). Last error: "
            f"{type(last).__name__}: {scrub(str(last), order)[:200]}",
            tried=failed, allowed=[c.id for c in order], configured=[c.id for c in self.providers])

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
        messages = build_vision_messages(system, user + json_instructions(schema), images, locale)
        t0 = time.monotonic()
        raw = llm.call(messages)
        text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
        try:
            parsed = parse_json_output(text, schema)
        except ValueError:  # one retry on invalid JSON, like the text path
            raw = llm.call(messages)
            text = raw if isinstance(raw, str) else str(getattr(raw, "raw", raw) or "")
            parsed = parse_json_output(text, schema)
        ms = int((time.monotonic() - t0) * 1000)
        img_tokens = sum(estimate_image_tokens(i) for i in images)
        summary = getattr(llm, "get_token_usage_summary", None)
        m = summary() if callable(summary) else None
        i, c, o = tokens_from_metrics(m, cfg.id)
        if i <= 0:  # the provider gave no usage: estimate (text at ~4 chars/token plus the images)
            i, c = (len(system) + len(user)) // 4 + img_tokens, 0
        if o <= 0:
            o = max(1, len(text) // 4)
        return parsed, Usage(model=cfg.model, input_tokens=i, output_tokens=o,
                             cost_usd=estimate_cost(i, o, cfg.model, cfg.id, c), duration_ms=ms, provider=cfg.id,
                             cached_tokens=c or None, image_count=len(images), image_tokens=img_tokens)

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
                                  provider=usage.provider, model=usage.model)

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
        except Exception as exc:  # provider failure, data policy, bad JSON...: the rules are the safe path
            log.warning("route: LLM unavailable (%s), using rules", type(exc).__name__)
        return rules_route(req)

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
