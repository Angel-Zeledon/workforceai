"""Slash commands support (docs/architecture/commands.md): request markers, summarize and context sizes.

Everything here works with ANY provider: the summarize prompt carries its JSON contract in the instructions
(no structured output needed) and the simulation answers deterministically without any model.
"""
from __future__ import annotations

import math
import re
from typing import Any

from .models import (
    ContextSegment,
    ContextSizesResponse,
    RunTaskRequest,
    SummarizeRequest,
)
from .security import (
    UNTRUSTED_NOTICE,
    _as_text,
    build_system_prompt,
    build_task_prompt,
    code_lines,
    dep_budget_tokens,
    _dep_label,
    fit_texts,
    language_rule,
    loaded_skill_lines,
    skill_lines,
    workspace_lines,
    wrap_untrusted,
)

# ---------------------------------------------------------------- markers
_MARK_LANG = re.compile(r"\[\[lang:(es|en)\]\]", re.I)
_MARK_MEETING = re.compile(r"\[\[meeting:([^\]]*)\]\]", re.I)
_MARK_DRAFT = re.compile(r"\[\[draft:(email|linkedin)\]\]", re.I)
_MARK_ANY = re.compile(r"\[\[(?:lang|meeting|draft|format):[^\]]*\]\]", re.I)


def marker_lang(text: str | None) -> str:
    """"es" | "en" when the request text carries [[lang:xx]] (the alias the person typed), else ""."""
    m = _MARK_LANG.search(text or "")
    return m.group(1).lower() if m else ""


def marker_meeting(text: str | None) -> list[str]:
    """Agent ids named by [[meeting:a,b,c]] (empty when the request is not a meeting)."""
    m = _MARK_MEETING.search(text or "")
    if not m:
        return []
    seen: list[str] = []
    for part in re.split(r"[,\s]+", m.group(1).strip()):
        if part and part not in seen:
            seen.append(part)
    return seen


def marker_draft(text: str | None) -> str:
    """"email" | "linkedin" when the request asks for a draft that needs approval, else ""."""
    m = _MARK_DRAFT.search(text or "")
    return m.group(1).lower() if m else ""


def strip_markers(text: str | None) -> str:
    return " ".join(_MARK_ANY.sub(" ", text or "").split())


# ---------------------------------------------------------------- tokens
def tokens_of(text: str) -> int:
    return math.ceil(len(text or "") / 4)


# ---------------------------------------------------------------- summarize
_TITLES = {
    "compact": {"es": "Resumen de la conversación", "en": "Conversation summary"},
    "meeting": {"es": "Conclusión de la reunión", "en": "Meeting conclusion"},
    "daily": {"es": "Resumen del día", "en": "Summary of the day"},
}


def build_summarize_prompt(req: SummarizeRequest) -> str:
    """Prompt for the summarizer. The messages are DATA (a user can paste instructions in a chat)."""
    loc = req.locale
    if req.mode == "meeting":
        task = (f"Las personas {', '.join(req.participants)} hablaron del tema «{req.topic}». "
                "Redacta la CONCLUSION de la reunion: acuerdos, desacuerdos, decisiones y proximos pasos con responsable.")
    elif req.mode == "daily":
        task = ("Redacta el RESUMEN DEL DIA para la direccion: que se hizo, que esta pendiente, riesgos y lo que "
                "necesita una decision humana. Usa solo los hechos dados.")
    else:
        task = ("Resume esta conversacion para que un agente pueda continuarla SIN ver los mensajes originales: "
                "decisiones, datos y cifras acordadas, tareas pendientes, preferencias de la persona. No inventes.")
    lines = [task, UNTRUSTED_NOTICE]
    if req.previous_summary:
        lines.append(wrap_untrusted("resumen anterior", req.previous_summary))
    lines.append(wrap_untrusted("mensajes", [{"from": m.from_, "text": m.text} for m in req.messages]))
    lines += ["", 'Responde SOLO JSON: {"text": "<el resumen>"}. Maximo 1200 palabras.', language_rule(loc, req.tone)]
    return "\n".join(lines)


def _short(text: str, n: int) -> str:
    text = " ".join((text or "").split())
    return text if len(text) <= n else text[: n - 1].rstrip() + "…"


def sim_summary(req: SummarizeRequest) -> str:
    """Deterministic summary for the simulation (and the extractive shape tests rely on)."""
    en = req.locale == "en"
    title = _TITLES[req.mode]["en" if en else "es"]
    lines: list[str] = []
    if req.mode == "meeting":
        who = ", ".join(req.participants)
        lines.append(f"{title}: {req.topic}" if req.topic else title)
        lines.append(("Participants: " if en else "Participantes: ") + who)
        for m in req.messages[:8]:
            lines.append(f"- {m.from_}: {_short(m.text, 160)}")
        lines.append(("Conclusion: the team aligned on the topic; owners follow up on their own points."
                      if en else "Conclusion: el equipo se alineo en el tema; cada responsable da seguimiento a su parte."))
        return "\n".join(lines)
    if req.previous_summary:
        lines.append(("Earlier: " if en else "Antes: ") + _short(req.previous_summary, 400))
    lines.insert(0, f"{title} ({len(req.messages)} " + ("messages)" if en else "mensajes)"))
    for m in req.messages[-12:]:
        who = m.from_ or "?"
        lines.append(f"- {who}: {_short(m.text, 140)}")
    return "\n".join(lines)


def sim_usage_tokens(req: SummarizeRequest, text: str) -> tuple[int, int]:
    n_in = tokens_of(req.previous_summary) + sum(tokens_of(m.text) for m in req.messages) + 120
    return n_in, max(tokens_of(text), 1)


# ---------------------------------------------------------------- context sizes
def context_sizes(req: RunTaskRequest) -> ContextSizesResponse:
    """What the agent would receive for this task, by segment, with the SAME builders as run-task. No LLM."""
    ctx = req.context
    system = build_system_prompt(req.agent, req.locale, req.tone)
    task = build_task_prompt(req)
    skills = "\n".join(skill_lines(getattr(req, "skills", None)) + loaded_skill_lines(getattr(req, "loaded_skills", None)))
    files = "\n".join(workspace_lines(req.workspace, ctx.request_text)) if req.workspace is not None else ""
    code = "\n".join(code_lines(req.agent, getattr(req, "code_auto_run", "") or "") or [])
    memory = wrap_untrusted("memoria del agente", [{"scope": m.scope, "key": m.key, "value": m.value}
                                                   for m in ctx.memory]) if ctx.memory else ""
    dep_texts = fit_texts([_as_text(d.output) for d in ctx.dependency_outputs], dep_budget_tokens())
    deps = "\n".join(wrap_untrusted(_dep_label(d), t) for d, t in zip(ctx.dependency_outputs, dep_texts))
    pc = ctx.project_context
    project = ""
    if pc is not None:
        parts: list[Any] = []
        if pc.index:
            parts.append(wrap_untrusted("indice del proyecto", [{"ref": e.ref, "title": e.title, "summary": e.summary}
                                                                 for e in pc.index]))
        parts += [a.text for a in pc.artifacts]
        project = "\n".join(str(p) for p in parts)
    seg = [
        ("profile", "Perfil y reglas del agente", tokens_of(system)),
        ("skills", "Skills", tokens_of(skills)),
        ("memory", "Memoria", tokens_of(memory)),
        ("files", "Archivos y carpetas", tokens_of(files) + tokens_of(code)),
        ("dependencies", "Dependencias", tokens_of(deps)),
        ("project", "Contexto del proyecto", tokens_of(project)),
    ]
    used = sum(t for _, _, t in seg[1:])
    rest = max(tokens_of(task) - used, 0)
    seg.append(("task", "Tarea y reglas", rest))
    segments = [ContextSegment(key=k, label=lb, tokens=t) for k, lb, t in seg]
    return ContextSizesResponse(segments=segments, total_tokens=tokens_of(system) + tokens_of(task))
