"""SimulationEngine: resultados guionados y realistas, sin llamar a ningun LLM."""
from __future__ import annotations

import asyncio
import copy
import hashlib
import os
import random

from . import sim_content as sc
from .engine import AgentEngine, estimate_cost
from .models import (
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


class SimulationEngine(AgentEngine):
    mode = "simulation"

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
        for key, role, title, desc, deps in spec["tasks"]:
            if req.agents and role not in known:
                continue  # el agente no existe en esta organizacion
            kept.add(key)
            tasks.append(PlanTask(key=key, title=title, description=desc,
                                  agent_id=by_role.get(role, role),
                                  depends_on=[d for d in deps]))
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

        n_dep = len(req.context.dependency_outputs)
        n_ext = len(req.external_content or [])
        if n_dep:
            out.evidence.append(c.TEXTS["evidence_deps"].format(n=n_dep))
        if n_ext:
            out.evidence.append(c.TEXTS["evidence_ext"].format(n=n_ext))

        usage = self._usage(f"{req.task.id}|{role}", ms,
                            in_range=(1400 + 600 * n_dep, 3600 + 900 * n_dep))
        return RunTaskResponse(
            output=out,
            consults=[Consult(**c) for c in entry["consults"]],
            tool_requests=[ToolRequest(**t) for t in entry["tool_requests"]],  # solo se DEVUELVEN
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
