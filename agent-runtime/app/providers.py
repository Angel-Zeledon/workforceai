"""Model provider registry, data policy and failover ordering.

Pure logic (no network, no CrewAI import) so it can be tested without real keys.

Providers: ``deepseek`` (the platform default), ``anthropic`` (Claude), ``openai``, ``kimi`` (Moonshot,
OpenAI-compatible) and ``custom`` (one OpenAI-compatible endpoint: regional, self-hosted vLLM/Ollama,
Azure-style gateway...). A provider is "configured" when its API key (and, for custom, its base URL) is
present in the environment, OR when the request carries the organization's own key for it
(bring-your-own-model, ``provider_credentials``): the organization's key always wins over the environment.

Candidate order for a call (first match wins as the primary, the rest are fallbacks):
  1. MODEL_PROVIDER_ORDER_<ROLE>   (per role/task kind, e.g. MODEL_PROVIDER_ORDER_LEGAL)
  2. request.preferred_providers   (optional field sent by the backend)
  3. MODEL_PROVIDER_ORDER          (global; default: deepseek,anthropic,custom)
Later sources only add providers missing from the earlier ones, so every configured and
allowed provider is a fallback. The DATA POLICY is applied after ordering and BEFORE any
send: ALLOWED_PROVIDERS (env, operator ceiling) intersected with request.allowed_providers.
"""
from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from typing import Iterable

KNOWN_PROVIDERS: tuple[str, ...] = ("deepseek", "anthropic", "openai", "kimi", "custom")
DEFAULT_ORDER: tuple[str, ...] = KNOWN_PROVIDERS  # deepseek first (platform default), legacy order kept

KIMI_BASE_URL = "https://api.moonshot.ai/v1"
DEFAULT_MODELS = {
    "anthropic": "anthropic/claude-sonnet-5-5",
    "deepseek": "deepseek/deepseek-chat",
    "openai": "openai/gpt-4.1",
    "kimi": "openai/kimi-k2-0905-preview",  # Moonshot speaks the OpenAI protocol: litellm prefix is openai/
    "custom": "openai/custom-model",  # overridden by CUSTOM_LLM_MODEL
}
_KEY_ENV = {"anthropic": "ANTHROPIC_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "openai": "OPENAI_API_KEY",
            "kimi": "KIMI_API_KEY", "custom": "CUSTOM_LLM_API_KEY"}
# litellm prefix per provider. Kimi and custom are OpenAI-compatible endpoints reached through base_url.
_LITELLM_PREFIX = {"deepseek": "deepseek", "anthropic": "anthropic", "openai": "openai", "kimi": "openai",
                   "custom": "openai"}


def litellm_model(provider: str, name: str) -> str:
    """The litellm model string for a provider and a bare (or already prefixed) model name."""
    name = (name or "").strip()
    prefix = _LITELLM_PREFIX.get(provider, provider)
    if not name:
        return DEFAULT_MODELS.get(provider, f"{prefix}/model")
    if provider in ("kimi", "custom"):
        return name if name.startswith("openai/") else f"openai/{name.removeprefix(provider + '/')}"
    return name if name.startswith(prefix + "/") else f"{prefix}/{name}"

# Stable machine-readable error codes (the backend can switch on these).
ERR_NO_ALLOWED_PROVIDER = "no_allowed_provider"
ERR_ALL_PROVIDERS_FAILED = "all_providers_failed"


class ProviderError(Exception):
    """Stable, non-silent provider failure. ``code`` is part of the API."""

    status_code = 502

    def __init__(self, code: str, message: str, *, tried: list[str] | None = None,
                 allowed: list[str] | None = None, configured: list[str] | None = None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.tried = tried or []
        self.allowed = allowed
        self.configured = configured or []
        if code == ERR_NO_ALLOWED_PROVIDER:
            self.status_code = 422

    def detail(self) -> dict:
        return {"code": self.code, "message": self.message, "providers_tried": self.tried,
                "allowed_providers": self.allowed, "configured_providers": self.configured}


@dataclass(frozen=True)
class ProviderConfig:
    id: str
    model: str  # litellm-style "prefix/name"
    api_key: str = field(repr=False, default="")
    base_url: str | None = None
    org_id: str = ""  # OpenAI organization (optional)
    project_id: str = ""  # OpenAI project (optional)
    byo: bool = False  # the organization's own key (never cached across requests)


@dataclass(frozen=True)
class ProviderPolicy:
    """Per-call routing input. ``allowed=None`` means "no request-level restriction"."""

    allowed: tuple[str, ...] | None = None
    preferred: tuple[str, ...] = ()
    role: str = ""
    role_order: tuple[str, ...] = ()  # organization policy for this role (request field role_providers)
    role_model: str = ""  # "provider/model" from the organization policy (request field role_models)
    credentials: tuple[ProviderConfig, ...] = ()  # the organization's own keys (provider_credentials)
    byo_only: bool = False  # only `credentials` may answer


def _norm_list(values: Iterable[str] | None) -> tuple[str, ...] | None:
    if values is None:
        return None
    out: list[str] = []
    for v in values:
        s = str(v).strip().lower()
        if s and s not in out:
            out.append(s)
    return tuple(out)


def _env_list(name: str) -> tuple[str, ...] | None:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return None
    return _norm_list(raw.split(","))


def _role_key(role: str) -> str:
    return re.sub(r"[^a-z0-9]+", "_", (role or "").lower()).strip("_")


def policy_from_request(req: object, role: str = "") -> ProviderPolicy:
    key = _role_key(role)
    by_role = {_role_key(k): v for k, v in (getattr(req, "role_providers", None) or {}).items()}
    models = {_role_key(k): v for k, v in (getattr(req, "role_models", None) or {}).items()}
    model = str(models.get(key, "") or "").strip() if key else ""
    if model and ("/" not in model or model.split("/", 1)[0].lower() not in KNOWN_PROVIDERS):
        model = ""  # malformed: ignored (the backend validates; this is a second barrier)
    creds = tuple(c for c in (config_from_credential(x) for x in (getattr(req, "provider_credentials", None) or []))
                  if c is not None)
    return ProviderPolicy(allowed=_norm_list(getattr(req, "allowed_providers", None)),
                          credentials=creds, byo_only=bool(getattr(req, "byo_only", False)) and bool(creds),
                          preferred=_norm_list(getattr(req, "preferred_providers", None)) or (),
                          role=role,
                          role_order=_norm_list(by_role.get(key)) or () if key else (),
                          role_model=model)


def _role_env(role: str) -> str:
    return "MODEL_PROVIDER_ORDER_" + re.sub(r"[^A-Za-z0-9]+", "_", role or "").strip("_").upper()


def _model_for(provider: str, first_in_order: bool) -> str:
    legacy = (os.getenv("MODEL") or "").strip()
    if provider == "custom":
        name = (os.getenv("CUSTOM_LLM_MODEL") or "").strip()
        if not name:
            return DEFAULT_MODELS["custom"]
        return name if "/" in name else f"openai/{name}"
    name = (os.getenv(f"{provider.upper()}_MODEL") or "").strip()
    if provider == "kimi":
        return litellm_model("kimi", name)
    if not name and legacy:
        # Legacy MODEL: "deepseek/x" targets deepseek; a bare name targets the primary provider.
        if "/" in legacy and legacy.split("/", 1)[0].lower() == provider:
            name = legacy
        elif "/" not in legacy and first_in_order:
            name = legacy
    name = name or DEFAULT_MODELS[provider]
    return name if "/" in name else f"{provider}/{name}"


def config_from_credential(cred: object) -> ProviderConfig | None:
    """A ProviderConfig from the organization's credential (ProviderCredential). None = unusable (ignored)."""
    pid = str(getattr(cred, "provider", "") or "").strip().lower()
    if pid not in KNOWN_PROVIDERS:
        return None
    key = getattr(cred, "api_key", None)
    key = key.get_secret_value() if hasattr(key, "get_secret_value") else str(key or "")
    key = key.strip()
    base = (getattr(cred, "base_url", None) or "").strip() or None
    if pid == "kimi":
        base = base or KIMI_BASE_URL
    if pid == "custom" and not base:
        return None
    if pid == "deepseek" or pid == "anthropic":
        base = base if base else None  # a proxy/gateway is allowed but never required
    if not key and pid != "custom":
        return None
    return ProviderConfig(id=pid, model=litellm_model(pid, str(getattr(cred, "model", "") or "")),
                          api_key=key or "not-needed", base_url=base,
                          org_id=str(getattr(cred, "org_id", "") or "").strip(),
                          project_id=str(getattr(cred, "project_id", "") or "").strip(), byo=True)


def scrub(text: str, configs: Iterable[ProviderConfig] = ()) -> str:
    """Remove the exact API keys of `configs` (and anything shaped like a credential) from a message."""
    from .redact import redact_secrets

    for c in configs:
        if c.api_key and len(c.api_key) >= 6:
            text = text.replace(c.api_key, "[REDACTED:provider_key]")
    return redact_secrets(text)


# Error classification (provider test and logs). Stable codes the backend and the UI switch on.
ERR_INVALID_KEY, ERR_QUOTA, ERR_MODEL_NOT_FOUND, ERR_NETWORK, ERR_UNKNOWN = (
    "invalid_key", "quota", "model_not_found", "network", "unknown")
_RE_QUOTA = re.compile(r"insufficient.?quota|exceeded your current quota|billing|credit balance|insufficient.?balance|"
                       r"rate.?limit|\b429\b|too many requests|account is not active|suspended", re.I)
_RE_MODEL = re.compile(r"model.?not.?found|does not exist|unknown model|invalid model|no such model|"
                       r"not_found_error|resource_not_found|\b404\b|not supported model|permission.*model", re.I)
_RE_KEY = re.compile(r"invalid.?api.?key|incorrect api key|invalid x-api-key|authentication|unauthori[sz]ed|\b401\b|"
                     r"\b403\b|api key.*(invalid|not valid|revoked|expired)|invalid_request_error.*key|permission.?denied", re.I)
_RE_NET = re.compile(r"timeout|timed out|connection|connect|dns|name or service|resolve|unreachable|ssl|certificate|"
                     r"\b50[234]\b|network|temporarily|unavailable", re.I)


def classify_error(exc: BaseException) -> str:
    """invalid_key | quota | model_not_found | network | unknown, from the exception type and text."""
    text = f"{type(exc).__name__} {exc}"
    # Order matters: a quota 429 also says "rate limit"; a wrong model also says 404.
    if _RE_KEY.search(text):
        return ERR_INVALID_KEY
    if _RE_QUOTA.search(text):
        return ERR_QUOTA
    if _RE_MODEL.search(text):
        return ERR_MODEL_NOT_FOUND
    if _RE_NET.search(text):
        return ERR_NETWORK
    return ERR_UNKNOWN


def global_order() -> tuple[str, ...]:
    order = list(_env_list("MODEL_PROVIDER_ORDER") or ())
    order = [p for p in order if p in KNOWN_PROVIDERS]
    return tuple(order + [p for p in DEFAULT_ORDER if p not in order])


def configured_providers() -> list[ProviderConfig]:
    """Providers with credentials in the environment, in global order."""
    order = global_order()
    out: list[ProviderConfig] = []
    for p in order:
        key = os.getenv(_KEY_ENV[p], "").strip()
        base = None
        if p == "custom":
            base = (os.getenv("CUSTOM_LLM_BASE_URL") or "").strip() or None
            if not base:
                continue
            key = key or "not-needed"  # local servers often need no key
        elif p == "kimi":
            if not key:
                continue
            base = (os.getenv("KIMI_BASE_URL") or "").strip() or KIMI_BASE_URL
        elif p == "openai":
            if not key:
                continue
            base = (os.getenv("OPENAI_BASE_URL") or "").strip() or None
        elif not key:
            continue
        out.append(ProviderConfig(id=p, model="", api_key=key, base_url=base))
    return [ProviderConfig(c.id, _model_for(c.id, i == 0), c.api_key, c.base_url) for i, c in enumerate(out)]


def default_provider(configs: list[ProviderConfig] | None = None) -> str:
    """The provider used first: DeepSeek when its key is present, otherwise Anthropic, otherwise the
    first other configured provider ("" when none). The default global order is deepseek, anthropic,
    custom, so this is the head of that list; an operator who sets MODEL_PROVIDER_ORDER overrides it."""
    configs = configured_providers() if configs is None else configs
    return configs[0].id if configs else ""


def effective_allowed(policy: ProviderPolicy) -> tuple[str, ...] | None:
    """Operator ceiling (ALLOWED_PROVIDERS) intersected with the request list. None = unrestricted."""
    env = _env_list("ALLOWED_PROVIDERS")
    req = policy.allowed
    if env is None:
        return req
    if req is None:
        return env
    return tuple(p for p in req if p in env)


def candidates(configs: list[ProviderConfig], policy: ProviderPolicy) -> list[ProviderConfig]:
    """Ordered failover list after the data policy. Raises ProviderError if empty."""
    configs = list(configs)
    if policy.credentials:  # the organization's own keys win over the environment's, for the same provider
        mine = {c.id: c for c in policy.credentials}
        if policy.byo_only:
            configs = list(mine.values())
        else:
            configs = [mine.pop(c.id, c) for c in configs] + list(mine.values())
    by_id = {c.id: c for c in configs}
    if policy.role_model:
        prov, name = policy.role_model.split("/", 1)
        prov = prov.lower()
        if prov in by_id:  # only swaps the model of a configured provider, never adds one
            c = by_id[prov]
            if prov == "custom":  # litellm name of an OpenAI-compatible endpoint
                model = name if "/" in name else f"openai/{name}"
            else:
                model = litellm_model(prov, name)
            by_id[prov] = ProviderConfig(c.id, model, c.api_key, c.base_url, c.org_id, c.project_id, c.byo)
    order: list[str] = []
    for src in (_env_list(_role_env(policy.role)) if policy.role else None, policy.role_order, policy.preferred,
                tuple(c.id for c in configs)):
        for p in src or ():
            if p in by_id and p not in order:
                order.append(p)
    allowed = effective_allowed(policy)
    if allowed is not None:
        order = [p for p in order if p in allowed]
    if not order:
        raise ProviderError(
            ERR_NO_ALLOWED_PROVIDER,
            "No allowed model provider is configured for this request; nothing was sent.",
            allowed=list(allowed) if allowed is not None else None,
            configured=[c.id for c in configs])
    return [by_id[p] for p in order]


_TRANSIENT = re.compile(r"timeout|timed out|rate.?limit|429|overload|502|503|504|529|connection|temporar|unavailable",
                        re.I)


def is_transient(exc: BaseException) -> bool:
    """Worth retrying on the same provider (timeout / rate limit / 5xx / network)."""
    return bool(_TRANSIENT.search(f"{type(exc).__name__} {exc}"))


# ---- V2: creativity -> bounded temperature ----------------------------------------------------
DEFAULT_TEMPERATURE = 0.3  # what the runtime always used; "medium" and "not set" both map to it

# provider -> (low, medium, high). Every value is clamped to the provider's accepted range, so a
# profile can make an agent more or less exploratory but never push a call outside safe sampling.
_TEMPERATURES = {
    "anthropic": (0.1, DEFAULT_TEMPERATURE, 0.8),
    "deepseek": (0.1, DEFAULT_TEMPERATURE, 1.0),
    "openai": (0.1, DEFAULT_TEMPERATURE, 0.9),
    "kimi": (0.1, DEFAULT_TEMPERATURE, 0.8),  # Moonshot accepts 0..1
    "custom": (0.1, DEFAULT_TEMPERATURE, 0.8),
}
_TEMPERATURE_MAX = {"anthropic": 1.0, "deepseek": 1.5, "openai": 1.0, "kimi": 1.0, "custom": 1.0}


def fixed_temperature_model(model: str) -> bool:
    """OpenAI reasoning models (o-series, gpt-5) reject a custom temperature: the runtime must not send one."""
    name = model.split("/")[-1].lower()
    return bool(re.match(r"^(o\d|gpt-5)", name))


def temperature_for(provider: str, creativity: str = "") -> float:
    """Sampling temperature for a provider and a creativity level; unknown/empty -> the current default."""
    level = {"low": 0, "medium": 1, "high": 2}.get((creativity or "").strip().lower())
    if level is None:
        return DEFAULT_TEMPERATURE
    low, med, high = _TEMPERATURES.get(provider, (0.1, DEFAULT_TEMPERATURE, 0.8))
    value = (low, med, high)[level]
    return round(max(0.0, min(value, _TEMPERATURE_MAX.get(provider, 1.0))), 2)
