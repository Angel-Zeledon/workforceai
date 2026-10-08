"""RUNTIME_TOKEN: shared secret between backend and runtime."""
from conftest import AGENTS


def plan_body():
    return {"request_text": "Prepara una propuesta", "agents": AGENTS}


def test_open_when_no_token(client, monkeypatch):
    monkeypatch.delenv("RUNTIME_TOKEN", raising=False)
    assert client.post("/v1/plan", json=plan_body()).status_code == 200


def test_token_required_when_set(client, monkeypatch):
    monkeypatch.setenv("RUNTIME_TOKEN", "s3cret-token")
    assert client.post("/v1/plan", json=plan_body()).status_code == 401
    bad = {"Authorization": "Bearer wrong"}
    assert client.post("/v1/plan", json=plan_body(), headers=bad).status_code == 401
    ok = {"Authorization": "Bearer s3cret-token"}
    assert client.post("/v1/plan", json=plan_body(), headers=ok).status_code == 200


def test_healthz_stays_open(client, monkeypatch):
    monkeypatch.setenv("RUNTIME_TOKEN", "s3cret-token")
    assert client.get("/healthz").status_code == 200
