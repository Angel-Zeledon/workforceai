"""Prompt-cache tokens: extraction per provider, convention (input includes cached) and cost without double counting."""
from types import SimpleNamespace

import pytest

from app import engine
from app.models import Usage
from app.usage_tokens import tokens_from_metrics, tokens_from_raw_usage


@pytest.fixture(autouse=True)
def no_forced_price(monkeypatch):
    monkeypatch.delenv("PRICE_IN_PER_M", raising=False)
    monkeypatch.delenv("PRICE_OUT_PER_M", raising=False)


def test_openai_cached_tokens_included_in_prompt():
    u = {"prompt_tokens": 1000, "completion_tokens": 50, "prompt_tokens_details": {"cached_tokens": 800}}
    assert tokens_from_raw_usage(u) == (1000, 800, 50)


def test_deepseek_cache_hit_tokens():
    u = {"prompt_tokens": 1000, "completion_tokens": 10, "prompt_cache_hit_tokens": 640, "prompt_cache_miss_tokens": 360}
    assert tokens_from_raw_usage(u) == (1000, 640, 10)


def test_anthropic_read_is_cached_creation_is_ordinary_input():
    u = {"input_tokens": 100, "cache_read_input_tokens": 700, "cache_creation_input_tokens": 200, "output_tokens": 30}
    assert tokens_from_raw_usage(u) == (1000, 700, 30)  # total includes read and write; only the read is a hit


def test_no_cache_fields_means_zero():
    assert tokens_from_raw_usage({"prompt_tokens": 500, "completion_tokens": 5}) == (500, 0, 5)
    assert tokens_from_raw_usage(None) == (0, 0, 0)


def test_cached_never_exceeds_prompt():
    assert tokens_from_raw_usage({"prompt_tokens": 100, "prompt_cache_hit_tokens": 999})[:2] == (100, 100)


def test_crewai_metrics_inclusive_and_exclusive_styles():
    m = SimpleNamespace(prompt_tokens=1000, completion_tokens=20, cached_prompt_tokens=400)
    assert tokens_from_metrics(m, "openai") == (1000, 400, 20)
    m = SimpleNamespace(prompt_tokens=100, completion_tokens=20, cached_prompt_tokens=900)  # excluded style
    assert tokens_from_metrics(m, "anthropic") == (1000, 900, 20)
    assert tokens_from_metrics(SimpleNamespace(prompt_tokens=7, completion_tokens=1), "x") == (7, 0, 1)
    assert tokens_from_metrics(None) == (0, 0, 0)


def test_cost_uses_cached_input_and_does_not_double_count():
    # deepseek-chat: input 0.30, cached 0.006, output 1.20 per M
    full = engine.estimate_cost(1_000_000, 0, "deepseek-chat", "deepseek")
    half = engine.estimate_cost(1_000_000, 0, "deepseek-chat", "deepseek", cached_tokens=500_000)
    assert full == 0.30
    assert half == pytest.approx(0.5 * 0.30 + 0.5 * 0.006)
    assert engine.estimate_cost(1_000_000, 0, "deepseek-chat", "deepseek", cached_tokens=1_000_000) == 0.006


def test_cost_cached_clamped_and_fallback_to_input_price():
    assert engine.estimate_cost(100, 0, "deepseek-chat", cached_tokens=10**9) == engine.estimate_cost(100, 0, "deepseek-chat", cached_tokens=100)
    # a model with no cached_input bills hits at the normal input price
    assert engine.estimate_cost(1_000_000, 0, "unknown-model-zzz", None, cached_tokens=500_000) == engine.estimate_cost(1_000_000, 0, "unknown-model-zzz")


def test_forced_price_and_custom_provider_ignore_cache_discount(monkeypatch):
    assert engine.estimate_cost(1_000_000, 0, "deepseek-chat", "custom", cached_tokens=500_000) == engine.estimate_cost(1_000_000, 0, "deepseek-chat", "custom")
    monkeypatch.setenv("PRICE_IN_PER_M", "1")
    monkeypatch.setenv("PRICE_OUT_PER_M", "2")
    assert engine.estimate_cost(1_000_000, 0, "deepseek-chat", cached_tokens=500_000) == 1.0


def test_usage_json_field_matches_backend_client():
    u = Usage(model="m", input_tokens=10, output_tokens=1, cost_usd=0.0, duration_ms=1, cached_tokens=4)
    assert u.model_dump()["cached_tokens"] == 4


def test_every_cached_input_is_below_its_input_price():
    for name, c in engine.CACHED_PRICES.items():
        assert c < engine.MODEL_PRICES[name][0], name
