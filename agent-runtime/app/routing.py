"""Conversational routing: who should answer a chat message, and the scripted replies of simulation.

Pure logic (no network, no LLM). It is the engine of POST /v1/route in simulation and the safe
fallback of live mode (a failed or invalid LLM classification always ends here). Contract and
rules: docs/architecture/chat-routing.md.
"""
from __future__ import annotations

import hashlib
import random
import re
import unicodedata
from typing import Iterable

from . import sim_content as sc
from .models import (
    ChatReplyRequest,
    Responder,
    ReplyConsult,
    RouteAgent,
    RouteConsult,
    RouteRequest,
    RouteResponse,
)

ASSISTANT = "assistant"
ROLES = ("sales", "hr", "legal", "accounting", "analyst", "operations", "assistant")
ROLE_TOPIC = {"accounting": "finance", "legal": "legal", "hr": "hr", "sales": "sales",
              "analyst": "data", "operations": "operations", "assistant": "general"}
TOPIC_ROLE = {v: k for k, v in ROLE_TOPIC.items()}

MAX_GREETERS = 2  # office greeting: the assistant plus at most this many colleagues
MAX_CONTRIBUTORS = 1  # topic questions: at most one extra voice


def norm(text: str) -> str:
    """Lowercase, no accents, single spaces: the form every rule below works on."""
    t = unicodedata.normalize("NFKD", text or "")
    t = "".join(c for c in t if not unicodedata.combining(c))
    return re.sub(r"\s+", " ", t.lower()).strip()


def _tokens(n: str) -> list[str]:
    return re.findall(r"[a-z0-9&$]+", n)


# ------------------------------------------------------------------ topics
# Stems over the normalized text (accents removed), Spanish and English.
_TOPIC_PATTERNS: dict[str, re.Pattern[str]] = {
    "accounting": re.compile(
        r"\b(finanz|financ|balance|margen|margin|costo|cost\b|costs\b|factur|invoice|presupuest|budget|flujo de caja|"
        r"cash ?flow|impuest|tax|contab|accounting|utilidad|profit|gasto|expense|ingreso|revenue|rentab|"
        r"deuda|debt|prestamo|loan|iva\b|p&l|estado de resultados|cuentas por)"),
    "legal": re.compile(
        r"\b(contratos?\b|contract|legal|clausula|clause|demanda|lawsuit|cumplimiento|compliance|abogad|lawyer|"
        r"attorney|ley\b|leyes|law\b|terminos y condiciones|terms and conditions|privacidad|privacy|nda\b|"
        r"propiedad intelectual|intellectual property|litigio|regulat|licencia|license|penalidad|penalty)"),
    "hr": re.compile(
        r"\b(contratar|contrataci|contratamos|vacante|empleado|rrhh|recursos humanos|personal\b|reclut|onboarding|"
        r"nomina|salario|salary|vacaciones|hire\b|hiring|recruit|payroll|employee|candidat|entrevista|interview|"
        r"talento|despido|dismissal)"),
    "sales": re.compile(
        r"\b(ventas?\b|vend|sales\b|sell\b|selling|propuesta|proposal|cliente|client|customer|cotizacion|quote\b|"
        r"pipeline|prospecto|lead\b|leads\b|comercial|negociacion|negotiation|descuento|discount|oferta)"),
    "analyst": re.compile(
        r"\b(datos|data\b|analisis|analysis|analiz|analyz|metric|kpi|tendencia|trend|estadistic|statistic|"
        r"dashboard|grafic|chart|insight|correlacion|forecast|pronostico|proyeccion)"),
    "operations": re.compile(
        r"\b(operacion|operaciones|operations|capacidad|capacity|logistic|entrega|delivery|proveedor|supplier|"
        r"inventario|inventory|plazo|produccion|production|almacen|warehouse|envio|shipping)"),
    "assistant": re.compile(r"\b(agenda|calendario|calendar|reunion|meeting|recordatorio|reminder|coordin)"),
}

# Real relations: a second voice is only allowed between roles that actually work together.
_RELATED: dict[str, set[str]] = {
    "accounting": {"legal", "analyst"},
    "legal": {"accounting", "hr", "sales"},
    "hr": {"legal", "accounting"},
    "sales": {"accounting", "legal", "operations"},
    "analyst": {"accounting", "sales"},
    "operations": {"hr", "accounting"},
}
# Extra cues that make a related role relevant even without naming its area.
_LINKS: dict[tuple[str, str], re.Pattern[str]] = {
    ("accounting", "legal"): re.compile(r"\b(impuest|tax|deuda|debt|prestamo|loan|auditor|audit)"),
    ("hr", "legal"): re.compile(r"\b(contrato|contract|salario|salary|despido|dismissal)"),
    ("sales", "accounting"): re.compile(r"\b(descuento|discount|margen|margin|precio|price)"),
}


def topic_scores(n: str) -> dict[str, int]:
    out: dict[str, int] = {}
    for role, pat in _TOPIC_PATTERNS.items():
        hits = {m.group(0) for m in pat.finditer(n)}
        if hits:
            out[role] = len(hits)
    return out


def _first_pos(n: str, role: str) -> int:
    m = _TOPIC_PATTERNS[role].search(n)
    return m.start() if m else len(n)


# ------------------------------------------------------------------ intent
_GREET = re.compile(
    r"^(hola+|holi+|buen(as|os)( (dias|tardes|noches))?|hey+|ey|hi|hello|hiya|good (morning|afternoon|evening)|"
    r"saludos|que tal|que onda|que hubo|quihubo|ola)\b")
_THANKS = re.compile(r"\b(gracias|thanks|thank you|thx|genial|perfecto|excelente|buen trabajo|great|awesome|perfect)\b")
_HELP = re.compile(r"\b(ayuda|ayudame|ayudenme|help|que puedes hacer|que haces|quien eres|who are you|what can you do|como funciona)\b")
_HOWARE = re.compile(r"\b(como estas|como esta|como andas|como va todo|como les va|how are you|how is it going|hows it going|que cuentas)\b")
_ACK = {"ok", "okay", "vale", "listo", "entendido", "dale", "bien", "cool", "ya", "claro", "si", "sip", "yes", "yep", "got", "it"}
_FLUFF = {"a", "todos", "todas", "equipo", "team", "everyone", "all", "there", "amigos", "chicos", "chicas",
          "companeros", "como", "estan", "estas", "esta", "va", "y", "you", "how", "are", "is", "it", "going",
          "que", "tal", "muy", "dias", "tardes", "noches", "de", "nuevo", "again", "morning", "afternoon", "evening",
          "les", "te", "bien", "yo", "aqui", "todo", "hows", "oficina", "office",
          # greeting words that can follow the opening ("hola equipo, buenos dias")
          "buenos", "buenas", "buen", "good", "dia", "hola", "hi", "hello", "hey", "saludos", "gente"}

_INTERROGATIVE = re.compile(
    r"^(que|como|cuanto|cuantos|cuanta|cuantas|cual|cuales|por que|porque|cuando|donde|quien|quienes|"
    r"what|how|why|when|where|who|which|is|are|do|does|did|will|would|should)\b")
_POLITE = re.compile(
    r"\b(puedes|podrias|podes|podria|puede usted|necesito|quiero|requiero|necesitamos|queremos|quisiera|"
    r"can you|could you|would you|please|por favor|i need|i want|we need|i d like|id like)\b")
_WANT_DET = re.compile(
    r"\b(necesito|quiero|requiero|necesitamos|queremos|quisiera|i need|i want|we need|id like|i d like)"
    r" (que |un |una |el |la |los |las |a |an |the |some )")
_TASK_STEMS = re.compile(
    r"\b(prepar|calcul|revis|redact|gener|elabor|armar|analiz|envi|mandar|agendar|investig|escrib|resum|compar|"
    r"estim|planific|organiz|buscar|cotiz|crear|hacer|contrat|draft|write|create|build|review|prepare|send|"
    r"schedule|research|summar|compare|estimate|plan\b|find|check)")
_IMPERATIVES_ES = re.compile(
    r"^(prepara|haz|calcula|revisa|redacta|genera|crea|elabora|arma|analiza|envia|manda|investiga|escribe|"
    r"resume|compara|estima|planifica|organiza|busca|contrata|cotiza|vende|proponme|agendame)(me|lo|la|le|nos|melo|mela|selo)?$|^dame$")
_AGENDA = re.compile(r"\bagenda (una|un|el|la)\b")
_IMPERATIVES_EN = {"prepare", "make", "calculate", "review", "draft", "write", "create", "build", "generate", "analyze",
                   "analyse", "send", "schedule", "research", "summarize", "summarise", "compare", "estimate", "plan",
                   "find", "give", "put", "check", "run", "set", "list", "show"}
_EN_FILLER = {"please", "just", "can", "you", "could", "would", "go", "ahead", "kindly", "ok", "so", "now", "hey",
              "hi", "hello", "hola", "team", "buenas", "buenos", "dias", "tardes"}
_NOT_TASK_AFTER_WANT = re.compile(r"\b(saber|entender|hablar|preguntar|conocer|ver|know|understand|talk|ask|see)\b")


def _name_tokens(agents: Iterable[RouteAgent]) -> set[str]:
    out: set[str] = set()
    for a in agents:
        out.update(_tokens(norm(a.name)))
    return out


def smalltalk_kind(text: str, extra_fluff: set[str] | None = None) -> str | None:
    """greeting | howare | thanks | help, or None when the message has real content."""
    n = re.sub(r"^[^a-z0-9]+", "", norm(text))
    if not n:
        return None
    fluff = _FLUFF | (extra_fluff or set())
    m = _GREET.match(n)
    rest = re.sub(r"[^a-z0-9 ]", " ", n[m.end():] if m else n).strip()
    rest_tokens = [t for t in rest.split() if t not in fluff]
    if m and not rest_tokens:
        return "greeting"
    if _HOWARE.search(rest) and len(rest_tokens) <= 6 and not topic_scores(n):
        return "greeting" if m else "howare"
    toks = _tokens(n)
    if _HELP.search(n) and len(toks) <= 8 and not topic_scores(n):
        return "help"
    if _THANKS.search(n) and len(toks) <= 6 and not topic_scores(n):
        return "thanks"
    if not m and toks and all(t in _ACK or t in fluff for t in toks) and len(toks) <= 3:
        return "thanks"
    return None


def is_task(text: str) -> bool:
    """The user asks to produce/execute something (prepare, calculate, review, draft...)."""
    n = re.sub(r"^[^a-z0-9]+", "", norm(text))
    toks = _tokens(n)
    if not toks:
        return False
    polite = bool(_POLITE.search(n))
    if _INTERROGATIVE.match(n) and not polite:
        return False
    # first-person plural ("hablemos", "revisemos") invites a conversation, it is not an order
    if any(t.endswith(("emos", "amos")) and len(t) > 6 and _TASK_STEMS.match(t) is not None
           and not _IMPERATIVES_ES.match(t) for t in toks[:3]):
        if not polite:
            return False
    if any(_IMPERATIVES_ES.match(t) for t in toks) or _AGENDA.search(n):
        return True
    first = next((t for t in toks if t not in _EN_FILLER), "")
    if first in _IMPERATIVES_EN:
        return True
    if _WANT_DET.search(n) and not _NOT_TASK_AFTER_WANT.search(n):
        return True
    if polite and _TASK_STEMS.search(n) and not _NOT_TASK_AFTER_WANT.search(n):
        return True
    return False


def classify_intent(text: str, extra_fluff: set[str] | None = None) -> str:
    if smalltalk_kind(text, extra_fluff):
        return "smalltalk"
    if is_task(text):
        return "task"
    return "question"


# ------------------------------------------------------------------ roster
class Roster:
    """Maps roles to agent ids. With no roster (legacy callers) the role names are the ids."""

    def __init__(self, agents: list[RouteAgent]):
        self.agents = list(agents) or [RouteAgent(id=r, role=r, title=r.title(), name="") for r in ROLES]
        self.by_id = {a.id: a for a in self.agents}
        self.by_role: dict[str, RouteAgent] = {}
        for a in self.agents:
            self.by_role.setdefault(a.role or a.id, a)

    def for_role(self, role: str) -> RouteAgent | None:
        return self.by_role.get(role)

    def assistant(self) -> RouteAgent:
        return self.by_role.get(ASSISTANT) or self.agents[0]

    def role_of(self, agent_id: str) -> str:
        a = self.by_id.get(agent_id)
        return (a.role or a.id) if a else agent_id

    def label(self, a: RouteAgent) -> str:
        return a.name or a.title or a.id


def _rng(*parts: str) -> random.Random:
    return random.Random(int(hashlib.sha256("|".join(parts).encode()).hexdigest()[:12], 16))


def _reasons(locale: str) -> dict[str, str]:
    return sc.bundle(locale).CHAT["reasons"]


def _topic_label(locale: str, topic: str) -> str:
    labels = sc.bundle(locale).CHAT["topic_labels"]
    return labels.get(topic, labels["general"])


def parse_conversation(conversation: str) -> str | None:
    """"agent:<id>" -> id; anything else ("office", "", junk) -> None."""
    c = (conversation or "").strip()
    if c.startswith("agent:") and len(c) > 6:
        return c[6:]
    return None


# ------------------------------------------------------------------ the rules router
def rules_route(req: RouteRequest) -> RouteResponse:
    """Deterministic router. Never raises; always returns at least one responder."""
    roster = Roster(req.agents)
    fluff = _name_tokens(roster.agents)
    n = norm(req.text)
    kind = smalltalk_kind(req.text, fluff)
    intent = "smalltalk" if kind else ("task" if is_task(req.text) else "question")
    scores = topic_scores(n)
    primary_role = None
    if scores:
        primary_role = sorted(scores, key=lambda r: (-scores[r], _first_pos(n, r)))[0]
    topic = ROLE_TOPIC.get(primary_role or "assistant", "general") if intent != "smalltalk" else (
        "greeting" if kind in ("greeting", "howare") else kind or "general")
    rs = _reasons(req.locale)
    label = _topic_label(req.locale, topic)

    direct_id = parse_conversation(req.conversation)
    direct = roster.by_id.get(direct_id) if direct_id else None

    # ---- 1:1 chat: ONLY that agent answers, always.
    if direct is not None:
        resp = RouteResponse(intent=intent, topic=topic,
                             responders=[Responder(agent_id=direct.id, role="primary", reason=rs["direct"])])
        owner = roster.for_role(primary_role) if primary_role and primary_role != ASSISTANT else None
        if intent in ("question", "task") and owner is not None and owner.id != direct.id and                 (direct.role or direct.id) != ASSISTANT:
            resp.consult = RouteConsult(agent_id=owner.id, reason=rs["consult"].format(
                topic=label, name=roster.label(owner)))
        return resp

    assistant = roster.assistant()
    # ---- office, but the user spoke to one colleague by name: only that person answers
    addressed = _addressed(n, roster) if intent in ("question", "task") else None
    if addressed is not None:
        resp = RouteResponse(intent=intent, topic=topic, responders=[
            Responder(agent_id=addressed.id, role="primary", reason=rs["direct"])])
        owner = roster.for_role(primary_role) if primary_role and primary_role != ASSISTANT else None
        if owner is not None and owner.id != addressed.id and (addressed.role or addressed.id) != ASSISTANT:
            resp.consult = RouteConsult(agent_id=owner.id, reason=rs["consult"].format(
                topic=label, name=roster.label(owner)))
        return resp
    # ---- office: smalltalk -> the assistant answers first, one or two colleagues say hi
    if intent == "smalltalk":
        responders = [Responder(agent_id=assistant.id, role="primary", reason=rs["greeting"])]
        k = {"greeting": MAX_GREETERS, "howare": 1}.get(kind or "", 0)
        peers = [a for a in roster.agents if a.id != assistant.id]
        if k and peers:
            rng = _rng("greeters", n)
            for a in rng.sample(peers, min(k, len(peers))):
                responders.append(Responder(agent_id=a.id, role="contributor", reason=rs["peer_greeting"]))
        return RouteResponse(intent=intent, topic=topic, responders=responders)

    # ---- office: a task is coordinated by the assistant (the plan decides who works)
    if intent == "task":
        return RouteResponse(intent=intent, topic=topic, responders=[
            Responder(agent_id=assistant.id, role="primary", reason=rs["task"])])

    # ---- office: a question goes to the owner of the topic; maybe ONE related colleague
    owner = roster.for_role(primary_role) if primary_role and primary_role != ASSISTANT else None
    if owner is None:
        return RouteResponse(intent=intent, topic="general", responders=[
            Responder(agent_id=assistant.id, role="primary", reason=rs["coordinates"])])
    responders = [Responder(agent_id=owner.id, role="primary", reason=rs["owner"].format(topic=label))]
    contributor = _pick_contributor(primary_role or "", scores, n, roster)
    if contributor is not None:
        crole = roster.role_of(contributor.id)
        responders.append(Responder(agent_id=contributor.id, role="contributor", reason=rs["related"].format(
            topic=_topic_label(req.locale, ROLE_TOPIC.get(crole, "general")))))
    return RouteResponse(intent=intent, topic=topic, responders=responders[:1 + MAX_CONTRIBUTORS])


def _addressed(n: str, roster: Roster) -> RouteAgent | None:
    """The one agent the user called by first name at the start of the message ("Tomás, ...", "oye Tomás ...")."""
    toks = [t for t in _tokens(n) if t not in _EN_FILLER and t not in ("oye", "ey", "disculpa", "perdona", "sorry")][:2]
    hits = [a for a in roster.agents if a.name and len(_tokens(norm(a.name))[0]) >= 3 and _tokens(norm(a.name))[0] in toks]
    return hits[0] if len(hits) == 1 else None


def _pick_contributor(primary: str, scores: dict[str, int], n: str, roster: Roster) -> RouteAgent | None:
    """A second voice only when the text really touches a related area (low noise)."""
    related = _RELATED.get(primary, set())
    candidates = []
    for role, s in scores.items():
        if role != primary and role in related and role != ASSISTANT:
            candidates.append((-s, _first_pos(n, role), role))
    for (a, b), pat in _LINKS.items():
        if a == primary and pat.search(n) and all(c[2] != b for c in candidates):
            candidates.append((0, len(n), b))
    for _, _, role in sorted(candidates):
        agent = roster.for_role(role)
        if agent is not None:
            return agent
    return None


# ------------------------------------------------------------------ validation of an LLM routing
def validate_route(data: dict, req: RouteRequest) -> RouteResponse | None:
    """Checks an LLM routing against the roster and the hard rules; None means "use the rules"."""
    roster = Roster(req.agents)
    if data.get("intent") not in ("smalltalk", "question", "task"):
        return None
    primary = roster.by_id.get(str(data.get("primary_agent_id") or ""))
    if primary is None:
        return None
    rs = _reasons(req.locale)
    reasons = data.get("reasons") if isinstance(data.get("reasons"), dict) else {}
    topic = str(data.get("topic") or "general").strip().lower()
    topic = topic if topic in TOPIC_ROLE or topic in ("greeting", "thanks", "help") else "general"
    direct_id = parse_conversation(req.conversation)
    if direct_id and direct_id in roster.by_id:  # 1:1: hard rule, never more than that agent
        primary = roster.by_id[direct_id]
        return RouteResponse(intent=data["intent"], topic=topic, source="llm", responders=[
            Responder(agent_id=primary.id, role="primary", reason=str(reasons.get(primary.id) or rs["direct"]))])
    responders = [Responder(agent_id=primary.id, role="primary", reason=str(reasons.get(primary.id) or rs["owner"].format(
        topic=_topic_label(req.locale, topic))))]
    extra = [i for i in (data.get("contributor_agent_ids") or []) if isinstance(i, str)]
    limit = MAX_GREETERS if data["intent"] == "smalltalk" else MAX_CONTRIBUTORS
    if data["intent"] == "task":
        limit = 0
    for aid in extra:
        if len(responders) > limit:
            break
        a = roster.by_id.get(aid)
        if a is not None and a.id != primary.id and all(r.agent_id != a.id for r in responders):
            responders.append(Responder(agent_id=a.id, role="contributor", reason=str(reasons.get(a.id) or rs["related"].format(
                topic=_topic_label(req.locale, ROLE_TOPIC.get(roster.role_of(a.id), "general"))))))
    return RouteResponse(intent=data["intent"], topic=topic, responders=responders, source="llm")


# ------------------------------------------------------------------ scripted replies (simulation)
class _Safe(dict):
    def __missing__(self, key: str) -> str:
        return ""


def _fmt(tpl: str, **kw: str) -> str:
    return tpl.format_map(_Safe(**kw))


def _pick(options: list[str], rng: random.Random, avoid: set[str], **kw: str) -> str:
    """A formatted option that was not said recently; falls back to any when all were."""
    rendered = [_fmt(o, **kw) for o in options]
    fresh = [r for r in rendered if r not in avoid]
    return rng.choice(fresh or rendered)


def apply_tone(text: str, tone: str, locale: str) -> str:
    subs = sc.bundle(locale).TONE_SUBS.get(tone) if locale == "es" else None
    for old, new in subs or ():
        text = re.sub(rf"\b{old}\b", new, text)
        text = re.sub(rf"\b{old.capitalize()}\b", new.capitalize(), text)
    return text


def compose_reply(req: ChatReplyRequest) -> tuple[str, str, ReplyConsult | None]:
    """(text, kind, consult) of one scripted chat reply. kind: chat | answer."""
    c = sc.bundle(req.locale).CHAT
    agent = req.agent
    role = agent.role or agent.id
    roster = Roster(req.agents)
    name = agent.name or agent.id
    title = agent.title or role
    avoid = {h.text for h in req.history if h.from_ == agent.id} | {h.text for h in req.prior_replies}
    rng = _rng(req.text, agent.id, str(len(req.history)), str(req.slot), req.consult.question if req.consult else "")
    area = c["area"].get(role, c["area"]["assistant"])
    base = dict(name=name, title=title, area=area)
    consult = None
    kind = "chat"

    other = roster.by_id.get(req.consult_to or "")
    if other is not None and other.id == agent.id:
        other = None
    other_role = (other.role or other.id) if other is not None else ""
    ctx = dict(other=roster.label(other) if other else "", other_title=(other.title or other.role) if other else "",
               topic_area=c["area"].get(other_role, ""), my_area=area)

    if req.limit:  # a real limit: say no in this role's own voice
        core = c["limit_core"].get(req.limit) or c["limit_core"]["paused"]
        lead = c["limit_lead"].get(role) or c["limit_lead"]["assistant"]
        options = [f"{l} {k}" for l in lead for k in core]
        text = _pick(options, rng, avoid, **base)
    elif req.handoff and other is not None:
        text = _pick(c["handoff_yes"], rng, avoid, other=ctx["other"])
    elif req.consult is not None:  # a colleague asked this agent
        options = c["consult_a"].get(role) or c["consult_a"]["default"]
        text, kind = _pick(options, rng, avoid, **base), "answer"
    elif req.intent == "smalltalk":
        sk = smalltalk_kind(req.text, _name_tokens(roster.agents)) or "greeting"
        if sk == "thanks":
            text = _pick(c["thanks"], rng, avoid, **base)
        elif sk == "help":
            text = _pick(c["help"] if role == ASSISTANT else c["help_self"], rng, avoid, **base)
        elif sk == "howare" and role == ASSISTANT:
            text = _pick(c["howare"], rng, avoid, **base)
        elif req.responder_role == "contributor":
            text = _pick(c["greet_peer"].get(role) or c["greet_peer"]["default"], rng, avoid, **base)
        elif role == ASSISTANT:
            text = _pick(c["greet_primary"], rng, avoid, **base)
        else:
            text = _pick(c["greet_self"], rng, avoid, **base)
    elif req.intent == "task" and other is not None and role != ASSISTANT:
        lead = c["limit_lead"].get(role) or c["limit_lead"]["assistant"]
        text = _pick([f"{l} {k}" for l in lead for k in c["refuse_core"]], rng, avoid, **base, **ctx)
    elif req.intent == "task":
        text = _pick(c["task_ack"] if role == ASSISTANT else c["task_ack_self"], rng, avoid, **base)
    elif req.responder_role == "contributor":
        text = _pick(c["contrib"].get(role) or c["contrib"]["default"], rng, avoid, **base)
    else:
        if other is not None:
            pool = list(c["deflect"].get(role) or c["deflect"]["assistant"])
            pool += c["deflect_pair"].get((role, other_role), [])
            text = _pick(pool, rng, avoid, **base, **ctx)
            q = re.sub(r"\s+", " ", req.text).strip()[:140]
            consult = ReplyConsult(to_agent_id=other.id, question=_pick(
                c["consult_q"], rng, set(), other=ctx["other"], q=q))
        elif role == ASSISTANT or role not in c["answer"]:
            text = _pick(c["answer_general"], rng, avoid, **base)
        else:
            text = _pick(c["answer"][role], rng, avoid, **base)
    return apply_tone(text, req.tone, req.locale), kind, consult


def assigned_reason(locale: str, scenario: str, key: str, role: str) -> str:
    """Why a planned task goes to its agent (simulation planner)."""
    c = sc.bundle(locale)
    return c.TASK_REASONS.get((scenario, key)) or c.ROLE_REASONS.get(role, "")
