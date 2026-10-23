"""FastAPI app: contrato Go -> agent-runtime."""
from __future__ import annotations

import asyncio
import hmac
import logging
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from .engine import AgentEngine, _truthy, select_engine
from .estimate import EstimateRequest, EstimateResponse
from .providers import ProviderError, config_from_credential, configured_providers, default_provider, scrub
from .replan import ReplanRequest, ReplanResponse
from .devils_advocate import CounterargumentRequest, CounterargumentResponse
from .debate import DebateRequest, DebateResponse
from .redact import redact_any, redact_secrets
from .security import build_trace
from .cost_control import CallBudgetError
from .commands import context_sizes, marker_lang
from .models import (
    ChatReplyRequest,
    ChatReplyResponse,
    ConsultRequest,
    ConsultResponse,
    HealthResponse,
    ProviderTestRequest,
    ProviderTestResponse,
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
    RunTrace,
    SummarizeRequest,
    SummarizeResponse,
    ContextSizesResponse,
    SynthesizeRequest,
    SynthesizeResponse,
)

log = logging.getLogger("agent_runtime")


def simulation_forced() -> bool:
    return _truthy(os.getenv("SIMULATION"))


def scrub_run_task(resp: RunTaskResponse) -> RunTaskResponse:
    """Nada con forma de credencial sale del runtime: ni en el resultado ni en los args
    de un tool_request (el modelo podria repetir un secreto visto en un correo)."""
    data = resp.model_dump()
    for key in ("output", "consults", "tool_requests"):
        data[key] = redact_any(data[key])
    return RunTaskResponse(**data)


def _with_marker_lang(req, text: str):
    """[[lang:xx]] (slash commands: the alias the person typed) overrides the organization locale for this call."""
    loc = marker_lang(text)
    if loc and getattr(req, "locale", None) != loc:
        return req.model_copy(update={"locale": loc})
    return req


def create_app(engine: AgentEngine | None = None) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        if getattr(app.state, "engine", None) is None:
            app.state.engine = select_engine()
        log.info("agent-runtime mode=%s", app.state.engine.mode)
        yield

    app = FastAPI(title="agent-runtime", version="1.0.0", lifespan=lifespan)
    app.state.engine = engine

    # Shared secret with the backend: when RUNTIME_TOKEN is set, every call except
    # /healthz must carry "Authorization: Bearer <token>". Read per request so that
    # rotating the variable needs no code change; compared in constant time.
    @app.middleware("http")
    async def require_token(request: Request, call_next):
        token = os.getenv("RUNTIME_TOKEN", "")
        if token and request.url.path != "/healthz":
            got = request.headers.get("authorization", "")
            if not hmac.compare_digest(got.encode(), f"Bearer {token}".encode()):
                return JSONResponse(status_code=401, content={"detail": "missing or invalid runtime token"})
        return await call_next(request)

    # Never echo a request body in a validation error: it may carry an organization's API key.
    @app.exception_handler(RequestValidationError)
    async def validation_error(_request: Request, exc: RequestValidationError):
        errs = [{"loc": list(e.get("loc", ())), "msg": str(e.get("msg", "")), "type": e.get("type", "")}
                for e in exc.errors()]
        return JSONResponse(status_code=422, content={"detail": errs})

    def eng() -> AgentEngine:
        if app.state.engine is None:  # TestClient sin lifespan
            app.state.engine = select_engine()
        return app.state.engine

    def live_engine():
        if not hasattr(app.state, "byo_engine"):
            try:
                from .crewai_engine import CrewAIEngine

                app.state.byo_engine = CrewAIEngine(providers=[], allow_empty=True)
            except Exception as exc:  # CrewAI missing: keep serving with the current engine
                log.error("bring-your-own-model engine unavailable (%s)", type(exc).__name__)
                app.state.byo_engine = None
        return app.state.byo_engine

    def eng_for(req) -> AgentEngine:
        """The engine for a request. A request carrying the organization's own key needs a live engine even
        when the operator configured no key of their own (SIMULATION=true always wins: no network)."""
        e = eng()
        if getattr(req, "provider_credentials", None) and e.mode != "live" and not simulation_forced():
            live = live_engine()
            if live is not None:
                return live
        return e

    async def guarded(coro):
        try:
            return await coro
        except HTTPException:
            raise
        except CallBudgetError as exc:  # the pre-call guard: nothing was sent to the provider
            log.warning("call budget guard: %s", exc)
            raise HTTPException(status_code=exc.status_code, detail=exc.detail()) from exc
        except ProviderError as exc:  # stable, explicit: never a silent fallback
            log.warning("provider error code=%s tried=%s", exc.code, exc.tried)
            raise HTTPException(status_code=exc.status_code, detail=exc.detail()) from exc
        except Exception as exc:  # fallo del motor/LLM
            log.exception("engine error")
            raise HTTPException(status_code=502, detail=f"engine error: {exc}") from exc

    @app.get("/healthz", response_model=HealthResponse, response_model_exclude_none=True)
    async def healthz():
        reason = getattr(eng(), "fallback_reason", None)
        return HealthResponse(status="degraded" if reason else "ok", mode=eng().mode,  # type: ignore[arg-type]
                              provider=getattr(eng(), "provider", "simulation"), warning=reason,
                              configured_providers=[c.id for c in configured_providers()],
                              default_provider=default_provider())

    @app.post("/v1/plan", response_model=PlanResponse, response_model_exclude_none=True)
    async def plan(req: PlanRequest):
        req = _with_marker_lang(req, req.request_text)
        return await guarded(eng_for(req).plan(req))

    @app.post("/v1/plan-phases", response_model=PlanPhasesResponse, response_model_exclude_none=True)
    async def plan_phases(req: PlanPhasesRequest):
        return await guarded(eng_for(req).plan_phases(req))

    @app.post("/v1/plan-phase", response_model=PlanPhaseResponse, response_model_exclude_none=True)
    async def plan_phase(req: PlanPhaseRequest):
        return await guarded(eng_for(req).plan_phase(req))

    @app.post("/v1/replan", response_model=ReplanResponse, response_model_exclude_none=True)
    async def replan(req: ReplanRequest):
        return await guarded(eng_for(req).replan(req))

    @app.post("/v1/run-task", response_model=RunTaskResponse, response_model_exclude_none=True)
    async def run_task(req: RunTaskRequest):
        req = _with_marker_lang(req, req.context.request_text)
        resp = await guarded(eng_for(req).run_task(req))
        out = scrub_run_task(resp)
        if req.want_trace:  # V2 thought audit: the prompt exactly as built (same builders the engine uses), redacted
            out.trace = RunTrace(**build_trace(req))
        return out

    @app.post("/v1/review", response_model=ReviewResponse, response_model_exclude_none=True)
    async def review(req: ReviewRequest):
        resp = await guarded(eng_for(req).review(req))
        data = redact_any(resp.model_dump(exclude={"usage"}))  # nothing with the shape of a credential leaves the runtime
        return ReviewResponse(**data, usage=resp.usage)

    @app.post("/v1/counterargument", response_model=CounterargumentResponse, response_model_exclude_none=True)
    async def counterargument(req: CounterargumentRequest):
        resp = await guarded(eng_for(req).counterargument(req))
        data = redact_any(resp.model_dump(exclude={"usage"}))  # nothing with the shape of a credential leaves the runtime
        return CounterargumentResponse(**data, usage=resp.usage)

    @app.post("/v1/debate", response_model=DebateResponse, response_model_exclude_none=True)
    async def debate(req: DebateRequest):
        resp = await guarded(eng_for(req).debate(req))
        data = redact_any(resp.model_dump(exclude={"usage"}))  # nothing with the shape of a credential leaves the runtime
        return DebateResponse(**data, usage=resp.usage)

    @app.post("/v1/estimate", response_model=EstimateResponse)
    async def estimate(req: EstimateRequest):
        return await guarded(eng().estimate(req))

    @app.post("/v1/consult", response_model=ConsultResponse, response_model_exclude_none=True)
    async def consult(req: ConsultRequest):
        return await guarded(eng_for(req).consult(req))

    @app.post("/v1/synthesize", response_model=SynthesizeResponse, response_model_exclude_none=True)
    async def synthesize(req: SynthesizeRequest):
        req = _with_marker_lang(req, req.request_text)
        return await guarded(eng_for(req).synthesize(req))

    @app.post("/v1/summarize", response_model=SummarizeResponse, response_model_exclude_none=True)
    async def summarize(req: SummarizeRequest):
        resp = await guarded(eng_for(req).summarize(req))
        return resp.model_copy(update={"text": redact_secrets(resp.text)})

    @app.post("/v1/context-sizes", response_model=ContextSizesResponse)
    async def context_sizes_endpoint(req: RunTaskRequest):
        return context_sizes(req)  # builders only: no model is ever called

    @app.post("/v1/route", response_model=RouteResponse, response_model_exclude_none=True)
    async def route(req: RouteRequest):
        return await guarded(eng_for(req).route(req))

    @app.post("/v1/chat-reply", response_model=ChatReplyResponse, response_model_exclude_none=True)
    async def chat_reply(req: ChatReplyRequest):
        resp = await guarded(eng_for(req).chat_reply(req))
        return resp.model_copy(update={"text": redact_secrets(resp.text)})

    @app.post("/v1/provider-test", response_model=ProviderTestResponse)
    async def provider_test(req: ProviderTestRequest):
        """Connection test with an organization's key: one tiny real call (a test double in SIMULATION).
        Always answers 200 with a stable code, so the UI can explain what is wrong."""
        cfg = config_from_credential(req.credential)
        if cfg is None:
            return ProviderTestResponse(ok=False, code="invalid_request", provider=req.credential.provider,
                                        message="Unknown provider, missing key or missing base URL.")
        e = eng()
        if e.mode != "live" and not simulation_forced():
            e = live_engine() or e
        out = await asyncio.to_thread(e.test_provider, cfg)
        out["message"] = scrub(str(out.get("message", "")), [cfg])
        return ProviderTestResponse(**out)

    return app


app = create_app()
