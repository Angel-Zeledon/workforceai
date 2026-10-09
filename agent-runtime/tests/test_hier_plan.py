"""Hierarchical planning (W4): simulated planner, validators and the HTTP contract."""
import pytest

from app.hier_plan import normalize_phase_tasks, normalize_phases
from app.models import PhaseSpec, PhaseTask, PlanAgent
from tests.conftest import AGENTS


def _phases(client, **kw):
    r = client.post("/v1/plan-phases", json={"request_text": "Lanzar un producto nuevo", "agents": AGENTS, **kw})
    assert r.status_code == 200, r.text
    return r.json()


def _expand(client, phase, size_target, **kw):
    r = client.post("/v1/plan-phase", json={"request_text": "Lanzar un producto nuevo", "phase": phase, "agents": AGENTS,
                                           "target_tasks": size_target, **kw})
    assert r.status_code == 200, r.text
    return r.json()


def test_phases_shape_and_deterministic(client):
    a, b = _phases(client), _phases(client)
    assert a == b
    assert len(a["phases"]) >= 3
    keys = {p["key"] for p in a["phases"]}
    assert all(d in keys for p in a["phases"] for d in p["depends_on"])
    assert any(len(p["depends_on"]) > 1 for p in a["phases"])  # a join: cross-phase DAG, not a chain
    assert a["usage"]["cost_usd"] > 0 and a["usage"]["model"]


def test_phase_tasks_valid_dag_with_complexity_and_known_agents(client):
    ph = _phases(client)["phases"][2]
    out = _expand(client, ph, 22)
    tasks = out["tasks"]
    assert len(tasks) == 22
    keys = [t["key"] for t in tasks]
    assert len(set(keys)) == len(keys)
    known = {a["id"] for a in AGENTS}
    assert {t["agent_id"] for t in tasks} <= known
    assert {t["complexity"] for t in tasks} <= {"S", "M", "L", "XL"} and len({t["complexity"] for t in tasks}) > 1
    assert all(d in keys for t in tasks for d in t["depends_on"])
    assert any(not t["depends_on"] for t in tasks)
    assert out["usage"]["input_tokens"] > 0
    assert _expand(client, ph, 22) == out


def test_whole_project_at_least_60_tasks(client):
    phases = _phases(client)["phases"]
    total = sum(len(_expand(client, p, {"S": 6, "M": 12, "L": 16, "XL": 22}[p["size"]])["tasks"]) for p in phases)
    assert total >= 60


def test_max_tasks_is_a_hard_cap(client):
    ph = _phases(client)["phases"][0]
    assert len(_expand(client, ph, 30, max_tasks=7)["tasks"]) == 7


def test_old_payloads_still_accepted(client):
    r = client.post("/v1/plan", json={"request_text": "cliente nuevo $50,000", "agents": AGENTS, "budget_usd": 5})
    assert r.status_code == 200 and r.json()["tasks"]
    assert client.post("/v1/plan-phases", json={}).status_code == 422


def test_normalize_breaks_cycles_and_unknown_refs():
    ph = normalize_phases([PhaseSpec(key="a", title="A", depends_on=["b", "zz", "a"]),
                           PhaseSpec(key="b", title="B", depends_on=["a"]),
                           PhaseSpec(key="a", title="dup"), PhaseSpec(key="", title="x")], 8)
    assert [p.key for p in ph] == ["a", "b"]
    assert ph[0].depends_on == ["b"] and ph[1].depends_on == []  # the edge that closes the cycle is dropped
    ag = [PlanAgent(id="sales", role="sales")]
    ts = normalize_phase_tasks([PhaseTask(key="x", title="X", agent_id="ghost", depends_on=["y"], complexity="huge"),
                                PhaseTask(key="y", title="Y", agent_id="sales", depends_on=["x"])], ag, 10)
    assert ts[0].agent_id == "sales" and ts[0].complexity == "M"
    assert ts[0].depends_on == ["y"] and ts[1].depends_on == []


@pytest.mark.parametrize("locale", ["es", "en"])
def test_locale(client, locale):
    out = _phases(client, locale=locale)
    assert out["phases"][0]["title"].startswith("Discovery" if locale == "en" else "Descubrimiento")
