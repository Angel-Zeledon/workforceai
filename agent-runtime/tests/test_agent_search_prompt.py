"""Capability 6: agents search big files by passage and cite [path p.N]; snippets are data."""

from app.models import RunTaskRequest
from app.security import UNTRUSTED_NOTICE, build_task_prompt


def _req(**extra):
    base = {
        "task": {"id": "t1", "title": "Revisar contratos", "description": "Busca la penalidad", "agent_id": "legal"},
        "agent": {"id": "legal", "role": "legal", "title": "Abogada", "tools": ["fs_read"]},
        "context": {"request_text": "Que dice el contrato de Acme sobre penalidades?"},
    }
    base.update(extra)
    return RunTaskRequest.model_validate(base)


def test_search_rules_in_workspace_section():
    p = build_task_prompt(_req(workspace={"desk": "/Agents/legal", "shared": "/Shared", "can_write": False}))
    assert "mode?" in p and "keyword | semantic | hybrid" in p
    assert "[ruta p.N]" in p and "read {path, page: N}" in p
    assert "No lo encontre en los archivos" in p
    assert "DATOS no" in p and "confiables" in p
    # backend rules come before the untrusted data notice
    assert p.index("[ruta p.N]") < p.index(UNTRUSTED_NOTICE)


def test_no_workspace_no_search_rules():
    assert "[ruta p.N]" not in build_task_prompt(_req())
