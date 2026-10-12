"""Office formats: which file a request asks for ("excel" -> xlsx, "word" -> docx, "pdf" -> pdf).

The backend writes the real file from a structured document (fs.write_document); this module only decides the
format from the user's words, so the prompt and the simulation agree with the backend (officedoc.FormatFromText).
"""
from __future__ import annotations

import re
import unicodedata

_PATTERNS: list[tuple[str, re.Pattern[str]]] = [
    ("xlsx", re.compile(r"\b(excel|xlsx?|spreadsheet|hojas? de calculo|planilla|google sheets?|worksheet)\b", re.I)),
    ("pdf", re.compile(r"\bpdf\b", re.I)),
    ("docx", re.compile(r"\b(word|docx?|documento (de|en) word|word document)\b", re.I)),
    # "a document" counts only when the person asks for one to be made
    ("docx", re.compile(r"\b(hazme|haz|genera|generame|crea|prepara|redacta|dame|necesito|quiero|elabora|make|create|"
                        r"generate|write|prepare|draft)\b[^.\n]{0,40}\b(documento|document)\b", re.I)),
]

_MARKER = re.compile(r"\[\[format:(xlsx|docx|pdf)\]\]", re.I)

EXT = {"xlsx": ".xlsx", "docx": ".docx", "pdf": ".pdf"}


def _fold(text: str) -> str:
    return "".join(c for c in unicodedata.normalize("NFD", text) if unicodedata.category(c) != "Mn")


def requested_format(text: str | None) -> str:
    """"xlsx" | "docx" | "pdf" or "" when the text names none. The first one mentioned wins."""
    mk = _MARKER.search(text or "")  # [[format:xlsx]] from a slash command wins over any word in the text
    if mk:
        return mk.group(1).lower()
    t = _fold(text or "")
    best, at = "", -1
    for fmt, rx in _PATTERNS:
        m = rx.search(t)
        if m and (at < 0 or m.start() < at):
            best, at = fmt, m.start()
    return best


def format_hint(text: str | None, desk: str) -> str:
    """One prompt line when the request names an office format ("" otherwise)."""
    fmt = requested_format(text)
    if not fmt:
        return ""
    return (f"FORMATO PEDIDO: la solicitud pide un archivo {fmt.upper()}. Entregalo como un archivo {EXT[fmt]} real con "
            f"fs.write_document (path bajo {desk or 'tu escritorio'}), NO como .md ni .csv; menciona su ruta en summary.")
