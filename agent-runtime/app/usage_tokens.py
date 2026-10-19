"""Prompt-cache hit tokens from provider usage, normalised to ONE convention.

Convention (what the backend ledger expects): `input_tokens` is the TOTAL prompt size and INCLUDES the cache
hits; `cached_tokens` is the part of it served from the provider cache. Cost = (input - cached) at the input
price + cached at the cache price (engine.estimate_cost), so nothing is counted twice.

Raw provider fields:
  OpenAI / Kimi (OpenAI-style)  usage.prompt_tokens_details.cached_tokens        (prompt_tokens includes them)
  DeepSeek                      usage.prompt_cache_hit_tokens                    (prompt_tokens includes hit + miss)
  Anthropic                     usage.cache_read_input_tokens                    (input_tokens EXCLUDES them)
                                usage.cache_creation_input_tokens: cache WRITES. Not a hit and not discounted
                                (Anthropic charges a premium for them): counted as ordinary input.
"""
from __future__ import annotations

from typing import Any


def _get(o: Any, name: str) -> Any:
    if o is None:
        return None
    return o.get(name) if isinstance(o, dict) else getattr(o, name, None)


def _int(v: Any) -> int:
    try:
        return max(0, int(v or 0))
    except (TypeError, ValueError):
        return 0


def tokens_from_raw_usage(usage: Any) -> tuple[int, int, int]:
    """(input_total, cached, output) from a raw provider `usage` object or dict (any supported provider)."""
    out = _int(_get(usage, "completion_tokens") or _get(usage, "output_tokens"))
    if _get(usage, "cache_read_input_tokens") is not None or _get(usage, "cache_creation_input_tokens") is not None:
        read, write = _int(_get(usage, "cache_read_input_tokens")), _int(_get(usage, "cache_creation_input_tokens"))
        return _int(_get(usage, "input_tokens")) + read + write, read, out  # Anthropic: input excludes both
    inp = _int(_get(usage, "prompt_tokens") or _get(usage, "input_tokens"))
    hit = _get(usage, "prompt_cache_hit_tokens")  # DeepSeek
    if hit is None:
        hit = _get(_get(usage, "prompt_tokens_details"), "cached_tokens")  # OpenAI / Kimi
    return inp, min(_int(hit), inp), out


def tokens_from_metrics(m: Any, provider: str = "") -> tuple[int, int, int]:
    """(input_total, cached, output) from CrewAI's UsageMetrics (prompt_tokens, completion_tokens and
    cached_prompt_tokens, which litellm fills from the fields above). Provider-agnostic: if the cached count
    exceeds the prompt count the framework reported the Anthropic style (input excluding the cache read), so
    the two are added; otherwise the prompt is assumed to already include them (litellm's convention for every
    provider, Anthropic included). KNOWN LIMIT: if a native Anthropic path reported prompt_tokens excluding a
    cache read smaller than the rest of the prompt, the total would be understated; confirm with one live run
    (see docs/cost-reduction.md)."""
    inp = _int(_get(m, "prompt_tokens"))
    out = _int(_get(m, "completion_tokens"))
    cached = _int(_get(m, "cached_prompt_tokens") or _get(m, "cached_tokens"))
    if cached > inp:
        inp += cached
    return inp, cached, out
