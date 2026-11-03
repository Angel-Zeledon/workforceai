"""Parts 1 and 2 of the file-finding design: the agent finds mentioned files on its own (recent files of the person,
confirm only when in doubt, say when it has no access) and never adds up invoices itself."""

from app.file_locate import DEFAULT_RECENT_DAYS, locate_lines, wants_files, wants_income_statement
from app.models import RunTaskRequest
from app.security import UNTRUSTED_NOTICE, build_task_prompt


def _req(text: str, can_write: bool = True, title: str = "Tarea", **extra):
    base = {
        "task": {"id": "t1", "title": title, "description": text, "agent_id": "accounting"},
        "agent": {"id": "accounting", "role": "accounting", "title": "Contador", "tools": ["fs"]},
        "context": {"request_text": text},
        "workspace": {"desk": "/Agents/accounting", "shared": "/Shared", "can_write": can_write},
    }
    base.update(extra)
    return RunTaskRequest.model_validate(base)


def test_mentioned_files_get_the_search_order_and_the_no_access_rule():
    p = build_task_prompt(_req("Puse las facturas del mes en PDF, revisalas"))
    assert "recent {days?, kind?, path?}" in p and f"ultimos {DEFAULT_RECENT_DAYS} dias" in p
    assert p.index("1) lo adjuntado") < p.index("2) recent") < p.index("3) search")
    assert "CONFIRMA SOLO SI HAY DUDA" in p and "help_request" in p
    assert "SIN ACCESO" in p and "Darle acceso a esta carpeta" in p and "Pedir acceso" in p
    assert p.index("SIN ACCESO") < p.index(UNTRUSTED_NOTICE)


def test_invoices_to_income_statement_are_computed_by_the_backend():
    p = build_task_prompt(_req("Puse las facturas de octubre, hazme el estado de resultados"))
    assert "NUNCA sumes" in p and "income_statement {path" in p
    assert 'status:"scanned"' in p and "Nunca inventes ni completes un importe" in p
    assert "Detalle, Estado de" in p and "cuantas hay que" in p


def test_no_rules_when_the_task_has_nothing_to_do_with_files():
    p = build_task_prompt(_req("Redacta un saludo de bienvenida para el equipo"))
    assert "recent {days?" not in p and "income_statement" not in p


def test_read_only_agents_get_the_finding_rules_but_not_the_writer():
    p = build_task_prompt(_req("hazme el estado de resultados con las facturas que subi", can_write=False))
    assert "2) recent" in p and "income_statement {path" not in p


def test_detectors_and_trim_off():
    assert wants_files("el contrato que subí ayer") and wants_files("the invoices I uploaded")
    assert not wants_files("hola, como estas")
    assert wants_income_statement("facturas -> estado de resultados") and wants_income_statement("income statement please")
    assert not wants_income_statement("cuantas facturas hay")

    class WS:
        can_write = True

    assert len(locate_lines(WS(), "hola", None, trim=False)) > 3
