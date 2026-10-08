"""Organization model policy per role (role_providers / role_models request fields)."""
import pytest

from app.models import RunTaskRequest
from app.providers import ProviderConfig, ProviderError, candidates, policy_from_request
from conftest import run_req

CONFIGS = [
    ProviderConfig("deepseek", "deepseek/deepseek-chat", "d"),
    ProviderConfig("anthropic", "anthropic/claude-sonnet-4-5", "a"),
]


def req(**policy):
    return RunTaskRequest.model_validate({**run_req("legal"), **policy})


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    for k in ("ALLOWED_PROVIDERS", "MODEL_PROVIDER_ORDER_LEGAL"):
        monkeypatch.delenv(k, raising=False)


def test_role_order_puts_the_role_provider_first():
    p = policy_from_request(req(role_providers={"legal": ["anthropic"]}), "legal")
    assert [c.id for c in candidates(CONFIGS, p)] == ["anthropic", "deepseek"]


def test_role_order_of_another_role_is_ignored():
    p = policy_from_request(req(role_providers={"sales": ["anthropic"]}), "legal")
    assert [c.id for c in candidates(CONFIGS, p)] == ["deepseek", "anthropic"]


def test_role_model_swaps_only_that_providers_model():
    p = policy_from_request(req(role_models={"Legal": "anthropic/claude-opus-5-5"}), "legal")
    got = {c.id: c.model for c in candidates(CONFIGS, p)}
    assert got == {"deepseek": "deepseek/deepseek-chat", "anthropic": "anthropic/claude-opus-5-5"}


def test_role_model_never_adds_a_provider_nor_bypasses_allowed():
    p = policy_from_request(req(role_models={"legal": "anthropic/claude-opus-5-5"}, allowed_providers=["deepseek"]),
                            "legal")
    assert [c.id for c in candidates(CONFIGS, p)] == ["deepseek"]
    p = policy_from_request(req(role_models={"legal": "anthropic/claude-opus-5-5"}), "legal")
    assert [c.id for c in candidates(CONFIGS[:1], p)] == ["deepseek"]


def test_malformed_role_model_is_ignored():
    for bad in ("claude-opus-5-5", "openai/gpt-x", ""):
        p = policy_from_request(req(role_models={"legal": bad}), "legal")
        assert p.role_model == ""


def test_operator_env_role_order_still_wins(monkeypatch):
    monkeypatch.setenv("MODEL_PROVIDER_ORDER_LEGAL", "deepseek")
    p = policy_from_request(req(role_providers={"legal": ["anthropic"]}), "legal")
    assert [c.id for c in candidates(CONFIGS, p)] == ["deepseek", "anthropic"]


def test_role_order_cannot_widen_the_operator_ceiling(monkeypatch):
    monkeypatch.setenv("ALLOWED_PROVIDERS", "deepseek")
    p = policy_from_request(req(role_providers={"legal": ["anthropic"]}), "legal")
    assert [c.id for c in candidates(CONFIGS, p)] == ["deepseek"]
    monkeypatch.setenv("ALLOWED_PROVIDERS", "custom")
    with pytest.raises(ProviderError):
        candidates(CONFIGS, p)
