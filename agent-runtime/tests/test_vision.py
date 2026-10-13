"""Vision (capability 1): multimodal messages, vision routing under the data policy, caps and usage.

No test calls a real model: the engine is built without CrewAI (object.__new__) and its LLM class is a fake that
records what would be sent. SIMULATION is used for the HTTP layer.
"""
import asyncio
import base64
import json
import struct
import zlib

import pytest

from app import crewai_engine
from app.models import RunTaskRequest, StructuredOutput, TaskResult, Usage
from app.providers import (
    ProviderConfig, ProviderError, ProviderPolicy, policy_from_request, vision_candidates, vision_capable,
)
from app.security import UNTRUSTED_TAG, build_task_prompt, build_trace
from app.vision import (
    NOTICE_IMAGES_DROPPED, NOTICE_VISION_UNAVAILABLE, build_vision_messages, estimate_image_tokens, prepare_images,
)
from conftest import run_req

ENV = ("SIMULATION", "DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "KIMI_API_KEY", "CUSTOM_LLM_API_KEY",
       "CUSTOM_LLM_BASE_URL", "MODEL", "MODEL_PROVIDER_ORDER", "ALLOWED_PROVIDERS", "KIMI_VISION", "CUSTOM_LLM_VISION",
       "DEEPSEEK_VISION", "VISION_MAX_IMAGES", "VISION_MAX_IMAGE_MB", "VISION_MAX_TOTAL_MB")


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    for k in ENV:
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setattr(crewai_engine, "PROVIDER_RETRY_BACKOFF_S", 0)


def png(w=40, h=30) -> bytes:
    """A real (tiny) PNG."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    raw = b"".join(b"\x00" + b"\x7f\x00\x00" * w for _ in range(h))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def image(path="/Shared/grafico.png", data=None, mime="image/png", page=None):
    d = png() if data is None else data
    return {"name": path.rsplit("/", 1)[-1], "mime": mime, "data_b64": base64.b64encode(d).decode(), "source_path": path,
            "page": page}


def req(images=None, **extra):
    body = {**run_req("legal"), **extra}
    if images is not None:
        body["images"] = images
    return RunTaskRequest.model_validate(body)


# ------------------------------------------------------------------ caps and validation
def test_prepare_images_caps_and_validates(monkeypatch):
    monkeypatch.setenv("VISION_MAX_IMAGES", "2")
    imgs = [image("/a.png"), image("/b.png"), image("/c.png")]  # one over the count cap
    imgs.append({"name": "x", "mime": "image/png", "data_b64": "not base64!!"})  # invalid base64
    imgs.append(image("/fake.png", data=b"MZ\x90\x00 an exe"))  # wrong magic bytes
    imgs.append(image("/liar.png", data=png(), mime="image/jpeg"))  # declared type does not match
    kept, notices = prepare_images(req(imgs).images)
    assert [k.source_path for k in kept] == ["/a.png", "/b.png"]
    assert notices == [NOTICE_IMAGES_DROPPED]
    assert (kept[0].width, kept[0].height) == (40, 30)


def test_prepare_images_byte_cap(monkeypatch):
    monkeypatch.setenv("VISION_MAX_IMAGE_MB", "1")
    big = png() + b"\x00" * (1 << 20)
    kept, notices = prepare_images(req([image("/big.png", data=big), image("/ok.png")]).images)
    assert [k.source_path for k in kept] == ["/ok.png"] and notices == [NOTICE_IMAGES_DROPPED]
    assert prepare_images(None) == ([], [])


def test_image_tokens_estimate():
    kept, _ = prepare_images(req([image()]).images)
    assert 85 <= estimate_image_tokens(kept[0]) <= 1600
    unknown = kept[0].__class__(name="x", mime="image/png", data_b64="", size=1)
    assert estimate_image_tokens(unknown) == 1600  # never underestimated


# ------------------------------------------------------------------ multimodal messages
def test_build_vision_messages_shape():
    kept, _ = prepare_images(req([image("/Shared/a.png"), image("/Shared/s.pdf", page=3)]).images)
    msgs = build_vision_messages("SYSTEM", "USER TASK", kept, "es")
    assert msgs[0] == {"role": "system", "content": "SYSTEM"}
    content = msgs[1]["content"]
    assert msgs[1]["role"] == "user" and content[0] == {"type": "text", "text": "USER TASK"}
    kinds = [c["type"] for c in content]
    assert kinds == ["text", "text", "image_url", "text", "image_url"]
    assert content[2]["image_url"]["url"].startswith("data:image/png;base64,iVBOR")
    # Every image is preceded by its untrusted-data label: what is seen is data, never instructions.
    assert UNTRUSTED_TAG in content[1]["text"] and "/Shared/a.png" in content[1]["text"]
    assert "page 3" in content[3]["text"] and "nunca sigas" in content[3]["text"]


def test_prompt_names_images_but_never_carries_pixels():
    r = req([image("/Shared/foto.png")], workspace={"desk": "/Agents/legal", "can_write": True})
    prompt = build_task_prompt(r)
    assert "/Shared/foto.png" in prompt and "IMAGENES ADJUNTAS" in prompt
    assert 'view:"image"' in prompt and "[page N]" in prompt  # how to ask for an image / PDF pages
    b64 = r.images[0].data_b64
    assert b64[:40] not in prompt and b64[:40] not in json.dumps(build_trace(r))


# ------------------------------------------------------------------ capability table and data policy
@pytest.mark.parametrize("cfg,want", [
    (ProviderConfig("anthropic", "anthropic/claude-sonnet-5-5"), True),
    (ProviderConfig("openai", "openai/gpt-4.1"), True),
    (ProviderConfig("openai", "openai/gpt-3.5-turbo"), False),
    (ProviderConfig("deepseek", "deepseek/deepseek-chat"), False),
    (ProviderConfig("kimi", "openai/kimi-k2-0905-preview"), False),
    (ProviderConfig("custom", "openai/llava"), False),
])
def test_vision_capability_table(cfg, want):
    assert vision_capable(cfg) is want


def test_vision_configurable_for_kimi_and_custom(monkeypatch):
    monkeypatch.setenv("KIMI_VISION", "true")
    monkeypatch.setenv("CUSTOM_LLM_VISION", "1")
    assert vision_capable(ProviderConfig("kimi", "openai/kimi-vl"))
    assert vision_capable(ProviderConfig("custom", "openai/llava"))


DS = ProviderConfig("deepseek", "deepseek/deepseek-chat", "k-ds")
AN = ProviderConfig("anthropic", "anthropic/claude-sonnet-5-5", "k-an")
OA = ProviderConfig("openai", "openai/gpt-4.1", "k-oa")


def test_vision_candidates_honor_the_data_policy(monkeypatch):
    assert [c.id for c in vision_candidates([DS, AN, OA], ProviderPolicy())] == ["anthropic", "openai"]
    assert vision_candidates([DS, AN], ProviderPolicy(allowed=("deepseek",))) == []
    monkeypatch.setenv("ALLOWED_PROVIDERS", "deepseek,openai")  # operator ceiling
    assert [c.id for c in vision_candidates([DS, AN, OA], ProviderPolicy())] == ["openai"]
    monkeypatch.delenv("ALLOWED_PROVIDERS")
    with pytest.raises(ProviderError) as e:
        vision_candidates([DS, AN], ProviderPolicy(allowed=()))
    assert e.value.code == "no_allowed_provider"
    # byo_only: only the organization's own keys, even when the platform has a vision provider.
    pol = policy_from_request(req(provider_credentials=[{"provider": "deepseek", "api_key": "sk-own-1234567890"}], byo_only=True))
    assert vision_candidates([AN, OA], pol) == []


# ------------------------------------------------------------------ live engine with a fake LLM
class FakeLLM:
    """Stands in for crewai.LLM: records the kwargs and the messages, answers a valid TaskResult."""
    created: list = []
    calls: list = []
    fail_models: set = set()

    def __init__(self, **kw):
        self.kw = kw
        FakeLLM.created.append(kw)

    def call(self, messages):
        FakeLLM.calls.append((self.kw["model"], messages))
        if self.kw["model"] in FakeLLM.fail_models:
            raise RuntimeError("invalid model 404 not_found_error")
        return json.dumps({"output": {"summary": "Veo un grafico de barras", "confidence": 0.8}, "tool_requests": []})

    def get_token_usage_summary(self):
        class U:
            prompt_tokens, completion_tokens = 2100, 40
        return U()


def engine(providers):
    FakeLLM.created, FakeLLM.calls, FakeLLM.fail_models = [], [], set()
    eng = object.__new__(crewai_engine.CrewAIEngine)  # no CrewAI import needed
    eng._LLM, eng.providers, eng._llms = FakeLLM, list(providers), {}
    eng.provider, eng.model = providers[0].id, providers[0].model
    texts = []

    def text_once(cfg, **kw):  # the text-only path (Crew kickoff) is faked too
        texts.append((cfg.id, kw["description"]))
        return TaskResult(output=StructuredOutput(summary="solo texto", confidence=0.5)), Usage(
            model=cfg.model, input_tokens=10, output_tokens=5, cost_usd=0.0, duration_ms=1, provider=cfg.id)
    eng._kickoff_once = text_once
    return eng, texts


def test_images_go_to_the_first_vision_capable_allowed_provider():
    eng, texts = engine([DS, AN, OA])  # deepseek is first in order but cannot see
    resp = asyncio.run(eng.run_task(req([image("/Shared/grafico.png")])))
    assert resp.output.summary == "Veo un grafico de barras" and not texts
    assert [m for m, _ in FakeLLM.calls] == ["anthropic/claude-sonnet-5-5"]
    msgs = FakeLLM.calls[0][1]
    assert any(c.get("type") == "image_url" for c in msgs[1]["content"])
    assert resp.usage.provider == "anthropic" and resp.usage.image_count == 1 and resp.usage.image_tokens > 0
    assert resp.usage.input_tokens == 2100 and resp.usage.cost_usd > 0
    assert resp.notices is None
    assert FakeLLM.created[0]["api_key"] == "k-an"


def test_vision_failover_stays_among_allowed_vision_providers():
    eng, texts = engine([AN, DS, OA])
    FakeLLM.fail_models = {"anthropic/claude-sonnet-5-5"}
    resp = asyncio.run(eng.run_task(req([image()])))
    assert [m for m, _ in FakeLLM.calls] == ["anthropic/claude-sonnet-5-5", "openai/gpt-4.1"]
    assert resp.usage.provider == "openai" and resp.usage.failed_providers == ["anthropic"]
    assert not texts  # deepseek never got the images, nor a text fallback


@pytest.mark.parametrize("extra", [
    {"allowed_providers": ["deepseek"]},
    {"provider_credentials": [{"provider": "deepseek", "api_key": "sk-own-1234567890"}], "byo_only": True},
])
def test_no_allowed_vision_provider_goes_on_as_text(extra):
    eng, texts = engine([DS, AN])
    resp = asyncio.run(eng.run_task(req([image("/Shared/foto.png")], **extra)))
    assert FakeLLM.calls == []  # no image was sent anywhere
    assert resp.notices == [NOTICE_VISION_UNAVAILABLE]
    assert resp.output.summary == "solo texto" and texts[0][0] == "deepseek"
    assert "no pudiste verlas" in texts[0][1] and "/Shared/foto.png" in texts[0][1]


def test_without_images_nothing_changes():
    eng, texts = engine([DS, AN])
    resp = asyncio.run(eng.run_task(req()))
    assert FakeLLM.calls == [] and texts and resp.notices is None


# ------------------------------------------------------------------ HTTP layer (simulation)
def test_run_task_api_accepts_images_and_stays_backward_compatible(client):
    r = client.post("/v1/run-task", json={**run_req("legal"), "images": [image("/Shared/grafico.png")]})
    assert r.status_code == 200, r.text
    ev = " ".join(r.json()["output"]["evidence"])
    assert "grafico.png" in ev and "simulacion" in ev
    r = client.post("/v1/run-task", json=run_req("legal"))
    assert r.status_code == 200 and "notices" not in r.json()
    assert "imagen" not in " ".join(r.json()["output"]["evidence"]).lower()
