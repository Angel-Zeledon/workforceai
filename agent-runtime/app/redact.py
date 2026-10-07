"""Redaccion de secretos (defensa en profundidad).

El backend Go es el unico que tiene credenciales y ya redacta lo que devuelve
una herramienta antes de enviarlo aqui. Este modulo es la SEGUNDA barrera: lo
que llegue al runtime como contenido externo (y lo que el modelo devuelva) pasa
por el mismo tipo de patrones, de modo que una clave filtrada por error no llegue
a un prompt ni a una respuesta.
"""
from __future__ import annotations

import re
from typing import Any

_PATTERNS: list[tuple[str, re.Pattern[str]]] = [
    ("private_key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)", re.S)),
    ("jwt", re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\b")),
    ("github_token", re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b")),
    ("aws_key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    ("google_token", re.compile(r"\bya29\.[A-Za-z0-9_.-]{20,}|\b1//[A-Za-z0-9_-]{20,}")),
    ("api_key", re.compile(r"\b(?:sk|pk|rk)[-_](?:live|test|proj|ant)?[-_]?[A-Za-z0-9]{20,}\b")),
    ("bearer", re.compile(r"\bbearer\s+[A-Za-z0-9._~+/=-]{16,}", re.I)),
    ("secret_assignment", re.compile(
        r"\b(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|contraseña|clave)"
        r"\s*[:=]\s*[\"']?[^\s\"'<>]{6,}", re.I)),
    ("token_url", re.compile(
        r"https?://[^\s<>\"')]*[?&](?:token|access_token|auth|key|sig|signature|reset|magic|code)=[^\s<>\"')&]+[^\s<>\"')]*", re.I)),
]
_CARD = re.compile(r"\b(?:\d[ -]?){13,19}\b")


def _luhn(digits: str) -> bool:
    total, alt = 0, False
    for ch in reversed(digits):
        n = int(ch)
        if alt:
            n *= 2
            if n > 9:
                n -= 9
        total += n
        alt = not alt
    return total % 10 == 0


def redact_secrets(text: str) -> str:
    """Reemplaza credenciales y tarjetas por [REDACTED:tipo]."""
    for kind, pat in _PATTERNS:
        text = pat.sub(f"[REDACTED:{kind}]", text)

    def card(m: re.Match[str]) -> str:
        digits = re.sub(r"\D", "", m.group(0))
        return "[REDACTED:card]" if 13 <= len(digits) <= 19 and _luhn(digits) else m.group(0)

    return _CARD.sub(card, text)


def redact_any(value: Any) -> Any:
    """Redacta recursivamente strings dentro de dict/list (para respuestas)."""
    if isinstance(value, str):
        return redact_secrets(value)
    if isinstance(value, list):
        return [redact_any(v) for v in value]
    if isinstance(value, dict):
        return {k: redact_any(v) for k, v in value.items()}
    return value
