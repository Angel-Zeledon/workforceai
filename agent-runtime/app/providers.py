"""Model provider registry, data policy and failover ordering.

Pure logic (no network, no CrewAI import) so it can be tested without real keys.

Providers: ``anthropic``, ``deepseek`` and ``custom`` (one OpenAI-compatible endpoint:
regional, self-hosted vLLM/Ollama, Azure-style gateway...). A provider is "configured"
when its API key (and, for custom, its base URL) is present in the environment.

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

KNOWN_PROVIDERS: tuple[str, ...] = ("deepseek", "anthropic", "custom")
DEFAULT_ORDER: tuple[str, ...] = ("deepseek", "anthropic", "custom")  # legacy priority, backwards compatible

DEFAULT_MODELS = {
    "anthropic": "anthropic/claude-sonnet-5-5",
    "deepseek": "deepseek/deepseek-chat",
    "custom": "openai/custom-model",  # overridden by CUSTOM_LLM_MODEL
}
_KEY_ENV = {"anthropic": "ANTHROPIC_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "custom": "CUSTOM_LLM_API_KEY"}

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


@dataclass(frozen=True)
class ProviderPolicy:
    """Per-call routing input. ``allowed=None`` means "no request-level restriction"."""

    allowed: tuple[str, ...] | None = None
    preferred: tuple[str, ...] = ()
    role: str = ""
    role_order: tuple[str, ...] = ()  # organization policy for this role (request field role_providers)
    role_model: str = ""  # "provider/model" from the organization policy (request field role_models)


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
    return ProviderPolicy(allowed=_norm_list(getattr(req, "allowed_providers", None)),
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
    if not name and legacy:
        # Legacy MODEL: "deepseek/x" targets deepseek; a bare name targets the primary provider.
        if "/" in legacy and legacy.split("/", 1)[0].lower() == provider:
            name = legacy
        elif "/" not in legacy and first_in_order:
            name = legacy
    name = name or DEFAULT_MODELS[provider]
    return name if "/" in name else f"{provider}/{name}"


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
        elif not key:
            continue
        out.append(ProviderConfig(id=p, model="", api_key=key, base_url=base))
    return [ProviderConfig(c.id, _model_for(c.id, i == 0), c.api_key, c.base_url) for i, c in enumerate(out)]


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
    by_id = {c.id: c for c in configs}
    if policy.role_model:
        prov, name = policy.role_model.split("/", 1)
        prov = prov.lower()
        if prov in by_id:  # only swaps the model of a configured provider, never adds one
            c = by_id[prov]
            if prov == "custom":  # litellm name of an OpenAI-compatible endpoint
                model = name if "/" in name else f"openai/{name}"
            else:
                model = f"{prov}/{name}"
            by_id[prov] = ProviderConfig(c.id, model, c.api_key, c.base_url)
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
