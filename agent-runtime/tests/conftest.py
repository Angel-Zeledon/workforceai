import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.main import create_app  # noqa: E402
from app.simulation import SimulationEngine  # noqa: E402

AGENTS = [
    {"id": i, "role": i, "title": i.title(), "responsibilities": "r"}
    for i in ("sales", "hr", "legal", "accounting", "analyst", "operations", "assistant")
]


@pytest.fixture
def engine():
    return SimulationEngine(latency_scale=0)


@pytest.fixture
def client(engine):
    return TestClient(create_app(engine))


def run_req(agent_id="sales", request_text="Tengo un cliente nuevo por $50,000, prepara todo",
            external=None, deps=None, memory=None, persona="Persona X"):
    body = {
        "task": {"id": "t1", "title": "Tarea", "description": "desc", "agent_id": agent_id},
        "agent": {"id": agent_id, "role": agent_id, "title": "Cargo", "persona": persona,
                  "responsibilities": ["a", "b"], "tools": ["email"]},
        "context": {"request_text": request_text, "dependency_outputs": deps or [], "memory": memory or []},
    }
    if external is not None:
        body["external_content"] = external
    return body
