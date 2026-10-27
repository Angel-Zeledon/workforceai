"""Gemini (Google) through its OpenAI-compatible endpoint: registry, BYO key, minimal thinking, JSON mode,
thinking tokens counted as output and the rate-limit vs empty-account split. No network."""
import pytest

from app import crewai_engine
from app.cost_control import reasoning_kwargs
from app.models import ProviderCredential
from app.providers import (GEMINI_BASE_URL, KNOWN_PROVIDERS, ProviderConfig, ProviderPolicy, candidates,
                           config_from_credential, configured_providers, default_provider, is_no_balance,
                           is_transient, litellm_model, retry_after_s, temperature_for, vision_capable)
from app.usage_tokens import tokens_from_metrics, tokens_from_raw_usage

ENV = ("GEMINI_API_KEY", "GEMINI_MODEL", "GEMINI_BASE_URL", "DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
       "KIMI_API_KEY", "CUSTOM_LLM_BASE_URL", "MODEL", "MODEL_PROVIDER_ORDER", "ALLOWED_PROVIDERS", "GEMINI_VISION",
       "LLM_REASONING")


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    for k in ENV:
        monkeypatch.delenv(k, raising=False)


def test_gemini_is_known_and_reached_through_the_openai_compatible_endpoint(monkeypatch):
    assert "gemini" in KNOWN_PROVIDERS
    monkeypatch.setenv("GEMINI_API_KEY", "g-key")
    [cfg] = configured_providers()
    assert cfg.id == "gemini"
    assert cfg.model == "openai/gemini-3.6-flash"
    assert cfg.base_url == GEMINI_BASE_URL


def test_gemini_comes_first_when_configured_but_does_not_change_installs_without_its_key(monkeypatch):
    monkeypatch.setenv("DEEPSEEK_API_KEY", "d-key")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "a-key")
    assert default_provider() == "deepseek"
    monkeypatch.setenv("GEMINI_API_KEY", "g-key")
    assert [c.id for c in configured_providers()] == ["gemini", "deepseek", "anthropic"]
    monkeypatch.setenv("MODEL_PROVIDER_ORDER", "deepseek")
    assert default_provider() == "deepseek"


def test_model_and_base_url_overrides(monkeypatch):
    monkeypatch.setenv("GEMINI_API_KEY", "g-key")
    monkeypatch.setenv("GEMINI_MODEL", "gemini-3.5-flash-lite")
    monkeypatch.setenv("GEMINI_BASE_URL", "https://proxy.example/v1beta/openai/")
    [cfg] = configured_providers()
    assert cfg.model == "openai/gemini-3.5-flash-lite"
    assert cfg.base_url == "https://proxy.example/v1beta/openai/"
    assert litellm_model("gemini", "gemini/gemini-3.8-flash") == "openai/gemini-3.8-flash"
    assert litellm_model("gemini", "") == "openai/gemini-3.6-flash"


def test_byo_gemini_key_gets_the_google_endpoint_and_wins_over_the_environment(monkeypatch):
    monkeypatch.setenv("GEMINI_API_KEY", "platform-key")
    cfg = config_from_credential(ProviderCredential(provider="gemini", api_key="org-key", model="gemini-3.8-flash"))
    assert cfg is not None and cfg.byo
    assert cfg.base_url == GEMINI_BASE_URL and cfg.model == "openai/gemini-3.8-flash"
    [picked] = candidates(configured_providers(), ProviderPolicy(credentials=(cfg,)))
    assert picked.api_key == "org-key"


def test_thinking_is_kept_low_unless_reasoning_is_enabled(monkeypatch):
    assert reasoning_kwargs("gemini", "openai/gemini-3.6-flash") == {"reasoning_effort": "low"}
    monkeypatch.setenv("LLM_REASONING", "true")
    assert reasoning_kwargs("gemini", "openai/gemini-3.6-flash") == {}


def test_llm_kwargs_use_json_mode_the_endpoint_and_low_reasoning():
    cfg = ProviderConfig("gemini", "openai/gemini-3.6-flash", "g-key", base_url=GEMINI_BASE_URL)
    kw = crewai_engine.CrewAIEngine._llm_kwargs(cfg, 512, 0.3)
    assert kw["response_format"] == {"type": "json_object"}
    assert kw["base_url"] == GEMINI_BASE_URL
    assert kw["reasoning_effort"] == "low"
    assert kw["model"] == "openai/gemini-3.6-flash"


def test_temperature_range_and_vision(monkeypatch):
    assert temperature_for("gemini", "high") == 1.0
    assert temperature_for("gemini", "") == 0.3
    cfg = ProviderConfig("gemini", "openai/gemini-3.6-flash", "k")
    assert vision_capable(cfg)
    monkeypatch.setenv("GEMINI_VISION", "false")
    assert not vision_capable(cfg)


def test_thinking_tokens_hidden_in_total_tokens_are_billed_as_output():
    # Live shape (2026-10-27, default thinking): completion 6, prompt 16, total 212.
    raw = {"prompt_tokens": 16, "completion_tokens": 6, "total_tokens": 212, "completion_tokens_details": None}
    assert tokens_from_raw_usage(raw) == (16, 0, 196)
    metrics = {"prompt_tokens": 16, "completion_tokens": 6, "total_tokens": 212}
    assert tokens_from_metrics(metrics, "gemini") == (16, 0, 196)
    # Other providers are untouched (OpenAI already counts reasoning inside completion_tokens).
    assert tokens_from_metrics(metrics, "openai") == (16, 0, 6)
    openai_raw = {"prompt_tokens": 16, "completion_tokens": 6, "total_tokens": 22,
                  "completion_tokens_details": {"reasoning_tokens": 0}}
    assert tokens_from_raw_usage(openai_raw) == (16, 0, 6)


class _Err(Exception):
    pass


def test_gemini_rate_limit_is_transient_but_a_zero_limit_is_final():
    rate = _Err("Error code: 429 - You exceeded your current quota, please check your plan and billing details. "
                "Quota exceeded for metric: generate_content_free_tier_requests, limit: 15. Please retry in 18.5s.")
    assert not is_no_balance(rate) and is_transient(rate)
    zero = _Err("Error code: 429 - You exceeded your current quota, please check your plan and billing details. "
                "Quota exceeded for metric: generate_content_free_tier_requests, limit: 0, model: gemini-3.8-flash. "
                "Please retry in 3s.")
    assert is_no_balance(zero)
    # Free tier daily cap (live text, 2026-10-27): a long wait is final, not a retry.
    daily = _Err("Error code: 429 - You exceeded your current quota, please check your plan and billing details. "
                 "Quota exceeded for metric: generate_content_free_tier_requests, limit: 20, model: gemini-3.6-flash, "
                 "description: Generate content requests per day limit exceeded on Free Tier.\n"
                 "Please retry in 11h27m33.817252769s. {'retryDelay': '41253s'}")
    assert is_no_balance(daily)
    long_wait = _Err("429 You exceeded your current quota. Please retry in 5m2s.")
    assert is_no_balance(long_wait)
    assert retry_after_s("Please retry in 11h27m33.8s") == pytest.approx(11 * 3600 + 27 * 60 + 33.8)
    assert retry_after_s("Please retry in 18.5s") == 18.5
    assert retry_after_s("{'retryDelay': '41253s'}") == 41253
    busy = _Err("Error code: 503 - This model is currently experiencing high demand. Please try again later.")
    assert is_transient(busy) and not is_no_balance(busy)
