"""Provider failover, data policy, stable errors and per-provider costs. No network: kickoff is mocked."""
import asyncio

import pytest
from fastapi.testclient import TestClient

from app import crewai_engine
from app.engine import estimate_cost, select_engine, select_provider
from app.main import create_app
from app.models import RunTaskRequest, StructuredOutput, TaskResult, Usage
from app.providers import ProviderConfig, ProviderError, ProviderPolicy, candidates
from conftest import run_req

ENV = ("SIMULATION", "DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "CUSTOM_LLM_API_KEY", "CUSTOM_LLM_BASE_URL",
       "CUSTOM_LLM_MODEL", "MODEL", "ANTHROPIC_MODEL", "DEEPSEEK_MODEL", "MODEL_PROVIDER_ORDER",
       "ALLOWED_PROVIDERS", "PRICE_IN_PER_M", "PRICE_OUT_PER_M", "CUSTOM_PRICE_IN_PER_M", "CUSTOM_PRICE_OUT_PER_M")


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    for k in ENV:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRY_BACKOFF_S", 0)


def cfgs():
    return [ProviderConfig("deepseek", "deepseek/deepseek-chat", "k1"),
            ProviderConfig("anthropic", "anthropic/claude-sonnet-4-5", "k2"),
            ProviderConfig("custom", "openai/local", "k3", base_url="http://llm.internal/v1")]


def make_engine(monkeypatch, outcomes, providers=None):
    """Engine whose per-provider call is scripted: outcomes[provider_id] = exception | tokens (in, out)."""
    pytest.importorskip("crewai")
    eng = crewai_engine.CrewAIEngine(providers=providers or cfgs())
    calls = []

    def fake_once(cfg, **kw):
        calls.append(cfg.id)
        out = outcomes[cfg.id]
        if isinstance(out, list):  # sequence of outcomes for repeated calls
            out = out.pop(0)
        if isinstance(out, Exception):
            raise out
        i, o = out
        res = TaskResult(output=StructuredOutput(summary="ok", confidence=0.9))
        return res, Usage(model=cfg.model, input_tokens=i, output_tokens=o,
                          cost_usd=estimate_cost(i, o, cfg.model, cfg.id), duration_ms=1, provider=cfg.id)

    monkeypatch.setattr(eng, "_kickoff_once", fake_once)
    eng.calls = calls
    return eng


def run(eng, **extra):
    return asyncio.run(eng.run_task(RunTaskRequest(**{**run_req("legal"), **extra})))


# ---------------------------------------------------------------- selection / ordering
def test_default_order_is_backwards_compatible(monkeypatch):
    monkeypatch.setenv("ANTHROPIC_API_KEY", "a")
    assert select_provider() == "anthropic"
    monkeypatch.setenv("DEEPSEEK_API_KEY", "d")
    assert select_provider() == "deepseek"
    monkeypatch.setenv("MODEL_PROVIDER_ORDER", "anthropic,deepseek")
    assert select_provider() == "anthropic"


def test_custom_slot_needs_base_url(monkeypatch):
    monkeypatch.setenv("CUSTOM_LLM_API_KEY", "x")
    assert select_provider() == "simulation"
    monkeypatch.setenv("CUSTOM_LLM_BASE_URL", "http://vllm:8000/v1")
    monkeypatch.setenv("CUSTOM_LLM_MODEL", "llama-3")
    assert select_provider() == "custom"
    pytest.importorskip("crewai")
    eng = select_engine()
    assert eng.providers[0].model == "openai/llama-3" and eng.providers[0].base_url == "http://vllm:8000/v1"


def test_role_env_and_request_preference_order(monkeypatch):
    monkeypatch.setenv("MODEL_PROVIDER_ORDER_LEGAL", "anthropic")
    order = [c.id for c in candidates(cfgs(), ProviderPolicy(role="legal"))]
    assert order == ["anthropic", "deepseek", "custom"]  # role first, the rest stay as fallbacks
    order = [c.id for c in candidates(cfgs(), ProviderPolicy(preferred=("custom",), role="sales"))]
    assert order == ["custom", "deepseek", "anthropic"]


# ---------------------------------------------------------------- failover
def test_failover_on_error_records_provider_model_and_cost(monkeypatch):
    eng = make_engine(monkeypatch, {"deepseek": RuntimeError("boom"), "anthropic": (1_000_000, 1_000_000)})
    out = run(eng)
    assert eng.calls == ["deepseek", "anthropic"]
    assert out.provider == out.usage.provider == "anthropic"
    assert out.model == out.usage.model == "anthropic/claude-sonnet-4-5"
    assert out.usage.failed_providers == ["deepseek"]
    assert out.usage.cost_usd == 18.0  # Sonnet rate, not the DeepSeek one


def test_transient_error_retried_on_same_provider_then_succeeds(monkeypatch):
    eng = make_engine(monkeypatch, {"deepseek": [TimeoutError("request timed out"), (10, 10)]})
    out = run(eng)
    assert eng.calls == ["deepseek", "deepseek"] and out.provider == "deepseek"
    assert out.usage.failed_providers is None


def test_rate_limit_fails_over_after_retries(monkeypatch):
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRIES", 1)
    eng = make_engine(monkeypatch, {"deepseek": RuntimeError("429 rate limit"), "anthropic": (5, 5)})
    out = run(eng)
    assert eng.calls == ["deepseek", "deepseek", "anthropic"] and out.provider == "anthropic"


def test_non_transient_error_is_not_retried(monkeypatch):
    eng = make_engine(monkeypatch, {"deepseek": ValueError("invalid api key"), "anthropic": (5, 5)})
    run(eng)
    assert eng.calls == ["deepseek", "anthropic"]


def test_all_providers_failed_is_stable_error_without_secrets(monkeypatch):
    err = RuntimeError("auth failed api_key=sk-ant-abcdefghijklmnopqrstuvwxyz123456")
    eng = make_engine(monkeypatch, {"deepseek": err, "anthropic": err, "custom": err})
    with pytest.raises(ProviderError) as e:
        run(eng)
    assert e.value.code == "all_providers_failed" and e.value.tried == ["deepseek", "anthropic", "custom"]
    assert "abcdefghijklmnopqrstuvwxyz" not in e.value.message
    with TestClient(create_app(eng), raise_server_exceptions=False) as c:
        r = c.post("/v1/run-task", json=run_req("legal"))
    assert r.status_code == 502 and r.json()["detail"]["code"] == "all_providers_failed"


# ---------------------------------------------------------------- data policy
def test_excluded_provider_is_never_called(monkeypatch):
    eng = make_engine(monkeypatch, {"anthropic": (5, 5)})  # a deepseek call would KeyError
    out = run(eng, allowed_providers=["anthropic"])
    assert eng.calls == ["anthropic"] and out.provider == "anthropic"


def test_excluded_provider_not_used_as_fallback_either(monkeypatch):
    eng = make_engine(monkeypatch, {"anthropic": RuntimeError("down"), "deepseek": (1, 1)})
    with pytest.raises(ProviderError) as e:
        run(eng, allowed_providers=["anthropic"])
    assert eng.calls == ["anthropic"] and e.value.code == "all_providers_failed"


def test_no_allowed_provider_fails_before_sending(monkeypatch):
    eng = make_engine(monkeypatch, {}, providers=[cfgs()[0]])  # only deepseek configured
    with TestClient(create_app(eng), raise_server_exceptions=False) as c:
        r = c.post("/v1/run-task", json={**run_req("legal"), "allowed_providers": ["anthropic"]})
        assert r.status_code == 422
        d = r.json()["detail"]
        assert d["code"] == "no_allowed_provider" and d["configured_providers"] == ["deepseek"]
        assert d["allowed_providers"] == ["anthropic"] and "key" not in str(d).lower().replace("provider", "")
        r2 = c.post("/v1/run-task", json={**run_req("legal"), "allowed_providers": []})
        assert r2.status_code == 422 and r2.json()["detail"]["code"] == "no_allowed_provider"
    assert eng.calls == []


def test_env_ceiling_intersects_request_list(monkeypatch):
    monkeypatch.setenv("ALLOWED_PROVIDERS", "anthropic, custom")
    eng = make_engine(monkeypatch, {"anthropic": (1, 1), "custom": (1, 1)})
    run(eng)
    assert eng.calls == ["anthropic"]  # deepseek excluded by the operator, no request field needed
    with pytest.raises(ProviderError) as e:
        run(eng, allowed_providers=["deepseek"])  # a request cannot widen the ceiling
    assert e.value.code == "no_allowed_provider"


def test_policy_applies_to_every_endpoint(monkeypatch):
    eng = make_engine(monkeypatch, {})
    with TestClient(create_app(eng), raise_server_exceptions=False) as c:
        bodies = {
            "/v1/plan": {"request_text": "x", "agents": [], "allowed_providers": []},
            "/v1/consult": {"from_agent_id": "a", "to_agent_id": "b", "question": "q", "allowed_providers": []},
            "/v1/synthesize": {"request_text": "x", "outputs": [], "allowed_providers": []},
        }
        for path, body in bodies.items():
            r = c.post(path, json=body)
            assert r.status_code == 422 and r.json()["detail"]["code"] == "no_allowed_provider", path


# ---------------------------------------------------------------- costs
def test_costs_per_provider(monkeypatch):
    assert estimate_cost(1_000_000, 1_000_000, "anthropic/claude-sonnet-4-5", "anthropic") == 18.0
    assert estimate_cost(1_000_000, 1_000_000, "deepseek/deepseek-chat", "deepseek") == 1.5
    # custom endpoint: the model name must not be priced as DeepSeek/Claude by accident
    assert estimate_cost(1_000_000, 1_000_000, "openai/deepseek-chat", "custom") == 18.0
    monkeypatch.setenv("CUSTOM_PRICE_IN_PER_M", "0")
    monkeypatch.setenv("CUSTOM_PRICE_OUT_PER_M", "0")
    assert estimate_cost(1_000_000, 1_000_000, "openai/local", "custom") == 0.0
    assert estimate_cost(1_000_000, 1_000_000, "anthropic/claude-sonnet-4-5", "anthropic") == 18.0


# ---------------------------------------------------------------- simulation intact
def test_simulation_ignores_policy_and_contract_is_unchanged(client):
    r = client.post("/v1/run-task", json={**run_req("legal"), "allowed_providers": []})
    assert r.status_code == 200
    body = r.json()
    assert "provider" not in body and "provider" not in body["usage"]
    assert client.get("/healthz").json()["mode"] == "simulation"


def test_old_requests_without_policy_fields_still_validate():
    req = RunTaskRequest(**run_req("legal"))
    assert req.allowed_providers is None and req.preferred_providers is None
