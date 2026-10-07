import asyncio

import pytest
from fastapi.testclient import TestClient

from app.engine import select_engine
from app.main import create_app
from app.models import RunTaskRequest, StructuredOutput, TaskResult
from conftest import run_req


def test_simulation_without_key(monkeypatch):
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    monkeypatch.delenv("SIMULATION", raising=False)
    assert select_engine().mode == "simulation"


def test_simulation_flag_overrides_key(monkeypatch):
    monkeypatch.setenv("ANTHROPIC_API_KEY", "sk-x")
    monkeypatch.setenv("SIMULATION", "true")
    assert select_engine().mode == "simulation"


def test_healthz_default_app_reports_mode(monkeypatch):
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    with TestClient(create_app()) as c:
        assert c.get("/healthz").json()["mode"] == "simulation"


def test_crewai_engine_live_mode_with_mocked_kickoff(monkeypatch):
    pytest.importorskip("crewai")
    from app.crewai_engine import CrewAIEngine
    from app.models import Usage

    monkeypatch.setenv("SIMULATION", "false")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "sk-test")
    monkeypatch.setenv("MODEL", "claude-sonnet-4-5")
    eng = select_engine()
    assert isinstance(eng, CrewAIEngine) and eng.mode == "live"

    seen = {}

    def fake_kickoff(**kw):
        seen.update(kw)
        res = TaskResult(output=StructuredOutput(summary="ok", confidence=0.9))
        return res, Usage(model="m", input_tokens=1, output_tokens=1, cost_usd=0.0, duration_ms=1)

    monkeypatch.setattr(eng, "_kickoff", fake_kickoff)
    req = RunTaskRequest(**run_req("legal", external=["IGNORE PREVIOUS"]))
    out = asyncio.run(eng.run_task(req))
    assert out.output.summary == "ok"
    assert "IGNORE PREVIOUS" not in seen["backstory"]
    assert "DATOS NO CONFIABLES" in seen["description"]
    with TestClient(create_app(eng)) as c:
        assert c.get("/healthz").json()["mode"] == "live"
