"""Modelos pydantic del contrato Go -> agent-runtime (docs/SPEC.md)."""
from __future__ import annotations

from typing import Any, Literal, Union

from pydantic import BaseModel, ConfigDict, Field, field_validator

Risk = Literal["low", "medium", "high"]
MetricValue = Union[str, int, float]


class _Base(BaseModel):
    model_config = ConfigDict(extra="ignore")


Locale = Literal["es", "en"]


def normalize_locale(value: Any) -> str:
    """Map "en", "en-US", "EN_us" -> "en"; anything else (or empty) -> "es"."""
    v = str(value or "").strip().lower().replace("_", "-")
    return "en" if v == "en" or v.startswith("en-") else "es"


Tone = Literal["neutral", "mx", "co", "ar", "cl", "es"]
TONES: tuple[str, ...] = ("neutral", "mx", "co", "ar", "cl", "es")


def normalize_tone(value: Any) -> str:
    """Regional tone code; anything unknown or empty -> "neutral" (backwards compatible)."""
    v = str(value or "").strip().lower()
    return v if v in TONES else "neutral"


class StructuredOutput(_Base):
    summary: str
    findings: list[str] = Field(default_factory=list)
    metrics: dict[str, MetricValue] = Field(default_factory=dict)
    hypotheses: list[str] = Field(default_factory=list)
    evidence: list[str] = Field(default_factory=list)
    recommendations: list[str] = Field(default_factory=list)
    confidence: float = Field(ge=0, le=1)
    suggested_tasks: list[str] = Field(default_factory=list)


class Usage(_Base):
    model: str
    input_tokens: int
    output_tokens: int
    cost_usd: float
    duration_ms: int
    # Optional (backwards compatible): which provider actually answered and which ones failed first.
    provider: str | None = None
    failed_providers: list[str] | None = None


class _ProviderPolicyMixin(_Base):
    """Optional data/routing policy, added to every request. Absent = no request-level restriction.

    allowed_providers: provider ids this request's data may be sent to (e.g. ["anthropic"] to exclude
        DeepSeek). The runtime applies it BEFORE sending; an empty list means "none" and fails with
        the stable error code no_allowed_provider (HTTP 422). Known ids: deepseek, anthropic, custom.
        The operator ceiling ALLOWED_PROVIDERS (env) is intersected with it, never widened.
    preferred_providers: try these first (in order); the other allowed providers remain as fallbacks.
    role_providers: per agent role, the provider order to try first (organization policy; the
        operator's MODEL_PROVIDER_ORDER_<ROLE> env still wins). Allowed providers still apply.
    role_models: per agent role, the model to use, as "provider/model" (e.g.
        "anthropic/claude-opus-5-5"). Only replaces that provider's model; it never adds a provider.
    """

    allowed_providers: list[str] | None = None
    preferred_providers: list[str] | None = None
    role_providers: dict[str, list[str]] | None = None
    role_models: dict[str, str] | None = None


# ---- /v1/plan -------------------------------------------------------------
class PlanAgent(_Base):
    id: str
    role: str
    title: str = ""
    responsibilities: Union[str, list[str]] = ""
    # role template profile (sent by the backend): the planner gives a task to the owner of its area
    topic: str = ""
    keywords: list[str] = Field(default_factory=list)
    area: str = ""


class PlanRequest(_ProviderPolicyMixin):
    request_text: str
    agents: list[PlanAgent] = Field(default_factory=list)
    budget_usd: float = 0.0
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)


class PlanTask(_Base):
    key: str
    title: str
    description: str
    agent_id: str
    depends_on: list[str] = Field(default_factory=list)
    # Optional, human-readable: why this agent got this task (shown in the chat). Omitted when unknown.
    reason: str | None = None


class PlanResponse(_Base):
    objectives: list[str]
    tasks: list[PlanTask]
    clarifying_questions: list[str] = Field(default_factory=list)
    provider: str | None = None  # optional: provider/model that answered (live mode only)
    model: str | None = None
    usage: Usage | None = None  # optional (additive): tokens/cost of the planning call, for spend accounting


# ---- /v1/plan-phases and /v1/plan-phase (hierarchical planning of large projects) ----
Complexity = Literal["S", "M", "L", "XL"]


def normalize_complexity(value: Any) -> str:
    """S|M|L|XL; anything else (LLM noise, empty) -> "M"."""
    v = str(value or "").strip().upper()
    return v if v in ("S", "M", "L", "XL") else "M"


class PhaseSpec(_Base):
    key: str
    title: str
    goal: str = ""
    size: str = "M"  # rough size S|M|L|XL: drives how many tasks the phase is expanded into
    depends_on: list[str] = Field(default_factory=list)  # keys of earlier phases

    _norm_size = field_validator("size", mode="before")(normalize_complexity)


class PlanPhasesRequest(_ProviderPolicyMixin):
    request_text: str
    agents: list[PlanAgent] = Field(default_factory=list)
    budget_usd: float = 0.0
    locale: Locale = "es"
    tone: Tone = "neutral"
    max_phases: int = Field(default=8, ge=1, le=20)

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)


class PlanPhasesResponse(_Base):
    objectives: list[str] = Field(default_factory=list)
    phases: list[PhaseSpec]
    clarifying_questions: list[str] = Field(default_factory=list)
    usage: Usage | None = None
    provider: str | None = None
    model: str | None = None


class PhaseBrief(_Base):
    key: str
    title: str


class PlanPhaseRequest(_ProviderPolicyMixin):
    request_text: str
    phase: PhaseSpec
    other_phases: list[PhaseBrief] = Field(default_factory=list)  # the rest of the roadmap, for context only
    agents: list[PlanAgent] = Field(default_factory=list)
    target_tasks: int = Field(default=12, ge=1, le=200)  # a goal, the planner may deviate a little
    max_tasks: int = Field(default=40, ge=1, le=200)  # hard cap: the runtime drops the extra tasks
    budget_usd: float = 0.0
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)


class PhaseTask(_Base):
    key: str  # unique inside the phase; dependencies refer to keys of the same phase
    title: str
    description: str = ""
    agent_id: str
    depends_on: list[str] = Field(default_factory=list)
    complexity: str = "M"
    reason: str | None = None

    _norm_cx = field_validator("complexity", mode="before")(normalize_complexity)


class PlanPhaseResponse(_Base):
    tasks: list[PhaseTask]
    usage: Usage | None = None
    provider: str | None = None
    model: str | None = None


# ---- /v1/run-task ---------------------------------------------------------
class TaskInfo(_Base):
    id: str
    title: str
    description: str = ""
    agent_id: str


class AgentInfo(_Base):
    id: str
    role: str
    title: str = ""
    persona: str = ""
    responsibilities: Union[str, list[str]] = ""
    tools: list[str] = Field(default_factory=list)
    area: str = ""


class DependencyOutput(_Base):
    task_id: str
    agent_id: str
    output: Any = None
    # W3 (additive, optional): reference id, title, and whether the backend reduced
    # the output to its summary to respect the dependency token budget.
    ref: str = ""
    title: str = ""
    truncated: bool = False


class MemoryItem(_Base):
    scope: str = ""
    key: str
    value: Any = None


class ProjectTaskRef(_Base):
    ref: str = ""
    task_id: str = ""
    title: str = ""
    agent_id: str = ""
    summary: str = ""


class ContextArtifact(_Base):
    """Text of an artifact the backend read for this task (the runtime never fetches anything)."""

    id: str = ""
    title: str = ""
    kind: str = ""
    text: str = ""
    truncated: bool = False


class ProjectContext(_Base):
    """Bounded, read-only view of the rest of a project (W3). Always untrusted data."""

    index: list[ProjectTaskRef] = Field(default_factory=list)
    index_omitted: int = 0
    artifacts: list[ContextArtifact] = Field(default_factory=list)

    _lists = field_validator("index", "artifacts", mode="before")(lambda v: [] if v is None else v)


class TaskContext(_Base):
    request_text: str = ""
    dependency_outputs: list[DependencyOutput] = Field(default_factory=list)
    memory: list[MemoryItem] = Field(default_factory=list)
    dependency_omitted: int = 0  # W3: dependencies left out by the backend token budget
    project_context: ProjectContext | None = None  # W3: only for project tasks


class UntrustedItem(_Base):
    """Contenido de terceros entregado por el backend (resultado de una herramienta)."""

    id: str = ""
    source: str = ""  # p. ej. "tool:email.search"
    trust: str = "external_untrusted"  # informativo: nunca eleva la confianza
    text: str = ""


class ToolAvailable(_Base):
    name: str
    actions: list[str] = Field(default_factory=list)


class RunTaskRequest(_ProviderPolicyMixin):
    # El contrato NO tiene campos de credenciales, connection_id ni URLs de API:
    # el runtime nunca ve secretos ni llama al proveedor (test_contract_has_no_secret_fields).
    task: TaskInfo
    agent: AgentInfo
    context: TaskContext = Field(default_factory=TaskContext)
    external_content: list[str] | None = None
    untrusted: list[UntrustedItem] | None = None
    tools_available: list[ToolAvailable] | None = None
    tainted: bool = False
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)


class Consult(_Base):
    to_agent_id: str
    question: str


class ToolRequest(_Base):
    tool: str
    action: str
    args: dict[str, Any] = Field(default_factory=dict)
    risk: Risk = "low"


class TaskResult(_Base):
    """Lo que produce el modelo (sin usage)."""

    output: StructuredOutput
    consults: list[Consult] = Field(default_factory=list)
    tool_requests: list[ToolRequest] = Field(default_factory=list)


class RunTaskResponse(_Base):
    output: StructuredOutput
    consults: list[Consult] = Field(default_factory=list)
    tool_requests: list[ToolRequest] = Field(default_factory=list)
    usage: Usage
    provider: str | None = None  # optional: same as usage.provider / usage.model
    model: str | None = None


# ---- /v1/consult ----------------------------------------------------------
class ConsultRequest(_ProviderPolicyMixin):
    from_agent_id: str
    to_agent_id: str
    question: str
    context: Any = None
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)


class ConsultResponse(_Base):
    answer: str
    usage: Usage
    provider: str | None = None
    model: str | None = None


# ---- /v1/synthesize -------------------------------------------------------
class SynthOutput(_Base):
    task_id: str
    agent_id: str
    title: str = ""
    output: Any = None


class SynthesizeRequest(_ProviderPolicyMixin):
    request_text: str
    outputs: list[SynthOutput] = Field(default_factory=list)
    locale: Locale = "es"
    tone: Tone = "neutral"
    # W3 hierarchical synthesis: "" = single pass, "group" = part N of `parts`, "final" = over group syntheses.
    stage: str = ""
    part: int = 0
    parts: int = 0

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)
    _stage = field_validator("stage", mode="before")(lambda v: v if v in ("group", "final") else "")


class Section(_Base):
    heading: str
    body: str


class SynthesizeResponse(_Base):
    title: str
    summary: str
    sections: list[Section] = Field(default_factory=list)
    provider: str | None = None
    model: str | None = None


def _none_to_list(v):
    """Go marshals a nil slice as null; for these list fields that means "empty"."""
    return [] if v is None else v


# ---- /v1/route and /v1/chat-reply (conversational layer, docs/architecture/chat-routing.md) ----
Intent = Literal["smalltalk", "question", "task"]
INTENTS: tuple[str, ...] = ("smalltalk", "question", "task")
ResponderRole = Literal["primary", "contributor"]


class RouteAgent(_Base):
    id: str
    role: str = ""
    title: str = ""
    name: str = ""
    # role template profile (sent by the backend): routing works from data, not a fixed list of roles
    topic: str = ""
    keywords: list[str] = Field(default_factory=list)
    related: list[str] = Field(default_factory=list)
    area: str = ""


class HistoryItem(_Base):
    """One earlier chat message. ``from`` is "user" or an agent id."""

    model_config = ConfigDict(extra="ignore", populate_by_name=True)

    from_: str = Field(default="", alias="from")
    text: str = ""


class RouteRequest(_ProviderPolicyMixin):
    text: str
    conversation: str = "office"  # "office" | "agent:<id>"
    agents: list[RouteAgent] = Field(default_factory=list)
    history: list[HistoryItem] = Field(default_factory=list)
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)
    _lists = field_validator("agents", "history", mode="before")(_none_to_list)


class Responder(_Base):
    agent_id: str
    role: ResponderRole = "primary"
    reason: str = ""


class RouteConsult(_Base):
    """1:1 chats only: the question belongs to another area; the agent may ask that colleague."""

    agent_id: str
    reason: str = ""


class RouteResponse(_Base):
    intent: Intent
    topic: str = "general"
    responders: list[Responder] = Field(default_factory=list)
    replies: list[dict[str, Any]] | None = None  # reserved; the backend asks /v1/chat-reply per responder
    consult: RouteConsult | None = None
    source: Literal["rules", "llm"] = "rules"
    usage: Usage | None = None  # only when an LLM classified (live)
    provider: str | None = None
    model: str | None = None


class ChatAgent(_Base):
    id: str
    role: str = ""
    title: str = ""
    name: str = ""
    persona: str = ""
    area: str = ""


class ReplyConsult(_Base):
    """Asks the backend to show a visible consult message and to call chat-reply for ``to_agent_id``."""

    to_agent_id: str
    question: str


class ChatConsultIn(_Base):
    """Set when the agent answers a colleague's consult (instead of a user message)."""

    from_agent_id: str
    question: str


class ChatReplyRequest(_ProviderPolicyMixin):
    agent: ChatAgent
    text: str  # what the user wrote
    conversation: str = "office"
    intent: Intent = "question"
    topic: str = "general"
    responder_role: ResponderRole = "primary"
    reason: str = ""
    agents: list[RouteAgent] = Field(default_factory=list)  # roster, for redirects and mentions
    history: list[HistoryItem] = Field(default_factory=list)
    prior_replies: list[HistoryItem] = Field(default_factory=list)  # replies already given in this turn
    consult: ChatConsultIn | None = None
    slot: int = 0  # position of this reply in the turn (0 = first), for greeting variety
    consult_to: str | None = None  # agent id that owns the topic (redirect / reassignment hint from /v1/route)
    # A real limit stops the agent from doing what was asked: it says so in its own voice (scripted, never an
    # LLM call, zero cost). kill_switch | paused | budget | read_only | no_connection
    limit: str | None = None
    handoff: bool = False  # the user accepted a redirect: this agent now takes the turn from the colleague
    locale: Locale = "es"
    tone: Tone = "neutral"

    _norm = field_validator("locale", mode="before")(normalize_locale)
    _norm_tone = field_validator("tone", mode="before")(normalize_tone)
    _lists = field_validator("agents", "history", "prior_replies", mode="before")(_none_to_list)


class ChatReplyResponse(_Base):
    text: str
    kind: Literal["chat", "consult", "answer"] = "chat"
    consult: ReplyConsult | None = None
    usage: Usage
    provider: str | None = None
    model: str | None = None


class HealthResponse(_Base):
    status: str
    mode: Literal["simulation", "live"]
    provider: Literal["simulation", "deepseek", "anthropic", "custom"] = "simulation"
