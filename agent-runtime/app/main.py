"""FastAPI app: contrato Go -> agent-runtime."""
from __future__ import annotations

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException

from .engine import AgentEngine, select_engine
from .estimate import EstimateRequest, EstimateResponse
from .providers import ProviderError
from .redact import redact_any, redact_secrets
from .models import (
    ChatReplyRequest,
    ChatReplyResponse,
    ConsultRequest,
    ConsultResponse,
    HealthResponse,
    PlanRequest,
    PlanResponse,
    RouteRequest,
    RouteResponse,
    RunTaskRequest,
    RunTaskResponse,
    SynthesizeRequest,
    SynthesizeResponse,
)

log = logging.getLogger("agent_runtime")


def scrub_run_task(resp: RunTaskResponse) -> RunTaskResponse:
    """Nada con forma de credencial sale del runtime: ni en el resultado ni en los args
    de un tool_request (el modelo podria repetir un secreto visto en un correo)."""
    data = resp.model_dump()
    for key in ("output", "consults", "tool_requests"):
        data[key] = redact_any(data[key])
    return RunTaskResponse(**data)


def create_app(engine: AgentEngine | None = None) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        if getattr(app.state, "engine", None) is None:
            app.state.engine = select_engine()
        log.info("agent-runtime mode=%s", app.state.engine.mode)
        yield

    app = FastAPI(title="agent-runtime", version="1.0.0", lifespan=lifespan)
    app.state.engine = engine

    def eng() -> AgentEngine:
        if app.state.engine is None:  # TestClient sin lifespan
            app.state.engine = select_engine()
        return app.state.engine

    async def guarded(coro):
        try:
            return await coro
        except HTTPException:
            raise
        except ProviderError as exc:  # stable, explicit: never a silent fallback
            log.warning("provider error code=%s tried=%s", exc.code, exc.tried)
            raise HTTPException(status_code=exc.status_code, detail=exc.detail()) from exc
        except Exception as exc:  # fallo del motor/LLM
            log.exception("engine error")
            raise HTTPException(status_code=502, detail=f"engine error: {exc}") from exc

    @app.get("/healthz", response_model=HealthResponse)
    async def healthz():
        return HealthResponse(status="ok", mode=eng().mode, provider=getattr(eng(), "provider", "simulation"))  # type: ignore[arg-type]

    @app.post("/v1/plan", response_model=PlanResponse, response_model_exclude_none=True)
    async def plan(req: PlanRequest):
        return await guarded(eng().plan(req))

    @app.post("/v1/run-task", response_model=RunTaskResponse, response_model_exclude_none=True)
    async def run_task(req: RunTaskRequest):
        resp = await guarded(eng().run_task(req))
        return scrub_run_task(resp)

    @app.post("/v1/estimate", response_model=EstimateResponse)
    async def estimate(req: EstimateRequest):
        return await guarded(eng().estimate(req))

    @app.post("/v1/consult", response_model=ConsultResponse, response_model_exclude_none=True)
    async def consult(req: ConsultRequest):
        return await guarded(eng().consult(req))

    @app.post("/v1/synthesize", response_model=SynthesizeResponse, response_model_exclude_none=True)
    async def synthesize(req: SynthesizeRequest):
        return await guarded(eng().synthesize(req))

    @app.post("/v1/route", response_model=RouteResponse, response_model_exclude_none=True)
    async def route(req: RouteRequest):
        return await guarded(eng().route(req))

    @app.post("/v1/chat-reply", response_model=ChatReplyResponse, response_model_exclude_none=True)
    async def chat_reply(req: ChatReplyRequest):
        resp = await guarded(eng().chat_reply(req))
        return resp.model_copy(update={"text": redact_secrets(resp.text)})

    return app


app = create_app()
