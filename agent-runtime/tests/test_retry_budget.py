"""Refreshed cap on retries: every retry / provider switch re-runs the budget guard against what is left
after the tokens already burned by failed attempts of the same call."""
from __future__ import annotations

import pytest

from app import cost_control as cc
from app.crewai_engine import CrewAIEngine
from app.models import TaskResult
from app.providers import ProviderConfig


class _SpentError(Exception):
    """Transient failure that already burned tokens; carries the spend like the engine's Usage does."""

    def __init__(self, cost_usd):
        super().__init__("connection reset")
        self.usage = type("U", (), {"cost_usd": cost_usd})()


def _engine(cfgs, spend, calls):
    class Eng(CrewAIEngine):
        def __init__(self):
            self.providers, self._llms = cfgs, {}

        def _kickoff_once(self, cfg, **kw):
            calls.append(cfg.id)
            if len(calls) == 1:
                raise _SpentError(spend)  # fake provider: fails once, after consuming tokens
            return "parsed", type("U", (), {"failed_providers": None})()

    return Eng()


KW = dict(role="r", goal="g", backstory="b" * 3000, description="d" * 3000, expected="e", schema=TaskResult,
          kind="run_task")


def _one_call(cfg):
    return cc.preflight(kind="run_task", model=cfg.model, provider=cfg.id, prompt_texts=["b" * 3000, "d" * 3000, "e"],
                        max_tokens=cc.max_tokens_for("run_task"), allowance_usd=None)


@pytest.fixture(autouse=True)
def _fast(monkeypatch):
    monkeypatch.setattr("app.crewai_engine.PROVIDER_RETRY_BACKOFF_S", 0)


def test_retry_is_refused_when_the_failed_attempt_ate_the_remaining_cap(monkeypatch):
    monkeypatch.setattr("app.crewai_engine.is_transient", lambda exc: True)
    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    one, calls = _one_call(cfg), []
    eng = _engine([cfg], one * 0.6, calls)  # cap fits one call (+20%), not one call after spending 60%
    with pytest.raises(cc.CallBudgetError) as ei:
        eng._kickoff(order=[cfg], max_cost_usd=one * 1.2, **KW)
    assert calls == ["anthropic"] and ei.value.code == "call_budget_exceeded"
    assert ei.value.allowance_usd == pytest.approx(one * 0.6)


def test_retry_goes_ahead_when_the_remaining_cap_fits(monkeypatch):
    monkeypatch.setattr("app.crewai_engine.is_transient", lambda exc: True)
    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    one, calls = _one_call(cfg), []
    out = _engine([cfg], one * 0.1, calls)._kickoff(order=[cfg], max_cost_usd=one * 1.2, **KW)
    assert out[0] == "parsed" and calls == ["anthropic", "anthropic"]


def test_no_cap_means_no_refusal(monkeypatch):
    monkeypatch.setattr("app.crewai_engine.is_transient", lambda exc: True)
    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    calls = []
    out = _engine([cfg], 5.0, calls)._kickoff(order=[cfg], max_cost_usd=0, **KW)
    assert out[0] == "parsed" and len(calls) == 2


def test_provider_switch_is_refused_after_a_spent_attempt(monkeypatch):
    monkeypatch.setattr("app.crewai_engine.is_transient", lambda exc: False)
    a = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    b = ProviderConfig(id="openai", model="openai/gpt-4o", api_key="k")
    one, calls = max(_one_call(a), _one_call(b)), []
    eng = _engine([a, b], one, calls)  # first provider burned the whole cap
    with pytest.raises(cc.CallBudgetError):
        eng._kickoff(order=[a, b], max_cost_usd=one * 1.1, **KW)
    assert calls == ["anthropic"]


# --- the spend of a REAL failed attempt reaches the allowance (not only the fakes above) -------------------

class _Metrics:
    prompt_tokens, completion_tokens, cached_prompt_tokens = 4000, 800, 0


def _direct_engine(llm_cls):
    class Eng(CrewAIEngine):
        def __init__(self):
            self.providers, self._llms, self._LLM = [], {}, llm_cls

        def _llm_kwargs(self, cfg, max_tokens, temperature):
            return {}

    return Eng()


def test_direct_call_whose_json_retry_also_fails_reports_what_it_burned():
    class BadJson:  # answers twice with something that is not the schema
        def call(self, messages):
            return "not json"

        def get_token_usage_summary(self):
            return _Metrics()

    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    with pytest.raises(ValueError) as ei:
        _direct_engine(BadJson)._direct_once(cfg, role="r", goal="g", backstory="b", description="d", expected="e",
                                             schema=TaskResult)
    u = ei.value.usage
    assert (u.input_tokens, u.output_tokens) == (4000, 800) and u.cost_usd > 0 and u.provider == "anthropic"


def test_direct_call_that_fails_before_any_tokens_carries_no_usage():
    class Down:
        def call(self, messages):
            raise ConnectionError("refused")

        def get_token_usage_summary(self):
            return type("M", (), {"prompt_tokens": 0, "completion_tokens": 0, "cached_prompt_tokens": 0})()

    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    with pytest.raises(ConnectionError) as ei:
        _direct_engine(Down)._direct_once(cfg, role="r", goal="g", backstory="b", description="d", expected="e",
                                          schema=TaskResult)
    assert getattr(ei.value, "usage", None) is None  # nothing burned: the retry keeps its full allowance
