"""Capability 5 (edit by chat): the prompt teaches fs.edit and keeps file content as untrusted data."""

from app.models import RunTaskRequest
from app.security import build_task_prompt, edit_lines


def _req(can_write=True):
    return RunTaskRequest.model_validate({
        "task": {"id": "t1", "title": "Cambiar el titulo", "description": "Hazlo", "agent_id": "analyst"},
        "agent": {"id": "analyst", "role": "analyst", "title": "Analista", "tools": ["fs"]},
        "context": {"request_text": "cambia el titulo de la tabla"},
        "workspace": {"desk": "/Agents/analyst", "shared": "/Shared", "can_write": can_write},
        "locale": "es",
    })


def test_prompt_teaches_fs_edit_the_smallest_change():
    p = build_task_prompt(_req())
    for needle in ("edit {path, expected_version, ops", "replace", "insert_after", "set_heading", "set_cell", "add_row",
                   "edicion MINIMA", "NUNCA reescribas un archivo entero", "EXACTO", "expected_version", "edit_ambiguous",
                   "version_conflict", "UNA frase", "DATO no confiable"):
        assert needle in p, needle


def test_read_only_agents_are_not_taught_edit():
    p = build_task_prompt(_req(can_write=False))
    assert "edit {path" not in p and "Solo lectura" in p


def test_edit_lines_are_self_contained():
    assert any("no confiable" in line for line in edit_lines())
