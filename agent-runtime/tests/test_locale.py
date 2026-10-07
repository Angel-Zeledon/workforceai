import asyncio

import pytest

from app.models import (
    ConsultRequest,
    PlanAgent,
    PlanRequest,
    RunTaskRequest,
    SynthesizeRequest,
    SynthOutput,
)
from app.security import build_consult_prompt, build_synthesis_prompt, build_system_prompt, build_task_prompt
from app.simulation import SimulationEngine
from conftest import AGENTS, run_req

ES_REQ = "Tengo un cliente nuevo por $50,000, prepara todo"
EN_REQ = "I have a new client for $50,000, prepare everything"


def run(coro):
    return asyncio.run(coro)


def engine():
    return SimulationEngine(latency_scale=0)


def test_locale_defaults_to_spanish_and_normalizes():
    assert PlanRequest(request_text="x").locale == "es"
    assert PlanRequest(request_text="x", locale="en-US").locale == "en"
    assert PlanRequest(request_text="x", locale="fr").locale == "es"
    assert RunTaskRequest(**run_req()).locale == "es"
    assert ConsultRequest(from_agent_id="a", to_agent_id="b", question="q", locale="EN").locale == "en"
    assert SynthesizeRequest(request_text="x").locale == "es"


@pytest.mark.parametrize("locale,title_word", [("es", "Analizar"), ("en", "Analyze")])
def test_plan_locale(locale, title_word):
    agents = [PlanAgent(**a) for a in AGENTS]
    plan = run(engine().plan(PlanRequest(request_text=ES_REQ if locale == "es" else EN_REQ,
                                         agents=agents, locale=locale)))
    assert plan.tasks[0].title.startswith(title_word)
    assert plan.tasks[0].key == "ventas"  # task keys are stable across locales


@pytest.mark.parametrize("locale,expected", [("es", "El cliente encaja"), ("en", "The client fits")])
def test_run_task_locale(locale, expected):
    body = run_req("sales", request_text=ES_REQ if locale == "es" else EN_REQ)
    body["locale"] = locale
    res = run(engine().run_task(RunTaskRequest(**body)))
    assert res.output.summary.startswith(expected)
    assert res.tool_requests[0].tool == "email"  # tool names stay in English


def test_run_task_without_locale_is_spanish():
    res = run(engine().run_task(RunTaskRequest(**run_req("sales"))))
    assert res.output.summary.startswith("El cliente encaja")


@pytest.mark.parametrize("locale,marker", [("es", "No, los $38,000"), ("en", "No, the $38,000")])
def test_consult_locale(locale, marker):
    req = ConsultRequest(from_agent_id="analyst", to_agent_id="accounting", question="q",
                         context=ES_REQ if locale == "es" else EN_REQ, locale=locale)
    assert run(engine().consult(req)).answer.startswith(marker)


@pytest.mark.parametrize("locale,heading", [("es", "Resumen ejecutivo"), ("en", "Executive summary")])
def test_synthesize_locale(locale, heading):
    out = SynthOutput(task_id="t", agent_id="sales", title="T", output={"summary": "s", "confidence": 0.5})
    res = run(engine().synthesize(SynthesizeRequest(request_text=ES_REQ, outputs=[out], locale=locale)))
    assert res.sections[0].heading == heading


def test_prompts_carry_language_instruction():
    req_en = RunTaskRequest(**{**run_req("sales"), "locale": "en"})
    req_es = RunTaskRequest(**run_req("sales"))
    assert "natural English" in build_system_prompt(req_en.agent, "en")
    assert "espanol latinoamericano neutro" in build_system_prompt(req_es.agent, "es")
    assert "natural English" in build_task_prompt(req_en)
    assert "espanol latinoamericano neutro" in build_task_prompt(req_es)
    assert "natural English" in build_consult_prompt("a", "b", "q", None, "en")
    assert "natural English" in build_synthesis_prompt("x", [], "en")
    assert "espanol latinoamericano neutro" in build_synthesis_prompt("x", [])
