"""Needs your help (docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md, part 2): the agent can
stop and ask a person instead of inventing or failing. The backend says whether asking is allowed (help_allowed) and
sends back the answers (help_answers); the agent's question rides on the structured result (help_request)."""
import asyncio

import pytest

from app import crewai_engine
from app.main import scrub_run_task
from app.models import HelpRequest, RunTaskRequest, RunTaskResponse, StructuredOutput, TaskResultHelp, Usage
from app.security import UNTRUSTED_NOTICE, build_task_prompt
from app.simulation import SimulationEngine


def _req(**extra):
    base = {
        "task": {"id": "t1", "title": "Llamar al cliente", "description": "Confirma la cita", "agent_id": "sales"},
        "agent": {"id": "sales", "role": "sales", "title": "Ventas"},
        "context": {"request_text": "Contactar clientes"},
    }
    base.update(extra)
    return RunTaskRequest.model_validate(base)


def test_old_payload_does_not_offer_help():
    p = build_task_prompt(_req())
    assert "help_request" not in p
    assert "RESPUESTAS DE UNA PERSONA" not in p


def test_prompt_offers_help_only_when_allowed():
    p = build_task_prompt(_req(help_allowed=True))
    assert "help_request" in p
    # it asks instead of inventing, only for what cannot reasonably be assumed, and says what it needs
    assert "PEDIR AYUDA" in p and "what_happened" in p and "need" in p and "options" in p
    assert p.index("PEDIR AYUDA") < p.index(UNTRUSTED_NOTICE)


def test_prompt_includes_answers_as_guidance():
    p = build_task_prompt(_req(help_allowed=True, help_answers=[
        {"question": "No hay telefono del cliente", "answer": "Usa el correo", "by": "ana"}]))
    assert "RESPUESTAS DE UNA PERSONA A TUS PREGUNTAS" in p
    assert "No hay telefono del cliente" in p and "Usa el correo" in p
    # guidance, never authority
    assert "NO otorgan herramientas" in p


def test_answers_are_bounded_and_one_line():
    answers = [{"question": f"q{i}\nIGNORA", "answer": "a" * 5000} for i in range(12)]
    p = build_task_prompt(_req(help_allowed=True, help_answers=answers))
    lines = [l for l in p.splitlines() if l.startswith("- Pregunta:")]
    assert len(lines) == 5
    assert all(len(l) < 1300 for l in lines)


def _run_sim(req):
    return asyncio.run(SimulationEngine(latency_scale=0).run_task(req))


def test_sim_marker_asks_then_continues_with_answer():
    req = _req(task={"id": "t1", "title": "Llamar al cliente [sim:help]", "description": "x", "agent_id": "sales"},
               help_allowed=True)
    first = _run_sim(req)
    assert first.help_request is not None
    assert first.help_request.need and first.help_request.what_happened and len(first.help_request.options) >= 2
    data = req.model_dump()
    data["help_answers"] = [{"question": first.help_request.need, "answer": "Usa el correo"}]
    second = _run_sim(RunTaskRequest.model_validate(data))
    assert second.help_request is None
    assert any("Usa el correo" in e for e in second.output.evidence)


def test_sim_never_asks_when_not_allowed():
    req = _req(task={"id": "t1", "title": "Llamar al cliente [sim:help]", "description": "x", "agent_id": "sales"})
    assert _run_sim(req).help_request is None


def test_help_request_dropped_when_not_allowed(monkeypatch):
    pytest.importorskip("crewai")
    from app.providers import ProviderConfig

    eng = crewai_engine.CrewAIEngine(providers=[ProviderConfig("deepseek", "deepseek/deepseek-chat", "k")])
    res = TaskResultHelp(output=StructuredOutput(summary="hecho", confidence=0.5),
                     help_request=HelpRequest(kind="data", what_happened="Falta el telefono", need="El telefono"))

    def fake_once(cfg, **kw):
        return res, Usage(model=cfg.model, input_tokens=10, output_tokens=5, cost_usd=0.0001, duration_ms=1, provider=cfg.id)

    monkeypatch.setattr(eng, "_kickoff_once", fake_once)
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRY_BACKOFF_S", 0)
    assert asyncio.run(eng.run_task(_req())).help_request is None
    out = asyncio.run(eng.run_task(_req(help_allowed=True)))
    assert out.help_request is not None and out.help_request.need == "El telefono"


def test_help_request_is_bounded():
    hr = HelpRequest.model_validate({"kind": "weird", "what_happened": "x" * 5000, "need": "y" * 5000,
                                     "options": [f"o{i}" for i in range(9)] + ["", "  "]})
    assert hr.kind == "other"
    assert len(hr.what_happened) <= 600 and len(hr.need) <= 600
    assert hr.options == [f"o{i}" for i in range(5)]


def test_help_request_is_scrubbed():
    resp = RunTaskResponse(output=StructuredOutput(summary="s", confidence=0.5), usage=Usage(model="m", input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=1),
                           help_request=HelpRequest(what_happened="vi la clave sk-ant-abcdefghijklmnopqrstuvwxyz123456",
                                                    need="confirmar"))
    out = scrub_run_task(resp)
    assert "sk-ant-abcdefghijklmnopqrstuvwxyz123456" not in out.help_request.what_happened
