"""Cost control: output caps per kind, prompt size, planner fast path, pre-call budget guard (docs/cost-reduction.md).

No test calls a model: the CrewAI engine is built with fake classes and the guard is exercised before any send.
"""
from __future__ import annotations

import json
from pathlib import Path

import pytest

from app import cost_control as cc
from app.crewai_engine import CrewAIEngine, json_instructions
from app.fast_path import is_simple_request, try_fast_plan
from app.models import PlanAgent, PlanRequest, RunTaskRequest, TaskResult
from app.providers import ProviderConfig
from app.security import build_system_prompt, build_task_prompt

AGENTS = [PlanAgent(id=i, role=i, title=i.title(), responsibilities="r")
          for i in ("sales", "hr", "legal", "accounting", "analyst", "operations", "assistant")]


# ------------------------------------------------------------------ max_tokens per kind
def test_each_kind_has_a_cap_and_env_overrides_it(monkeypatch):
    for kind in ("plan", "run_task", "consult", "synthesize"):
        monkeypatch.delenv(f"MAX_TOKENS_{kind.upper()}", raising=False)
        assert 0 < cc.max_tokens_for(kind) <= 8192
    assert cc.max_tokens_for("consult") < cc.max_tokens_for("run_task") < cc.max_tokens_for("synthesize")
    monkeypatch.setenv("MAX_TOKENS_RUN_TASK", "1234")
    assert cc.max_tokens_for("run_task") == 1234
    monkeypatch.setenv("MAX_TOKENS_RUN_TASK", "junk")
    assert cc.max_tokens_for("run_task") == cc.DEFAULT_MAX_TOKENS["run_task"]
    monkeypatch.setenv("MAX_TOKENS_DEFAULT", "999")
    assert cc.max_tokens_for("some_new_kind") == 999


def test_llm_kwargs_use_the_cap_not_a_floor():
    cfg = ProviderConfig(id="deepseek", model="deepseek/deepseek-chat", api_key="k")
    kw = CrewAIEngine._llm_kwargs(cfg, 700, 0.2)
    assert kw["max_tokens"] == 700  # the old 8192 floor for DeepSeek/Kimi is gone
    assert "thinking" not in kw and "reasoning_effort" not in kw


def test_no_reasoning_unless_asked(monkeypatch):
    monkeypatch.delenv("LLM_REASONING", raising=False)
    assert cc.non_reasoning_model("deepseek/deepseek-reasoner") == "deepseek/deepseek-chat"
    assert cc.reasoning_kwargs("openai", "openai/gpt-5") == {"reasoning_effort": "minimal"}
    assert cc.reasoning_kwargs("openai", "openai/o3-mini") == {"reasoning_effort": "low"}
    assert cc.reasoning_kwargs("anthropic", "anthropic/claude-sonnet-5-5") == {}
    monkeypatch.setenv("LLM_REASONING", "true")
    assert cc.non_reasoning_model("deepseek/deepseek-reasoner") == "deepseek/deepseek-reasoner"
    assert cc.reasoning_kwargs("openai", "openai/gpt-5") == {}


def test_crew_loop_is_capped(monkeypatch):
    monkeypatch.delenv("CREW_MAX_ITER", raising=False)
    assert cc.crew_max_iter() == 3 < 25
    monkeypatch.setenv("CREW_MAX_ITER", "2")
    assert cc.crew_max_iter() == 2
    monkeypatch.setenv("CREW_MAX_RETRY_LIMIT", "0")
    assert cc.crew_max_retry_limit() == 0


def test_agent_is_built_with_the_caps(monkeypatch):
    seen = {}

    class FakeAgent:
        def __init__(self, **kw):
            seen.update(kw)

    class FakeResult:
        pydantic = None
        raw = '{"answer": "ok"}'
        token_usage = type("U", (), {"prompt_tokens": 10, "completion_tokens": 5})()

    class FakeCrew:
        def __init__(self, **kw): ...
        def kickoff(self): return FakeResult()

    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng.providers, eng._llms = [], {}
    eng._LLM = lambda **kw: object()
    eng._Agent, eng._Task, eng._Crew = FakeAgent, lambda **kw: object(), FakeCrew
    eng._Process = type("P", (), {"sequential": 1})
    from app.crewai_engine import _ConsultAnswer

    cfg = ProviderConfig(id="deepseek", model="deepseek/deepseek-chat", api_key="k")
    eng._kickoff_once(cfg, role="r", goal="g", backstory="b", description="d", expected="e", schema=_ConsultAnswer)
    assert seen["max_iter"] == 3 and seen["reasoning"] is False and seen["max_retry_limit"] == 1


# ------------------------------------------------------------------ prompt size
def _trivial_run_task() -> RunTaskRequest:
    return RunTaskRequest.model_validate({
        "task": {"id": "t1", "title": "Calcular margen bruto", "description": "Calcula el margen bruto del trimestre",
                 "agent_id": "accounting"},
        "agent": {"id": "accounting", "role": "accounting", "title": "Contador", "persona": "Contador meticuloso",
                  "responsibilities": ["conciliacion", "margen"], "tools": ["fs", "code", "web", "ledger"]},
        "context": {"request_text": "calcula el margen bruto"},
        "workspace": {"desk": "/Agents/accounting", "project": "/Projects/x", "shared": "/Shared", "can_write": True},
        "recalled": [{"ref": f"task:{i}", "kind": "task", "title": "t" * 160, "snippet": "s" * 500} for i in range(6)],
    })


def _tokens(req: RunTaskRequest) -> int:
    chars = len(build_system_prompt(req.agent, "es", "neutral")) + len(build_task_prompt(req)) \
        + len(json_instructions(TaskResult))
    return chars // 4


# Size of the same request before the trimming (measured on the parent commit: system 346 + user 2939 + schema 649).
BEFORE_TOKENS = 3934


def test_trivial_task_prompt_is_much_smaller_than_before(monkeypatch):
    monkeypatch.setenv("PROMPT_TRIM", "1")
    after = _tokens(_trivial_run_task())
    assert after <= BEFORE_TOKENS * 0.70, (after, BEFORE_TOKENS)
    monkeypatch.setenv("PROMPT_TRIM", "0")
    untrimmed = _tokens(_trivial_run_task())
    assert untrimmed > after  # the switch restores the manuals


def test_manuals_come_back_when_the_task_needs_them():
    base = build_task_prompt(_trivial_run_task())
    assert "write_document" not in base and "EXISTENTE" not in base and "NO los leas enteros" not in base
    r = _trivial_run_task()
    r.task.description = "Genera un Excel con el margen y corrige el archivo existente; busca en la carpeta"
    p = build_task_prompt(r)
    assert "write_document" in p and "EXISTENTE" in p and "NO los leas enteros" in p


def test_recalled_memory_is_capped():
    p = build_task_prompt(_trivial_run_task())
    assert 1 <= p.count('"ref": "task:') <= 4  # was 6
    assert "s" * 301 not in p  # snippet cut at 300 (was 500)


def test_schema_sent_in_the_prompt_has_no_prose():
    text = json_instructions(TaskResult)
    assert '"summary"' in text and '"tool_requests"' in text  # the structure survives
    assert "Tool arguments" in text and len(text) < 2600


# ------------------------------------------------------------------ planner fast path
@pytest.mark.parametrize("text", [
    "calcula el margen bruto",
    "Compute gross margin for Q3",
    "¿Cuánto es el IVA de 1200?",
    "Resume en dos líneas este texto: hola mundo",
])
def test_simple_requests_are_one_task(text, monkeypatch):
    monkeypatch.delenv("PLANNER_FAST_PATH", raising=False)
    plan = try_fast_plan(PlanRequest(request_text=text, agents=AGENTS))
    assert plan is not None and len(plan.tasks) == 1
    assert plan.tasks[0].depends_on == [] and plan.tasks[0].agent_id in {a.id for a in AGENTS}
    assert plan.usage is not None and plan.usage.cost_usd == 0.0 and plan.usage.input_tokens == 0


def test_best_fit_agent_is_the_owner_of_the_area(monkeypatch):
    monkeypatch.delenv("PLANNER_FAST_PATH", raising=False)
    plan = try_fast_plan(PlanRequest(request_text="calcula el margen bruto", agents=AGENTS))
    assert plan.tasks[0].agent_id == "accounting"


@pytest.mark.parametrize("text", [
    "Tengo un cliente nuevo por $50,000, prepara todo",
    "Prepara una propuesta comercial y luego revisa el contrato con legal",
    "Lanza la campaña de marketing y calcula el presupuesto y contrata a dos personas",
    "1. Calcula el margen\n2. Revisa el contrato\n3. Redacta un correo",
    "Calcula el margen bruto. Después redacta el contrato. Y también agenda una reunión.",
    "x" * 400,
    "",
])
def test_complex_requests_go_to_the_planner(text):
    assert not is_simple_request(text, AGENTS)
    assert try_fast_plan(PlanRequest(request_text=text, agents=AGENTS)) is None


def test_two_areas_means_the_planner():
    assert not is_simple_request("revisa la cláusula del contrato y el margen de la factura", AGENTS)


def test_switch_off_restores_the_planner(monkeypatch):
    monkeypatch.setenv("PLANNER_FAST_PATH", "false")
    assert try_fast_plan(PlanRequest(request_text="calcula el margen bruto", agents=AGENTS)) is None


def test_no_agents_means_the_planner():
    assert try_fast_plan(PlanRequest(request_text="calcula el margen bruto", agents=[])) is None


def test_engine_plan_does_not_call_the_model_on_the_fast_path(monkeypatch):
    import asyncio

    monkeypatch.delenv("PLANNER_FAST_PATH", raising=False)
    eng = CrewAIEngine.__new__(CrewAIEngine)
    eng._run = lambda **kw: (_ for _ in ()).throw(AssertionError("the model must not be called"))
    plan = asyncio.run(eng.plan(PlanRequest(request_text="calcula el margen bruto", agents=AGENTS)))
    assert len(plan.tasks) == 1


# ------------------------------------------------------------------ budget guard before the call
def test_estimate_is_prompt_plus_the_whole_output_cap():
    tin = cc.estimate_tokens("x" * 3500)
    assert tin == 1000 + cc.WRAPPER_TOKENS
    est = cc.call_cost_upper_bound("deepseek-chat", None, tin, 4000)
    prices = json.loads(Path(cc.__file__).with_name("model_prices.json").read_text(encoding="utf-8"))["models"]["deepseek-chat"]
    assert est == pytest.approx(tin * prices["input"] / 1e6 + 4000 * prices["output"] / 1e6, abs=1e-5)


def test_preflight_refuses_a_call_that_crosses_the_allowance():
    with pytest.raises(cc.CallBudgetError) as e:
        cc.preflight(kind="run_task", model="anthropic/claude-sonnet-5-5", provider="anthropic",
                     prompt_texts=["x" * 40000], max_tokens=4000, allowance_usd=0.001)
    d = e.value.detail()
    assert d["code"] == "call_budget_exceeded" and d["estimated_usd"] > d["allowance_usd"]
    # no allowance sent, or enough room: allowed
    assert cc.preflight(kind="run_task", model="anthropic/claude-sonnet-5-5", provider="anthropic",
                        prompt_texts=["x" * 40000], max_tokens=4000, allowance_usd=0) > 0
    assert cc.preflight(kind="run_task", model="anthropic/claude-sonnet-5-5", provider="anthropic",
                        prompt_texts=["x" * 40000], max_tokens=4000, allowance_usd=10.0) > 0


def test_guard_switch(monkeypatch):
    monkeypatch.setenv("BUDGET_PREFLIGHT", "false")
    assert cc.preflight(kind="plan", model="deepseek-chat", provider=None, prompt_texts=["x" * 90000],
                        max_tokens=3000, allowance_usd=0.0001) > 0


class _NeverCalled(CrewAIEngine):
    def __init__(self, cfgs):
        self.providers, self._llms, self.sent = cfgs, {}, 0

    def _kickoff_once(self, cfg, **kw):
        self.sent += 1
        raise AssertionError("a refused call must not reach the provider")


def test_engine_refuses_before_sending_and_http_maps_to_402():
    cfg = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    eng = _NeverCalled([cfg])
    with pytest.raises(cc.CallBudgetError):
        eng._kickoff(order=[cfg], role="r", goal="g", backstory="b" * 20000, description="d" * 20000, expected="e",
                     schema=TaskResult, kind="run_task", max_cost_usd=0.0005)
    assert eng.sent == 0


def test_a_cheaper_provider_still_answers_when_the_expensive_one_is_refused(monkeypatch):
    pricey = ProviderConfig(id="anthropic", model="anthropic/claude-sonnet-5-5", api_key="k")
    cheap = ProviderConfig(id="deepseek", model="deepseek/deepseek-chat", api_key="k")
    calls = []

    class Eng(_NeverCalled):
        def _kickoff_once(self, cfg, **kw):
            calls.append((cfg.id, kw["max_tokens"]))
            return "parsed", "usage"

    eng = Eng([pricey, cheap])
    kw = dict(role="r", goal="g", backstory="b" * 3000, description="d" * 3000, expected="e", schema=TaskResult,
              kind="consult")
    # allowance between the two worst cases
    hi = cc.preflight(kind="consult", model=pricey.model, provider="anthropic",
                      prompt_texts=["b" * 3000, "d" * 3000, "e"], max_tokens=cc.max_tokens_for("consult"), allowance_usd=None)
    lo = cc.preflight(kind="consult", model=cheap.model, provider="deepseek",
                      prompt_texts=["b" * 3000, "d" * 3000, "e", json_instructions(TaskResult)],
                      max_tokens=cc.max_tokens_for("consult"), allowance_usd=None)
    assert lo < hi
    out = eng._kickoff(order=[pricey, cheap], max_cost_usd=(lo + hi) / 2, **kw)
    assert out == ("parsed", "usage") and calls == [("deepseek", cc.max_tokens_for("consult"))]


def test_model_prices_are_byte_identical_in_backend():
    here = Path(cc.__file__).with_name("model_prices.json").read_bytes()
    there = Path(cc.__file__).resolve().parents[2] / "backend" / "internal" / "application" / "model_prices.json"
    assert here == there.read_bytes()


def test_api_returns_402_with_a_stable_code(monkeypatch):
    from fastapi.testclient import TestClient

    from app.main import create_app

    class Boom(_NeverCalled):
        async def plan(self, req):
            raise cc.CallBudgetError(kind="plan", model="m", input_tokens=10, max_tokens=5, estimated_usd=1.0, allowance_usd=0.1)

    eng = Boom([])
    eng.mode = "live"
    client = TestClient(create_app(eng))
    r = client.post("/v1/plan", json={"request_text": "x", "agents": [], "max_cost_usd": 0.1})
    assert r.status_code == 402 and r.json()["detail"]["code"] == "call_budget_exceeded"


def test_max_cost_usd_is_an_optional_field_on_every_request():
    from app.models import ConsultRequest, SynthesizeRequest

    assert PlanRequest(request_text="x").max_cost_usd == 0.0
    assert RunTaskRequest.model_json_schema()["properties"]["max_cost_usd"]["default"] == 0.0
    assert ConsultRequest.model_fields["max_cost_usd"].default == 0.0
    assert SynthesizeRequest.model_fields["max_cost_usd"].default == 0.0
