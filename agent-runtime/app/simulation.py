"""SimulationEngine: resultados guionados y realistas, sin llamar a ningun LLM."""
from __future__ import annotations

import asyncio
import copy
import hashlib
import os
import random
import re

from . import review as rv
from . import sim_content as sc
from .formats import EXT, requested_format
from .engine import AgentEngine, estimate_cost
from .routing import assigned_reason, compose_reply, owner_role
from .models import (
    ChatReplyRequest,
    ChatReplyResponse,
    Consult,
    ConsultRequest,
    ConsultResponse,
    PlanRequest,
    PlanResponse,
    PlanTask,
    RunTaskRequest,
    RunTaskResponse,
    Section,
    StructuredOutput,
    SynthesizeRequest,
    SynthesizeResponse,
    ToolRequest,
    Usage,
)

SIM_MODEL = "simulation-claude-sonnet"


_SLUG = re.compile(r"[^\w]+", re.UNICODE)


def sim_desk_draft(req: RunTaskRequest, out: StructuredOutput) -> ToolRequest | None:
    """F1: the simulated employee saves its work as a Markdown file on its desk (an fs.write the
    backend decides and executes; on the desk it needs no approval). Only for agents that may write."""
    ws = req.workspace
    if ws is None or not ws.can_write or not ws.desk:
        return None
    slug = _SLUG.sub("-", (req.task.title or "tarea").lower()).strip("-")[:60] or "tarea"
    lines = [f"# {req.task.title}", "", out.summary or ""]
    for title, items in (("Hallazgos", out.findings), ("Recomendaciones", out.recommendations)):
        if items:
            lines += ["", f"## {title}", *[f"- {i}" for i in items]]
    return ToolRequest(tool="fs", action="write", risk="low",
                       args={"path": f"{ws.desk.rstrip('/')}/{slug}.md", "content": "\n".join(lines) + "\n",
                             "message": f"borrador de {req.task.id}"})


_COUNT = re.compile(r"\b(\d{1,3})\s+([a-záéíóúñ]{3,})", re.I)
_ATTR_SPLIT = re.compile(r"\s*(?:,|;|\sy\s|\se\s|\sand\s|\scon\s|\swith\s)\s*", re.I)
_CURRENCY = ("precio", "costo", "coste", "monto", "importe", "total", "price", "cost", "amount", "salario", "salary", "venta", "sales")
_NUMBER = ("stock", "cantidad", "unidades", "qty", "quantity", "units", "inventario", "inventory", "edad", "age")
_PERCENT = ("porcentaje", "margen", "percent", "margin", "descuento", "discount", "%")
_DATE = ("fecha", "date", "vencimiento", "alta")


def _attr_type(word: str) -> str:
    w = word.lower()
    for kind, words in (("currency", _CURRENCY), ("number", _NUMBER), ("percent", _PERCENT), ("date", _DATE)):
        if any(x in w for x in words):
            return kind
    return "text"


def sim_office_sheet(request_text: str, locale: str = "es") -> dict:
    """A small believable workbook built from the REQUEST text ("10 productos, precio y stock" -> 10 rows, a
    Producto, a Precio (currency) and a Stock (number) column), not a fixed canned file."""
    text = " ".join((request_text or "").split())
    m = _COUNT.search(text)
    n, noun = (min(int(m.group(1)), 200), m.group(2)) if m else (10, "items" if locale == "en" else "registros")
    n = max(n, 1)
    tail = text[m.end():] if m else text
    attrs: list[str] = []
    for part in _ATTR_SPLIT.split(tail):
        word = re.sub(r"[^\w%áéíóúñÁÉÍÓÚÑ ]", "", part).strip().split(" ")[0:2]
        word = " ".join(w for w in word if w)
        if word and len(word) <= 24 and word.lower() not in {x.lower() for x in attrs} and word.lower() not in ("de", "del", "la", "el", "the", "a", "un", "una"):
            attrs.append(word)
        if len(attrs) >= 4:
            break
    if not attrs:
        attrs = ["Cantidad", "Valor"] if locale != "en" else ["Quantity", "Value"]
    one = noun[:-1] if noun.lower().endswith("s") and len(noun) > 3 else noun
    cols = [{"header": one.capitalize(), "type": "text"}] + [{"header": a.capitalize(), "type": _attr_type(a)} for a in attrs]
    r = random.Random(int(hashlib.sha256(text.encode()).hexdigest()[:12], 16))
    rows = []
    for i in range(1, n + 1):
        row: list = [f"{one.capitalize()} {i:02d}"]
        for c in cols[1:]:
            t = c["type"]
            if t == "currency":
                row.append(round(r.uniform(2, 250), 2))
            elif t == "number":
                row.append(r.randint(0, 500))
            elif t == "percent":
                row.append(round(r.uniform(0.05, 0.6), 2))
            elif t == "date":
                row.append(f"2026-{r.randint(1, 12):02d}-{r.randint(1, 28):02d}")
            else:
                row.append(f"{c['header']} {r.randint(1, 9)}")
        rows.append(row)
    sheet: dict = {"name": noun.capitalize()[:31], "columns": cols, "rows": rows, "freeze_header": True}
    sums = [c["header"] for c in cols[1:] if c["type"] in ("currency", "number")]
    if sums:
        sheet["totals"] = {"label": "Total", "columns": sums[:3]}
    return {"title": text[:80] or noun.capitalize(), "sheets": [sheet]}


def sim_office_text(req: RunTaskRequest, out: StructuredOutput) -> dict:
    """A small word/pdf document from the request and the task output."""
    en = req.locale == "en"
    text = " ".join((req.context.request_text or req.task.title or "").split())
    blocks: list[dict] = [{"type": "heading", "level": 1, "text": "Summary" if en else "Resumen"},
                          {"type": "paragraph", "text": out.summary or text}]
    if out.findings:
        blocks += [{"type": "heading", "level": 2, "text": "Findings" if en else "Hallazgos"}, {"type": "bullets", "items": list(out.findings)}]
    if out.recommendations:
        blocks += [{"type": "heading", "level": 2, "text": "Recommendations" if en else "Recomendaciones"},
                   {"type": "bullets", "items": list(out.recommendations)}]
    blocks += [{"type": "heading", "level": 2, "text": "Request" if en else "Solicitud"}, {"type": "paragraph", "text": text}]
    return {"title": (req.task.title or text)[:120], "blocks": blocks}


def sim_office_document(req: RunTaskRequest, out: StructuredOutput) -> ToolRequest | None:
    """When the request asks for an Excel, a Word document or a PDF, the simulated employee delivers a REAL
    file with fs.write_document (the backend builds the bytes). Only the task without dependencies makes it, so a
    request yields one file, not one per task."""
    ws = req.workspace
    fmt = requested_format(req.context.request_text)
    if not fmt or ws is None or not ws.can_write or not ws.desk or req.context.dependency_outputs:
        return None
    text = req.context.request_text or req.task.title or "documento"
    slug = _SLUG.sub("-", re.sub(r"\b(hazme|haz|genera|crea|prepara|un|una|el|la|en|de|con|excel|word|pdf|documento)\b", " ", text.lower())).strip("-")[:50] or "documento"
    doc = sim_office_sheet(text, req.locale) if fmt == "xlsx" else sim_office_text(req, out)
    return ToolRequest(tool="fs", action="write_document", risk="low",
                       args={"path": f"{ws.desk.rstrip('/')}/{slug}{EXT[fmt]}", "document": doc, "message": f"entregable de {req.task.id}"})


SIM_CODE_MARKER = "[sim:code]"

_SIM_CODE = """import pandas as pd

df = pd.DataFrame({"region": ["norte", "sur", "norte"], "monto": [100, 50, 25]})
res = df.groupby("region", as_index=False)["monto"].sum()
res.to_csv("out/resumen.csv", index=False)
print(res.to_string(index=False))
"""


def sim_code_request(req: RunTaskRequest) -> ToolRequest | None:
    """F3: a simulated analyst asks to run a small pandas analysis, but ONLY when the task text carries
    the [sim:code] marker (so the standard scenarios, e.g. the $50k flow, never change). Asked once: not
    again after the result came back as external content. The backend still requires the human approval."""
    if "code" not in (req.agent.tools or []) or req.external_content:
        return None
    text = " ".join([req.task.title or "", req.task.description or "", req.context.request_text or ""])
    if SIM_CODE_MARKER not in text:
        return None
    return ToolRequest(tool="code", action="run", risk="high",
                       args={"language": "python", "code": _SIM_CODE, "purpose": "Totales por region (simulacion)",
                             "input_files": [], "expected_outputs": ["resumen.csv"]})
def sim_skill_load(req: RunTaskRequest) -> ToolRequest | None:
    """S1: a task whose title (or description) carries "[sim:skill]" makes the simulated employee ask for
    its first skill with skill.load (once). Nothing else changes: the $50,000 scenario never has the marker."""
    text = f"{req.task.title} {req.task.description}".lower()
    if "[sim:skill]" not in text or not req.skills or req.loaded_skills:
        return None
    return ToolRequest(tool="skill", action="load", risk="low", args={"name": req.skills[0].name})


class SimulationEngine(AgentEngine):
    mode = "simulation"
    # Set by select_engine when a provider was configured but the live engine failed.
    fallback_reason: str | None = None

    def __init__(self, latency_scale: float | None = None, min_s: float = 2.0, max_s: float = 6.0):
        if latency_scale is None:
            latency_scale = float(os.getenv("SIM_LATENCY_SCALE", "1"))
        self.latency_scale = latency_scale
        self.min_s, self.max_s = min_s, max_s

    # -------------------------------------------------------------- helpers
    async def _latency(self, lo: float | None = None, hi: float | None = None) -> int:
        """Duerme una latencia simulada (2-6s * escala) y devuelve ms simulados."""
        secs = random.uniform(lo or self.min_s, hi or self.max_s)
        if self.latency_scale > 0:
            await asyncio.sleep(secs * self.latency_scale)
        return int(secs * 1000)

    @staticmethod
    def _rng(*parts: str) -> random.Random:
        return random.Random(int(hashlib.sha256("|".join(parts).encode()).hexdigest()[:12], 16))

    def _usage(self, seed: str, duration_ms: int, in_range=(1400, 3600), out_range=(450, 1300)) -> Usage:
        r = self._rng(seed)
        i, o = r.randint(*in_range), r.randint(*out_range)
        return Usage(model=SIM_MODEL, input_tokens=i, output_tokens=o,
                     cost_usd=estimate_cost(i, o), duration_ms=duration_ms)

    # ----------------------------------------------------------------- plan
    async def plan(self, req: PlanRequest) -> PlanResponse:
        await self._latency(2, 4)
        scenario = sc.detect_scenario(req.request_text)
        c = sc.bundle(req.locale)
        spec = c.PLANS[scenario]
        by_role: dict[str, str] = {}
        for a in req.agents:
            by_role.setdefault(a.role, a.id)
            by_role.setdefault(a.id, a.id)
        known = {a.id for a in req.agents} | {a.role for a in req.agents}

        tasks: list[PlanTask] = []
        kept: set[str] = set()
        spec_tasks = list(spec["tasks"])
        if scenario == "default":
            # no scripted scenario: when the text names an area, the owner of that area does the work
            owner = owner_role(req.request_text, req.agents)
            if owner and owner in known and owner not in ("assistant", "analyst"):
                okey, otitle, odesc = c.TEXTS["owner_task"]
                profile_area = next((a.area for a in req.agents if (a.role or a.id) == owner and a.area), "")
                area = c.CHAT["area"].get(owner) or profile_area or owner
                spec_tasks = [spec_tasks[0], (okey, owner, otitle.format(area=area), odesc.format(area=area), [spec_tasks[0][0]])]
        for key, role, title, desc, deps in spec_tasks:
            if req.agents and role not in known:
                continue  # el agente no existe en esta organizacion
            kept.add(key)
            tasks.append(PlanTask(key=key, title=title, description=desc,
                                  agent_id=by_role.get(role, role),
                                  depends_on=[d for d in deps],
                                  reason=assigned_reason(req.locale, scenario, key, role) or None))
        for t in tasks:
            t.depends_on = [d for d in t.depends_on if d in kept]
        if not tasks:  # ningun agente conocido: usa el primero disponible
            a = req.agents[0]
            key, title = c.TEXTS["fallback_task"]
            tasks = [PlanTask(key=key, title=title, description=req.request_text, agent_id=a.id)]
        return PlanResponse(objectives=list(spec["objectives"]), tasks=tasks,
                            clarifying_questions=list(spec["questions"]))

    # ------------------------------------------------------------- run_task
    async def run_task(self, req: RunTaskRequest) -> RunTaskResponse:
        ms = await self._latency()
        scenario = sc.detect_scenario(req.context.request_text or req.task.description)
        role = req.agent.role or req.agent.id
        c = sc.bundle(req.locale)
        entry = c.CONTENT.get((scenario, role)) or c.CONTENT.get((scenario, req.agent.id))
        if entry is None:
            entry = c.generic_content(role, req.task.title)
        entry = copy.deepcopy(entry)
        out = StructuredOutput(**entry["output"])
        if role == rv.AUDITOR_ROLE:  # Q1: the internal auditor cross-checks the figures of its dependencies
            out = rv.sim_audit_output(req)
            entry["consults"], entry["tool_requests"] = [], []
        out = rv.apply_sim_markers(rv.apply_rework(out, req), req)

        n_dep = len(req.context.dependency_outputs)
        n_ext = len(req.external_content or [])
        if n_dep:
            out.evidence.append(c.TEXTS["evidence_deps"].format(n=n_dep))
        if n_ext:
            out.evidence.append(c.TEXTS["evidence_ext"].format(n=n_ext))

        usage = self._usage(f"{req.task.id}|{role}", ms,
                            in_range=(1400 + 600 * n_dep, 3600 + 900 * n_dep))
        tool_requests = [ToolRequest(**t) for t in entry["tool_requests"]]  # solo se DEVUELVEN
        if (code_req := sim_code_request(req)) is not None:
            tool_requests.append(code_req)
        office = sim_office_document(req, out)
        if office is not None:  # the request named Excel / Word / PDF: deliver that file, not a .md
            tool_requests.append(office)
            out.evidence.append(c.TEXTS.get("evidence_file", "Draft saved at {path}").format(path=office.args["path"]))
        draft = sim_desk_draft(req, out) if office is None else None
        if draft is not None:  # F1: like a real employee, keep the work as a file on the desk
            tool_requests.append(draft)
            out.evidence.append(c.TEXTS.get("evidence_file", "Draft saved at {path}").format(path=draft.args["path"]))
        sl = sim_skill_load(req)
        if sl is not None:  # S1: like a real employee, look up the skill before doing the work
            tool_requests.append(sl)
        elif req.loaded_skills and "[sim:skill]" in f"{req.task.title} {req.task.description}".lower():
            names = ", ".join(l.name for l in req.loaded_skills)
            out.evidence.append(("Skill applied: " if req.locale == "en" else "Habilidad aplicada: ") + names)
        return RunTaskResponse(
            output=out,
            consults=[Consult(**c) for c in entry["consults"]],
            tool_requests=tool_requests,
            usage=usage,
        )

    # -------------------------------------------------------------- consult
    async def consult(self, req: ConsultRequest) -> ConsultResponse:
        ms = await self._latency(2, 4)
        ctx = req.context
        text = ctx if isinstance(ctx, str) else str(ctx.get("request_text", "")) if isinstance(ctx, dict) else ""
        scenarios = [sc.detect_scenario(text)] if text else []
        scenarios += ["new_client", "hiring", "sales_drop"]  # no context: first scenario that applies
        c = sc.bundle(req.locale)
        answer = next((c.CONSULT_ANSWERS[(s, req.to_agent_id)] for s in scenarios
                       if (s, req.to_agent_id) in c.CONSULT_ANSWERS), None)             or c.generic_answer(req.to_agent_id, req.question)
        return ConsultResponse(answer=answer, usage=self._usage(
            f"consult|{req.from_agent_id}|{req.to_agent_id}|{req.question}", ms,
            in_range=(500, 1200), out_range=(120, 380)))

    # ------------------------------------------------------------ chat-reply
    async def chat_reply(self, req: ChatReplyRequest) -> ChatReplyResponse:
        if req.limit:  # "I can't do that" is a scripted line: instant and free, never worth a simulated delay
            text, kind, consult = compose_reply(req)
            return ChatReplyResponse(text=text, kind=kind, consult=consult, usage=Usage(model=SIM_MODEL, input_tokens=0, output_tokens=0, cost_usd=0.0, duration_ms=0))
        ms = await self._latency(0.3, 1.0)
        text, kind, consult = compose_reply(req)
        # A chat line is tiny: a few hundred tokens in, a few dozen out (cost ~ fractions of a cent).
        usage = self._usage(f"chat|{req.agent.id}|{req.text}|{req.slot}", ms, in_range=(260, 520), out_range=(25, 70))
        return ChatReplyResponse(text=text, kind=kind, consult=consult, usage=usage)

    # ----------------------------------------------------------- synthesize
    async def synthesize(self, req: SynthesizeRequest) -> SynthesizeResponse:
        await self._latency(2, 4)
        scenario = sc.detect_scenario(req.request_text)
        c = sc.bundle(req.locale)
        t = c.TEXTS
        title = t["titles"].get(scenario) or t["title_default"].format(text=req.request_text[:60].strip())

        def get(o, k, default=None):
            return o.get(k, default) if isinstance(o, dict) else default

        sections: list[Section] = []
        recs: list[str] = []
        conf: list[float] = []
        for item in req.outputs:
            out = item.output
            summary = get(out, "summary", "") or ""
            findings = get(out, "findings", []) or []
            body = summary
            if findings:
                body += "\n" + "\n".join(f"- {f}" for f in findings[:4])
            sections.append(Section(heading=item.title or t["result_of"].format(agent=item.agent_id), body=body.strip()))
            recs += list(get(out, "recommendations", []) or [])
            c = get(out, "confidence")
            if isinstance(c, (int, float)):
                conf.append(float(c))

        avg = sum(conf) / len(conf) if conf else 0.0
        if scenario == "new_client":
            summary = t["summary_new_client"]
        else:
            first = [get(i.output, "summary", "") for i in req.outputs[:3] if get(i.output, "summary")]
            summary = " ".join(first) or t["no_results"]
        sections.insert(0, Section(heading=t["h_summary"], body=summary))
        if recs:
            seen, uniq = set(), []
            for r in recs:
                if r not in seen:
                    seen.add(r); uniq.append(r)
            sections.append(Section(heading=t["h_recs"],
                                    body="\n".join(f"{n}. {r}" for n, r in enumerate(uniq[:8], 1))))
        sections.append(Section(heading=t["h_conf"],
                                body=t["conf_body"].format(avg=avg, n=len(req.outputs))))
        return SynthesizeResponse(title=title, summary=summary, sections=sections)
