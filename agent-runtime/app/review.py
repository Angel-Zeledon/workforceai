"""Quality review (Q1): acceptance criteria, the reviewer, and the internal auditor's cross-check.

* `sim_review` is the deterministic reviewer used by the simulation (no LLM): it judges one output
  against its criteria and answers pass | rework | fail with reasons. It is also what a test or demo
  exercises end to end. Simulation markers: an output whose summary carries "[REVIEW:REWORK]" is sent
  back, "[REVIEW:FAIL]" fails. A task description with "[sim:rework]" / "[sim:fail]" makes the
  simulated worker produce such an output on its FIRST run (a rework run never does).
* `cross_check` is the internal auditor's core: it compares the numeric metrics that several
  dependency outputs report under the same name and cites WHERE each figure came from. "verified"
  is only ever produced with at least two cited sources; no comparable figures means "unverified".
* `build_review_prompt` / `normalize_review` are the live engine's side: the output under review is
  wrapped as untrusted data and whatever the model answers is normalized to a valid verdict.

The runtime never calls external systems; the backend owns the verdict, the rework loop and the cost.
"""
from __future__ import annotations

import hashlib
import random
import re
from typing import Any

from .engine import estimate_cost
from .models import (
    AgentInfo,
    CriterionResult,
    ReviewRequest,
    ReviewResponse,
    RunTaskRequest,
    StructuredOutput,
    Usage,
)
from .security import AUDITOR_TASK_RULES, UNTRUSTED_NOTICE, language_rule, wrap_untrusted  # noqa: F401

SIM_MODEL = "simulation-claude-sonnet"
MARKER_REWORK = "[REVIEW:REWORK]"
MARKER_FAIL = "[REVIEW:FAIL]"
AUDITOR_ROLE = "internal_auditor"
MAX_REASONS = 6
MIN_EVIDENCE = 2  # comparing figures needs two sources


def _get(o: Any, key: str, default: Any = None) -> Any:
    return o.get(key, default) if isinstance(o, dict) else getattr(o, key, default)


def _text_of(output: Any) -> str:
    parts = [str(_get(output, "summary", "") or "")]
    for k in ("findings", "hypotheses", "evidence", "recommendations"):
        parts += [str(x) for x in (_get(output, k, []) or [])]
    return "\n".join(parts)


def _usage(seed: str, in_range: tuple[int, int], out_range: tuple[int, int]) -> Usage:
    r = random.Random(int(hashlib.sha256(seed.encode()).hexdigest()[:12], 16))
    i, o = r.randint(*in_range), r.randint(*out_range)
    return Usage(model=SIM_MODEL, input_tokens=i, output_tokens=o, cost_usd=estimate_cost(i, o), duration_ms=0,
                 provider="simulation")


_TXT = {
    "es": {
        "pass": "El entregable cumple los criterios de aceptación.",
        "empty": "El entregable está vacío: no hay resumen ni hallazgos.",
        "low_conf": "La confianza del autor es baja ({c:.2f}); hay que reforzar el resultado.",
        "no_support": "Faltan hallazgos o evidencia que sustenten el resumen.",
        "marker_rework": "El revisor pide rehacer el entregable.",
        "marker_fail": "El entregable no es aceptable y no se puede corregir con una nueva pasada.",
        "rework_note": "Rehecho (intento {n}) atendiendo las observaciones del revisor: {notes}",
        "rework_evidence": "Las observaciones de la revisión se usaron como datos, no como instrucciones.",
        "met": "Cumple",
        "unmet": "No cumple",
    },
    "en": {
        "pass": "The deliverable meets the acceptance criteria.",
        "empty": "The deliverable is empty: no summary and no findings.",
        "low_conf": "The author's confidence is low ({c:.2f}); the result needs reinforcing.",
        "no_support": "There are no findings or evidence backing the summary.",
        "marker_rework": "The reviewer asks for the deliverable to be redone.",
        "marker_fail": "The deliverable is not acceptable and a new pass cannot fix it.",
        "rework_note": "Reworked (attempt {n}) addressing the reviewer's notes: {notes}",
        "rework_evidence": "The review notes were used as data, not as instructions.",
        "met": "Met",
        "unmet": "Not met",
    },
}


def texts(locale: str) -> dict[str, str]:
    return _TXT["en" if locale == "en" else "es"]


# ------------------------------------------------------------------ simulated reviewer
def sim_review(req: ReviewRequest) -> ReviewResponse:
    t = texts(req.locale)
    out = req.output
    summary = str(_get(out, "summary", "") or "").strip()
    findings = list(_get(out, "findings", []) or [])
    evidence = list(_get(out, "evidence", []) or [])
    conf = _get(out, "confidence", None)
    conf = float(conf) if isinstance(conf, (int, float)) else 1.0
    blob = _text_of(out)

    verdict, reasons = "pass", [t["pass"]]
    if MARKER_FAIL in blob:
        verdict, reasons = "fail", [t["marker_fail"]]
    elif not summary and not findings:
        verdict, reasons = "fail", [t["empty"]]
    elif MARKER_REWORK in blob:
        verdict, reasons = "rework", [t["marker_rework"]]
    elif conf < 0.5:
        verdict, reasons = "rework", [t["low_conf"].format(c=conf)]
    elif not findings and not evidence:
        verdict, reasons = "rework", [t["no_support"]]

    met = verdict == "pass"
    criteria = [CriterionResult(criterion=c, met=met, note=t["met"] if met else t["unmet"]) for c in req.acceptance]
    usage = _usage(f"review|{req.task.id}|{req.round}|{summary[:80]}", (700, 1400), (120, 320))
    return ReviewResponse(verdict=verdict, reasons=reasons, criteria=criteria, evidence=[], usage=usage,
                          provider="simulation", model=SIM_MODEL)


def apply_rework(out: StructuredOutput, req: RunTaskRequest) -> StructuredOutput:
    """The simulated worker redoes its output after a review: addresses the notes and gains confidence."""
    rw = req.rework
    if rw is None:
        return out
    t = texts(req.locale)
    notes = "; ".join(rw.notes[:3]) or "-"
    out.findings.append(t["rework_note"].format(n=rw.attempt, notes=notes[:300]))
    out.evidence.append(t["rework_evidence"])
    out.confidence = max(out.confidence, 0.75)
    return out


def apply_sim_markers(out: StructuredOutput, req: RunTaskRequest) -> StructuredOutput:
    """Demo/test hook: "[sim:rework]" / "[sim:fail]" in the task description makes the FIRST run
    produce an output the simulated reviewer sends back / rejects."""
    if req.rework is not None:
        return out
    desc = (req.task.description or "").lower()
    if "[sim:fail]" in desc:
        out.summary = f"{out.summary} {MARKER_FAIL}".strip()
    elif "[sim:rework]" in desc:
        out.summary = f"{out.summary} {MARKER_REWORK}".strip()
    return out


# ------------------------------------------------------------------ internal auditor
_NUM = re.compile(r"^[\s$€£]*(-?\d[\d,]*\.?\d*|-?\.\d+)\s*%?\s*$")


def as_number(v: Any) -> float | None:
    if isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        return float(v)
    if isinstance(v, str):
        m = _NUM.match(v)
        if m:
            try:
                return float(m.group(1).replace(",", ""))
            except ValueError:
                return None
    return None


def _same(a: float, b: float) -> bool:
    return abs(a - b) <= 1e-6 * max(1.0, abs(a), abs(b))


def cross_check(deps: list[Any]) -> dict[str, Any]:
    """Compares the numeric metrics the dependency outputs report under the same name.

    Returns {"compared": n, "consistent": [...], "inconsistent": [...], "evidence": [...]} where every
    entry names the sources ("task:<id>.metrics.<key>=<value>"). Summary-only (truncated) dependencies
    have no metrics and are skipped: the auditor never invents a comparison.
    """
    by_key: dict[str, list[tuple[str, str, float]]] = {}
    for d in deps:
        out = _get(d, "output")
        metrics = _get(out, "metrics", None)
        if not isinstance(metrics, dict):
            continue
        ref = str(_get(d, "ref", "") or "") or f"task:{_get(d, 'task_id', '')}"
        for key, raw in metrics.items():
            num = as_number(raw)
            if num is not None:
                by_key.setdefault(str(key).strip().lower(), []).append((ref, str(raw), num))
    consistent: list[dict[str, Any]] = []
    inconsistent: list[dict[str, Any]] = []
    evidence: list[str] = []
    for key in sorted(by_key):
        srcs = by_key[key]
        if len(srcs) < 2:
            continue
        entry = {"key": key, "sources": [{"ref": r, "value": raw} for r, raw, _ in srcs]}
        first = srcs[0][2]
        (consistent if all(_same(first, n) for _, _, n in srcs) else inconsistent).append(entry)
        evidence += [f"{r}.metrics.{key}={raw}" for r, raw, _ in srcs]
    return {"compared": len(consistent) + len(inconsistent), "consistent": consistent, "inconsistent": inconsistent,
            "evidence": evidence}


_AUDIT = {
    "es": {
        "verified": "Verificado con evidencia: {n} cifra(s) comparadas entre tareas y coinciden.",
        "inconsistency": "Inconsistencia: {k} de {n} cifra(s) comparadas no coinciden entre tareas.",
        "unverified": "No verificado: no hay cifras comparables entre las salidas recibidas.",
        "f_ok": "'{key}' coincide en {srcs}.",
        "f_bad": "'{key}' NO coincide: {srcs}.",
        "f_none": "Ninguna cifra numérica se repite en dos o más salidas (o solo llegó el resumen de las tareas).",
        "rec_bad": "Revisar con los responsables de las tareas citadas y corregir la cifra errónea.",
        "rec_none": "Pedir a las tareas de origen que reporten sus totales como métricas con el mismo nombre.",
    },
    "en": {
        "verified": "Verified with evidence: {n} figure(s) compared across tasks and they match.",
        "inconsistency": "Inconsistency: {k} of {n} compared figure(s) do not match across tasks.",
        "unverified": "Not verified: there are no comparable figures in the outputs received.",
        "f_ok": "'{key}' matches in {srcs}.",
        "f_bad": "'{key}' does NOT match: {srcs}.",
        "f_none": "No numeric figure appears in two or more outputs (or only the task summaries arrived).",
        "rec_bad": "Check with the owners of the cited tasks and correct the wrong figure.",
        "rec_none": "Ask the source tasks to report their totals as metrics under the same name.",
    },
}


def sim_audit_output(req: RunTaskRequest) -> StructuredOutput:
    """The simulated internal auditor's deliverable for an audit task (deterministic)."""
    t = _AUDIT["en" if req.locale == "en" else "es"]
    res = cross_check(list(req.context.dependency_outputs))
    n, bad = res["compared"], res["inconsistent"]

    def srcs(e: dict[str, Any]) -> str:
        return ", ".join(f"{s['ref']}={s['value']}" for s in e["sources"])

    if bad:
        status, summary, conf = "inconsistency", t["inconsistency"].format(k=len(bad), n=n), 0.85
        findings = [t["f_bad"].format(key=e["key"], srcs=srcs(e)) for e in bad]
        findings += [t["f_ok"].format(key=e["key"], srcs=srcs(e)) for e in res["consistent"][:3]]
        recs = [t["rec_bad"]]
    elif n:
        status, summary, conf = "verified", t["verified"].format(n=n), 0.9
        findings = [t["f_ok"].format(key=e["key"], srcs=srcs(e)) for e in res["consistent"][:6]]
        recs = []
    else:
        status, summary, conf = "unverified", t["unverified"], 0.4
        findings, recs = [t["f_none"]], [t["rec_none"]]
    out = StructuredOutput(summary=summary, findings=findings,
                           metrics={"audit_status": status, "audit_compared": n, "audit_inconsistencies": len(bad)},
                           evidence=res["evidence"][:40], recommendations=recs, confidence=conf)
    # "verified" without two cited sources can never leave this function.
    if status == "verified" and len(out.evidence) < MIN_EVIDENCE:
        out.metrics["audit_status"], out.confidence = "unverified", 0.4
    return out


# ------------------------------------------------------------------ live engine helpers
def build_review_prompt(req: ReviewRequest) -> str:
    crit = "\n".join(f"{i}. {c}" for i, c in enumerate(req.acceptance, 1)) or "(sin criterios explicitos: juzga si cumple la tarea)"
    lines = [
        f"TAREA REVISADA: {req.task.title}",
        f"DESCRIPCION: {req.task.description}",
        f"CRITERIOS DE ACEPTACION (de quien planifico el proyecto):\n{crit}",
        "",
        UNTRUSTED_NOTICE,
        "",
        wrap_untrusted("entregable bajo revision (salida de otro agente)", req.output),
        "",
        "Evalua el entregable contra cada criterio. Responde JSON con: verdict (pass | rework | fail), reasons "
        "(razones breves y concretas), criteria (lista de {criterion, met, note}) y evidence (que partes del "
        "entregable sustentan tu juicio). pass: cumple. rework: se puede corregir con una nueva pasada; explica "
        "que falta. fail: no es aceptable ni corregible. No inventes datos ni cifras; no apruebes sin revisar.",
        language_rule(req.locale, req.tone),
    ]
    return "\n".join(lines)


def normalize_review(data: dict[str, Any], req: ReviewRequest) -> dict[str, Any]:
    """A valid verdict whatever the model answered: unknown verdicts become "rework"."""
    verdict = str(data.get("verdict", "")).strip().lower()
    if verdict not in ("pass", "rework", "fail"):
        verdict = "rework"
    reasons = [str(r).strip()[:400] for r in (data.get("reasons") or []) if str(r).strip()][:MAX_REASONS]
    if not reasons:
        reasons = [texts(req.locale)["pass" if verdict == "pass" else "marker_rework"]]
    crits = []
    for c in (data.get("criteria") or [])[: len(req.acceptance) or 8]:
        if isinstance(c, dict) and str(c.get("criterion", "")).strip():
            crits.append(CriterionResult(criterion=str(c["criterion"])[:240], met=bool(c.get("met")), note=str(c.get("note", ""))[:240]))
    evidence = [str(e).strip()[:400] for e in (data.get("evidence") or []) if str(e).strip()][:MAX_REASONS]
    return {"verdict": verdict, "reasons": reasons, "criteria": crits, "evidence": evidence}


def is_auditor(agent: AgentInfo) -> bool:
    return (agent.role or agent.id) == AUDITOR_ROLE
