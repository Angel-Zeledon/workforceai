"""Hireable professions (role templates): routing, out-of-scope dialog and simulated work.

The agents are built from the real templates in backend/internal/roles/templates, as the backend sends them
(role, name, title and the profile: topic, keywords es+en, related, area).
"""
import asyncio
import json
from pathlib import Path

import pytest

from app.models import ChatAgent, ChatReplyRequest, PlanAgent, PlanRequest, RouteAgent, RouteRequest, RunTaskRequest
from app.routing import compose_reply, owner_role, rules_route
from app.simulation import SimulationEngine

TEMPLATES = Path(__file__).resolve().parents[2] / "backend" / "internal" / "roles" / "templates"
NEW = ("project_manager", "education", "data_analyst", "software_engineer")

pytestmark = pytest.mark.skipif(not TEMPLATES.is_dir(), reason="role templates not available (runtime-only checkout)")


def _tpl(role: str) -> dict:
    return json.loads((TEMPLATES / f"{role}.json").read_text(encoding="utf-8"))


def _agent(role: str, loc: str = "es") -> RouteAgent:
    t = _tpl(role)
    name = t["seed"]["name"] if t.get("seed") else t["display"]["name_pool"][loc][0]
    agent_id = t["seed"]["agent_id"] if t.get("seed") else role
    kw = t["routing"]["keywords"]["es"] + t["routing"]["keywords"]["en"]
    return RouteAgent(id=agent_id, role=role, title=t["i18n"][loc]["title"], name=name, topic=t["routing"]["topic"],
                      keywords=kw, related=t["routing"]["related"], area=t["i18n"][loc]["area"])


SEED = ("sales", "hr", "legal", "accounting", "analyst", "operations", "assistant")


def office(*extra: str) -> list[RouteAgent]:
    return [_agent(r) for r in SEED + extra]


@pytest.mark.parametrize("text,role", [
    ("¿cómo va el cronograma del proyecto?", "project_manager"),
    ("what is the critical path of the project?", "project_manager"),
    ("¿qué rúbrica usamos para el examen del curso?", "education"),
    ("which syllabus do the students follow?", "education"),
    ("¿la consulta sql de cohortes está lista?", "data_analyst"),
    ("is the data quality of the csv ok?", "data_analyst"),
    ("¿el pull request del bug ya está revisado?", "software_engineer"),
    ("is the pull request for the bug ready?", "software_engineer"),
])
def test_questions_reach_the_hired_profession(text, role):
    r = rules_route(RouteRequest(text=text, agents=office(*NEW)))
    assert r.responders[0].agent_id == role, r
    assert r.topic == _tpl(role)["routing"]["topic"]


def test_a_profession_nobody_hired_never_answers():
    r = rules_route(RouteRequest(text="¿cómo va el cronograma del proyecto?", agents=office()))
    assert all(x.agent_id in SEED for x in r.responders)


def test_seed_roles_keep_their_tuned_routing():
    r = rules_route(RouteRequest(text="¿cuál es el margen del mes?", agents=office(*NEW)))
    assert r.responders[0].agent_id == "accounting"


def test_out_of_scope_question_in_a_direct_chat_is_consulted_with_the_owner():
    roster = office(*NEW)
    r = rules_route(RouteRequest(text="¿esta cláusula del contrato es válida?", conversation="agent:project_manager", agents=roster))
    assert [x.agent_id for x in r.responders] == ["project_manager"]
    assert r.consult is not None and r.consult.agent_id == "legal"
    pm = _agent("project_manager")
    text, kind, consult = compose_reply(ChatReplyRequest(
        agent=ChatAgent(id=pm.id, role=pm.role, title=pm.title, name=pm.name, area=pm.area),
        text="¿esta cláusula del contrato es válida?", conversation="agent:project_manager", intent="question",
        agents=roster, consult_to="legal"))
    assert text and consult is not None and consult.to_agent_id == "legal"


def test_out_of_scope_task_in_a_direct_chat_is_declined_in_voice():
    roster = office(*NEW)
    se = _agent("software_engineer")
    text, _, _ = compose_reply(ChatReplyRequest(
        agent=ChatAgent(id=se.id, role=se.role, title=se.title, name=se.name, area=se.area),
        text="prepara la nómina de este mes", conversation="agent:software_engineer", intent="task",
        agents=roster, consult_to="hr"))
    assert "Marcos" in text


def test_in_scope_answers_speak_from_the_profession():
    for role in NEW:
        a = _agent(role)
        text, _, _ = compose_reply(ChatReplyRequest(agent=ChatAgent(id=a.id, role=a.role, title=a.title, name=a.name, area=a.area),
                                                    text="¿cómo lo ves?", conversation=f"agent:{role}", intent="question", agents=office(*NEW)))
        assert text and "{" not in text


def test_unknown_custom_role_speaks_from_its_area():
    a = RouteAgent(id="vet", role="veterinarian", title="Vet", name="Ana", area="salud animal")
    text, _, _ = compose_reply(ChatReplyRequest(agent=ChatAgent(id=a.id, role=a.role, title=a.title, name=a.name, area=a.area),
                                                text="¿qué opinas?", conversation="agent:vet", intent="question", agents=[a]))
    assert "salud animal" in text


def _plan_agent(role: str) -> PlanAgent:
    a = _agent(role)
    return PlanAgent(id=a.id, role=role, title=a.title, topic=a.topic, keywords=a.keywords, area=a.area)


def test_simulated_planner_gives_the_work_to_the_profession():
    eng = SimulationEngine(latency_scale=0)
    agents = [_plan_agent(r) for r in SEED + NEW]
    assert owner_role("arma el cronograma y la ruta critica del proyecto", agents) == "project_manager"
    plan = asyncio.run(eng.plan(PlanRequest(request_text="arma el cronograma y la ruta crítica del proyecto", agents=agents)))
    assert "project_manager" in {t.agent_id for t in plan.tasks}


@pytest.mark.parametrize("role", NEW)
@pytest.mark.parametrize("locale", ["es", "en"])
def test_simulated_work_has_profession_output(role, locale):
    eng = SimulationEngine(latency_scale=0)
    a = _agent(role, locale)
    out = asyncio.run(eng.run_task(RunTaskRequest(
        task={"id": f"t-{role}", "title": "Trabajo", "description": "", "agent_id": role},
        agent={"id": role, "role": role, "title": a.title, "area": a.area},
        context={"request_text": "Una solicitud cualquiera"}, locale=locale)))
    assert out.output.summary and out.output.findings and not out.tool_requests
