"""End-to-end behaviour sweep of the chat agents against a REAL backend + runtime (HTTP + WebSocket).

Not collected by pytest (no test_ prefix). Usage:

    # terminal 1: runtime            cd agent-runtime && .venv\\Scripts\\python -m uvicorn app.main:app --port 8400
    # terminal 2: backend (in memory) PORT=8490 RUNTIME_URL=http://127.0.0.1:8400 AUTH_ENABLED=false \\
    #                                  ENABLE_DEMO_RESET=true CHAT_STAGGER=50ms  go run ./cmd/server
    # terminal 3:                     python tests/behavior_sweep.py --base http://127.0.0.1:8490 [--only routing,limits]

It resets the demo org between groups, prints a pass-rate per group and every failure with the observed
route/messages, and exits non-zero when a hard invariant breaks. Scenarios that need infrastructure (runtime
down, tiny budget) are opt-in: --runtime-down-pid / --budget-base.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import re
import sys
import time
from collections import defaultdict

import httpx
import websockets

sys.path.insert(0, __file__.rsplit("\\", 1)[0] if "\\" in __file__ else __file__.rsplit("/", 1)[0])
import behavior_cases as bc  # noqa: E402

NAMES = {"sales": "Valeria", "hr": "Marcos", "legal": "Elena", "accounting": "Tomás", "analyst": "Nadia",
         "operations": "Iván", "assistant": "Sofía"}


class Turn:
    def __init__(self, turn_id: str, conv: str):
        self.id, self.conv = turn_id, conv
        self.events: list[dict] = []

    @property
    def route(self) -> dict:
        return next((e["payload"] for e in self.events if e["type"] == "route.decided"), {})

    @property
    def msgs(self) -> list[dict]:
        return [e["payload"] for e in self.events if e["type"] == "chat.message" and e["payload"]["from"] != "user"]

    def responders(self) -> list[str]:
        return [r["agent_id"] for r in self.route.get("responders", [])]

    def spoken(self) -> list[str]:
        return [m["from"] for m in self.msgs if m["kind"] in ("chat", "answer")]


class Sweep:
    def __init__(self, base: str):
        self.base = base.rstrip("/")
        self.http = httpx.AsyncClient(base_url=self.base + "/api/v1", timeout=30)
        self.turns: dict[str, Turn] = {}
        self.stream: list[dict] = []  # every event in arrival order
        self.results: dict[str, list[tuple[bool, str]]] = defaultdict(list)
        self.hard_failures = 0

    async def ws_loop(self):
        url = self.base.replace("http", "ws", 1) + "/ws"
        async with websockets.connect(url, max_size=None) as ws:
            self.ws_ready.set()
            async for raw in ws:
                ev = json.loads(raw)
                self.stream.append(ev)
                tid = (ev.get("payload") or {}).get("turn_id")
                if tid:
                    self.turns.setdefault(tid, Turn(tid, "")).events.append(ev)

    async def start(self):
        self.ws_ready = asyncio.Event()
        self.ws_task = asyncio.create_task(self.ws_loop())
        await asyncio.wait_for(self.ws_ready.wait(), 5)

    async def reset(self):
        await self.http.post("/demo/reset")
        await asyncio.sleep(0.2)

    async def say(self, text: str, conv: str = "office", wait: float = 0.8, cap: float = 12) -> Turn:
        r = await self.http.post("/messages", json={"conversation": conv, "text": text})
        if r.status_code != 202:
            raise RuntimeError(f"POST /messages -> {r.status_code} {r.text}")
        tid = r.json()["turn_id"]
        t = self.turns.setdefault(tid, Turn(tid, conv))
        t.conv = conv
        await self.settle(t, wait, cap)
        return t

    async def settle(self, t: Turn, quiet: float = 0.5, cap: float = 12):
        """Wait until the turn has a route, every typing is closed and nothing new arrives for `quiet` s."""
        start = time.monotonic()
        last_n, last_change = -1, time.monotonic()
        while time.monotonic() - start < cap:
            n = len(t.events)
            if n != last_n:
                last_n, last_change = n, time.monotonic()
            opened = sum(1 for e in t.events if e["type"] == "chat.typing" and e["payload"]["on"])
            closed = sum(1 for e in t.events if e["type"] == "chat.typing" and not e["payload"]["on"])
            if t.route and opened == closed and time.monotonic() - last_change >= quiet:
                return
            await asyncio.sleep(0.05)

    def check(self, group: str, ok: bool, label: str, detail: str = "", hard: bool = False):
        self.results[group].append((ok, f"{label} {detail}" if not ok else label))
        if not ok and hard:
            self.hard_failures += 1

    def report(self) -> int:
        total = fails = 0
        for g, rows in self.results.items():
            bad = [d for ok, d in rows if not ok]
            total += len(rows)
            fails += len(bad)
            print(f"\n== {g}: {len(rows) - len(bad)}/{len(rows)} ({100 * (len(rows) - len(bad)) / max(len(rows), 1):.1f}%)")
            for d in bad:
                print("   FAIL", d)
        print(f"\nTOTAL {total - fails}/{total} ({100 * (total - fails) / max(total, 1):.1f}%), hard failures: {self.hard_failures}")
        return 1 if self.hard_failures else 0


def _no_names(text: str) -> str:
    for n in ("Valeria Ríos", "Marcos Peña", "Elena Castro", "Tomás Vidal", "Nadia Ortega", "Iván Duarte", "Sofía Lara"):
        text = text.replace(n, "").replace(n.split()[0], "")
    return text


def brief(t: Turn) -> str:
    return f"intent={t.route.get('intent')} topic={t.route.get('topic')} src={t.route.get('source')} resp={t.responders()} consult={(t.route.get('consult') or {}).get('agent_id')} msgs={[(m['from'], m['text'][:70]) for m in t.msgs]}"


# ------------------------------------------------------------------ groups
async def g_routing(s: Sweep):
    await s.reset()
    for text in bc.GREETINGS:
        t = await s.say(text)
        r = t.responders()
        s.check("routing/greetings", t.route.get("intent") == "smalltalk" and r[:1] == ["assistant"] and len(r) <= 3, text, brief(t))
    for text in bc.THANKS + bc.HELP:
        t = await s.say(text)
        s.check("routing/thanks+help", t.route.get("intent") == "smalltalk" and t.responders() == ["assistant"], text, brief(t))
    for text, role in bc.TOPIC_QUESTIONS:
        t = await s.say(text)
        r = t.responders()
        s.check("routing/topic", t.route.get("intent") == "question" and r[:1] == [role] and len(r) <= 2, text, brief(t))
    for text in bc.AMBIGUOUS:
        t = await s.say(text)
        s.check("routing/ambiguous", t.responders() == ["assistant"], text, brief(t))
    for text, role in bc.TASKS:
        t = await s.say(text, wait=0.4)
        s.check("routing/tasks", t.route.get("intent") == "task" and len(t.responders()) == 1, text, brief(t))
    for text, role in bc.NOT_TASKS:
        t = await s.say(text)
        s.check("routing/not-tasks", t.route.get("intent") == "question" and t.responders()[:1] == [role], text, brief(t))
    for text, role in bc.NAMED:
        t = await s.say(text)
        s.check("routing/named", t.responders() == [role], text, brief(t))
    for ag, text in bc.DIRECT_OWN:
        t = await s.say(text, f"agent:{ag}")
        s.check("routing/1:1 own role", t.responders() == [ag] and not t.route.get("consult") and t.spoken() == [ag], f"{ag}: {text}", brief(t), hard=True)
    for ag, text, owner in bc.DIRECT_FOREIGN:
        t = await s.say(text, f"agent:{ag}")
        named = any(NAMES[owner] in m["text"] for m in t.msgs if m["from"] == ag and m["kind"] == "chat")
        s.check("routing/1:1 foreign", t.responders() == [ag] and (t.route.get("consult") or {}).get("agent_id") == owner and named,
                f"{ag}: {text}", brief(t), hard=True)
    for text in bc.EDGE_EMOJI:
        t = await s.say(text)
        s.check("routing/emoji+punct", len(t.responders()) >= 1 and len(t.msgs) >= 1, repr(text), brief(t), hard=True)


async def g_variety(s: Sweep):
    await s.reset()
    for text in ("hola", "buenas", "hola equipo"):
        replies: list[str] = []
        for _ in range(4):
            t = await s.say(text)
            replies.append(next((m["text"] for m in t.msgs if m["from"] == "assistant"), ""))
        s.check("variety/greeting rotates", len(set(replies)) >= 3, text, str(replies))
    for ag in ("accounting", "sales", "legal", "hr", "analyst", "operations"):
        q = {"accounting": "¿cómo va el balance?", "sales": "¿cómo van las ventas?", "legal": "¿tenemos riesgo con el contrato?",
             "hr": "¿cuántas vacantes hay?", "analyst": "¿qué dicen los datos?", "operations": "¿cómo va la capacidad?"}[ag]
        replies = []
        for _ in range(3):
            t = await s.say(q, f"agent:{ag}")
            replies.append(next((m["text"] for m in t.msgs if m["from"] == ag), ""))
        s.check("variety/same question 3x", len(set(replies)) == 3 and "" not in replies, f"{ag}", str(replies))
    # gendered or misattributed lines: each role answers in its own register and never claims another role's name
    t = await s.say("gracias")
    for ag in ("accounting", "legal", "sales", "hr", "analyst", "operations"):
        tt = await s.say("gracias", f"agent:{ag}")
        txt = " ".join(m["text"] for m in tt.msgs)
        s.check("variety/thanks has no gendered slip", not re.search(r"\b(atenta|lista)\b", txt) or ag in ("legal", "hr", "sales"), ag, txt)


async def g_handoff(s: Sweep):
    await s.reset()
    for ag, text, owner in bc.DIRECT_FOREIGN[:6]:
        conv = f"agent:{ag}"
        t1 = await s.say(text, conv)
        t2 = await s.say("sí, pásaselo", conv)
        ok = t2.route.get("source") == "handoff" and t2.responders() == [owner]
        s.check("handoff/accept", ok, f"{ag}->{owner}", brief(t2), hard=True)
        # after the handoff the offer is consumed: a plain "sí" must not hand off again
        t3 = await s.say("sí", conv)
        s.check("handoff/offer consumed", t3.route.get("source") != "handoff", f"{ag}", brief(t3))
    for acc in bc.ACCEPTANCES:
        await s.say("¿cómo van las ventas?", "agent:accounting")
        t = await s.say(acc, "agent:accounting")
        s.check("handoff/acceptance phrases", t.route.get("source") == "handoff", acc, brief(t))
    for rej in bc.NOT_ACCEPTANCES:
        await s.say("¿cómo van las ventas?", "agent:accounting")
        t = await s.say(rej, "agent:accounting")
        s.check("handoff/rejection phrases", t.route.get("source") != "handoff" and t.responders() == ["accounting"], rej, brief(t))


async def g_tasks(s: Sweep):
    await s.reset()
    for ag, text, owner in bc.DIRECT_FOREIGN_TASKS:
        before = {x["id"] for x in (await s.http.get("/tasks")).json()}
        t = await s.say(text, f"agent:{ag}", wait=1.5, cap=20)
        tasks: list[dict] = []
        for _ in range(60):  # the plan is created asynchronously after the chat turn
            tasks = [x for x in (await s.http.get("/tasks")).json() if x["id"] not in before]
            if tasks:
                break
            await asyncio.sleep(0.25)
        await asyncio.sleep(0.5)
        tasks = [x for x in (await s.http.get("/tasks")).json() if x["id"] not in before]
        moved = [x for x in tasks if x.get("assigned_reason", "").startswith(("Reasignada", "Reassigned"))]
        s.check("tasks/foreign reassigned", (t.route.get("consult") or {}).get("agent_id") == owner and bool(tasks) and
                all(x["agent_id"] != ag or "reasign" not in x.get("assigned_reason", "").lower() for x in tasks),
                f"{ag}: {text}", brief(t) + f" tasks={[(x['agent_id'], x.get('assigned_reason', '')[:50]) for x in tasks]}")
        s.check("tasks/every task has assigned_reason", all(x.get("assigned_reason") for x in tasks), f"{ag}: {text}")
        s.check("tasks/the declined agent keeps no unrelated task", not any(x["agent_id"] == ag and "reasign" in x.get("assigned_reason", "").lower() for x in tasks), f"{ag}: {text}")
        s.check("tasks/some work moved to the owner", any(x["agent_id"] == owner for x in tasks), f"{ag}->{owner}: {text}",
                str([(x["agent_id"], x.get("assigned_reason", "")[:40]) for x in tasks]))
    t = await s.say("hola, prepara el balance", wait=1.5, cap=20)
    s.check("tasks/mixed greeting+command is a task", t.route.get("intent") == "task", "hola, prepara el balance", brief(t))
    await asyncio.sleep(3)
    plan_msgs = [e["payload"]["text"] for e in s.stream if e["type"] == "chat.message" and e["payload"].get("request_id")]
    s.check("tasks/chat explains who does what and why", any("Por qué" in m for m in plan_msgs), "plan explanation", str(plan_msgs[:1]))


async def g_limits(s: Sweep):
    await s.reset()
    # paused agent: speaks in its own voice, no request created
    r = await s.http.post("/agents/accounting/control", json={"action": "pause", "drain": "immediate", "reason": "sweep"})
    t = await s.say("¿cómo va el balance?", "agent:accounting")
    s.check("limits/paused agent 1:1", r.status_code < 300 and any(m["from"] == "accounting" and "paus" in m["text"].lower() for m in t.msgs), "paused 1:1", brief(t), hard=True)
    t = await s.say("prepara el balance", "agent:accounting", wait=1)
    reqs = (await s.http.get("/requests")).json()
    s.check("limits/paused agent task creates nothing", not reqs and any("paus" in m["text"].lower() for m in t.msgs), "paused task", brief(t) + f" reqs={len(reqs)}", hard=True)
    t = await s.say("¿cómo va el balance y el contrato?")
    s.check("limits/paused owner in office is announced", any("paus" in m["text"].lower() for m in t.msgs), "paused office", brief(t))
    await s.http.post("/agents/accounting/control", json={"action": "resume", "reason": "sweep"})
    t = await s.say("¿cómo va el balance?", "agent:accounting")
    s.check("limits/resume works", any(m["from"] == "accounting" and "paus" not in m["text"].lower() for m in t.msgs), "resume", brief(t), hard=True)
    # read-only
    await s.http.post("/agents/sales/control", json={"action": "read_only", "reason": "sweep"})
    t = await s.say("prepara una propuesta para Acme", "agent:sales", wait=1.5, cap=15)
    s.check("limits/read-only task still announces the limit", any("lectura" in m["text"].lower() for m in t.msgs), "read_only", brief(t))
    await s.http.post("/agents/sales/control", json={"action": "read_write", "reason": "sweep"})
    # kill switch
    reqs_before = len((await s.http.get("/requests")).json())
    r = await s.http.post("/org/controls/kill-switch", json={"level": "freeze", "reason": "sweep"})
    for conv, text in (("office", "hola"), ("agent:legal", "revisa el contrato"), ("agent:hr", "hola")):
        t = await s.say(text, conv, wait=0.8)
        who = conv.split(":")[1] if ":" in conv else "assistant"
        s.check("limits/kill switch", r.status_code < 300 and any(m["from"] == who and ("emergencia" in m["text"].lower() or "pausa" in m["text"].lower()) for m in t.msgs)
                and len(t.msgs) == 1, f"{conv}: {text}", brief(t), hard=True)
    reqs = (await s.http.get("/requests")).json()
    s.check("limits/kill switch creates no request", len(reqs) == reqs_before, "no requests", f"{reqs_before}->{len(reqs)}", hard=True)
    await s.http.post("/org/controls/release", json={"reason": "sweep", "resume_connections": "all"})


async def g_robust(s: Sweep):
    await s.reset()
    mark = len(s.stream)
    h = s.http
    s.check("robust/empty text -> 400", (await h.post("/messages", json={"text": ""})).status_code == 400, "empty", hard=True)
    s.check("robust/blank text -> 400", (await h.post("/messages", json={"text": "  \n\t"})).status_code == 400, "blank", hard=True)
    s.check("robust/missing text -> 400", (await h.post("/messages", json={})).status_code == 400, "missing", hard=True)
    r = await h.post("/messages", json={"text": bc.EDGE_TOO_LONG})
    s.check("robust/too long -> 400", r.status_code == 400, "4001 chars", r.text, hard=True)
    r = await h.post("/messages", json={"text": bc.EDGE_LONG_OK})
    s.check("robust/4000 chars ok", r.status_code == 202, "3999 chars", r.text)
    r = await h.post("/messages", json={"text": "hola", "conversation": "agent:nope"})
    s.check("robust/unknown agent -> 404", r.status_code == 404, "agent:nope", r.text, hard=True)
    r = await h.post("/messages", json={"text": "hola", "conversation": "foo"})
    s.check("robust/bad conversation -> 400", r.status_code == 400, "foo", r.text, hard=True)
    r = await h.post("/messages", json={"text": "hola", "conversation": "agent:"})
    s.check("robust/bare agent: -> 400", r.status_code == 400, "agent:", r.text, hard=True)
    r = await h.post("/messages", content="{not json", headers={"Content-Type": "application/json"})
    s.check("robust/invalid json -> 400", r.status_code == 400, "bad json", r.text, hard=True)
    r = await h.post("/messages", json={"text": 123})
    s.check("robust/non-string text -> 400", r.status_code == 400, "int text", r.text, hard=True)
    r = await h.post("/messages", content=json.dumps({"text": "x" * 2_000_000}), headers={"Content-Type": "application/json"})
    s.check("robust/huge body -> 413", r.status_code == 413, "2MB body", str(r.status_code), hard=True)
    r = await h.post("/messages", json={"text": "hola", "conversation": "office", "extra": 1})
    s.check("robust/unknown field tolerated", r.status_code == 202, "extra field", r.text)
    # emoji / odd unicode / control chars
    for text in bc.EDGE_EMOJI + bc.EDGE_INJECTION + ["ｈｏｌａ", "H O L A", "hola\u200b", "\u202ehola", "¿¿¿???", "a\x00b hola"]:
        r = await h.post("/messages", json={"text": text})
        s.check("robust/odd inputs accepted or cleanly refused", r.status_code in (202, 400), repr(text)[:40], f"{r.status_code} {r.text}", hard=True)
    await asyncio.sleep(2)
    # injection: the stored/echoed text must not leak secrets or follow instructions
    for text in bc.EDGE_INJECTION[:2]:
        t = await s.say(text)
        joined = " ".join(m["text"] for m in t.msgs).lower()
        s.check("robust/injection not obeyed", "system prompt" not in joined and "sk-" not in joined, text[:40], joined[:200], hard=True)
    # concurrency: 12 messages fired at once -> 12 202s, 12 routed turns, each with at least one reply
    async def fire(i):
        r = await h.post("/messages", json={"text": f"hola número {i}" if i % 2 else f"¿cómo va el balance? {i}"})
        return r
    rs = await asyncio.gather(*[fire(i) for i in range(12)])
    s.check("robust/concurrent all accepted", all(r.status_code == 202 for r in rs), "12 concurrent", str([r.status_code for r in rs]), hard=True)
    ids = [r.json()["turn_id"] for r in rs if r.status_code == 202]
    for tid in ids:
        await s.settle(s.turns.setdefault(tid, Turn(tid, "office")), 0.8, 20)
    ok = [bool(s.turns[i].route) and len(s.turns[i].msgs) >= 1 for i in ids]
    s.check("robust/concurrent every turn answered", all(ok), "12 turns", f"{sum(ok)}/{len(ids)} answered", hard=True)
    # event integrity over the whole stream
    seen_ids, dup = set(), []
    for ev in s.stream:
        if ev["type"] == "chat.message":
            i = ev["payload"]["id"]
            if i in seen_ids:
                dup.append(i)
            seen_ids.add(i)
    s.check("events/no duplicated chat.message ids", not dup, "dup ids", str(dup), hard=True)
    bad_order = []
    window = {e["payload"]["turn_id"] for e in s.stream[mark:] if (e.get("payload") or {}).get("turn_id")}
    for tid, t in s.turns.items():
        if tid not in window:
            continue
        evs = t.events
        if not evs:
            continue
        types = [e["type"] for e in evs]
        if "route.decided" in types:
            ri = types.index("route.decided")
            early = [e for e in evs[:ri] if e["type"] in ("chat.typing",) or (e["type"] == "chat.message" and e["payload"]["from"] != "user")]
            if early:
                bad_order.append((tid, "agent event before route.decided"))
        users = [i for i, e in enumerate(evs) if e["type"] == "chat.message" and e["payload"]["from"] == "user"]
        if users and "route.decided" in types and users[0] > types.index("route.decided"):
            bad_order.append((tid, "user message after route"))
        depth = defaultdict(int)
        for e in evs:
            if e["type"] == "chat.typing":
                a = e["payload"]["agent_id"]
                depth[a] += 1 if e["payload"]["on"] else -1
                if depth[a] < 0 or depth[a] > 1:
                    bad_order.append((tid, f"typing unbalanced for {a}"))
            if e["type"] == "chat.message" and e["payload"]["from"] not in ("user", "system") and not e["payload"].get("request_id") \
                    and t.route.get("intent") != "task":
                if depth[e["payload"]["from"]] != 1 and e["payload"]["kind"] in ("chat", "answer"):
                    bad_order.append((tid, f"message from {e['payload']['from']} without typing on"))
        if any(v != 0 for v in depth.values()):
            bad_order.append((tid, f"typing left open {dict(depth)}"))
    s.check("events/typing/message/route order", not bad_order, "order", str(bad_order[:5]), hard=True)
    # no lost messages: every stored message was announced and vice versa
    stored = {m["id"] for conv in ("office",) for m in (await h.get(f"/conversations/{conv}/messages")).json()}
    announced = {ev["payload"]["id"] for ev in s.stream[mark:] if ev["type"] == "chat.message" and ev["payload"]["conversation"] == "office"}
    s.check("events/no lost or phantom messages", stored == announced, "stored vs events", f"missing_events={len(stored - announced)} phantom={len(announced - stored)}", hard=True)
    # history is chronological
    hist = (await h.get("/conversations/office/messages")).json()
    s.check("events/history chronological", [m["ts"] for m in hist] == sorted(m["ts"] for m in hist), "ts sorted")


async def g_idempotency(s: Sweep):
    await s.reset()
    h = s.http
    key = f"sweep-{time.time_ns()}"
    r1 = await h.post("/messages", json={"text": "hola"}, headers={"Idempotency-Key": key})
    r2 = await h.post("/messages", json={"text": "hola"}, headers={"Idempotency-Key": key})
    s.check("idempotency/same key returns the first turn", r1.status_code == r2.status_code == 202 and r1.json() == r2.json()
            and r2.headers.get("idempotent-replayed") == "true", "Idempotency-Key", f"{r1.text} {r2.text}", hard=True)
    r3 = await h.post("/messages", json={"text": "hola", "client_message_id": key + "b"})
    r4 = await h.post("/messages", json={"text": "hola", "client_message_id": key + "b"})
    s.check("idempotency/client_message_id works too", r3.json() == r4.json(), "client_message_id")
    await asyncio.sleep(1.5)
    both = await asyncio.gather(*[h.post("/messages", json={"text": "gracias"}, headers={"Idempotency-Key": key + "c"}) for _ in range(6)])
    s.check("idempotency/6 simultaneous double clicks -> one turn", len({r.json()["turn_id"] for r in both}) == 1, "race", str([r.json() for r in both]), hard=True)
    await asyncio.sleep(1.5)
    users = [m for m in (await h.get("/conversations/office/messages")).json() if m["from"] == "user"]
    s.check("idempotency/stored once", len(users) == 3, "user messages", str([m["text"] for m in users]), hard=True)
    r = await h.post("/messages", json={"text": "hola"}, headers={"Idempotency-Key": "k" * 200})
    s.check("idempotency/oversized key -> 400", r.status_code == 400, "long key", r.text)
    # control characters never reach the team
    r = await h.post("/messages", json={"text": "ho\x00la\u202e \x1b[31mrojo"})
    await asyncio.sleep(1.2)
    texts = [m["text"] for m in (await h.get("/conversations/office/messages")).json() if m["from"] == "user"]
    s.check("sanitize/stored text has no control or bidi characters", texts[-1] == "hola [31mrojo", "sanitize", repr(texts[-1]), hard=True)
    r = await h.post("/messages", json={"text": "\x00\u200b\u202e"})
    s.check("sanitize/invisible-only text -> 400", r.status_code == 400, "invisible", r.text, hard=True)
    r = await h.post("/conversations/office/messages", json={"text": "x" * 5000})
    s.check("legacy endpoint caps the length", r.status_code in (400, 404), "legacy 5000", f"{r.status_code} {r.text[:80]}")


async def g_context(s: Sweep):
    await s.reset()
    t = await s.say("¿cómo va el balance?")
    t2 = await s.say("¿y eso cómo se explica?")
    s.check("context/follow-up goes to the same owner", t2.responders()[:1] == ["accounting"], "y eso cómo se explica", brief(t2))
    t3 = await s.say("gracias")
    s.check("context/thanks go to the one who answered", t3.responders()[:1] == ["accounting"], "gracias after a finance answer", brief(t3))
    t = await s.say("¿cómo van las ventas?")
    t2 = await s.say("¿y por qué?")
    s.check("context/pronoun follow-up keeps the last speaker", t2.responders()[:1] == ["sales"], "¿y por qué?", brief(t2))
    t2 = await s.say("y cuánto es?", "agent:operations")
    s.check("context/1:1 follow-up stays", t2.responders() == ["operations"], "1:1 y cuánto es", brief(t2))
    t = await s.say("Tomás, ¿cómo va el balance?")
    t2 = await s.say("¿y los costos?")
    s.check("context/named colleague then follow-up", t2.responders()[:1] == ["accounting"], "y los costos", brief(t2))


async def g_tone_locale(s: Sweep):
    await s.reset()
    await s.http.put("/org/settings", json={"tone": "ar"})
    t = await s.say("hola", "agent:accounting")
    txt = " ".join(m["text"] for m in t.msgs)
    s.check("tone/ar uses voseo in the greeting or stays neutral", not re.search(r"\b(dime|cuéntame)\b", txt), "ar", txt)
    await s.http.put("/org/settings", json={"tone": "neutral", "locale": "en"})
    for text, conv in (("hello", "office"), ("how's the budget looking?", "office"), ("thanks", "agent:sales"), ("how are sales going?", "agent:legal")):
        t = await s.say(text, conv)
        txt = _no_names(" ".join(m["text"] for m in t.msgs))
        s.check("locale/en replies in English", not re.search(r"[áéíóúñ¿¡]|\b(hola|gracias|dime|puedes|nuestro)\b", txt, re.I), f"{conv}: {text}", txt)
    await s.http.put("/org/settings", json={"locale": "es"})
    for text, conv in (("hello", "office"), ("thanks", "agent:sales"), ("how are sales going?", "agent:legal"), ("what is our cash flow?", "office")):
        t = await s.say(text, conv)
        txt = _no_names(" ".join(m["text"] for m in t.msgs))
        s.check("locale/es org, English message -> reply in English", not re.search(r"[áéíóúñ¿¡]|\b(hola|gracias|dime|puedes)\b", txt, re.I), f"{conv}: {text}", txt)
    await s.http.put("/org/settings", json={"tone": "neutral", "locale": "es"})


async def g_budget(s: Sweep):
    """Backend started with a tiny BUDGET_USD: every voice says so in its own role's words, nothing is created."""
    await s.reset()
    for conv, who in (("office", "assistant"), ("agent:sales", "sales"), ("agent:legal", "legal"), ("agent:hr", "hr")):
        t = await s.say("hola", conv, wait=1.0)
        said = [m for m in t.msgs if m["from"] == who]
        s.check("budget/the asked agent says the cap is reached", bool(said) and any(w in said[0]["text"].lower() for w in ("tope", "presupuesto", "gasto")),
                f"{conv}", brief(t), hard=True)
    s.check("budget/no request was created", not (await s.http.get("/requests")).json(), "no requests")


async def g_runtime_down(s: Sweep, runtime_url: str | None):
    """Runs against a backend whose runtime is unreachable (start it with RUNTIME_URL=http://127.0.0.1:1)."""
    await s.reset()
    for text, conv, who in (("hola", "office", "assistant"), ("¿cómo va el balance?", "office", "accounting"),
                            ("¿cómo van las ventas?", "agent:accounting", "accounting"), ("gracias", "agent:hr", "hr")):
        t = await s.say(text, conv, wait=1, cap=40)
        s.check("runtime-down/answers anyway", t.route.get("source") == "local" and who in t.spoken(), f"{conv}: {text}", brief(t), hard=True)


GROUPS = {"routing": g_routing, "variety": g_variety, "handoff": g_handoff, "tasks": g_tasks, "limits": g_limits,
          "robust": g_robust, "idempotency": g_idempotency, "context": g_context, "tone": g_tone_locale}


async def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8490")
    ap.add_argument("--only", default="")
    ap.add_argument("--runtime-down", action="store_true", help="backend is pointed at a dead runtime: run only that group")
    ap.add_argument("--budget", action="store_true", help="backend started with a tiny BUDGET_USD: run only the budget group")
    a = ap.parse_args()
    s = Sweep(a.base)
    await s.start()
    try:
        if a.runtime_down:
            await g_runtime_down(s, None)
        elif a.budget:
            await g_budget(s)
        else:
            for name, fn in GROUPS.items():
                if a.only and name not in a.only.split(","):
                    continue
                try:
                    await fn(s)
                except Exception as e:  # a crashing group is a finding, not a reason to stop
                    s.check(f"{name}/harness", False, "exception", repr(e), hard=True)
    finally:
        s.ws_task.cancel()
    sys.exit(s.report())


if __name__ == "__main__":
    asyncio.run(main())
