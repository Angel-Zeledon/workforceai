"""The price table is shared with the backend: same file, same numbers."""
from pathlib import Path

import pytest

from app import engine

BACKEND_COPY = Path(__file__).resolve().parents[2] / "backend" / "internal" / "application" / "model_prices.json"


def test_price_table_matches_backend_copy():
    if not BACKEND_COPY.exists():
        pytest.skip("backend tree not available (runtime image only)")
    assert engine.PRICES_FILE.read_bytes() == BACKEND_COPY.read_bytes(), \
        "agent-runtime/app/model_prices.json and backend/internal/application/model_prices.json differ"


@pytest.mark.parametrize("model,expected", [
    ("deepseek/deepseek-chat", (0.30, 1.20)),
    ("anthropic/claude-opus-5-5", (4.0, 20.0)),
    ("claude-sonnet-5-5", (2.0, 10.0)),
    ("anthropic/claude-haiku-4-5-20251001", (1.0, 5.0)),
])
def test_known_models(monkeypatch, model, expected):
    monkeypatch.delenv("PRICE_IN_PER_M", raising=False)
    monkeypatch.delenv("PRICE_OUT_PER_M", raising=False)
    assert engine._price_for(model) == expected


def test_unknown_model_uses_default_and_warns_once(monkeypatch, caplog):
    monkeypatch.delenv("PRICE_IN_PER_M", raising=False)
    monkeypatch.delenv("PRICE_OUT_PER_M", raising=False)
    engine._warned_models.discard("brand-new-model")
    with caplog.at_level("WARNING", logger="agent_runtime"):
        assert engine._price_for("x/brand-new-model") == (engine.PRICE_IN_PER_M, engine.PRICE_OUT_PER_M)
        engine._price_for("x/brand-new-model")
    assert sum("brand-new-model" in r.message for r in caplog.records) == 1


def test_estimate_cost_opus():
    assert engine.estimate_cost(1_000_000, 100_000, "anthropic/claude-opus-5-5") == 6.0
