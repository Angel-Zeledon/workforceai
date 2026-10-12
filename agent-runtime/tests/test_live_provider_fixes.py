"""Bugs found running against DeepSeek for real: JSON-schema response_format is not
supported by DeepSeek, and a failed live engine must never look healthy."""

import json

import pytest

from app.models import StructuredOutput


def test_only_anthropic_gets_native_structured_output():
    crewai_engine = pytest.importorskip("app.crewai_engine")
    from app.providers import ProviderConfig

    assert crewai_engine.native_structured_output(ProviderConfig(id="anthropic", model="anthropic/x"))
    assert not crewai_engine.native_structured_output(ProviderConfig(id="deepseek", model="deepseek/deepseek-chat"))
    assert not crewai_engine.native_structured_output(ProviderConfig(id="custom", model="openai/x"))


def test_json_instructions_carry_the_schema():
    crewai_engine = pytest.importorskip("app.crewai_engine")
    text = crewai_engine.json_instructions(StructuredOutput)
    schema = json.loads(text[text.index("{"):])
    assert "summary" in schema["properties"] and "confidence" in schema["properties"]
    assert "SOLO con un objeto JSON" in text


def test_failed_live_engine_reports_degraded(monkeypatch):
    from fastapi.testclient import TestClient

    from app import engine as engine_mod
    from app.main import create_app

    monkeypatch.setenv("DEEPSEEK_API_KEY", "sk-test")
    monkeypatch.delenv("SIMULATION", raising=False)

    class Boom:
        def __init__(self, *a, **k):
            raise PermissionError("[Errno 13] Permission denied: '/home/app'")

    import app.crewai_engine as ce
    monkeypatch.setattr(ce, "CrewAIEngine", Boom)
    eng = engine_mod.select_engine()
    assert eng.mode == "simulation" and "Permission denied" in eng.fallback_reason

    client = TestClient(create_app(engine=eng))
    h = client.get("/healthz").json()
    assert h["status"] == "degraded" and h["mode"] == "simulation" and "live engine unavailable" in h["warning"]
