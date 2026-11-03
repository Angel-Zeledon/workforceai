"""Regressions for what a live run against Gemini (gemini-3.6-flash, free tier, 2026-11-02) found. No network:
the CrewAI objects are test doubles that behave like the real ones where it matters (lifetime token counters,
total_tokens dropped by the framework, 429 bodies with Gemini's retryDelay)."""
from __future__ import annotations

import asyncio
import os
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

from app import cost_control as cc
from app import crewai_engine, rate_limit
from app.crewai_engine import CrewAIEngine, _ConsultAnswer, _UsageTap
from app.main import create_app
from app.models import RouteRequest, SynthesizeRequest, Usage
from app.providers import (ERR_NO_BALANCE, ERR_RATE_LIMITED, ProviderConfig, ProviderError, is_no_balance,
                           is_rate_limited, retry_after_from_exc)
from app.simulation import SimulationEngine
from app.usage_tokens import tokens_from_raw_usage

GEMINI = ProviderConfig(id="gemini", model="openai/gemini-3.6-flash", api_key="k",
                        base_url="https://generativelanguage.googleapis.com/v1beta/openai/")
# What Gemini's OpenAI-compatible endpoint answers on a per-minute limit (shape of the live body, shortened).
GEMINI_429 = ("Error code: 429 - [{'error': {'code': 429, 'message': 'You exceeded your current quota, please check "
              "your plan and billing details. Quota exceeded for metric: generativelanguage.googleapis.com/"
              "generate_content_free_tier_requests, limit: 10, model: gemini-3.6-flash. Please retry in 6.2s.', "
              "'status': 'RESOURCE_EXHAUSTED', 'details': [{'@type': 'type.googleapis.com/google.rpc.RetryInfo', "
              "'retryDelay': '6s'}]}}]")


class RateLimitError(Exception):  # same class name as openai.RateLimitError
    status_code = 429


# ------------------------------------------------------------------ CrewAI doubles
class FakeLLM:
    """Like crewai's BaseLLM: lifetime counters, total_tokens rebuilt as prompt + completion (thinking lost)."""

    built: list["FakeLLM"] = []

    def __init__(self, **kw):
        self.kw = kw
        self.counters = {"prompt_tokens": 0, "completion_tokens": 0, "cached_prompt_tokens": 0}
        FakeLLM.built.append(self)

    def _track_token_usage_internal(self, usage: dict) -> None:
        self.counters["prompt_tokens"] += usage.get("prompt_tokens", 0)
        self.counters["completion_tokens"] += usage.get("completion_tokens", 0)

    def get_token_usage_summary(self):
        c = self.counters
        return SimpleNamespace(**c, total_tokens=c["prompt_tokens"] + c["completion_tokens"])


class FakeAgent:
    def __init__(self, **kw):
        self.llm = kw["llm"]


def _engine(answers: list[tuple[str, dict]]) -> CrewAIEngine:
    """Each kickoff consumes one (raw text, raw provider usage) from `answers`."""

    class FakeCrew:
        def __init__(self, agents, **kw):
            self.llm = agents[0].llm

        def kickoff(self):
            raw, usage = answers.pop(0)
            self.llm._track_token_usage_internal(usage)
            return SimpleNamespace(pydantic=None, raw=raw, token_usage=self.llm.get_token_usage_summary())

    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    eng._LLM, eng._Agent, eng._Task, eng._Crew = FakeLLM, FakeAgent, lambda **kw: object(), FakeCrew
    eng._Process = SimpleNamespace(sequential=1)
    FakeLLM.built = []
    return eng


ONCE = dict(role="r", goal="g", backstory="b", description="d", expected="e", schema=_ConsultAnswer)


# ------------------------------------------------------------------ 1. a repo .env never leaks into the runtime
def test_importing_the_runtime_disables_dotenv_before_crewai_can_load_a_repo_env(tmp_path):
    env_file = tmp_path / ".env"
    env_file.write_text("LEAKED_FROM_DOTENV=yes\n", encoding="utf-8")
    code = ("import os, app, dotenv; dotenv.load_dotenv(r'%s'); "
            "print(os.environ.get('PYTHON_DOTENV_DISABLED'), os.environ.get('LEAKED_FROM_DOTENV'))" % env_file)
    env = {k: v for k, v in os.environ.items() if k not in ("PYTHON_DOTENV_DISABLED", "LEAKED_FROM_DOTENV")}
    out = subprocess.run([sys.executable, "-c", code], cwd=Path(__file__).resolve().parents[1], env=env,
                         capture_output=True, text=True, timeout=60)
    assert out.returncode == 0, out.stderr
    assert out.stdout.split() == ["true", "None"]


# ------------------------------------------------------------------ 2. per-call usage, thinking included
def test_usage_is_per_call_not_the_lifetime_of_a_shared_llm():
    usage = {"prompt_tokens": 1000, "completion_tokens": 50, "total_tokens": 1050}
    eng = _engine([('{"answer": "a"}', dict(usage)), ('{"answer": "b"}', dict(usage))])
    _, u1 = eng._kickoff_once(GEMINI, **ONCE, max_tokens=800)
    _, u2 = eng._kickoff_once(GEMINI, **ONCE, max_tokens=800)
    assert (u1.input_tokens, u1.output_tokens) == (u2.input_tokens, u2.output_tokens) == (1000, 50)
    assert len(FakeLLM.built) == 2 and eng._llms == {}  # one LLM per call, nothing cached


def test_gemini_thinking_tokens_are_billed_even_though_crewai_drops_them():
    # live: prompt 1045, completion 10, total 1341 -> 286 thinking tokens billed as output
    eng = _engine([('{"answer": "ok"}', {"prompt_tokens": 1045, "completion_tokens": 10, "total_tokens": 1341})])
    _, u = eng._kickoff_once(GEMINI, **ONCE, max_tokens=812)
    assert (u.input_tokens, u.output_tokens) == (1045, 296)
    assert u.cost_usd == pytest.approx((1045 * 1.5 + 296 * 7.5) / 1e6, abs=1e-5)


def test_usage_tap_falls_back_to_the_framework_metrics_for_plain_objects():
    tap = _UsageTap(object())
    assert not tap.ok
    assert tap.tokens(GEMINI, SimpleNamespace(prompt_tokens=7, completion_tokens=3)) == (7, 0, 3)


def test_crewai_style_usage_dict_keeps_cache_hits():
    # CrewAI normalises every provider's cache hits to cached_prompt_tokens (Anthropic input already includes them)
    assert tokens_from_raw_usage({"input_tokens": 1200, "output_tokens": 40, "total_tokens": 1240,
                                  "cached_prompt_tokens": 900, "cache_creation_tokens": 100}) == (1200, 900, 40)
    assert tokens_from_raw_usage({"prompt_tokens": 500, "completion_tokens": 9, "total_tokens": 509,
                                  "cached_prompt_tokens": 300}) == (500, 300, 9)


# ------------------------------------------------------------------ 3. answers cut by the cap
def test_json_cut_by_the_output_cap_is_retried_with_a_bigger_cap():
    # live: the route JSON came back "Unterminated string" twice: same cap, same cut
    eng = _engine([('{"answer": "cortad', {"prompt_tokens": 1000, "completion_tokens": 10, "total_tokens": 1300}),
                   ('{"answer": "completo"}', {"prompt_tokens": 1000, "completion_tokens": 20, "total_tokens": 1320})])
    parsed, u = eng._kickoff_once(GEMINI, **ONCE, max_tokens=300)
    assert parsed.answer == "completo"
    assert [b.kw["max_tokens"] for b in FakeLLM.built] == [300, 600]
    assert (u.input_tokens, u.output_tokens) == (2000, 620)  # both requests billed, once each


def test_malformed_but_not_cut_json_retries_on_the_same_llm():
    eng = _engine([("not json", {"prompt_tokens": 1000, "completion_tokens": 20, "total_tokens": 1020}),
                   ('{"answer": "ok"}', {"prompt_tokens": 1000, "completion_tokens": 20, "total_tokens": 1020})])
    _, u = eng._kickoff_once(GEMINI, **ONCE, max_tokens=800)
    assert len(FakeLLM.built) == 1 and (u.input_tokens, u.output_tokens) == (2000, 40)


def test_gemini_calls_get_room_for_thinking_and_the_guard_counts_it(monkeypatch):
    sent = []
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        sent.append(kw["max_tokens"])
        return "ok", Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=0)

    eng._kickoff(order=[GEMINI], once=once, kind="route", **{k: v for k, v in ONCE.items()})
    assert sent == [cc.max_tokens_for("route") + cc.thinking_headroom("gemini")]
    anthropic = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    eng._kickoff(order=[anthropic], once=once, kind="route", **ONCE)
    assert sent[-1] == cc.max_tokens_for("route")  # no headroom where the cap does not count thinking
    assert cc.thinking_headroom("gemini") >= 590 + 80  # live: a route thought 590 tokens for a 71-token answer


def test_thinking_headroom_is_dropped_rather_than_refusing_a_call_that_fits_without_it():
    sent = []
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        sent.append(kw["max_tokens"])
        return "ok", Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=0)

    texts = CrewAIEngine._prompt_texts(GEMINI, dict(ONCE))
    plain = cc.preflight(kind="consult", model=GEMINI.model, provider="gemini", prompt_texts=texts,
                         max_tokens=cc.max_tokens_for("consult"), allowance_usd=None)
    eng._kickoff(order=[GEMINI], once=once, kind="consult", max_cost_usd=plain * 1.01, **ONCE)
    assert sent == [cc.max_tokens_for("consult")]
    with pytest.raises(cc.CallBudgetError):  # below even the plain cap: still refused, nothing sent
        eng._kickoff(order=[GEMINI], once=once, kind="consult", max_cost_usd=plain * 0.9, **ONCE)
    assert len(sent) == 1


# ------------------------------------------------------------------ 4. rate limits
def test_no_hidden_retry_layers():
    assert CrewAIEngine._llm_kwargs(GEMINI, 300, 0.3)["max_retries"] == 0  # SDK retries off
    assert cc.crew_max_retry_limit() == 0  # CrewAI's re-run of the task on any error off


def test_gemini_429_is_a_rate_limit_with_its_retry_delay_not_a_lack_of_balance():
    exc = RateLimitError(GEMINI_429)
    assert is_rate_limited(exc) and not is_no_balance(exc)
    assert retry_after_from_exc(exc) == 6.0  # retryDelay wins over the rounded "retry in"
    hdr = RateLimitError("429")
    hdr.response = SimpleNamespace(headers={"retry-after": "3"})
    assert retry_after_from_exc(hdr) == 3.0


def test_rate_limit_waits_what_the_provider_asked_then_succeeds(monkeypatch):
    slept, calls = [], []
    monkeypatch.setattr(crewai_engine.time, "sleep", slept.append)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        calls.append(cfg.id)
        if len(calls) == 1:
            raise RateLimitError(GEMINI_429)
        return "ok", Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=0)

    out, _ = eng._kickoff(order=[GEMINI], once=once, kind="run_task", **ONCE)
    assert out == "ok" and calls == ["gemini", "gemini"] and slept == [6.5]


def test_interactive_call_does_not_wait_longer_than_the_chat_timeout(monkeypatch):
    slept, calls = [], []
    monkeypatch.setattr(crewai_engine.time, "sleep", slept.append)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        calls.append(1)
        raise RateLimitError(GEMINI_429.replace("6.2s", "25.0s").replace("'6s'", "'25s'"))

    with pytest.raises(ProviderError) as ei:
        eng._kickoff(order=[GEMINI], once=once, kind="chat", **ONCE)
    e = ei.value
    assert e.code == ERR_RATE_LIMITED and e.status_code == 429 and e.retry_after_s == 25.0
    assert calls == [1] and slept == []  # 25 s > the 8 s an interactive call may wait: one request, no sleep
    assert "25s" in e.message


def test_a_known_cooldown_answers_at_once_without_sending(monkeypatch):
    calls = []
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    rate_limit.note_rate_limited("gemini", 40)
    with pytest.raises(ProviderError) as ei:
        eng._kickoff(order=[GEMINI], once=lambda cfg, **kw: calls.append(1), kind="route", **ONCE)
    assert ei.value.code == ERR_RATE_LIMITED and calls == [] and 30 < ei.value.retry_after_s <= 40


def test_daily_free_tier_cap_is_still_final():
    daily = RateLimitError(GEMINI_429.replace("generate_content_free_tier_requests", "GenerateRequestsPerDay")
                           .replace("Please retry in 6.2s", "Please retry in 11h27m33s").replace("'6s'", "'41253s'"))
    assert is_no_balance(daily)


def test_rate_limited_answer_is_http_429_with_retry_after():
    class Limited(SimulationEngine):
        async def consult(self, req):
            raise ProviderError(ERR_RATE_LIMITED, "rate limited", tried=["gemini"], retry_after_s=12.3)

    r = TestClient(create_app(Limited(latency_scale=0))).post(
        "/v1/consult", json={"from_agent_id": "a", "to_agent_id": "b", "question": "q"})
    assert r.status_code == 429 and r.headers["retry-after"] == "13"
    assert r.json()["detail"]["code"] == ERR_RATE_LIMITED and r.json()["detail"]["retry_after_s"] == 12.3


# ------------------------------------------------------------------ 5. pacing (LLM_RPM_<PROVIDER>)
def test_pacing_spaces_calls_within_the_per_minute_quota(monkeypatch):
    now = [1000.0]
    monkeypatch.setattr(rate_limit, "_clock", lambda: now[0])
    monkeypatch.setattr(rate_limit, "_sleep", lambda s: now.__setitem__(0, now[0] + s))
    monkeypatch.setenv("LLM_RPM_GEMINI", "2")
    assert rate_limit.acquire("gemini", 0) == 0 and rate_limit.acquire("gemini", 0) == 0
    assert rate_limit.acquire("gemini", 5) == pytest.approx(60)  # would need a minute: refused, nothing waited
    assert now[0] == 1000.0
    assert rate_limit.acquire("gemini", 90) == 0 and now[0] >= 1060  # waits for the window when allowed
    assert rate_limit.acquire("deepseek", 0) == 0  # other providers are not paced


# ------------------------------------------------------------------ 6. nothing billed is lost
def test_route_fallback_reports_the_tokens_the_failed_llm_calls_burned():
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    burned = Usage(model=GEMINI.model, provider="gemini", input_tokens=2090, output_tokens=600, cost_usd=0.0076,
                   duration_ms=5)

    async def boom(**kw):
        err = ProviderError("all_providers_failed", "bad json", tried=["gemini"])
        err.usage = burned
        raise err

    eng._run = boom
    req = RouteRequest.model_validate({"text": "Calcula el margen", "conversation": "office",
                                       "agents": [{"id": "accounting", "role": "accounting"},
                                                  {"id": "assistant", "role": "assistant"}]})
    out = asyncio.run(eng.route(req))
    assert out.source == "rules" and out.usage == burned


def test_failed_attempts_travel_on_the_final_error():
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        exc = ValueError("Unterminated string")
        exc.usage = Usage(model=cfg.model, provider="gemini", input_tokens=1000, output_tokens=300, cost_usd=0.004,
                          duration_ms=1)
        raise exc

    with pytest.raises(ProviderError) as ei:
        eng._kickoff(order=[GEMINI], once=once, kind="route", **ONCE)
    assert ei.value.usage.input_tokens == 1000 and ei.value.detail()["spent_usd"] == 0.004


def test_synthesis_reports_its_usage_to_the_backend():
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    u = Usage(model=GEMINI.model, provider="gemini", input_tokens=5000, output_tokens=900, cost_usd=0.014,
              duration_ms=1)

    async def run(**kw):
        return crewai_engine._Synth(title="t", summary="s", sections=[]), u

    eng._run = run
    out = asyncio.run(eng.synthesize(SynthesizeRequest.model_validate({"request_text": "x", "outputs": []})))
    assert out.usage == u
    assert "usage" in out.model_dump(exclude_none=True)


# ------------------------------------------------------------------ 7. 503 "high demand" (frequent on the free tier)
GEMINI_503 = ("Error code: 503 - [{'error': {'code': 503, 'message': 'This model is currently experiencing high "
              "demand. Spikes in demand are usually temporary. Please try again later.', 'status': 'UNAVAILABLE'}}]")


class InternalServerError(Exception):  # openai maps 503 to InternalServerError
    status_code = 503


def _flaky(errors: list[Exception]):
    calls = []

    def once(cfg, **kw):
        calls.append(cfg.id)
        if errors:
            raise errors.pop(0)
        return "ok", Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=0)

    return once, calls


def test_503_high_demand_is_retried_with_exponential_backoff(monkeypatch):
    from app.providers import is_overloaded

    slept = []
    monkeypatch.setattr(crewai_engine.time, "sleep", slept.append)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    once, calls = _flaky([InternalServerError(GEMINI_503), InternalServerError(GEMINI_503)])
    out, _ = eng._kickoff(order=[GEMINI], once=once, kind="run_task", **ONCE)
    assert out == "ok" and len(calls) == 3 and slept == [2.0, 4.0]
    assert is_overloaded(InternalServerError(GEMINI_503)) and not is_rate_limited(InternalServerError(GEMINI_503))


def test_503_that_does_not_pass_fails_cleanly_after_the_retries(monkeypatch):
    slept = []
    monkeypatch.setattr(crewai_engine.time, "sleep", slept.append)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    once, calls = _flaky([InternalServerError(GEMINI_503) for _ in range(5)])
    with pytest.raises(ProviderError) as ei:
        eng._kickoff(order=[GEMINI], once=once, kind="chat", **ONCE)
    assert len(calls) == 3 and ei.value.code == "all_providers_failed" and "high demand" in ei.value.message
    assert all(s <= cc.max_retry_wait_s("chat") for s in slept)  # a chat never waits past its own bound


def test_deepseek_permanent_unavailable_parameter_is_not_an_overload():
    from app.providers import is_overloaded

    assert not is_overloaded(ValueError("400 This response_format type is unavailable now"))


def test_an_invalid_json_retry_is_logged(caplog):
    """Live (DeepSeek): one run_task in 30 cost double because its first answer was not valid JSON; nothing said so."""
    eng = _engine([("no es json", {"prompt_tokens": 1644, "completion_tokens": 1489, "total_tokens": 3133}),
                   ('{"answer": "ok"}', {"prompt_tokens": 1644, "completion_tokens": 1449, "total_tokens": 3093})])
    with caplog.at_level("WARNING", logger="agent_runtime"):
        eng._kickoff_once(GEMINI, **ONCE, max_tokens=4000)
    assert any("invalid JSON" in r.getMessage() and "retrying once" in r.getMessage() for r in caplog.records)


# ------------------------------------------------------------------ 8. interactive calls end before the backend gives up
def test_interactive_call_requests_are_bounded_by_the_chat_deadline():
    """Live (DeepSeek): a /v1/route outlived the backend's 30 s chat timeout; its answer was thrown away, still billed."""
    seen = []
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}

    def once(cfg, **kw):
        seen.append(CrewAIEngine._llm_kwargs(cfg, kw["max_tokens"], 0.3)["timeout"])
        return "ok", Usage(model=cfg.model, input_tokens=1, output_tokens=1, cost_usd=0, duration_ms=0)

    eng._kickoff(order=[GEMINI], once=once, kind="route", **ONCE)
    eng._kickoff(order=[GEMINI], once=once, kind="run_task", **ONCE)
    assert 20 < seen[0] <= cc.call_deadline_s("route") <= 29  # route: what is left of its deadline
    assert seen[1] == crewai_engine.PROVIDER_TIMEOUT_S  # background work keeps the full timeout
    assert CrewAIEngine._llm_kwargs(GEMINI, 300, 0.3)["timeout"] == crewai_engine.PROVIDER_TIMEOUT_S  # reset after


def test_no_retry_starts_when_it_cannot_finish_before_the_deadline(monkeypatch):
    monkeypatch.setenv("PROVIDER_DEADLINE_S_CHAT", "4")  # 2 s backoff would leave < MIN_CALL_S
    slept = []
    monkeypatch.setattr(crewai_engine.time, "sleep", slept.append)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [GEMINI], {}
    once, calls = _flaky([InternalServerError(GEMINI_503) for _ in range(3)])
    with pytest.raises(ProviderError):
        eng._kickoff(order=[GEMINI], once=once, kind="chat", **ONCE)
    assert len(calls) == 1 and slept == []
    once, calls = _flaky([InternalServerError(GEMINI_503)])
    eng._kickoff(order=[GEMINI], once=once, kind="run_task", **ONCE)  # background work still retries
    assert len(calls) == 2


# ------------------------------------------------------------------ 9. DeepSeek: routes thrown away for a name instead of an id
def _route_req(conv="office"):
    return RouteRequest.model_validate({"text": "¿cómo va el balance?", "conversation": conv, "agents": [
        {"id": "a-acc", "role": "accounting", "title": "Contador", "name": "Tomás Vidal"},
        {"id": "a-sales", "role": "sales", "title": "Gerente de Ventas", "name": "Valeria Ríos"},
        {"id": "a-asst", "role": "assistant", "title": "Asistente", "name": "Sofía Lara"}]})


def test_route_accepts_the_agent_by_name_role_or_title_when_unambiguous():
    from app.routing import validate_route

    for primary in ("a-acc", "Tomás", "tomas vidal", "accounting", "Contador", "@a-acc"):
        out = validate_route({"intent": "question", "topic": "finance", "primary_agent_id": primary,
                              "contributor_agent_ids": ["Valeria"]}, _route_req())
        assert out is not None and out.responders[0].agent_id == "a-acc", primary
        assert [r.agent_id for r in out.responders[1:]] == ["a-sales"]


def test_route_intent_synonyms_and_what_is_still_rejected(caplog):
    from app.routing import validate_route

    out = validate_route({"intent": "Greeting", "primary_agent_id": "Sofía"}, _route_req())
    assert out.intent == "smalltalk" and out.responders[0].agent_id == "a-asst"
    with caplog.at_level("WARNING", logger="agent_runtime"):
        assert validate_route({"intent": "question", "primary_agent_id": "Pedro"}, _route_req()) is None
        assert validate_route({"intent": "maybe", "primary_agent_id": "a-acc"}, _route_req()) is None
    msgs = " ".join(r.getMessage() for r in caplog.records)
    assert "primary_agent_id='Pedro'" in msgs and "intent='maybe'" in msgs and "balance" not in msgs  # never the text


# ------------------------------------------------------------------ 10. DeepSeek sweep: prompts
def test_route_prompt_separates_talking_about_a_topic_from_asking_for_a_deliverable():
    """Live (DeepSeek sweep): "revisemos las finanzas" became a task (a plan and a paid run) because the prompt listed
    "revisar" as a task verb; "que licencia tiene el software" went to the assistant (no area hints)."""
    from app.security import build_route_prompt

    p = build_route_prompt("revisemos las finanzas", "office", _route_req().agents, [])
    assert "'revisemos...'" in p and "es question, no task" in p
    assert "revisar," not in p.split("Reglas:")[1].split("Invitar")[0]  # no longer a task verb
    assert "licencias" in p and "tendencias" in p


def test_chat_prompt_says_the_pause_is_over_when_the_history_still_mentions_it():
    from app.models import ChatReplyRequest
    from app.security import build_chat_prompt

    base = {"agent": {"id": "accounting", "role": "accounting", "name": "Tomás"}, "text": "¿cómo va el balance?",
            "conversation": "agent:accounting"}
    paused = [{"from": "accounting", "text": "Estoy en pausa por ahora, no puedo revisar el balance."}]
    p = build_chat_prompt(ChatReplyRequest.model_validate({**base, "history": paused}))
    assert "ESTADO ACTUAL" in p and "ya termino" in p
    assert "ESTADO ACTUAL" not in build_chat_prompt(ChatReplyRequest.model_validate(base))  # nothing to correct


def test_route_intent_copied_from_the_topic_list_is_understood():
    """Live (DeepSeek after-run, logged by the new rejection warning): intent='help' for "¿qué puedes hacer?"."""
    from app.routing import validate_route

    out = validate_route({"intent": "help", "topic": "help", "primary_agent_id": "assistant"}, _route_req())
    assert out is not None and out.intent == "question" and out.responders[0].agent_id == "a-asst"
    assert validate_route({"intent": "ack", "primary_agent_id": "a-asst"}, _route_req()).intent == "smalltalk"
