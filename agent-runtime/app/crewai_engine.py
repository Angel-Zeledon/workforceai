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

from .engine import AgentEngine, estimate_cost
from .hier_plan import normalize_phase_tasks, normalize_phases
from .redact import redact_secrets
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
    DEFAULT_MODELS,
    ERR_ALL_PROVIDERS_FAILED,
    ProviderConfig,
    ProviderError,
    ProviderPolicy,
    candidates,
    is_transient,
    policy_from_request,
)
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


class _ConsultAnswer(BaseModel):
    answer: str


class _RouteLLM(BaseModel):
    intent: str
    topic: str = "general"
    primary_agent_id: str
    contributor_agent_ids: list[str] = Field(default_factory=list)
    reasons: dict[str, str] = Field(default_factory=dict)


class _ChatLLM(BaseModel):
    text: str
    consult_to_agent_id: str | None = None
    consult_question: str | None = None


class _Synth(BaseModel):
    title: str
    summary: str
    sections: list[Section] = Field(default_factory=list)


class CrewAIEngine(AgentEngine):
    mode = "live"

    def __init__(self, model: str | None = None, api_key: str | None = None, provider: str = "anthropic",
                 providers: list[ProviderConfig] | None = None):
        from crewai import LLM, Agent, Crew, Process, Task  # import aislado

        self._LLM, self._Agent, self._Task, self._Crew, self._Process = LLM, Agent, Task, Crew, Process
        if providers is None:  # legacy single-provider construction
            name = model or os.getenv("MODEL") or DEFAULT_MODELS[provider]
            if "/" not in name:
                name = f"{provider}/{name}"
            env_name = "DEEPSEEK_API_KEY" if provider == "deepseek" else "ANTHROPIC_API_KEY"
            api_key = api_key or os.getenv(env_name)
            if not api_key:
                raise RuntimeError(f"{env_name} requerido para CrewAIEngine")
            providers = [ProviderConfig(id=provider, model=name, api_key=api_key)]
        if not providers:
            raise RuntimeError("no model provider configured for CrewAIEngine")
        self.providers = list(providers)
        self.provider = self.providers[0].id  # primary (reported by /healthz)
        self.model = self.providers[0].model
        self._llms: dict[str, Any] = {}

    def _llm_for(self, cfg: ProviderConfig, max_tokens: int | None = None) -> Any:
        key = f"{cfg.id}:{max_tokens or 0}"
        if key not in self._llms:
            kw: dict[str, Any] = {"model": cfg.model, "api_key": cfg.api_key, "temperature": 0.3,
                                  "max_tokens": max_tokens or 4096, "timeout": PROVIDER_TIMEOUT_S}
            if cfg.base_url:
                kw["base_url"] = cfg.base_url
            self._llms[key] = self._LLM(**kw)
        return self._llms[key]

    # ------------------------------------------------------------ nucleo
    def _kickoff_once(self, cfg: ProviderConfig, *, role: str, goal: str, backstory: str, description: str,
                      expected: str, schema: type[BaseModel], max_tokens: int | None = None) -> tuple[Any, Usage]:
        agent = self._Agent(role=role, goal=goal, backstory=backstory, llm=self._llm_for(cfg, max_tokens),
                            tools=[], allow_delegation=False, verbose=False)
        task = self._Task(description=description, expected_output=expected,
                          agent=agent, output_pydantic=schema)
        crew = self._Crew(agents=[agent], tasks=[task], process=self._Process.sequential, verbose=False)
        t0 = time.monotonic()
        result = crew.kickoff()
        parsed = getattr(result, "pydantic", None)
        if parsed is None:  # tolerant parse of the raw text + one retry if the JSON is invalid
            try:
                parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
            except ValueError:
                result = crew.kickoff()
                parsed = getattr(result, "pydantic", None)
                if parsed is None:
                    parsed = parse_json_output(getattr(result, "raw", "") or "", schema)
        ms = int((time.monotonic() - t0) * 1000)
        m = getattr(result, "token_usage", None)
        i = int(getattr(m, "prompt_tokens", 0) or 0)
        o = int(getattr(m, "completion_tokens", 0) or 0)
        return parsed, Usage(model=cfg.model, input_tokens=i, output_tokens=o,
                             cost_usd=estimate_cost(i, o, cfg.model, cfg.id), duration_ms=ms,
                             provider=cfg.id)

    def _kickoff(self, *, policy: ProviderPolicy | None = None, **kw) -> tuple[Any, Usage]:
        """Failover wrapper: data policy first (before anything is sent), then providers in order.
        Transient errors are retried on the same provider; any failure then moves to the next one."""
        order = candidates(self.providers, policy or ProviderPolicy())
        failed: list[str] = []
        last: BaseException | None = None
        for cfg in order:
            attempt = 0
            while True:
                try:
                    parsed, usage = self._kickoff_once(cfg, **kw)
                    if failed:
                        log.warning("provider failover: %s failed, answered by %s (%s)",
                                    ",".join(failed), cfg.id, cfg.model)
                        usage.failed_providers = list(failed)
                    return parsed, usage
                except Exception as exc:
                    last = exc
                    if is_transient(exc) and attempt < PROVIDER_RETRIES:
                        attempt += 1
                        log.warning("provider %s transient error (%s); retry %d", cfg.id,
                                    type(exc).__name__, attempt)
                        time.sleep(PROVIDER_RETRY_BACKOFF_S * attempt)
                        continue
                    log.warning("provider %s failed: %s: %s", cfg.id, type(exc).__name__,
                                redact_secrets(str(exc))[:200])
                    failed.append(cfg.id)
                    break
        raise ProviderError(
            ERR_ALL_PROVIDERS_FAILED,
            f"All allowed model providers failed ({', '.join(failed)}). Last error: "
            f"{type(last).__name__}: {redact_secrets(str(last))[:200]}",
            tried=failed, allowed=[c.id for c in order], configured=[c.id for c in self.providers])

    async def _run(self, **kw) -> tuple[Any, Usage]:
        return await asyncio.to_thread(self._kickoff, **kw)

    # -------------------------------------------------------------- API
    async def plan(self, req: PlanRequest) -> PlanResponse:
        agents = [{"id": a.id, "role": a.role, "title": a.title, "responsibilities": a.responsibilities}
                  for a in req.agents]
        desc = (
            f"SOLICITUD DEL USUARIO: {req.request_text}\n\n"
            f"Agentes disponibles (usa SOLO estos agent_id): {json.dumps(agents, ensure_ascii=False)}\n"
            f"Presupuesto maximo USD: {req.budget_usd}\n\n{language_rule(req.locale, req.tone)}\n\n"
            "Descompone la solicitud en objetivos y tareas. Cada tarea tiene key unica, title, "
            "description, agent_id y depends_on (lista de keys). Maximiza el paralelismo, sin ciclos. "
            "Si falta informacion critica, agrega clarifying_questions. "
            "En cada tarea agrega reason: una frase corta y humana que explique por que ESE agente tiene esa tarea."
        )
        plan, usage = await self._run(
            role="Orquestadora / Asistente Ejecutiva",
            goal="Planificar el trabajo de la empresa asignando tareas a los agentes correctos",
            backstory=system_rules(req.locale, req.tone), description=desc,
            expected="JSON con objectives, tasks y clarifying_questions", schema=PlanResponse,
            policy=policy_from_request(req, "plan"))
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
            policy=policy_from_request(req, "plan"), max_tokens=2048)
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
            policy=policy_from_request(req, "plan"), max_tokens=6144)
        res.tasks = normalize_phase_tasks(res.tasks, req.agents, req.max_tasks)
        if not res.tasks:
            raise ValueError("la fase generada no contiene tareas validas")
        res.usage, res.provider, res.model = usage, usage.provider, usage.model
        return res

    async def run_task(self, req: RunTaskRequest) -> RunTaskResponse:
        # El system prompt (persona/reglas) va en backstory; los datos no confiables solo en la descripcion.
        res, usage = await self._run(
            role=req.agent.title or req.agent.role,
            goal=f"Completar la tarea asignada como {req.agent.title or req.agent.role}",
            backstory=build_system_prompt(req.agent, req.locale, req.tone),
            description=build_task_prompt(req),
            expected="JSON con output (StructuredOutput), consults y tool_requests",
            schema=TaskResult, policy=policy_from_request(req, req.agent.role))
        out = StructuredOutput.model_validate(res.output.model_dump())  # validacion estricta
        return RunTaskResponse(output=out, consults=res.consults,
                               tool_requests=res.tool_requests, usage=usage,
                               provider=usage.provider, model=usage.model)

    async def consult(self, req: ConsultRequest) -> ConsultResponse:
        res, usage = await self._run(
            role=req.to_agent_id, goal="Responder consultas de colegas con precision",
            backstory=system_rules(req.locale, req.tone),
            description=build_consult_prompt(req.from_agent_id, req.to_agent_id, req.question,
                                             req.context, req.locale, req.tone),
            expected="JSON con answer", schema=_ConsultAnswer,
            policy=policy_from_request(req, "consult"))
        return ConsultResponse(answer=res.answer, usage=usage, provider=usage.provider, model=usage.model)

    async def synthesize(self, req: SynthesizeRequest) -> SynthesizeResponse:
        res, usage = await self._run(
            role="Asistente Ejecutiva", goal="Consolidar los resultados en un reporte ejecutivo",
            backstory=system_rules(req.locale, req.tone),
            description=build_synthesis_prompt(req.request_text, req.outputs, req.locale, req.tone,
                                           req.stage, req.part, req.parts),
            expected="JSON con title, summary y sections", schema=_Synth,
            policy=policy_from_request(req, "synthesize"))
        return SynthesizeResponse(title=res.title, summary=res.summary, sections=res.sections,
                                  provider=usage.provider, model=usage.model)

    # ------------------------------------------------------------ chat layer
    async def route(self, req: RouteRequest) -> RouteResponse:
        """LLM classification with validated JSON; ANY failure ends in the deterministic rules (never fails)."""
        try:
            res, usage = await self._run(
                role="Recepcionista del equipo", goal="Decidir quien responde un mensaje de chat",
                backstory=system_rules(req.locale, req.tone),
                description=build_route_prompt(req.text, req.conversation, req.agents, req.history, req.locale),
                expected="JSON con intent, topic, primary_agent_id, contributor_agent_ids y reasons",
                schema=_RouteLLM, policy=policy_from_request(req, "route"), max_tokens=300)
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
            schema=_ChatLLM, policy=policy_from_request(req, req.agent.role), max_tokens=350)
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
