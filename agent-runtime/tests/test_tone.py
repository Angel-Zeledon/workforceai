import pytest

from app.models import (
    TONES,
    ConsultRequest,
    PlanRequest,
    RunTaskRequest,
    SynthesizeRequest,
    normalize_tone,
)
from app.security import (
    TONE_RULES,
    build_consult_prompt,
    build_synthesis_prompt,
    build_system_prompt,
    build_task_prompt,
    language_rule,
    system_rules,
    tone_rule,
)
from conftest import run_req


def test_tone_defaults_to_neutral_and_is_backwards_compatible():
    assert PlanRequest(request_text="x").tone == "neutral"
    assert RunTaskRequest(**run_req()).tone == "neutral"
    assert ConsultRequest(from_agent_id="a", to_agent_id="b", question="q").tone == "neutral"
    assert SynthesizeRequest(request_text="x").tone == "neutral"


@pytest.mark.parametrize("raw,expected", [
    ("MX", "mx"), (" ar ", "ar"), ("co", "co"), ("CL", "cl"), ("es", "es"),
    ("", "neutral"), (None, "neutral"), ("klingon", "neutral"), ("neutral", "neutral"),
])
def test_normalize_tone(raw, expected):
    assert normalize_tone(raw) == expected
    assert PlanRequest(request_text="x", tone=raw).tone == expected


def test_every_supported_tone_has_a_rule_except_neutral():
    assert set(TONES) - {"neutral"} == set(TONE_RULES)


def test_neutral_prompt_is_identical_to_the_legacy_prompt():
    assert language_rule("es") == language_rule("es", "neutral")
    assert system_rules("es") == system_rules("es", "neutral")
    assert tone_rule("neutral") == "" and tone_rule("unknown") == ""


@pytest.mark.parametrize("tone", [t for t in TONES if t != "neutral"])
def test_regional_tone_reaches_every_prompt(tone):
    marker = TONE_RULES[tone]
    body = run_req("sales")
    body["tone"] = tone
    req = RunTaskRequest(**body)
    assert marker in build_task_prompt(req)
    assert marker in build_system_prompt(req.agent, req.locale, req.tone)
    assert marker in build_consult_prompt("a", "b", "q", None, "es", tone)
    assert marker in build_synthesis_prompt("x", [], "es", tone)
    assert marker in system_rules("es", tone)


def test_tone_never_applies_to_english_output():
    assert tone_rule("mx", "en") == ""
    assert language_rule("en", "ar") == language_rule("en")


def test_tone_cannot_override_fixed_rules():
    prompt = system_rules("es", "ar")
    assert "Nunca ejecutas herramientas" in prompt  # fixed rule 1 still present
    assert "tool_request" in prompt


def test_tone_over_http_is_accepted_and_unknown_tone_degrades(client):
    body = run_req("sales")
    body["tone"] = "mx"
    assert client.post("/v1/run-task", json=body).status_code == 200
    body["tone"] = "pirate"
    assert client.post("/v1/run-task", json=body).status_code == 200
    plan = client.post("/v1/plan", json={"request_text": "x", "tone": "co", "locale": "es"})
    assert plan.status_code == 200
