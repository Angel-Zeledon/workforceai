"""Golden cases of scripts/eval-live stay valid; the script refuses to spend without --live."""
import importlib.machinery
import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest

from app.models import PlanRequest, RouteRequest, RunTaskRequest

GOLDEN = Path(__file__).resolve().parents[1] / "evals" / "golden"
SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "eval-live"
MODELS = {"/v1/plan": PlanRequest, "/v1/run-task": RunTaskRequest, "/v1/route": RouteRequest}
CASES = sorted(GOLDEN.glob("*.json"))


def test_there_are_five_golden_cases():
    assert len(CASES) >= 5


@pytest.mark.parametrize("path", CASES, ids=lambda p: p.stem)
def test_golden_case_matches_the_contract(path):
    case = json.loads(path.read_text(encoding="utf-8"))
    assert case["id"] and case["rubric"] and case["endpoint"] in MODELS
    MODELS[case["endpoint"]].model_validate(case["request"])


def _script():
    if not SCRIPT.exists():
        pytest.skip("scripts/ not available (runtime image only)")
    loader = importlib.machinery.SourceFileLoader("eval_live", str(SCRIPT))
    spec = importlib.util.spec_from_loader("eval_live", loader)
    mod = importlib.util.module_from_spec(spec)
    loader.exec_module(mod)
    return mod


def test_refuses_without_live_flag():
    if not SCRIPT.exists():
        pytest.skip("scripts/ not available (runtime image only)")
    r = subprocess.run([sys.executable, str(SCRIPT)], capture_output=True, text=True)
    assert r.returncode == 2 and "--live" in r.stderr


def test_refuses_without_a_key(monkeypatch):
    if not SCRIPT.exists():
        pytest.skip("scripts/ not available (runtime image only)")
    env = {k: v for k, v in __import__("os").environ.items()
           if k not in ("ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "CUSTOM_LLM_API_KEY")}
    r = subprocess.run([sys.executable, str(SCRIPT), "--live"], capture_output=True, text=True, env=env)
    assert r.returncode == 2 and "API_KEY" in r.stderr


@pytest.mark.parametrize("path", CASES, ids=lambda p: p.stem)
def test_rubric_grades_a_simulated_answer(path, client):
    """Not a quality measurement: checks that the grader runs on real response shapes."""
    mod = _script()
    case = json.loads(path.read_text(encoding="utf-8"))
    resp = client.post(case["endpoint"], json=case["request"])
    assert resp.status_code == 200
    checks = mod.grade(case, resp.json())
    assert checks and all(isinstance(p, bool) for _, p, _ in checks)
