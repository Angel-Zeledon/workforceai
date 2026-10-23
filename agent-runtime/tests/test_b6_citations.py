"""B6 evidence chain: figures carry {value, label, ref}; the runtime only proposes them, the backend verifies."""
from conftest import run_req

from app.models import MAX_CITATIONS, RunTaskRequest, StructuredOutput
from app.security import CITATION_RULE, build_task_prompt


def _dep(task_id, metrics, summary="ok"):
    return {"task_id": task_id, "agent_id": "accounting", "ref": f"task:{task_id}", "title": "Margen",
            "output": {"summary": summary, "metrics": metrics, "confidence": 0.9}}


def test_citations_are_optional_and_old_outputs_still_validate():
    out = StructuredOutput.model_validate({"summary": "x", "confidence": 0.5})
    assert out.citations == []


def test_malformed_citations_are_dropped_instead_of_failing_the_output():
    raw = [
        {"value": "24%", "label": "margen real", "ref": "task:t3"},
        {"value": "sin cifra", "ref": "task:t3"},              # not a figure
        {"value": "$5", "ref": ""},                            # no source
        "task:t3",                                             # not an object
        {"value": "$" + "9" * 80, "label": "l" * 500, "ref": "conn:email:" + "x" * 500,
         "status": "verified", "source": {"task_id": "forged"}},  # bounded; status/source are not part of the contract
    ] + [{"value": str(i + 10), "ref": "task:t3"} for i in range(40)]
    out = StructuredOutput.model_validate({"summary": "x", "confidence": 0.5, "citations": raw})
    assert len(out.citations) == MAX_CITATIONS
    assert out.citations[0].model_dump() == {"value": "24%", "label": "margen real", "ref": "task:t3"}
    big = out.citations[1]
    assert len(big.value) == 40 and len(big.label) == 120 and len(big.ref) == 300
    assert "status" not in big.model_dump() and "source" not in big.model_dump()


def test_prompt_asks_for_citations_only_when_there_is_something_to_cite():
    with_deps = RunTaskRequest.model_validate(run_req("analyst", deps=[_dep("t3", {"margen_real": "24%"})]))
    assert CITATION_RULE in build_task_prompt(with_deps)
    assert "conn:<herramienta>:<id>" in CITATION_RULE and "Nunca inventes" in CITATION_RULE
    trivial = RunTaskRequest.model_validate(run_req("analyst"))
    assert CITATION_RULE not in build_task_prompt(trivial)
    read = RunTaskRequest.model_validate(run_req("analyst", external=['<untrusted_data id="m1">x</untrusted_data>']))
    assert CITATION_RULE in build_task_prompt(read)


def test_simulation_cites_the_dependency_figures_with_the_backend_refs(client):
    deps = [_dep("ventas1", {"valor_propuesta_usd": 50000, "probabilidad_cierre": "68%", "ciclo_estimado_dias": 21}),
            _dep("legal1", {"clausulas_revisadas": 24, "flag": True})]
    out = client.post("/v1/run-task", json=run_req("accounting", deps=deps)).json()["output"]
    cites = out["citations"]
    assert {"value": "$50,000", "label": "valor propuesta usd", "ref": "task:ventas1"} in cites
    assert {"value": "68%", "label": "probabilidad cierre", "ref": "task:ventas1"} in cites
    assert all(c["ref"] in ("task:ventas1", "task:legal1") for c in cites)  # only refs the task saw
    assert len([c for c in cites if c["ref"] == "task:ventas1"]) == 2  # bounded per dependency


def test_simulation_without_dependencies_cites_nothing(client):
    assert client.post("/v1/run-task", json=run_req("sales")).json()["output"]["citations"] == []


def test_auditor_cites_every_compared_figure(client):
    body = run_req("internal_auditor", deps=[_dep("a1", {"total": 500}), _dep("b2", {"total": "500.00"})])
    out = client.post("/v1/run-task", json=body).json()["output"]
    assert {"value": "500", "label": "total", "ref": "task:a1"} in out["citations"]
    assert {"value": "500.00", "label": "total", "ref": "task:b2"} in out["citations"]
