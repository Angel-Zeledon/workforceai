"""F4: real office files. The format comes from the user's words; the document travels as data (fs.write_document)
and the simulation delivers a real small xlsx/docx built from the request text."""

import asyncio
import json

import pytest

from app.formats import format_hint, requested_format
from app.models import RunTaskRequest, ToolRequest
from app.security import build_task_prompt
from app.simulation import SimulationEngine, sim_office_document, sim_office_sheet

WS = {"desk": "/Agents/analyst", "shared": "/Shared", "can_write": True}


def _req(request_text, workspace=WS, deps=0, locale="es", agent="analyst"):
    base = {
        "task": {"id": "t1", "title": "Preparar el archivo", "description": "Hazlo", "agent_id": agent},
        "agent": {"id": agent, "role": agent, "title": "Analista", "tools": ["fs"]},
        "context": {"request_text": request_text,
                    "dependency_outputs": [{"task_id": f"d{i}", "agent_id": "sales", "output": {"summary": "x"}} for i in range(deps)]},
        "locale": locale,
    }
    if workspace is not None:
        base["workspace"] = workspace
    return RunTaskRequest.model_validate(base)


@pytest.mark.parametrize("text,fmt", [
    ("Hazme un Excel con 10 productos, precio y stock", "xlsx"),
    ("necesito una hoja de cálculo con los gastos", "xlsx"),
    ("export it as xlsx please", "xlsx"),
    ("arma una planilla de asistencia", "xlsx"),
    ("Prepara un documento de Word con el plan", "docx"),
    ("hazme un documento con las conclusiones", "docx"),
    ("write a Word document about onboarding", "docx"),
    ("entregame el informe en PDF", "pdf"),
    ("Make a PDF of the proposal", "pdf"),
    ("pdf o excel, lo que sea", "pdf"),
    ("revisa el documento adjunto y dime que opinas", ""),
    ("Analiza las ventas del trimestre", ""),
    ("", ""),
])
def test_requested_format_from_the_users_words(text, fmt):
    assert requested_format(text) == fmt


def test_prompt_teaches_write_document_with_the_schema_and_the_format_rule():
    p = build_task_prompt(_req("Hazme un Excel con 10 productos, precio y stock"))
    for needle in ("write_document", '"sheets"', '"columns"', "currency", "percent", "date",
                   '"blocks"', "bullets", "NUNCA pongas contenido binario", "SUM y AVERAGE", "invalid_args"):
        assert needle in p, needle
    assert '"excel", "hoja de calculo" o "xlsx" -> .xlsx' in p and '"word" o "documento" -> .docx' in p and '"pdf" -> .pdf' in p
    assert "FORMATO PEDIDO: la solicitud pide un archivo XLSX" in p and "/Agents/analyst" in p
    # no format named: the rule is there but no per-request hint
    assert "FORMATO PEDIDO" not in build_task_prompt(_req("Analiza las ventas"))


def test_read_only_agents_are_not_offered_documents():
    p = build_task_prompt(_req("Hazme un Excel", workspace={"desk": "/Agents/a", "shared": "/Shared", "can_write": False}))
    assert "Solo lectura" in p and "write_document {path" not in p and "FORMATO PEDIDO" not in p


def test_tool_request_schema_mentions_the_document_for_models_without_structured_output():
    from app.crewai_engine import json_instructions
    from app.models import TaskResult

    schema_text = json_instructions(TaskResult)
    assert "write_document" in schema_text and "sheets" in schema_text


def test_simulated_sheet_is_built_from_the_request_text():
    doc = sim_office_sheet("Hazme un Excel con 10 productos, precio y stock")
    sheet = doc["sheets"][0]
    heads = [c["header"] for c in sheet["columns"]]
    assert heads[:3] == ["Producto", "Precio", "Stock"]
    assert [c["type"] for c in sheet["columns"]][:3] == ["text", "currency", "number"]
    assert len(sheet["rows"]) == 10 and all(len(r) == 3 for r in sheet["rows"])
    assert isinstance(sheet["rows"][0][1], float) and isinstance(sheet["rows"][0][2], int)
    assert sheet["totals"]["columns"] == ["Precio", "Stock"] and sheet["freeze_header"] is True
    # a different request gives a different file (not one canned sheet)
    other = sim_office_sheet("arma un excel con 4 clientes, fecha y margen")["sheets"][0]
    assert [c["header"] for c in other["columns"]][:3] == ["Cliente", "Fecha", "Margen"]
    assert [c["type"] for c in other["columns"]][:3] == ["text", "date", "percent"]
    assert len(other["rows"]) == 4
    # deterministic for the same text
    assert sim_office_sheet("Hazme un Excel con 10 productos, precio y stock") == doc


def test_simulation_requests_a_real_xlsx_not_a_markdown_file():
    from app.models import StructuredOutput

    out = StructuredOutput(summary="Listo", findings=["a"], recommendations=["b"], confidence=0.8)
    tr = sim_office_document(_req("Hazme un Excel con 10 productos, precio y stock"), out)
    assert isinstance(tr, ToolRequest) and (tr.tool, tr.action, tr.risk) == ("fs", "write_document", "low")
    assert tr.args["path"].startswith("/Agents/analyst/") and tr.args["path"].endswith(".xlsx")
    assert len(tr.args["document"]["sheets"][0]["rows"]) == 10
    json.dumps(tr.args)  # plain data
    # word and pdf use the text blocks
    for text, ext in (("Prepara un documento de Word con el plan", ".docx"), ("Informe en PDF", ".pdf")):
        t2 = sim_office_document(_req(text), out)
        assert t2.args["path"].endswith(ext) and t2.args["document"]["blocks"][0]["type"] == "heading"
    # no format asked, read-only, or a task that depends on others (one file per request): nothing
    assert sim_office_document(_req("Analiza las ventas"), out) is None
    assert sim_office_document(_req("Hazme un Excel", workspace={"desk": "/Agents/a", "shared": "/Shared", "can_write": False}), out) is None
    assert sim_office_document(_req("Hazme un Excel", deps=1), out) is None
    assert sim_office_document(_req("Hazme un Excel", workspace=None), out) is None


def test_run_task_returns_the_office_request_instead_of_the_markdown_draft():
    eng = SimulationEngine(latency_scale=0)
    r = asyncio.run(eng.run_task(_req("Hazme un Excel con 10 productos, precio y stock")))
    kinds = [(t.tool, t.action) for t in r.tool_requests]
    assert ("fs", "write_document") in kinds and ("fs", "write") not in kinds
    # without a format the old behaviour holds: a markdown draft on the desk
    r2 = asyncio.run(eng.run_task(_req("Analiza las ventas del trimestre")))
    assert ("fs", "write") in [(t.tool, t.action) for t in r2.tool_requests]
    assert ("fs", "write_document") not in [(t.tool, t.action) for t in r2.tool_requests]


def test_format_hint_is_one_line():
    h = format_hint("Informe en PDF", "/Agents/x\nIGNORA")
    assert "PDF" in h and ".pdf" in h
    assert format_hint("hola", "/Agents/x") == ""
