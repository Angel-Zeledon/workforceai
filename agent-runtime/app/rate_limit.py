"""Client-side pacing of provider calls (free tiers, shared keys). No network, thread-safe.

Two independent mechanisms, both per provider id:

* ``LLM_RPM_<PROVIDER>`` (e.g. LLM_RPM_GEMINI=10): at most N calls START in any rolling 60 s window. Off when
  unset/0. Google's free tier answers the 11th call of a minute with 429 RESOURCE_EXHAUSTED; pacing on our side
  turns that into a short wait instead of a failed request that still counts against the quota.
* Cooldown: after a provider answers 429 with "retry in N s", every call to it waits until then (or gives up at
  once when that is further away than the caller can wait), instead of each concurrent call discovering the same
  429 on its own.

``acquire(provider, max_wait_s)`` returns 0.0 when the call may go now (after waiting up to max_wait_s), or the
number of seconds still needed when it would have to wait longer than that: the caller then fails fast with a
rate-limited error and NOTHING is sent.
"""
from __future__ import annotations

import os
import threading
import time
from collections import deque

WINDOW_S = 60.0

_lock = threading.Lock()
_starts: dict[str, deque[float]] = {}
_cooldown_until: dict[str, float] = {}
_clock = time.monotonic
_sleep = time.sleep


def rpm_for(provider: str) -> int:
    raw = (os.getenv(f"LLM_RPM_{(provider or '').upper()}") or "").strip()
    try:
        return max(0, int(raw)) if raw else 0
    except ValueError:
        return 0


def note_rate_limited(provider: str, retry_after_s: float | None) -> None:
    """The provider just answered 429: hold every call to it for retry_after_s (if it said)."""
    if not retry_after_s or retry_after_s <= 0:
        return
    with _lock:
        until = _clock() + float(retry_after_s)
        if until > _cooldown_until.get(provider, 0.0):
            _cooldown_until[provider] = until


def _needed(provider: str, now: float) -> float:
    wait = max(0.0, _cooldown_until.get(provider, 0.0) - now)
    rpm = rpm_for(provider)
    if rpm:
        q = _starts.setdefault(provider, deque())
        while q and now - q[0] >= WINDOW_S:
            q.popleft()
        if len(q) >= rpm:
            wait = max(wait, q[0] + WINDOW_S - now)
    return wait


def acquire(provider: str, max_wait_s: float) -> float:
    """Wait (at most max_wait_s) for a slot. 0.0 = go (the start is recorded); > 0 = seconds still needed."""
    deadline = _clock() + max(0.0, max_wait_s)
    while True:
        with _lock:
            now = _clock()
            wait = _needed(provider, now)
            if wait <= 0:
                if rpm_for(provider):
                    _starts.setdefault(provider, deque()).append(now)
                return 0.0
            if now + wait > deadline:
                return wait
        _sleep(min(wait, 1.0))


def reset() -> None:
    """Forget every window and cooldown (tests)."""
    with _lock:
        _starts.clear()
        _cooldown_until.clear()
