import os
import sys
from pathlib import Path

# Tests never reach a real model: the built-in default key is off (providers._api_key).
os.environ.setdefault("DISABLE_DEFAULT_LLM_KEY", "true")

import pytest
from fastapi.testclient import TestClient

# Tests never call (or authenticate to) a real model: a platform-level default key must stay off.
os.environ.setdefault("DISABLE_DEFAULT_LLM_KEY", "1")

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

# Order-dependent pollution (found with test_byo_llm.py before test_engine_selection.py): importing CrewAI/litellm
# calls dotenv.load_dotenv(), which walks UP the directory tree, finds a developer's real .env and copies its
# DEEPSEEK_API_KEY / SIMULATION into os.environ for the rest of the session. A test run must never read a real
# .env (nor risk a real key), so loading is disabled before anything imports it.
try:  # pragma: no cover - dotenv is a dependency of crewai/litellm, absent only without them
    import dotenv

    dotenv.load_dotenv = lambda *a, **k: False
    dotenv.main.load_dotenv = dotenv.load_dotenv
except ImportError:
    pass

from app.main import create_app  # noqa: E402
from app.simulation import SimulationEngine  # noqa: E402

AGENTS = [
    {"id": i, "role": i, "title": i.title(), "responsibilities": "r"}
    for i in ("sales", "hr", "legal", "accounting", "analyst", "operations", "assistant")
]


@pytest.fixture(autouse=True)
def _restore_environ():
    """Whatever a test (or a lazily imported library) writes to os.environ is undone afterwards."""
    before = dict(os.environ)
    yield
    os.environ.clear()
    os.environ.update(before)


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
