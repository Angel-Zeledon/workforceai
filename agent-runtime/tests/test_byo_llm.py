"""Bring-your-own-model: provider building, selection, redaction and the connection test.

No test here calls a real model: the engine's per-provider call is mocked and SIMULATION is used for the
HTTP layer. conftest sets DISABLE_DEFAULT_LLM_KEY so no platform default key can leak into a test.
"""
import asyncio
import json

import pytest
from fastapi.testclient import TestClient

from app import crewai_engine
from app.engine import estimate_cost
from app.main import create_app
from app.models import ProviderCredential, RunTaskRequest, StructuredOutput, TaskResult, Usage
from app.providers import (
    KIMI_BASE_URL, ProviderConfig, ProviderError, ProviderPolicy, candidates, classify_error,
    config_from_credential, litellm_model, policy_from_request, scrub,
)
from app.simulation import SimulationEngine
from conftest import run_req

KEY = "sk-proj-TESTONLYabcdefghijklmnop1234567890"
ENV = ("SIMULATION", "DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "KIMI_API_KEY", "CUSTOM_LLM_API_KEY",
       "CUSTOM_LLM_BASE_URL", "MODEL", "MODEL_PROVIDER_ORDER", "ALLOWED_PROVIDERS")


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    for k in ENV:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRY_BACKOFF_S", 0)


def cred(provider="openai", **kw):
    return {"provider": provider, "api_key": kw.pop("api_key", KEY), **kw}


def req(**extra):
    return RunTaskRequest.model_validate({**run_req("legal"), **extra})


PLATFORM = [ProviderConfig("deepseek", "deepseek/deepseek-chat", "platform-key")]


# ------------------------------------------------------------------------- building
@pytest.mark.parametrize("provider,model,base,want_model,want_base", [
    ("openai", "gpt-4.1", None, "openai/gpt-4.1", None),
    ("openai", "", None, "openai/gpt-4.1", None),
    ("anthropic", "claude-sonnet-5-5", None, "anthropic/claude-sonnet-5-5", None),
    ("deepseek", "deepseek-chat", None, "deepseek/deepseek-chat", None),
    ("kimi", "kimi-k2-0905-preview", None, "openai/kimi-k2-0905-preview", KIMI_BASE_URL),
    ("kimi", "kimi-latest", "https://api.moonshot.cn/v1", "openai/kimi-latest", "https://api.moonshot.cn/v1"),
    ("custom", "llama3", "http://llm.internal/v1", "openai/llama3", "http://llm.internal/v1"),
])
def test_credential_to_litellm_config(provider, model, base, want_model, want_base):
    c = config_from_credential(ProviderCredential(provider=provider, api_key=KEY, model=model, base_url=base))
    assert (c.model, c.base_url, c.byo) == (want_model, want_base, True)


def test_openai_org_and_project_are_carried():
    c = config_from_credential(ProviderCredential(provider="openai", api_key=KEY, org_id="org-1", project_id="proj_2"))
    assert (c.org_id, c.project_id) == ("org-1", "proj_2")
    kw = crewai_engine.CrewAIEngine._llm_kwargs(c, 100, 0.3)
    assert kw["organization"] == "org-1" and kw["project"] == "proj_2" and kw["model"] == "openai/gpt-4.1"


@pytest.mark.parametrize("bad", [dict(provider="mistral"), dict(provider="openai", api_key=""),
                                 dict(provider="custom", api_key="k")])  # custom without base_url
def test_unusable_credentials_are_ignored(bad):
    assert config_from_credential(ProviderCredential(**{"api_key": "x", **bad})) is None


def test_litellm_prefixes():
    assert litellm_model("kimi", "kimi-k2") == "openai/kimi-k2"
    assert litellm_model("kimi", "kimi/kimi-k2") == "openai/kimi-k2"
    assert litellm_model("openai", "openai/gpt-4o") == "openai/gpt-4o"
    assert litellm_model("anthropic", "claude-opus-5-5") == "anthropic/claude-opus-5-5"
    assert litellm_model("deepseek", "") == "deepseek/deepseek-chat"


def test_reasoning_models_get_no_temperature():
    c = config_from_credential(ProviderCredential(provider="openai", api_key=KEY, model="gpt-5-mini"))
    assert "temperature" not in crewai_engine.CrewAIEngine._llm_kwargs(c, 100, 0.3)
    c = config_from_credential(ProviderCredential(provider="openai", api_key=KEY, model="gpt-4.1"))
    assert crewai_engine.CrewAIEngine._llm_kwargs(c, 100, 0.3)["temperature"] == 0.3


def test_json_mode_for_kimi_and_deepseek_only():
    kimi = config_from_credential(ProviderCredential(provider="kimi", api_key=KEY))
    oa = config_from_credential(ProviderCredential(provider="openai", api_key=KEY))
    assert crewai_engine.CrewAIEngine._llm_kwargs(kimi, 100, 0.3)["response_format"] == {"type": "json_object"}
    assert "response_format" not in crewai_engine.CrewAIEngine._llm_kwargs(oa, 100, 0.3)


def test_structured_output_only_for_providers_that_have_it():
    ns = crewai_engine.native_structured_output
    assert ns(ProviderConfig("openai", "openai/gpt-4.1")) and ns(ProviderConfig("anthropic", "a/b"))
    assert not ns(ProviderConfig("deepseek", "d/x")) and not ns(ProviderConfig("kimi", "openai/k"))
    assert not ns(ProviderConfig("custom", "openai/x"))


# ------------------------------------------------------------------------- selection
def test_org_key_wins_over_environment_for_the_same_provider():
    p = policy_from_request(req(provider_credentials=[cred("deepseek", api_key="org-deepseek-key-123456")]), "legal")
    got = candidates(PLATFORM, p)
    assert [c.id for c in got] == ["deepseek"] and got[0].api_key == "org-deepseek-key-123456" and got[0].byo


def test_org_choice_is_first_and_platform_stays_a_fallback_by_default():
    p = policy_from_request(req(provider_credentials=[cred("openai")], preferred_providers=["openai"]), "legal")
    assert [c.id for c in candidates(PLATFORM, p)] == ["openai", "deepseek"]


def test_byo_only_never_falls_back_to_platform_keys():
    p = policy_from_request(req(provider_credentials=[cred("openai")], preferred_providers=["openai"], byo_only=True),
                            "legal")
    assert [c.id for c in candidates(PLATFORM, p)] == ["openai"]


def test_no_credentials_means_platform_default():
    p = policy_from_request(req(), "legal")
    assert [c.id for c in candidates(PLATFORM, p)] == ["deepseek"]


def test_allowed_providers_still_apply_to_org_keys():
    p = policy_from_request(req(provider_credentials=[cred("openai")], allowed_providers=["deepseek"]), "legal")
    assert [c.id for c in candidates(PLATFORM, p)] == ["deepseek"]
    p = policy_from_request(req(provider_credentials=[cred("openai")], allowed_providers=["anthropic"], byo_only=True),
                            "legal")
    with pytest.raises(ProviderError):
        candidates(PLATFORM, p)


def test_operator_ceiling_applies_to_org_keys(monkeypatch):
    monkeypatch.setenv("ALLOWED_PROVIDERS", "deepseek")
    p = policy_from_request(req(provider_credentials=[cred("openai")], byo_only=True), "legal")
    with pytest.raises(ProviderError) as e:
        candidates(PLATFORM, p)
    assert e.value.code == "no_allowed_provider"


def test_role_model_override_works_on_an_org_key():
    p = policy_from_request(req(provider_credentials=[cred("openai", model="gpt-4.1")], byo_only=True,
                                role_models={"legal": "openai/gpt-4o-mini"}), "legal")
    assert candidates(PLATFORM, p)[0].model == "openai/gpt-4o-mini"


def test_cost_uses_the_new_price_table():
    assert estimate_cost(1_000_000, 1_000_000, "openai/gpt-4o-mini", "openai") == pytest.approx(0.75)
    assert estimate_cost(1_000_000, 0, "openai/kimi-k2-0905-preview", "kimi") == pytest.approx(0.60)
    # unknown model: default (conservative) rate, as before
    assert estimate_cost(1_000_000, 0, "openai/never-heard-of-it", "openai") == pytest.approx(3.0)


# ------------------------------------------------------------------------- engine
def test_org_key_is_used_on_the_call_and_never_cached(monkeypatch):
    pytest.importorskip("crewai")
    eng = crewai_engine.CrewAIEngine(providers=list(PLATFORM))
    seen = []

    def fake_once(cfg, **kw):
        seen.append((cfg.id, cfg.api_key, cfg.byo))
        res = TaskResult(output=StructuredOutput(summary="ok", confidence=0.9))
        return res, Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=1, provider=cfg.id)

    monkeypatch.setattr(eng, "_kickoff_once", fake_once)
    body = {**run_req("legal"), "provider_credentials": [cred("openai")], "preferred_providers": ["openai"],
            "byo_only": True}
    out = asyncio.run(eng.run_task(RunTaskRequest.model_validate(body)))
    assert seen == [("openai", KEY, True)] and out.provider == "openai"


def test_org_llm_objects_are_not_shared_between_tenants():
    pytest.importorskip("crewai")
    eng = crewai_engine.CrewAIEngine(providers=[], allow_empty=True)
    built = []
    eng._LLM = lambda **kw: built.append(kw) or object()
    a = config_from_credential(ProviderCredential(provider="openai", api_key="tenant-A-key-aaaaaaaaaaaa"))
    b = config_from_credential(ProviderCredential(provider="openai", api_key="tenant-B-key-bbbbbbbbbbbb"))
    eng._llm_for(a), eng._llm_for(b), eng._llm_for(a)
    assert [k["api_key"] for k in built] == ["tenant-A-key-aaaaaaaaaaaa", "tenant-B-key-bbbbbbbbbbbb",
                                              "tenant-A-key-aaaaaaaaaaaa"]
    assert eng._llms == {}


def test_failure_message_does_not_contain_the_key(monkeypatch):
    pytest.importorskip("crewai")
    eng = crewai_engine.CrewAIEngine(providers=[], allow_empty=True)
    monkeypatch.setattr(eng, "_kickoff_once", lambda cfg, **kw: (_ for _ in ()).throw(
        RuntimeError(f"401 Incorrect API key provided: {KEY}")))
    body = {**run_req("legal"), "provider_credentials": [cred("openai")], "byo_only": True}
    with pytest.raises(ProviderError) as e:
        asyncio.run(eng.run_task(RunTaskRequest.model_validate(body)))
    assert KEY not in e.value.message and KEY not in json.dumps(e.value.detail())


# ------------------------------------------------------------------------- redaction
def test_key_is_not_in_repr_or_dump_of_the_request():
    r = req(provider_credentials=[cred("openai")])
    assert KEY not in repr(r) and KEY not in str(r)
    assert KEY not in repr(r.provider_credentials[0])
    assert KEY not in json.dumps(r.model_dump(mode="json"))  # SecretStr dumps as ********


def test_scrub_removes_exact_key_even_if_unusual_shaped():
    weird = "abc123XYZ-not-a-known-prefix"
    c = ProviderConfig("custom", "openai/x", weird)
    assert weird not in scrub(f"failed with {weird} at host", [c])


def test_validation_error_does_not_echo_the_key(monkeypatch):
    monkeypatch.setenv("SIMULATION", "true")
    client = TestClient(create_app(SimulationEngine(latency_scale=0)))
    body = run_req("legal")
    body["task"] = "not-an-object"  # invalid
    body["provider_credentials"] = [cred("openai")]
    r = client.post("/v1/run-task", json=body)
    assert r.status_code == 422 and KEY not in r.text


def test_run_task_response_never_contains_the_key(monkeypatch):
    monkeypatch.setenv("SIMULATION", "true")
    client = TestClient(create_app(SimulationEngine(latency_scale=0)))
    body = {**run_req("legal"), "provider_credentials": [cred("openai")], "byo_only": True}
    r = client.post("/v1/run-task", json=body)
    assert r.status_code == 200 and KEY not in r.text


def test_runtime_token_is_required_for_the_credential_hops(monkeypatch):
    monkeypatch.setenv("RUNTIME_TOKEN", "t0ken")
    monkeypatch.setenv("SIMULATION", "true")
    client = TestClient(create_app(SimulationEngine(latency_scale=0)))
    assert client.post("/v1/provider-test", json={"credential": cred()}).status_code == 401
    ok = client.post("/v1/provider-test", json={"credential": cred()}, headers={"Authorization": "Bearer t0ken"})
    assert ok.status_code == 200


# ------------------------------------------------------------------------- connection test
@pytest.mark.parametrize("msg,code", [
    ("litellm.AuthenticationError: 401 Incorrect API key provided", "invalid_key"),
    ("anthropic 401 invalid x-api-key", "invalid_key"),
    ("RateLimitError: 429 You exceeded your current quota, check billing", "quota"),
    ("Insufficient Balance", "quota"),
    ("NotFoundError: 404 The model `gpt-9` does not exist", "model_not_found"),
    ("APIConnectionError: Connection error", "network"),
    ("Timeout: request timed out", "network"),
    ("something odd", "unknown"),
])
def test_classify_error(msg, code):
    assert classify_error(RuntimeError(msg)) == code


@pytest.fixture
def sim_client(monkeypatch):
    monkeypatch.setenv("SIMULATION", "true")
    return TestClient(create_app(SimulationEngine(latency_scale=0)))


@pytest.mark.parametrize("key,code", [("sk-fake-123456789012345", "invalid_key"), ("sim-ok-1", "ok"),
                                      ("sim-quota-1", "quota"), ("sim-nomodel-1", "model_not_found"),
                                      ("sim-net-1", "network")])
def test_provider_test_double_in_simulation(sim_client, key, code):
    r = sim_client.post("/v1/provider-test", json={"credential": cred("openai", api_key=key)})
    assert r.status_code == 200
    j = r.json()
    assert j["code"] == code and j["ok"] == (code == "ok") and key not in r.text


def test_provider_test_rejects_unusable_credential_gracefully(sim_client):
    j = sim_client.post("/v1/provider-test", json={"credential": cred("mistral")}).json()
    assert j["ok"] is False and j["code"] == "invalid_request"


def test_live_engine_provider_test_classifies_and_redacts(monkeypatch):
    pytest.importorskip("crewai")
    eng = crewai_engine.CrewAIEngine(providers=[], allow_empty=True)

    class Boom:
        def __init__(self, **kw):
            pass

        def call(self, _m):
            raise RuntimeError(f"401 Incorrect API key provided: {KEY}")

    eng._LLM = Boom
    c = config_from_credential(ProviderCredential(provider="openai", api_key=KEY))
    out = eng.test_provider(c)
    assert out["code"] == "invalid_key" and not out["ok"] and KEY not in json.dumps(out)


def test_health_lists_new_providers(monkeypatch):
    monkeypatch.setenv("SIMULATION", "true")
    monkeypatch.setenv("OPENAI_API_KEY", KEY)
    monkeypatch.setenv("DEEPSEEK_API_KEY", "dk-1234567890123456")
    client = TestClient(create_app(SimulationEngine(latency_scale=0)))
    j = client.get("/healthz").json()
    assert j["configured_providers"] == ["deepseek", "openai"] and j["default_provider"] == "deepseek"
    assert KEY not in client.get("/healthz").text
