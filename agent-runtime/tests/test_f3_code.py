"""F3: agents with the code tool know how to ask to run Python (always with approval)."""

import asyncio

from app.models import RunTaskRequest
from app.security import build_task_prompt
from app.simulation import SIM_CODE_MARKER, SimulationEngine, sim_code_request


def _req(tools, title="Analiza ventas", **extra):
    base = {
        "task": {"id": "t1", "title": title, "description": "Totales", "agent_id": "data_analyst"},
        "agent": {"id": "data_analyst", "role": "data_analyst", "title": "Analista", "tools": tools},
        "context": {"request_text": "analiza"},
    }
    base.update(extra)
    return RunTaskRequest.model_validate(base)


def test_prompt_teaches_the_code_tool_only_to_agents_that_hold_it():
    p = build_task_prompt(_req(["fs", "code"]))
    assert "EJECUCION DE CODIGO" in p and 'tool:"code"' in p
    assert "SIEMPRE necesita la aprobacion de una persona" in p
    assert "SIN red ni secretos" in p
    assert "EJECUCION DE CODIGO" not in build_task_prompt(_req(["fs", "bi"]))


def test_simulation_asks_for_code_only_with_the_marker():
    assert sim_code_request(_req(["code"])) is None  # standard scenarios never change
    tr = sim_code_request(_req(["code"], title=f"Analiza {SIM_CODE_MARKER}"))
    assert tr is not None and tr.tool == "code" and tr.action == "run" and tr.risk == "high"
    assert tr.args["language"] == "python" and "out/resumen.csv" in tr.args["code"]
    assert sim_code_request(_req(["fs"], title=f"Analiza {SIM_CODE_MARKER}")) is None  # no code tool, no request
    # the result came back as external content: do not ask again
    assert sim_code_request(_req(["code"], title=f"Analiza {SIM_CODE_MARKER}", external_content=["<untrusted_data>x</untrusted_data>"])) is None


def test_prompt_tells_the_truth_for_each_code_mode():
    off = build_task_prompt(_req(["code"]))
    assert "SIEMPRE necesita la aprobacion de una persona" in off and "MODO ESCRITORIO" not in off
    assert build_task_prompt(_req(["code"], code_auto_run="")) == off  # the default prompt does not change
    desk = build_task_prompt(_req(["code"], code_auto_run="desk"))
    assert "MODO ESCRITORIO" in desk and "SIEMPRE necesita la aprobacion de una persona" not in desk
    assert "tu escritorio" in desk and "una persona debe aprobarlo" in desk  # the fallback is explained
    assert "SIN red ni secretos" in desk
    # an unknown value is not desk mode, and an agent without the tool never hears about the mode
    assert "MODO ESCRITORIO" not in build_task_prompt(_req(["code"], code_auto_run="full"))
    assert "MODO ESCRITORIO" not in build_task_prompt(_req(["fs"], code_auto_run="desk"))


def test_run_task_in_simulation_returns_the_code_request():
    eng = SimulationEngine(latency_scale=0)
    res = asyncio.run(eng.run_task(_req(["code"], title=f"Analiza {SIM_CODE_MARKER}")))
    assert [t.tool for t in res.tool_requests].count("code") == 1
    res = asyncio.run(eng.run_task(_req(["code"])))
    assert all(t.tool != "code" for t in res.tool_requests)
