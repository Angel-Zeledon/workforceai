from conftest import AGENTS, run_req


def test_healthz(client):
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"status": "ok", "mode": "simulation", "provider": "simulation"}


def test_plan_shape(client):
    r = client.post("/v1/plan", json={"request_text": "cliente nuevo $50,000", "agents": AGENTS, "budget_usd": 5})
    assert r.status_code == 200
    d = r.json()
    assert set(d) == {"objectives", "tasks", "clarifying_questions"}
    assert set(d["tasks"][0]) == {"key", "title", "description", "agent_id", "depends_on"}
    assert len(d["tasks"]) == 5


def test_run_task_shape(client):
    r = client.post("/v1/run-task", json=run_req("sales"))
    assert r.status_code == 200
    d = r.json()
    assert set(d) == {"output", "consults", "tool_requests", "usage"}
    assert set(d["output"]) == {"summary", "findings", "metrics", "hypotheses", "evidence",
                                "recommendations", "confidence", "suggested_tasks"}
    assert set(d["usage"]) == {"model", "input_tokens", "output_tokens", "cost_usd", "duration_ms"}
    tr = d["tool_requests"][0]
    assert set(tr) == {"tool", "action", "args", "risk"} and tr["risk"] == "high"


def test_consult_shape(client):
    r = client.post("/v1/consult", json={"from_agent_id": "operations", "to_agent_id": "hr",
                                         "question": "¿Cuanto tardamos en contratar?", "context": "x"})
    assert r.status_code == 200
    assert set(r.json()) == {"answer", "usage"}


def test_synthesize_shape(client):
    out = client.post("/v1/run-task", json=run_req("legal")).json()["output"]
    r = client.post("/v1/synthesize", json={"request_text": "cliente nuevo $50,000", "outputs": [
        {"task_id": "t1", "agent_id": "legal", "title": "Revision", "output": out}]})
    assert r.status_code == 200
    d = r.json()
    assert set(d) == {"title", "summary", "sections"}
    assert set(d["sections"][0]) == {"heading", "body"}


def test_validation_error(client):
    assert client.post("/v1/plan", json={}).status_code == 422
    assert client.post("/v1/run-task", json={"task": {}}).status_code == 422


def test_external_content_optional_and_accepted(client):
    assert client.post("/v1/run-task", json=run_req("sales", external=["email del cliente"])).status_code == 200
    assert client.post("/v1/run-task", json=run_req("sales", external=None)).status_code == 200
