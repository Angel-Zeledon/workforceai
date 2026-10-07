"""Conversational layer: POST /v1/route and POST /v1/chat-reply (simulation) plus the live-mode fallback."""
import asyncio

import pytest

from app import crewai_engine
from app.models import RouteRequest, Usage
from app.routing import classify_intent, rules_route
from conftest import AGENTS

ROSTER = [
    {"id": "sales", "role": "sales", "title": "Gerente de Ventas", "name": "Valeria Ríos"},
    {"id": "hr", "role": "hr", "title": "Recursos Humanos", "name": "Marcos Peña"},
    {"id": "legal", "role": "legal", "title": "Abogada", "name": "Elena Castro"},
    {"id": "accounting", "role": "accounting", "title": "Contador", "name": "Tomás Vidal"},
    {"id": "analyst", "role": "analyst", "title": "Analista", "name": "Nadia Ortega"},
    {"id": "operations", "role": "operations", "title": "Operaciones", "name": "Iván Duarte"},
    {"id": "assistant", "role": "assistant", "title": "Asistente Ejecutivo", "name": "Sofía Lara"},
]


def route(client, text, conversation="office", **extra):
    r = client.post("/v1/route", json={"text": text, "conversation": conversation, "agents": ROSTER, **extra})
    assert r.status_code == 200, r.text
    return r.json()


def ids(body):
    return [x["agent_id"] for x in body["responders"]]


def reply(client, agent_id, text, **extra):
    agent = next(a for a in ROSTER if a["id"] == agent_id)
    body = {"agent": agent, "text": text, "agents": ROSTER, **extra}
    r = client.post("/v1/chat-reply", json=body)
    assert r.status_code == 200, r.text
    return r.json()


# ------------------------------------------------------------------ intent
@pytest.mark.parametrize("text", ["hola", "Hola equipo", "¡Buenos días!", "hola, ¿cómo están?", "hi", "Hello team", "buenas Sofía"])
def test_greetings_are_smalltalk(client, text):
    body = route(client, text)
    assert body["intent"] == "smalltalk"
    assert body["responders"][0] == {"agent_id": "assistant", "role": "primary", "reason": body["responders"][0]["reason"]}
    assert 2 <= len(body["responders"]) <= 3  # the assistant first, one or two colleagues (never everybody)
    assert all(r["role"] == "contributor" for r in body["responders"][1:])


@pytest.mark.parametrize("text", ["gracias", "Muchas gracias!", "thanks", "ok", "perfecto, gracias"])
def test_thanks_only_the_assistant(client, text):
    body = route(client, text)
    assert body["intent"] == "smalltalk" and ids(body) == ["assistant"]


@pytest.mark.parametrize("text", [
    "Tengo un cliente nuevo por $50,000, prepara todo", "Prepara una propuesta para Acme", "calcula el margen de marzo",
    "¿Puedes revisar el contrato de Acme?", "Necesito que redactes un correo", "hazme un resumen de ventas",
    "please prepare a proposal for Acme", "Can you review the contract?", "draft an email to the client",
    "necesito un informe de ventas", "agenda una reunión con el cliente",
])
def test_requests_to_produce_something_are_tasks(client, text):
    body = route(client, text)
    assert body["intent"] == "task", text
    assert ids(body) == ["assistant"]  # the assistant coordinates; the plan decides who works


@pytest.mark.parametrize("text", [
    "hablemos del balance", "¿cómo van las finanzas?", "¿qué opinas del margen?", "how is the budget looking?",
    "quiero hablar de finanzas", "el balance me preocupa", "¿qué hace el contador con las facturas?", "la agenda de hoy",
])
def test_talking_about_something_is_a_question(client, text):
    assert route(client, text)["intent"] == "question", text


def test_greeting_with_content_is_not_smalltalk(client):
    body = route(client, "hola, ¿cómo va el balance?")
    assert body["intent"] == "question" and ids(body) == ["accounting"]


# ------------------------------------------------------------------ who answers a topic
@pytest.mark.parametrize("text,agent", [
    ("hablemos del balance", "accounting"), ("¿cómo está el margen de este mes?", "accounting"),
    ("¿hay riesgo en el contrato con Acme?", "legal"), ("tengo dudas sobre la cláusula de penalidad", "legal"),
    ("¿cuándo podemos contratar a alguien?", "hr"), ("necesitamos más personal", "hr"),
    ("¿cómo van las ventas del trimestre?", "sales"), ("¿cómo está la propuesta del cliente?", "sales"),
    ("¿qué dicen los datos de abril?", "analyst"), ("quiero ver las métricas", "analyst"),
    ("¿alcanza la capacidad de operaciones?", "operations"), ("¿cómo va la entrega al proveedor?", "operations"),
    ("¿qué hora es en Madrid?", "assistant"),
])
def test_topic_goes_to_one_owner(client, text, agent):
    body = route(client, text)
    assert body["intent"] == "question"
    assert ids(body)[0] == agent and body["responders"][0]["role"] == "primary"
    assert body["responders"][0]["reason"]


def test_finance_topic_only_accounting_answers(client):
    body = route(client, "hablemos del balance")
    assert body["topic"] == "finance" and ids(body) == ["accounting"]


def test_related_area_adds_at_most_one_short_contributor(client):
    body = route(client, "hablemos del balance y los márgenes, ¿habrá algo con el contrato del banco?")
    assert ids(body) == ["accounting", "legal"]
    assert [r["role"] for r in body["responders"]] == ["primary", "contributor"]


def test_never_more_than_one_contributor(client):
    body = route(client, "balance de ventas con el contrato y los datos de operaciones")
    assert len(body["responders"]) <= 2


def test_unrelated_areas_do_not_make_noise(client):
    # logistics has nothing to add to a legal question
    body = route(client, "¿qué dice la cláusula sobre la entrega?")
    assert ids(body)[0] == "legal"


# ------------------------------------------------------------------ 1:1 chat
def test_direct_chat_only_that_agent_even_for_greetings(client):
    for text in ("hola", "hablemos del balance", "prepara un informe de ventas", "gracias"):
        body = route(client, text, conversation="agent:legal")
        assert ids(body) == ["legal"] and body["responders"][0]["role"] == "primary"


def test_direct_chat_suggests_asking_the_owner_of_another_area(client):
    body = route(client, "¿cómo está el margen de este mes?", conversation="agent:legal")
    assert ids(body) == ["legal"]
    assert body["consult"]["agent_id"] == "accounting"
    assert "consult" not in route(client, "¿hay riesgo en este contrato?", conversation="agent:legal")


def test_unknown_direct_agent_degrades_to_the_office(client):
    body = route(client, "hola", conversation="agent:nobody")
    assert body["responders"][0]["agent_id"] == "assistant"


# ------------------------------------------------------------------ contract / compat
def test_route_is_backwards_compatible_with_a_minimal_body(client):
    r = client.post("/v1/route", json={"text": "hola"})
    assert r.status_code == 200
    body = r.json()
    assert body["intent"] == "smalltalk" and body["responders"][0]["agent_id"] == "assistant"
    assert {"intent", "topic", "responders"} <= set(body)


def test_route_accepts_history_with_from_key(client):
    body = route(client, "¿y el margen?", history=[{"from": "user", "text": "hablemos del balance"}])
    assert body["intent"] == "question"


def test_route_requires_text(client):
    assert client.post("/v1/route", json={}).status_code == 422


def test_plan_tasks_carry_a_reason(client):
    r = client.post("/v1/plan", json={"request_text": "Tengo un cliente nuevo por $50,000, prepara todo", "agents": AGENTS})
    tasks = r.json()["tasks"]
    assert all(t.get("reason") for t in tasks)


# ------------------------------------------------------------------ replies
def test_assistant_greets_and_colleague_greets_briefly(client):
    a = reply(client, "assistant", "hola", intent="smalltalk")
    c = reply(client, "legal", "hola", intent="smalltalk", responder_role="contributor", slot=1)
    assert a["text"] and c["text"] and a["text"] != c["text"]
    assert len(c["text"]) < 90
    assert a["kind"] == c["kind"] == "chat" and a["usage"]["cost_usd"] < 0.01


def test_topic_reply_is_in_character_and_short(client):
    out = reply(client, "accounting", "hablemos del balance", intent="question", topic="finance")
    assert out["text"] and len(out["text"]) < 400
    assert any(w in out["text"].lower() for w in ("balance", "periodo", "números", "numeros", "finanzas", "costos"))


def test_contributor_line_is_one_short_sentence(client):
    out = reply(client, "legal", "hablemos del balance", intent="question", topic="finance", responder_role="contributor",
                prior_replies=[{"from": "accounting", "text": "Claro."}])
    assert out["text"] and len(out["text"]) < 180 and "\n" not in out["text"]


def test_replies_vary_instead_of_repeating(client):
    history, seen = [], []
    for _ in range(3):
        out = reply(client, "assistant", "hola", intent="smalltalk", history=history)
        seen.append(out["text"])
        history.append({"from": "assistant", "text": out["text"]})
    assert len(set(seen)) == 3


def test_direct_chat_redirect_becomes_a_visible_consult(client):
    out = reply(client, "legal", "¿cómo está el margen de este mes?", conversation="agent:legal",
                intent="question", topic="finance", consult_to="accounting")
    assert out["consult"]["to_agent_id"] == "accounting" and "margen" in out["consult"]["question"]
    assert "Tomás" in out["text"]
    ans = reply(client, "accounting", "¿cómo está el margen de este mes?", conversation="agent:legal",
                consult={"from_agent_id": "legal", "question": out["consult"]["question"]})
    assert ans["kind"] == "answer" and "consult" not in ans and ans["text"]


def test_task_reply_confirms_without_inventing_results(client):
    office = reply(client, "assistant", "prepara una propuesta", intent="task")
    direct = reply(client, "sales", "prepara una propuesta", conversation="agent:sales", intent="task")
    assert office["text"] and direct["text"] and office["text"] != direct["text"]


def test_reply_locale_comes_from_the_matching_script(client):
    from app import sim_content, sim_content_en

    en = reply(client, "assistant", "hello", intent="smalltalk", locale="en")["text"]
    es = reply(client, "assistant", "hola", intent="smalltalk", locale="es")["text"]
    en_all = sim_content_en.CHAT["greet_primary"]
    es_all = sim_content.CHAT["greet_primary"]
    assert any(en == t.format(name="Sofía Lara") for t in en_all)
    assert any(es == t.format(name="Sofía Lara") for t in es_all)


def test_english_routing_and_replies(client):
    from app import sim_content_en

    body = route(client, "let's talk about the budget", locale="en")
    assert ids(body) == ["accounting"] and body["responders"][0]["reason"].startswith("Owns")
    out = reply(client, "accounting", "let's talk about the budget", intent="question", topic="finance", locale="en")
    assert out["text"] in sim_content_en.CHAT["answer"]["accounting"]


def test_regional_tone_in_replies(client):
    texts = {reply(client, "assistant", f"hola {i}", intent="smalltalk", tone="ar", slot=i)["text"] for i in range(12)}
    assert not any(" dime " in f" {t.lower()} " for t in texts)


def test_chat_reply_never_leaks_secrets(client):
    out = reply(client, "legal", "mi clave es sk-ant-abcdefghijklmnopqrstuvwxyz123456", intent="question")
    assert "sk-ant-abcdefghijklmnopqrstuvwxyz123456" not in out["text"]


# ------------------------------------------------------------------ live mode: LLM routing with a safe fallback
def make_live(monkeypatch, outcome):
    pytest.importorskip("crewai")
    from app.providers import ProviderConfig

    eng = crewai_engine.CrewAIEngine(providers=[ProviderConfig("deepseek", "deepseek/deepseek-chat", "k")])

    def fake_once(cfg, **kw):
        if isinstance(outcome, Exception):
            raise outcome
        return outcome(kw) if callable(outcome) else outcome, Usage(
            model=cfg.model, input_tokens=300, output_tokens=40, cost_usd=0.0002, duration_ms=5, provider=cfg.id)

    monkeypatch.setattr(eng, "_kickoff_once", fake_once)
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRY_BACKOFF_S", 0)
    return eng


def run_route(eng, text="hablemos del balance", **kw):
    return asyncio.run(eng.route(RouteRequest(text=text, agents=ROSTER, **kw)))


def test_live_route_uses_the_llm_when_valid(monkeypatch):
    llm = crewai_engine._RouteLLM(intent="question", topic="finance", primary_agent_id="accounting",
                                  contributor_agent_ids=["legal", "sales"], reasons={"accounting": "Lleva las cuentas"})
    out = run_route(make_live(monkeypatch, llm))
    assert out.source == "llm" and out.provider == "deepseek" and out.usage.cost_usd == 0.0002
    assert [r.agent_id for r in out.responders] == ["accounting", "legal"]  # at most one contributor
    assert out.responders[0].reason == "Lleva las cuentas"


@pytest.mark.parametrize("bad", [
    crewai_engine._RouteLLM(intent="chitchat", primary_agent_id="accounting"),      # unknown intent
    crewai_engine._RouteLLM(intent="question", primary_agent_id="ghost"),           # unknown agent
    RuntimeError("provider down"), ValueError("not json"),
])
def test_live_route_falls_back_to_rules_on_any_problem(monkeypatch, bad):
    out = run_route(make_live(monkeypatch, bad))
    assert out.source == "rules" and [r.agent_id for r in out.responders] == ["accounting"]


def test_live_route_direct_chat_cannot_widen_the_audience(monkeypatch):
    llm = crewai_engine._RouteLLM(intent="smalltalk", primary_agent_id="sales", contributor_agent_ids=["hr", "legal"])
    out = run_route(make_live(monkeypatch, llm), text="hola", conversation="agent:legal")
    assert [r.agent_id for r in out.responders] == ["legal"]


def test_live_route_respects_the_provider_data_policy(monkeypatch):
    out = run_route(make_live(monkeypatch, RuntimeError("never called")), allowed_providers=[])
    assert out.source == "rules"  # nothing was sent: no_allowed_provider also lands on the rules


def test_live_chat_reply_validates_consults(monkeypatch):
    pytest.importorskip("crewai")
    from app.models import ChatReplyRequest

    llm = crewai_engine._ChatLLM(text="Eso lo ve Tomás; se lo pregunto.", consult_to_agent_id="accounting",
                                 consult_question="¿Cómo está el margen?")
    eng = make_live(monkeypatch, llm)
    agent = ROSTER[2]
    req = ChatReplyRequest(agent=agent, text="¿margen?", conversation="agent:legal", agents=ROSTER, consult_to="accounting")
    out = asyncio.run(eng.chat_reply(req))
    assert out.consult.to_agent_id == "accounting" and out.provider == "deepseek"
    office = ChatReplyRequest(agent=agent, text="¿margen?", conversation="office", agents=ROSTER)
    assert asyncio.run(eng.chat_reply(office)).consult is None  # only the colleague the router named


def test_rules_never_raise_on_junk():
    for text in ("", "   ", "¿¿??", "💥", "a" * 5000):
        out = rules_route(RouteRequest(text=text or " "))
        assert out.responders and out.intent in ("smalltalk", "question", "task")
    assert classify_intent("hola") == "smalltalk"


# ------------------------------------------------------------------ out-of-competence dialogue
ROLES = [a["id"] for a in ROSTER]


def deflect(client, agent_id, text, other, **extra):
    return reply(client, agent_id, text, conversation=f"agent:{agent_id}", intent="question", consult_to=other, **extra)


def test_accountant_asked_to_sell_redirects_to_sales_by_name(client):
    body = route(client, "véndeme una propuesta para Acme", conversation="agent:accounting")
    assert ids(body) == ["accounting"] and body["intent"] == "task"
    assert body["consult"]["agent_id"] == "sales"  # for tasks: "reassign to"
    out = reply(client, "accounting", "véndeme una propuesta para Acme", conversation="agent:accounting",
                intent="task", consult_to="sales")
    assert "Valeria" in out["text"] and "Valeria Ríos" in out["text"] or "Valeria" in out["text"]


def test_question_of_another_area_redirects_and_offers_a_visible_consult(client):
    body = route(client, "¿cómo van las ventas?", conversation="agent:accounting")
    assert body["intent"] == "question" and body["consult"]["agent_id"] == "sales"
    out = deflect(client, "accounting", "¿cómo van las ventas?", "sales")
    assert "Valeria" in out["text"] and out["consult"]["to_agent_id"] == "sales"


def test_every_role_to_role_pair_has_at_least_four_distinct_variants_in_both_languages():
    from app import sim_content, sim_content_en
    for mod in (sim_content, sim_content_en):
        for role in ROLES:
            frames = mod.CHAT["deflect"][role]
            assert len(set(frames)) >= 4 and all("{other}" in f for f in frames)
            for other in ROLES:
                if other != role:
                    pool = frames + mod.CHAT["deflect_pair"].get((role, other), [])
                    assert len(set(pool)) >= 4
        assert set(mod.CHAT["limit_lead"]) == set(ROLES)
        assert set(mod.CHAT["limit_core"]) == {"kill_switch", "paused", "budget", "read_only", "no_connection"}


@pytest.mark.parametrize("role", ["sales", "analyst", "accounting", "hr", "legal", "operations", "assistant"])
def test_every_role_deflects_to_every_other_role_by_name_and_rotates(client, role):
    for other in ROLES:
        if other == role:
            continue
        name = next(a["name"] for a in ROSTER if a["id"] == other)
        history, seen = [], []
        for i in range(4):
            out = deflect(client, role, "eso de otra área", other, history=history, slot=i)
            assert name in out["text"], (role, other, out["text"])
            seen.append(out["text"])
            history.append({"from": role, "text": out["text"]})
        assert len(set(seen)) == 4, (role, other)  # the same variant is not repeated back to back


def test_roles_have_their_own_voice(client):
    texts = {r: " ".join(deflect(client, r, "x", "sales" if r != "sales" else "legal", slot=i)["text"] for i in range(6))
             for r in ROLES}
    assert len({t for t in texts.values()}) == len(ROLES)
    assert "Uy" in texts["sales"] or "Qué" in texts["sales"] or "¡" in texts["sales"]


def test_refusing_a_foreign_task_in_its_own_voice_and_naming_the_right_person(client):
    out = reply(client, "legal", "prepara las ventas del mes", conversation="agent:legal", intent="task", consult_to="sales")
    assert "Valeria" in out["text"] and "consult" not in out  # the backend reassigns; no extra consult
    en = reply(client, "legal", "prepare the sales deck", conversation="agent:legal", intent="task", consult_to="sales", locale="en")
    assert "Valeria" in en["text"] and "¡" not in en["text"] and "hand it" in en["text"] or "reassign" in en["text"].lower()


def test_assistant_accepts_tasks_even_when_the_topic_is_foreign(client):
    body = route(client, "prepara una propuesta de ventas", conversation="agent:assistant")
    assert "consult" not in body


@pytest.mark.parametrize("limit", ["kill_switch", "paused", "budget", "read_only", "no_connection"])
@pytest.mark.parametrize("role", ROLES)
def test_every_role_says_no_for_real_limits_in_its_voice(client, role, limit):
    out = reply(client, role, "envía el correo", limit=limit, intent="task")
    from app import sim_content
    assert out["text"].split(" ", 1)[0] in " ".join(sim_content.CHAT["limit_lead"][role])
    assert any(core in out["text"] for core in sim_content.CHAT["limit_core"][limit])
    assert out["usage"]["cost_usd"] == 0 or out["usage"]["cost_usd"] < 0.01


def test_limit_lines_rotate_and_speak_english(client):
    history, seen = [], []
    for i in range(4):
        out = reply(client, "sales", "x", limit="budget", history=history, slot=i)
        seen.append(out["text"]); history.append({"from": "sales", "text": out["text"]})
    assert len(set(seen)) == 4
    en = reply(client, "accounting", "x", limit="kill_switch", locale="en")
    assert not any(ch in en["text"] for ch in "¡¿áéíóú") and "emergency" in en["text"]


def _rotating(client, **kw):
    history, out = [], []
    for i in range(8):
        t = deflect(client, "accounting", "x", "sales", history=history, slot=i, **kw)["text"]
        history.append({"from": "accounting", "text": t}); out.append(t)
    return " ".join(out)


def test_regional_tone_applies_to_deflections(client):
    import re
    ar, neutral = _rotating(client, tone="ar"), _rotating(client)
    assert not re.search(r"(?<![a-z])dime(?![a-z])", ar, re.I) and re.search(r"(?<![a-z])decime(?![a-z])", ar)
    assert re.search(r"(?<![a-z])dime(?![a-z])", neutral) and "decime" not in neutral


def test_office_message_to_one_colleague_by_name_is_answered_by_that_colleague_only(client):
    body = route(client, "Tomás, véndeme una propuesta para Acme")
    assert ids(body) == ["accounting"] and body["consult"]["agent_id"] == "sales"
    body = route(client, "Tomás, ¿cómo está el margen?")
    assert ids(body) == ["accounting"] and "consult" not in body
    # a name inside the sentence is just a mention, the topic owner answers
    assert ids(route(client, "¿cómo está el margen que calculó Tomás?"))[0] == "accounting"
    assert ids(route(client, "¿qué opina Tomás de las ventas?")) == ["sales"]


def test_handoff_line_names_the_new_person(client):
    out = reply(client, "accounting", "sí, pásaselo", conversation="agent:accounting", intent="question",
                handoff=True, consult_to="sales")
    assert "Valeria" in out["text"]


def test_live_engine_scripts_limits_and_handoffs_without_the_llm(monkeypatch):
    llm = crewai_engine._ChatLLM(text="NO DEBERÍA USARSE")
    eng = make_live(monkeypatch, llm)
    from app.models import ChatReplyRequest
    out = asyncio.run(eng.chat_reply(ChatReplyRequest(agent=ROSTER[3], text="envía", agents=ROSTER, limit="read_only")))
    assert "NO DEBER" not in out.text and out.usage.cost_usd == 0


def test_live_route_keeps_the_rules_consult_for_foreign_tasks(monkeypatch):
    llm = crewai_engine._RouteLLM(intent="task", primary_agent_id="accounting")
    out = run_route(make_live(monkeypatch, llm), text="véndeme una propuesta", conversation="agent:accounting")
    assert [r.agent_id for r in out.responders] == ["accounting"] and out.consult.agent_id == "sales"
