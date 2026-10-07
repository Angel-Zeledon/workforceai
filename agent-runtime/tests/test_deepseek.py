from types import SimpleNamespace

import pytest
from pydantic import BaseModel

from app.engine import estimate_cost, select_engine, select_provider


class S(BaseModel):
    answer: str


def clear(mp):
    for k in ("SIMULATION", "DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "MODEL", "PRICE_IN_PER_M", "PRICE_OUT_PER_M"):
        mp.delenv(k, raising=False)


def test_provider_selection(monkeypatch):
    clear(monkeypatch)
    assert select_provider() == "simulation"
    monkeypatch.setenv("ANTHROPIC_API_KEY", "a")
    assert select_provider() == "anthropic"
    monkeypatch.setenv("DEEPSEEK_API_KEY", "d")
    assert select_provider() == "deepseek"
    monkeypatch.setenv("SIMULATION", "true")
    assert select_provider() == "simulation"


def test_deepseek_engine_model_and_health(monkeypatch):
    pytest.importorskip("crewai")
    from fastapi.testclient import TestClient

    from app.main import create_app

    clear(monkeypatch)
    monkeypatch.setenv("DEEPSEEK_API_KEY", "dummy")
    eng = select_engine()
    assert eng.provider == "deepseek" and eng.model == "deepseek/deepseek-chat"
    with TestClient(create_app(eng)) as c:
        assert c.get("/healthz").json() == {"status": "ok", "mode": "live", "provider": "deepseek"}


def test_model_env_without_prefix_gets_provider(monkeypatch):
    pytest.importorskip("crewai")
    clear(monkeypatch)
    monkeypatch.setenv("DEEPSEEK_API_KEY", "dummy")
    monkeypatch.setenv("MODEL", "deepseek-reasoner")
    assert select_engine().model == "deepseek/deepseek-reasoner"


def test_costs_not_claude_for_deepseek(monkeypatch):
    clear(monkeypatch)
    claude = estimate_cost(1_000_000, 1_000_000)
    ds = estimate_cost(1_000_000, 1_000_000, "deepseek/deepseek-chat")
    assert claude == 18.0 and ds < claude / 5
    assert estimate_cost(1_000_000, 0, "deepseek/deepseek-reasoner") > 0
    monkeypatch.setenv("PRICE_IN_PER_M", "1")
    monkeypatch.setenv("PRICE_OUT_PER_M", "2")
    assert estimate_cost(1_000_000, 1_000_000, "deepseek/deepseek-chat") == 3.0


def test_parse_json_fences_and_noise():
    from app.crewai_engine import parse_json_output

    assert parse_json_output('```json\n{"answer": "x"}\n```', S).answer == "x"
    assert parse_json_output('Aqui va:\n{"answer": "y"} gracias', S).answer == "y"
    with pytest.raises(ValueError):
        parse_json_output("no json", S)


def test_kickoff_retries_on_invalid_json(monkeypatch):
    pytest.importorskip("crewai")
    from app.crewai_engine import CrewAIEngine

    clear(monkeypatch)
    eng = CrewAIEngine(provider="deepseek", api_key="dummy")
    outs = iter(["basura", '```json\n{"answer": "ok"}\n```'])
    calls = []

    class FakeCrew:
        def __init__(self, **kw):
            pass

        def kickoff(self):
            calls.append(1)
            return SimpleNamespace(pydantic=None, raw=next(outs), token_usage=None)

    eng._Crew = FakeCrew
    eng._Agent = lambda **kw: object()
    eng._Task = lambda **kw: object()
    parsed, usage = eng._kickoff(role="r", goal="g", backstory="b", description="d", expected="e", schema=S)
    assert parsed.answer == "ok" and len(calls) == 2
