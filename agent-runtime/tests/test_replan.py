"""POST /v1/replan (Q2): deterministic replacement sub-plan for a failed node."""
from tests.conftest import AGENTS


def _replan(client, **kw):
    body = {"project_goal": "Cerrar el mes", "failed": {"id": "n1", "title": "Conciliar bancos", "agent_id": AGENTS[0]["id"]},
            "agents": AGENTS, **kw}
    r = client.post("/v1/replan", json=body)
    assert r.status_code == 200, r.text
    return r.json()


def test_replan_is_deterministic_valid_and_uses_the_owner(client):
    a, b = _replan(client), _replan(client)
    assert a == b
    tasks = a["tasks"]
    assert 1 <= len(tasks) <= 6
    keys = [t["key"] for t in tasks]
    assert len(set(keys)) == len(keys)
    assert all(d in keys for t in tasks for d in t["depends_on"])
    assert {t["agent_id"] for t in tasks} == {AGENTS[0]["id"]}
    assert "Conciliar bancos" in tasks[0]["title"]
    assert a["usage"]["cost_usd"] > 0 and a["reason"]


def test_replan_respects_max_tasks_and_locale(client):
    out = _replan(client, max_tasks=2, locale="en")
    assert len(out["tasks"]) == 2
    assert out["tasks"][0]["title"].startswith("Diagnose")
