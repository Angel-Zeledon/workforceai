/*
 * Hero live office: an illustrative animation of the $50,000 scenario in the app's visual
 * language (frontend/src/components/office): initial + state chips, arcs per link kind with a
 * traveling dot, speech bubbles, "live activity" feed and the request bar.
 * It is a fixed script, not real data. No dependencies. With prefers-reduced-motion it renders
 * a still frame (approval pending) and does not loop.
 */
(function () {
  "use strict";
  var stage = document.querySelector("[data-stage]");
  if (!stage) return;

  // Same values as the app: LINK_KIND (kinds.ts), STATE_META (meta.ts), TTLs of Links/LabelLayer.
  var KIND = { delegation: "#7461b5", consult: "#3f8aa3", answer: "#3d8f6b", chat: "#b8832a" };
  var STATE = {
    idle: "#66707f", thinking: "#6d5bb5", working: "#2b6cb0", waiting: "#7b8494", talking: "#2f8f6b",
    reviewing: "#4f5fb8", awaiting_approval: "#a86208", completed: "#2f7d55"
  };
  var NAMES = { sales: "Valeria Ríos", analyst: "Nadia Ortega", accounting: "Tomás Vidal", legal: "Elena Castro", assistant: "Sofía Lara",
    marketing: "Lucía Méndez", procurement: "Rafael Torres" };
  var TTL = 7000, DOT_MS = 1400, LOOP_MS = 24000, STILL_AT = 14200;

  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var svg = stage.querySelector(".st-links");
  var feed = stage.querySelector(".st-feed ol");
  var input = stage.querySelector(".st-cmd .st-input");
  var sendBtn = stage.querySelector(".st-cmd .st-send");
  var card = stage.querySelector(".st-appr");
  var report = stage.querySelector(".st-report");
  var bubbles = stage.querySelector(".st-bubbles");
  var toggle = document.querySelector("[data-stage-toggle]");
  var Sound = window.StageSound || { sfx: function () {}, hold: function () {} };
  function sfx(name, instant) { if (!instant) Sound.sfx(name); }
  var frameEl = stage.closest("figure") || document;
  var counters = { active: frameEl.querySelector("[data-count=active]"), approvals: frameEl.querySelector("[data-count=approvals]") };

  function lang() { return document.documentElement.lang === "en" ? "en" : "es"; }
  function t(key, vars) {
    var D = window.I18N || {}, s = (D[lang()] || {})[key];
    if (s == null) s = (D.es || {})[key] || "";
    for (var k in vars || {}) s = s.replace("{" + k + "}", vars[k]);
    return s;
  }
  function agent(id) { return stage.querySelector('.ag[data-id="' + id + '"]'); }

  // ---- visible effects ----

  function setState(id, st) {
    var el = agent(id);
    if (!el) return;
    el.setAttribute("data-state", st);
    var pill = el.querySelector(".st");
    pill.textContent = st === "idle" ? "" : t("state." + st);
    pill.style.setProperty("--s", STATE[st] || STATE.idle);
    counters.active.textContent = String(stage.querySelectorAll('.ag:not([data-state=idle]):not([data-state=completed])').length);
  }

  // Anchor point: the character's head, in px relative to the stage.
  function anchor(id) {
    var box = stage.getBoundingClientRect();
    var el = id === "user" ? stage.querySelector(".st-you") : agent(id).querySelector(".char");
    var r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2 - box.left, y: r.top + r.height * 0.25 - box.top };
  }

  function arcPath(a, b) {
    var lift = 24 + Math.hypot(b.x - a.x, b.y - a.y) * 0.18;
    return "M" + a.x + " " + a.y + " Q" + (a.x + b.x) / 2 + " " + (Math.min(a.y, b.y) - lift) + " " + b.x + " " + b.y;
  }

  var NS = "http://www.w3.org/2000/svg";
  var stillAt = -1; // script time being replayed by still(); -1 while live
  // Example agents are hidden on narrow screens: their traffic is skipped there.
  function shown(id) { return id === "user" || (agent(id) && agent(id).offsetParent !== null); }
  function link(from, to, kind, text, instant) {
    if (!shown(from) || !shown(to)) return;
    if (instant && stillAt >= 0 && stillAt < STILL_AT - TTL) return; // already faded at the still moment
    var color = KIND[kind] || KIND.chat;
    var g = document.createElementNS(NS, "g");
    var path = document.createElementNS(NS, "path");
    var dot = document.createElementNS(NS, "circle");
    var end = document.createElementNS(NS, "circle");
    g.setAttribute("class", "lk");
    g.style.color = color;
    path.setAttribute("d", arcPath(anchor(from), anchor(to)));
    dot.setAttribute("r", "5");
    end.setAttribute("r", "3.2");
    g.appendChild(path); g.appendChild(end); g.appendChild(dot);
    g._ends = [from, to];
    svg.appendChild(g);
    var len = path.getTotalLength(), p = path.getPointAtLength(len);
    end.setAttribute("cx", p.x); end.setAttribute("cy", p.y);
    if (instant) {
      dot.remove();
    } else {
      var t0 = performance.now();
      (function step(now) {
        var k = Math.min(1, (now - t0) / DOT_MS), q = path.getPointAtLength(len * k);
        dot.setAttribute("cx", q.x); dot.setAttribute("cy", q.y);
        if (k < 1 && g.isConnected) requestAnimationFrame(step); else dot.remove();
      })(t0);
      later(function () { g.classList.add("out"); }, TTL - 1500);
      later(function () { g.remove(); }, TTL);
    }
    if (text) bubble(from, kind, text, instant);
    if (kind !== "chat") sfx(kind, instant);
  }

  // One bubble per sender (the latest), like LabelLayer.
  function bubble(from, kind, text, instant) {
    var prev = bubbles.querySelector('[data-from="' + from + '"]');
    if (prev) prev.remove();
    var max = stage.clientWidth < 620 ? 1 : 3;
    while (bubbles.children.length >= max) bubbles.firstChild.remove();
    var b = document.createElement("div");
    var a = clampX(anchor(from));
    b.className = "bb" + (instant ? "" : " pop");
    b.setAttribute("data-from", from);
    b.style.setProperty("--k", from === "user" ? KIND.chat : (KIND[kind] || KIND.chat));
    b.style.left = a.x + "px";
    b.style.top = a.y + "px";
    var label = document.createElement("span");
    label.textContent = t("kind." + kind);
    var body = document.createElement("p");
    body.textContent = text;
    b.appendChild(label); b.appendChild(body);
    bubbles.appendChild(b);
    if (b.getBoundingClientRect().top < stage.getBoundingClientRect().top + 4) b.classList.add("below");
    if (!instant) {
      later(function () { b.classList.add("out"); }, TTL - 1200);
      later(function () { b.remove(); }, TTL);
    }
  }

  // Keep a bubble (about 17cqw wide, at least 130px) inside the stage.
  function clampX(a) {
    var w = stage.clientWidth, half = Math.max(130, Math.min(200, w * 0.17)) / 2 + 6;
    return { x: Math.min(Math.max(a.x, half), w - half), y: a.y };
  }

  function log(text, instant) {
    var li = document.createElement("li");
    if (!instant) li.className = "pop";
    var time = document.createElement("time");
    time.textContent = new Date().toLocaleTimeString(lang() === "en" ? "en-US" : "es-MX", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    var span = document.createElement("span");
    span.textContent = text;
    li.appendChild(time); li.appendChild(span);
    feed.insertBefore(li, feed.firstChild);
    while (feed.children.length > 4) feed.lastChild.remove();
  }

  function typeRequest(instant) {
    var full = t("stage.request");
    input.classList.add("typed");
    if (instant) { input.textContent = full; return; }
    var i = 0;
    (function tick() {
      input.textContent = full.slice(0, ++i);
      if (i % 3 === 0) Sound.sfx("type");
      if (i < full.length) later(tick, 28);
    })();
  }

  function show(el, on) { el.classList.toggle("on", on); }

  // ---- script (ms from start); texts follow the format of the orchestrator's real events ----

  var assign = function (id, key) { return function (i) { link("assistant", id, "delegation", t("msg.assign", { t: t(key) }), i); setState(id, "working"); }; };
  var done = function (id, key) { return function (i) { setState(id, "completed"); log(t("ev.done", { n: NAMES[id], t: t(key) }), i); sfx("complete", i); }; };
  var SCRIPT = [
    [200, function (i) { typeRequest(i); }],
    [1900, function (i) { sendBtn.classList.add("press"); input.textContent = ""; input.classList.remove("typed"); log(t("ev.received", { t: t("stage.request") }), i); setState("assistant", "thinking"); sfx("send", i); }],
    [2300, function () { sendBtn.classList.remove("press"); }],
    [2600, function () { setState("support", "working"); }], // support keeps working its own inbox
    [3400, function (i) { log(t("ev.plan"), i); setState("assistant", "waiting"); }],
    [3900, assign("sales", "task.proposal")],
    [4400, assign("accounting", "task.margin")],
    [4900, assign("legal", "task.clauses")],
    [5400, assign("analyst", "task.scenarios")],
    [5900, assign("marketing", "task.pitch")],
    [6400, assign("procurement", "task.quote")],
    [7600, function (i) { setState("sales", "talking"); setState("legal", "talking"); link("sales", "legal", "consult", t("msg.q1"), i); }],
    [8400, function (i) { setState("marketing", "talking"); setState("data", "talking"); link("marketing", "data", "consult", t("msg.q3"), i); }],
    [9200, function (i) { link("legal", "sales", "answer", t("msg.a1"), i); setState("sales", "working"); }],
    [9800, function (i) { link("data", "marketing", "answer", t("msg.a3"), i); setState("marketing", "working"); }],
    [10200, done("legal", "task.clauses")],
    [10400, function () { setState("data", "idle"); }],
    [10600, function (i) { setState("accounting", "talking"); setState("analyst", "talking"); link("accounting", "analyst", "consult", t("msg.q2"), i); }],
    [11900, function (i) { link("analyst", "accounting", "answer", t("msg.a2"), i); }],
    [12300, done("accounting", "task.margin")],
    [12600, done("procurement", "task.quote")],
    [12900, done("marketing", "task.pitch")],
    [13200, done("analyst", "task.scenarios")],
    [13600, function (i) {
      setState("sales", "awaiting_approval");
      link("sales", "user", "chat", t("msg.ask"), i);
      log(t("ev.apprReq", { t: t("task.send") }), i);
      counters.approvals.textContent = "1";
      show(card, true);
      sfx("approval", i);
    }],
    [16400, function () { card.classList.add("press"); sfx("click"); }],
    [16800, function (i) {
      card.classList.remove("press"); card.classList.add("ok");
      log(t("ev.apprOk", { t: t("task.send") }), i);
      counters.approvals.textContent = "0";
      setState("sales", "working");
      sfx("approved", i);
    }],
    [18000, function (i) { show(card, false); done("sales", "task.proposal")(i); link("sales", "assistant", "answer", t("msg.sent"), i); setState("assistant", "reviewing"); }],
    [19800, function (i) { setState("assistant", "completed"); log(t("ev.report"), i); show(report, true); sfx("report", i); }],
    [21000, function () { setState("support", "completed"); }]
  ];

  function reset() {
    stage.querySelectorAll(".ag").forEach(function (el) { setState(el.getAttribute("data-id"), "idle"); });
    svg.textContent = ""; bubbles.textContent = ""; feed.textContent = "";
    input.textContent = ""; input.classList.remove("typed");
    card.classList.remove("ok", "press"); show(card, false); show(report, false);
    counters.approvals.textContent = "0";
  }

  // ---- pausable clock ----

  var timers = [], paused = false, clock = 0, last = 0, visible = true, manual = false;
  function later(fn, ms) { timers.push({ at: clock + ms, fn: fn }); }
  function frame(now) {
    if (!paused) clock += Math.min(100, now - last);
    last = now;
    var due = timers.filter(function (x) { return x.at <= clock; });
    timers = timers.filter(function (x) { return x.at > clock; });
    due.forEach(function (x) { x.fn(); });
    requestAnimationFrame(frame);
  }
  function run() {
    reset();
    SCRIPT.forEach(function (s) { later(function () { s[1](false); }, s[0]); });
    later(run, LOOP_MS);
  }
  function setPaused(p) {
    paused = p;
    Sound.hold(p);
    stage.classList.toggle("paused", p);
    if (toggle) {
      toggle.setAttribute("aria-pressed", String(manual));
      toggle.querySelector("span").textContent = t(manual ? "stage.play" : "stage.pause");
    }
  }

  // Still frame: applies the script up to the pending approval, without animation.
  function still() {
    reset();
    SCRIPT.forEach(function (s) { if (s[0] <= STILL_AT) { stillAt = s[0]; s[1](true); } });
    stillAt = -1;
  }

  // Arcs use px: redraw them on resize.
  var rs;
  window.addEventListener("resize", function () {
    clearTimeout(rs);
    rs = setTimeout(function () {
      svg.querySelectorAll("g").forEach(function (g) { g.querySelector("path").setAttribute("d", arcPath(anchor(g._ends[0]), anchor(g._ends[1]))); var p = g.querySelector("path"), e = p.getPointAtLength(p.getTotalLength()); g.querySelector("circle").setAttribute("cx", e.x); g.querySelector("circle").setAttribute("cy", e.y); });
      bubbles.querySelectorAll(".bb").forEach(function (b) { var a = clampX(anchor(b.getAttribute("data-from"))); b.style.left = a.x + "px"; b.style.top = a.y + "px"; });
    }, 120);
  });

  // Language change: re-render the frame in the new language.
  document.addEventListener("langchange", function () {
    if (reduce) return still();
    stage.querySelectorAll(".ag").forEach(function (el) { setState(el.getAttribute("data-id"), el.getAttribute("data-state")); });
    setPaused(paused);
  });

  // Sound toggles (off by default; sound.js remembers the choice).
  document.querySelectorAll("[data-sound]").forEach(function (b) {
    if (!window.StageSound) { b.hidden = true; return; }
    var kind = b.getAttribute("data-sound");
    b.setAttribute("aria-pressed", String(window.StageSound.get()[kind]));
    b.addEventListener("click", function () {
      var on = b.getAttribute("aria-pressed") !== "true";
      b.setAttribute("aria-pressed", String(on));
      window.StageSound.set(kind, on);
    });
  });

  stage.classList.add("live");
  if (reduce) {
    if (toggle) toggle.hidden = true;
    return still();
  }
  if (toggle) toggle.addEventListener("click", function () { manual = !manual; setPaused(manual || !visible); });
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (es) { visible = es[0].isIntersecting; setPaused(manual || !visible); }).observe(stage);
  }
  document.addEventListener("visibilitychange", function () { setPaused(manual || document.hidden || !visible); });
  last = performance.now();
  run();
  requestAnimationFrame(frame);
})();
