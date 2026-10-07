"""Behaviour regressions of the chat agents found by the end-to-end sweep (tests/behavior_sweep.py).

In-process: the scenario tables of behavior_cases.py run through the rules router; the HTTP-level contract
(null lists, English in a Spanish workspace, default-scenario plans) goes through the test client.
"""
import re
import string

import pytest

import behavior_cases as bc
from app import routing as r
from app import sim_content as sc
from app import sim_content_en as sce
from app.models import ChatReplyRequest, HistoryItem, RouteAgent, RouteRequest
from test_chat import ROSTER, reply, route

AGENTS = [RouteAgent(**a) for a in ROSTER]


def rules(text, conversation="office", history=None, locale="es"):
    return r.rules_route(RouteRequest(text=text, conversation=conversation, agents=AGENTS, history=history or [], locale=locale))


def ids(resp):
    return [x.agent_id for x in resp.responders]


# ------------------------------------------------------------------ the scenario tables
@pytest.mark.parametrize("text", bc.GREETINGS)
def test_greetings_assistant_first_at_most_three_voices(text):
    x = rules(text)
    assert x.intent == "smalltalk" and ids(x)[0] == "assistant" and len(x.responders) <= 3


@pytest.mark.parametrize("text", bc.THANKS + bc.HELP)
def test_thanks_and_help_only_the_assistant(text):
    x = rules(text)
    assert x.intent == "smalltalk" and ids(x) == ["assistant"]


@pytest.mark.parametrize("text, role", bc.TOPIC_QUESTIONS)
def test_topic_questions_go_to_the_owner_with_at_most_one_contributor(text, role):
    x = rules(text)
    assert x.intent == "question" and ids(x)[0] == role and len(x.responders) <= 2


@pytest.mark.parametrize("text", bc.AMBIGUOUS)
def test_ambiguous_questions_stay_with_the_assistant(text):
    assert ids(rules(text)) == ["assistant"]


@pytest.mark.parametrize("text, role", bc.TASKS)
def test_office_tasks_have_one_coordinator_and_the_right_topic(text, role):
    x = rules(text)
    assert x.intent == "task" and len(x.responders) == 1 and x.topic == r.ROLE_TOPIC[role]


@pytest.mark.parametrize("text, role", bc.NOT_TASKS)
def test_discussion_is_not_a_task(text, role):
    x = rules(text)
    assert x.intent == "question" and ids(x)[0] == role


@pytest.mark.parametrize("text, role", bc.NAMED)
def test_naming_a_colleague_gives_the_turn_to_that_colleague(text, role):
    assert ids(rules(text)) == [role]


@pytest.mark.parametrize("agent, text", bc.DIRECT_OWN)
def test_direct_chat_own_role_has_no_redirect(agent, text):
    x = rules(text, f"agent:{agent}")
    assert ids(x) == [agent] and x.consult is None


@pytest.mark.parametrize("agent, text, owner", bc.DIRECT_FOREIGN)
def test_direct_chat_foreign_topic_redirects_to_the_owner(agent, text, owner):
    x = rules(text, f"agent:{agent}")
    assert ids(x) == [agent] and x.consult is not None and x.consult.agent_id == owner and x.intent == "question"


@pytest.mark.parametrize("agent, text, owner", bc.DIRECT_FOREIGN_TASKS)
def test_direct_chat_foreign_task_is_reassigned_to_the_owner(agent, text, owner):
    x = rules(text, f"agent:{agent}")
    assert x.intent == "task" and x.consult is not None and x.consult.agent_id == owner


@pytest.mark.parametrize("text", bc.EDGE_EMOJI)
def test_reactions_and_punctuation_always_get_an_answer(text):
    assert len(rules(text).responders) >= 1


@pytest.mark.parametrize("text, intent, primary", bc.HARD_ROUTING)
def test_hard_phrasings(text, intent, primary):
    x = rules(text)
    assert x.intent == intent and ids(x)[0] == primary, (x.intent, ids(x))


# ------------------------------------------------------------------ specific behaviours
def test_typos_are_corrected_only_for_topic_words():
    assert "balance" in r.fix_typos("balanse del mes")
    assert "contrato" in r.fix_typos("el contrado esta firmado")
    # real words and task verbs are left alone ("contrata" must not turn into the legal topic "contrato")
    assert r.fix_typos("contrata a un desarrollador") == "contrata a un desarrollador"
    assert r.fix_typos("imagen del producto") == "imagen del producto"


def test_follow_up_without_topic_stays_with_the_last_answerer():
    hist = [HistoryItem(**{"from": "user", "text": "¿cómo van las ventas?"}), HistoryItem(**{"from": "sales", "text": "Bien."}),
            HistoryItem(**{"from": "accounting", "text": "Ojo con el margen."})]
    assert ids(rules("¿y por qué?", history=hist)) == ["sales"]  # the first one who answered, not the contributor
    assert ids(rules("¿y eso cómo se explica?", history=hist)) == ["sales"]
    # a message with its own topic is routed by that topic
    assert ids(rules("¿y los costos?", history=hist))[0] == "accounting"
    # no history: nothing to follow
    assert ids(rules("¿y por qué?")) == ["assistant"]


def test_thanks_after_an_answer_goes_to_who_answered():
    hist = [HistoryItem(**{"from": "user", "text": "¿cómo va el balance?"}), HistoryItem(**{"from": "accounting", "text": "Bien."})]
    assert ids(rules("gracias", history=hist)) == ["accounting"]
    greeting = [HistoryItem(**{"from": "user", "text": "hola"}), HistoryItem(**{"from": "assistant", "text": "Hola"})]
    assert ids(rules("gracias", history=greeting)) == ["assistant"]


def test_follow_up_never_overrides_a_one_to_one_chat():
    hist = [HistoryItem(**{"from": "user", "text": "ventas?"}), HistoryItem(**{"from": "sales", "text": "Bien."})]
    assert ids(rules("¿y por qué?", "agent:legal", hist)) == ["legal"]


def test_question_mark_turns_a_need_into_a_question():
    assert rules("¿necesitamos un NDA?").intent == "question"
    assert rules("necesitamos un NDA para el cliente").intent == "task"
    assert rules("¿puedes preparar el presupuesto?").intent == "task"


def test_reactions_are_acknowledged_not_thanked(client):
    for text in ("👍", "jajaja", "ok", "no gracias"):
        body = route(client, text)
        assert body["intent"] == "smalltalk" and body["topic"] == "ack", text
    assert route(client, "gracias")["topic"] == "thanks"
    assert route(client, "👋")["topic"] == "greeting"


def test_ack_and_thanks_lines_are_not_gendered_and_rotate(client):
    hist: list[dict] = []
    said = []
    for _ in range(5):
        t = reply(client, "accounting", "gracias", intent="smalltalk", history=hist)["text"]
        assert not re.search(r"\batent[ao]\b", t), t
        said.append(t)
        hist += [{"from": "user", "text": "gracias"}, {"from": "accounting", "text": t}]
    assert len(set(said)) == 5, said  # never the same line twice in a row while fresh ones remain


# ------------------------------------------------------------------ language of the answer
@pytest.mark.parametrize("text, expected", [
    ("hello team", "en"), ("how are sales going?", "en"), ("what is our cash flow?", "en"), ("thanks", "en"),
    ("hola equipo", "es"), ("¿cómo va el balance?", "es"), ("balance", "es"), ("ok", "es"), ("Tomás, hola", "es"),
])
def test_detect_locale_from_a_spanish_workspace(text, expected):
    assert r.detect_locale(text, "es") == expected


def test_a_spanish_message_in_an_english_workspace_is_answered_in_spanish():
    assert r.detect_locale("hola, ¿cómo estás?", "en") == "es"
    assert r.detect_locale("hello", "en") == "en"


def test_english_message_in_spanish_workspace_gets_english_lines(client):
    for agent, text in (("assistant", "hello"), ("sales", "thanks"), ("legal", "how are sales going?"), ("accounting", "what is our cash flow?")):
        out = reply(client, agent, text, locale="es", intent="question" if "?" in text else "smalltalk")["text"]
        clean = out
        for n in [a["name"] for a in ROSTER]:
            clean = clean.replace(n, "")
        assert not re.search(r"[áéíóúñ¿¡]|\b(hola|gracias|dime|puedes|eso)\b", clean, re.I), out


def test_spanish_workspace_keeps_spanish_for_spanish_text(client):
    out = reply(client, "assistant", "hola", locale="es", intent="smalltalk")["text"]
    assert not re.search(r"\b(hello|hi|how can i)\b", out, re.I)


# ------------------------------------------------------------------ the contract with the Go backend
def test_go_nil_slices_arrive_as_null_and_mean_empty(client):
    r1 = client.post("/v1/route", json={"text": "hola", "conversation": "office", "agents": ROSTER, "history": None})
    assert r1.status_code == 200
    agent = ROSTER[3]
    base = {"agent": agent, "text": "hola", "agents": ROSTER, "limit": "kill_switch", "history": None, "prior_replies": None}
    r2 = client.post("/v1/chat-reply", json=base)
    assert r2.status_code == 200, r2.text  # this was a 422 for every "I can't" line (the backend omitted prior_replies)
    assert r2.json()["text"]
    r3 = client.post("/v1/chat-reply", json={"agent": agent, "text": "hola", "agents": None})
    assert r3.status_code == 200


def test_model_accepts_null_lists():
    m = ChatReplyRequest.model_validate({"agent": ROSTER[0], "text": "x", "history": None, "prior_replies": None, "agents": None})
    assert m.history == [] and m.prior_replies == [] and m.agents == []


# ------------------------------------------------------------------ simulated plans follow the topic
@pytest.mark.parametrize("text, role", [("prepara el balance del mes", "accounting"), ("calcula el margen de marzo", "accounting"),
                                        ("necesito saber cómo va la nómina, prepara un resumen", "hr")])
def test_default_scenario_plan_gives_the_work_to_the_topic_owner(client, text, role):
    from conftest import AGENTS as PLAN_AGENTS  # the roster the planner endpoint is tested with
    resp = client.post("/v1/plan", json={"request_text": text, "agents": PLAN_AGENTS, "locale": "es"})
    assert resp.status_code == 200, resp.text
    owners = [t["agent_id"] for t in resp.json()["tasks"]]
    assert role in owners and "assistant" in owners
    assert all(t.get("reason") for t in resp.json()["tasks"])


def test_default_scenario_without_a_topic_keeps_the_generic_plan(client):
    from conftest import AGENTS as PLAN_AGENTS
    resp = client.post("/v1/plan", json={"request_text": "haz algo útil", "agents": PLAN_AGENTS, "locale": "es"})
    assert [t["agent_id"] for t in resp.json()["tasks"]] == ["assistant", "analyst"]


# ------------------------------------------------------------------ the scripts themselves
_PLACEHOLDERS = {"name", "title", "area", "other", "other_title", "topic_area", "my_area", "q", "topic"}


def _strings(node):
    if isinstance(node, str):
        yield node
    elif isinstance(node, dict):
        for v in node.values():
            yield from _strings(v)
    elif isinstance(node, (list, tuple)):
        for v in node:
            yield from _strings(v)


@pytest.mark.parametrize("bundle", [sc, sce], ids=["es", "en"])
def test_script_lines_only_use_known_placeholders(bundle):
    for line in _strings(bundle.CHAT):
        fields = {f for _, f, _, _ in string.Formatter().parse(line) if f}
        assert fields <= _PLACEHOLDERS, (line, fields - _PLACEHOLDERS)


def test_both_languages_have_the_same_script_keys():
    def keys(node, prefix=""):
        out = set()
        if isinstance(node, dict):
            for k, v in node.items():
                out |= {f"{prefix}/{k}"} | keys(v, f"{prefix}/{k}")
        return out
    assert keys(sc.CHAT) == keys(sce.CHAT)


# ------------------------------------------------------------------ script quality
def test_assistant_answers_agenda_questions_with_agenda_lines(client):
    out = reply(client, "assistant", "¿qué tengo en la agenda?", intent="question", topic="general")["text"]
    assert re.search(r"agenda|reuni|hoy|semana", out, re.I), out
    en = reply(client, "assistant", "what's on my calendar?", intent="question", topic="general", locale="en")["text"]
    assert re.search(r"calendar|meeting|today|week", en, re.I), en


def test_voseo_covers_the_common_verbs():
    out = r.apply_tone("Si me cuentas qué quieres, te digo. ¿Puedes avisarme? A ti te toca. Las cuentas están al día.", "ar", "es")
    assert "me contás" in out and "querés" in out and "Podés" in out and "A vos" in out
    assert "Las cuentas están" in out  # the noun "cuentas" must not become a verb


def test_consult_question_does_not_repeat_who_the_user_addressed(client):
    out = reply(client, "accounting", "Tomás, ¿cuánto vendimos?", intent="question", topic="sales", consult_to="sales")
    assert out["consult"] and "Tomás," not in out["consult"]["question"] and "cuánto vendimos" in out["consult"]["question"]


def test_limit_lines_are_instant_and_free(client):
    out = reply(client, "legal", "hola", limit="budget")
    assert out["usage"]["cost_usd"] == 0 and out["usage"]["duration_ms"] == 0
