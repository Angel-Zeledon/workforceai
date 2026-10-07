import asyncio

import pytest

from app.engine import estimate_cost
from app.estimate import EstimateRequest, EstimateTaskIn, estimate_plan
from app.models import ConsultRequest, PlanRequest, RunTaskRequest
from conftest import AGENTS

TEXTS = [
    "Tengo un cliente nuevo por $50,000, prepara todo",
    "Las ventas bajaron este trimestre",
    "Necesito contratar un empleado nuevo",
    "Revisa este contrato con el proveedor",
    "Organiza mi semana",
]


def _estimate_body(plan):
    return {"request_text": "x", "tasks": [
        {"id": t.key, "key": t.key, "title": t.title, "description": t.description,
         "agent_id": t.agent_id, "depends_on": t.depends_on} for t in plan.tasks]}


def test_estimate_endpoint_shape_is_a_range(client):
    plan = client.post("/v1/plan", json={"request_text": TEXTS[0], "agents": AGENTS, "budget_usd": 5}).json()
    body = {"request_text": TEXTS[0], "tasks": [{"id": t["key"], "title": t["title"], "description": t["description"],
                                                  "agent_id": t["agent_id"], "depends_on": t["depends_on"]} for t in plan["tasks"]]}
    r = client.post("/v1/estimate", json=body)
    assert r.status_code == 200
    d = r.json()
    assert d["mode"] == "simulation" and d["basis"] == "simulation" and d["currency"] == "USD"
    assert len(d["tasks"]) == len(plan["tasks"])
    for t in d["tasks"]:
        assert 0 < t["min_usd"] < t["max_usd"]  # a range, never a point
    assert 0 < d["total"]["min_usd"] < d["total"]["max_usd"]
    # total = sum of tasks + synthesis (rounded)
    assert d["total"]["min_usd"] == pytest.approx(sum(t["min_usd"] for t in d["tasks"]) + d["synthesis"]["min_usd"], abs=1e-4)
    assert d["total"]["max_usd"] == pytest.approx(sum(t["max_usd"] for t in d["tasks"]) + d["synthesis"]["max_usd"], abs=1e-4)
    assert d["rates"] == {"input_per_m": 3.0, "output_per_m": 15.0}


def test_estimate_requires_tasks_field_defaults(client):
    r = client.post("/v1/estimate", json={})
    assert r.status_code == 200
    assert r.json()["total"]["max_usd"] > 0  # synthesis only


@pytest.mark.parametrize("text", TEXTS)
def test_simulated_real_cost_always_falls_inside_the_estimate(engine, text):
    """The simulation draws tokens from the same ranges the estimate uses."""
    plan = asyncio.run(engine.plan(PlanRequest(request_text=text, agents=AGENTS, budget_usd=10)))
    est = asyncio.run(engine.estimate(EstimateRequest(**_estimate_body(plan))))
    by_id = {t.id: t for t in est.tasks}
    for i, pt in enumerate(plan.tasks):
        deps = [{"task_id": d, "agent_id": "x", "output": {"summary": "s"}} for d in pt.depends_on]
        req = RunTaskRequest.model_validate({
            "task": {"id": f"{pt.key}-{i}", "title": pt.title, "description": pt.description, "agent_id": pt.agent_id},
            "agent": {"id": pt.agent_id, "role": pt.agent_id, "title": "T", "persona": "p"},
            "context": {"request_text": text, "dependency_outputs": deps, "memory": []}})
        res = asyncio.run(engine.run_task(req))
        cost = res.usage.cost_usd
        for c in res.consults:
            cost += asyncio.run(engine.consult(ConsultRequest(
                from_agent_id=pt.agent_id, to_agent_id=c.to_agent_id, question=c.question, context=text))).usage.cost_usd
        te = by_id[pt.key]
        assert te.min_usd - 1e-4 <= cost <= te.max_usd + 1e-4, (pt.key, te.min_usd, cost, te.max_usd)


def test_estimate_uses_engine_rates(monkeypatch):
    req = EstimateRequest(tasks=[EstimateTaskIn(id="a", title="t", description="d", agent_id="sales")])
    base = estimate_plan(req, mode="simulation", model=None)
    pricey = estimate_plan(req, mode="simulation", model="deepseek-v4-pro")
    assert pricey.rates.input_per_m == 1.32 and pricey.rates.output_per_m == 3.96
    assert pricey.total.max_usd < base.total.max_usd  # V4 Pro is cheaper than the default Sonnet rate
    monkeypatch.setenv("PRICE_IN_PER_M", "10")
    monkeypatch.setenv("PRICE_OUT_PER_M", "20")
    forced = estimate_plan(req, mode="simulation", model=None)
    assert forced.rates.input_per_m == 10.0
    assert forced.tasks[0].max_usd > base.tasks[0].max_usd


def test_live_estimate_is_a_wide_range_bounded_by_max_tokens():
    req = EstimateRequest(tasks=[
        EstimateTaskIn(id="a", title="Analiza", description="x" * 700, agent_id="analyst"),
        EstimateTaskIn(id="b", title="Resume", description="y", agent_id="sales", depends_on=["a"])])
    est = estimate_plan(req, mode="live", model="anthropic/claude-sonnet-4-5")
    assert est.basis == "token_heuristic" and est.mode == "live"
    for t in est.tasks:
        assert t.min_usd < t.max_usd
        assert t.output_tokens_min < t.output_tokens_max
    # a dependency adds input tokens
    assert est.tasks[1].input_tokens_min > 700
    # the minimum is a plausible floor: one cheap call
    floor = estimate_cost(est.tasks[0].input_tokens_min, est.tasks[0].output_tokens_min, "anthropic/claude-sonnet-4-5")
    assert est.tasks[0].min_usd == pytest.approx(floor, abs=1e-5)
