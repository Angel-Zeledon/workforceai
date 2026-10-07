"""Regressions found while wiring the chat UI to the real backend."""
import pytest

from app import routing as r


def _norm(text: str) -> str:
    return r.norm(text)


@pytest.mark.parametrize("text, role", [
    ("finanzas", "accounting"),
    ("Hablemos de finanzas", "accounting"),
    ("Véndele un paquete", "sales"),
    ("necesito vender más", "sales"),
])
def test_topic_stems_cover_inflections(text, role):
    n = _norm(text)
    assert r._TOPIC_PATTERNS[role].search(n), f"{role} topic not detected for {text!r}"


@pytest.mark.parametrize("text", [
    "Hola equipo, buenos días",
    "hola equipo, buenas tardes",
    "Good morning team",
    "hey, buenas noches a todos",
])
def test_greeting_with_trailing_greeting_words_is_smalltalk(text):
    assert r.classify_intent(text) == "smalltalk"


def test_real_content_after_greeting_is_not_smalltalk():
    assert r.classify_intent("Hola, necesito ver el balance") != "smalltalk"
