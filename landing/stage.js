/*
 * Hero live office: an illustrative animation of the $50,000 scenario in the app's visual
 * language (frontend/src/components/office): initial + state chips, arcs per link kind with a
 * traveling dot, speech bubbles, "live activity" feed, the request bar and a demo pointer.
 * It is a fixed script, not real data. No dependencies.
 *
 * The script is a timeline that can be entered at any point (seek), which powers the chapter
 * buttons under the stage. With prefers-reduced-motion it renders a still frame (approval
 * pending) and the chapters switch between still frames; nothing loops.
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
  var TTL = 7000, DOT_MS = 1100, LOOP_MS = 26000, STILL_AT = 14400, TYPE_MS = 26;
  // Chapters: start time of each one (ms into the loop).
  var CH = [0, 3000, 7200, 13400, 18200];

  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var svg = stage.querySelector(".st-links");
  var feed = stage.querySelector(".st-feed ol");
  var input = stage.querySelector(".st-cmd .st-input");
  var sendBtn = stage.querySelector(".st-cmd .st-send");
  var card = stage.querySelector(".st-appr");
  var report = stage.querySelector(".st-report");
  var bubbles = stage.querySelector(".st-bubbles");
  var cursor = stage.querySelector(".st-cursor");
  var frameEl = stage.closest("figure") || document;
  var toggle = frameEl.querySelector("[data-stage-toggle]");
  var chapters = Array.prototype.slice.call(frameEl.querySelectorAll("[data-ch]"));
  var Sound = window.StageSound || { sfx: function () {}, hold: function () {} };
  var counters = { active: frameEl.querySelector("[data-count=active]"), approvals: frameEl.querySelector("[data-count=approvals]") };

  function lang() { return document.documentElement.lang === "en" ? "en" : "es"; }
  function t(key, vars) {
    var D = window.I18N || {}, s = (D[lang()] || {})[key];
    if (s == null) s = (D.es || {})[key] || "";
    for (var k in vars || {}) s = s.replace("{" + k + "}", vars[k]);
    return s;
  }
  function agent(id) { return stage.querySelector('.ag[data-id="' + id + '"]'); }

  // ---- layout helpers: positions from offsets (not getBoundingClientRect), so a CSS transform on the
  // frame (the scroll tilt) never skews the arcs ----
  function offsetIn(el) {
    var x = 0, y = 0;
    while (el && el !== stage) { x += el.offsetLeft; y += el.offsetTop; el = el.offsetParent; }
    return { x: x, y: y };
  }
  function narrow() { return stage.clientWidth <= 620; }

  // Anchor: the character's head (or the "You" badge), in px relative to the stage.
  function anchor(id) {
    if (id === "user") {
      var you = stage.querySelector(".st-you"), p = offsetIn(you);
      return { x: p.x + you.offsetWidth / 2, y: p.y + you.offsetHeight * 0.3 };
    }
    var ag = agent(id), ch = ag.querySelector(".char"), o = offsetIn(ag);
    return { x: o.x, y: o.y + ch.offsetHeight * 0.13 };
  }

  // ---- visible effects ----

  function setCount(el, v) {
    if (!el || el.textContent === String(v)) return;
    el.textContent = String(v);
    el.classList.remove("bump"); void el.offsetWidth; el.classList.add("bump");
  }

  function setState(id, st) {
    var el = agent(id);
    if (!el) return;
    el.setAttribute("data-state", st);
    var pill = el.querySelector(".st");
    pill.textContent = st === "idle" ? "" : t("state." + st);
    pill.style.setProperty("--s", STATE[st] || STATE.idle);
    setCount(counters.active, stage.querySelectorAll(".ag:not([data-state=idle]):not([data-state=completed])").length);
  }

  function arcPath(a, b) {
    var lift = 24 + Math.hypot(b.x - a.x, b.y - a.y) * 0.18;
    return "M" + a.x + " " + a.y + " Q" + (a.x + b.x) / 2 + " " + (Math.min(a.y, b.y) - lift) + " " + b.x + " " + b.y;
  }

  var NS = "http://www.w3.org/2000/svg";
  var frozen = false; // still frames: nothing fades or animates
  // Example agents are hidden on narrow screens: their traffic is skipped there.
  function shown(id) { return id === "user" || (agent(id) && agent(id).offsetParent !== null); }

  // age: ms since this event happened (0 = now). Old enough events are skipped; younger ones are drawn complete.
  function link(from, to, kind, text, age) {
    if (!shown(from) || !shown(to)) return;
    if (age >= TTL) return;
    var color = KIND[kind] || KIND.chat;
    var g = document.createElementNS(NS, "g");
    var path = document.createElementNS(NS, "path");
    var end = document.createElementNS(NS, "circle");
    var dot = document.createElementNS(NS, "circle");
    g.setAttribute("class", "lk");
    g.style.color = color;
    path.setAttribute("d", arcPath(anchor(from), anchor(to)));
    end.setAttribute("r", "3.2");
    dot.setAttribute("r", "4.5");
    dot.setAttribute("class", "dot");
    g.appendChild(path); g.appendChild(end);
    g._ends = [from, to];
    svg.appendChild(g);
    var len = path.getTotalLength(), p = path.getPointAtLength(len);
    end.setAttribute("cx", p.x); end.setAttribute("cy", p.y);
    if (age === 0 && !frozen) {
      // Draw the arc behind a traveling dot; the end point appears when the dot lands.
      g.appendChild(dot);
      path.style.strokeDasharray = len + " " + len;
      path.style.strokeDashoffset = String(len);
      end.style.opacity = "0";
      var t0 = performance.now();
      (function step(now) {
        if (!g.isConnected) return;
        var k = Math.min(1, (now - t0) / DOT_MS), e = 1 - Math.pow(1 - k, 2.2), q = path.getPointAtLength(len * e);
        dot.setAttribute("cx", q.x); dot.setAttribute("cy", q.y);
        path.style.strokeDashoffset = String(len * (1 - e));
        if (k < 1) requestAnimationFrame(step);
        else { dot.remove(); path.style.strokeDasharray = ""; path.style.strokeDashoffset = ""; end.style.opacity = ""; }
      })(t0);
    }
    if (!frozen) {
      later(function () { g.classList.add("out"); }, Math.max(0, TTL - 1500 - age));
      later(function () { g.remove(); }, TTL - age);
    }
    if (text) bubble(from, kind, text, age);
    if (kind !== "chat") sfx(kind, age);
  }

  // One bubble per sender (the latest), like LabelLayer.
  function bubble(from, kind, text, age) {
    var prev = bubbles.querySelector('[data-from="' + from + '"]');
    if (prev) prev.remove();
    var max = narrow() ? 1 : 3;
    while (bubbles.children.length >= max) bubbles.firstChild.remove();
    var b = document.createElement("div");
    var a = clampX(anchor(from));
    b.className = "bb" + (age === 0 && !frozen ? " pop" : "");
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
    placeBubble(b, a);
    if (!frozen) {
      later(function () { b.classList.add("out"); }, Math.max(0, TTL - 1200 - age));
      later(function () { b.remove(); }, TTL - age);
    }
  }
  // Flip a bubble below its anchor when it would leave the top of the stage.
  function placeBubble(b, a) {
    var lift = stage.clientWidth * (narrow() ? 0.154 : 0.096);
    b.classList.toggle("below", a.y - lift - b.offsetHeight < 4);
  }

  // Keep a bubble (about 17cqw wide, at least 130px) inside the stage.
  function clampX(a) {
    var w = stage.clientWidth, half = Math.max(130, Math.min(200, w * 0.17)) / 2 + 6;
    return { x: Math.min(Math.max(a.x, half), w - half), y: a.y };
  }

  function log(text, age) {
    var li = document.createElement("li");
    if (age === 0 && !frozen) li.className = "pop";
    var time = document.createElement("time");
    time.textContent = new Date(Date.now() - age).toLocaleTimeString(lang() === "en" ? "en-US" : "es-MX", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    var span = document.createElement("span");
    span.textContent = text;
    li.appendChild(time); li.appendChild(span);
    feed.insertBefore(li, feed.firstChild);
    while (feed.children.length > 4) feed.lastChild.remove();
  }

  // Each character is scheduled from the same base (no drift), and sending cancels any that are left.
  var typing = 0;
  function typeRequest(age) {
    var full = t("stage.request"), run = ++typing;
    input.classList.add("typed");
    var i0 = frozen ? full.length : Math.min(full.length, Math.floor(age / TYPE_MS));
    input.textContent = full.slice(0, i0);
    for (var k = i0 + 1; k <= full.length; k++) {
      (function (k) {
        later(function () {
          if (run !== typing) return;
          input.textContent = full.slice(0, k);
          if (k % 3 === 0) Sound.sfx("type");
        }, (k - i0) * TYPE_MS - (age % TYPE_MS));
      })(k);
    }
  }
  function sendRequest() { typing++; input.textContent = ""; input.classList.remove("typed"); }

  function show(el, on) { el.classList.toggle("on", on); }
  function sfx(name, age) { if (!age && !frozen) Sound.sfx(name); }

  // Demo pointer: glides to an element of the scene and can click it.
  function pointTo(el, dx, dy) {
    if (!cursor || !el) return;
    var p = offsetIn(el);
    cursor.style.transform = "translate(" + (p.x + el.offsetWidth * (dx || 0.5)) + "px," + (p.y + el.offsetHeight * (dy || 0.6)) + "px)";
  }
  function cursorOn(on) { if (cursor) cursor.classList.toggle("on", on); }
  function click() { if (!cursor) return; cursor.classList.add("click"); later(function () { cursor.classList.remove("click"); }, 160); }
  function parkCursor() {
    if (!cursor) return;
    cursor.classList.remove("on", "click");
    cursor.style.transition = "none";
    cursor.style.transform = "translate(" + stage.clientWidth * 0.62 + "px," + stage.clientHeight * 1.02 + "px)";
    void cursor.offsetWidth;
    cursor.style.transition = "";
  }

  // ---- script (ms from start); texts follow the format of the orchestrator's real events ----
  // Every step receives `age` (0 when it happens live, >0 when it is replayed instantly after a seek).

  var assign = function (id, key) { return function (a) { link("assistant", id, "delegation", t("msg.assign", { t: t(key) }), a); setState(id, "working"); }; };
  var done = function (id, key) { return function (a) { setState(id, "completed"); log(t("ev.done", { n: NAMES[id], t: t(key) }), a); sfx("complete", a); }; };
  var live = function (fn) { return function (a) { if (!a && !frozen) fn(); }; }; // pointer moves: only when they happen live
  var SCRIPT = [
    [200, function (a) { typeRequest(a); }],
    [900, live(function () { parkCursor(); later(function () { cursorOn(true); pointTo(sendBtn, 0.5, 0.55); }, 40); })],
    [1950, function (a) {
      sendBtn.classList.add("press"); sendRequest();
      log(t("ev.received", { t: t("stage.request") }), a); setState("assistant", "thinking"); sfx("send", a);
      if (!a && !frozen) click();
    }],
    [2300, function () { sendBtn.classList.remove("press"); }],
    [2600, live(function () { cursorOn(false); })],
    [2700, function () { setState("support", "working"); }], // support keeps working its own inbox
    [3400, function (a) { log(t("ev.plan"), a); setState("assistant", "waiting"); }],
    [3900, assign("sales", "task.proposal")],
    [4400, assign("accounting", "task.margin")],
    [4900, assign("legal", "task.clauses")],
    [5400, assign("analyst", "task.scenarios")],
    [5900, assign("marketing", "task.pitch")],
    [6400, assign("procurement", "task.quote")],
    [7600, function (a) { setState("sales", "talking"); setState("legal", "talking"); link("sales", "legal", "consult", t("msg.q1"), a); }],
    [8400, function (a) { setState("marketing", "talking"); setState("data", "talking"); link("marketing", "data", "consult", t("msg.q3"), a); }],
    [9300, function (a) { link("legal", "sales", "answer", t("msg.a1"), a); setState("sales", "working"); setState("legal", "working"); }],
    [9900, function (a) { link("data", "marketing", "answer", t("msg.a3"), a); setState("marketing", "working"); }],
    [10300, done("legal", "task.clauses")],
    [10500, function () { setState("data", "idle"); }],
    [10700, function (a) { setState("accounting", "talking"); setState("analyst", "talking"); link("accounting", "analyst", "consult", t("msg.q2"), a); }],
    [11900, function (a) { link("analyst", "accounting", "answer", t("msg.a2"), a); setState("accounting", "working"); setState("analyst", "working"); }],
    [12400, done("accounting", "task.margin")],
    [12700, done("procurement", "task.quote")],
    [13000, done("marketing", "task.pitch")],
    [13300, done("analyst", "task.scenarios")],
    [13700, function (a) {
      setState("sales", "awaiting_approval");
      link("sales", "user", "chat", t("msg.ask"), a);
      log(t("ev.apprReq", { t: t("task.send") }), a);
      setCount(counters.approvals, 1);
      show(card, true);
      sfx("approval", a);
    }],
    [15300, live(function () { parkCursor(); later(function () { cursorOn(true); pointTo(card.querySelector(".yes"), 0.45, 0.55); }, 40); })],
    [16500, function (a) { card.classList.add("press"); if (!a && !frozen) { click(); sfx("click"); } }],
    [16850, function (a) {
      card.classList.remove("press"); card.classList.add("ok");
      log(t("ev.apprOk", { t: t("task.send") }), a);
      setCount(counters.approvals, 0);
      setState("sales", "working");
      sfx("approved", a);
    }],
    [17300, live(function () { cursorOn(false); })],
    [18200, function (a) { show(card, false); done("sales", "task.proposal")(a); link("sales", "assistant", "answer", t("msg.sent"), a); setState("assistant", "reviewing"); }],
    [20000, function (a) { setState("assistant", "completed"); log(t("ev.report"), a); show(report, true); sfx("report", a); }],
    [21200, function () { setState("support", "completed"); }],
    [LOOP_MS - 800, function (a) { if (!frozen) stage.classList.add("ending"); }]
  ];

  function reset() {
    stage.classList.remove("ending");
    stage.querySelectorAll(".ag").forEach(function (el) { setState(el.getAttribute("data-id"), "idle"); });
    svg.textContent = ""; bubbles.textContent = ""; feed.textContent = "";
    sendRequest();
    sendBtn.classList.remove("press");
    card.classList.remove("ok", "press"); show(card, false); show(report, false);
    setCount(counters.approvals, 0);
    if (cursor) cursor.classList.remove("on", "click");
  }

  // ---- pausable clock ----

  var timers = [], paused = false, clock = 0, last = 0, visible = true, manual = false, loopStart = 0;
  function later(fn, ms) { timers.push({ at: clock + ms, fn: fn }); }
  function frame(now) {
    if (!paused) clock += Math.min(100, now - last);
    last = now;
    var due = timers.filter(function (x) { return x.at <= clock; });
    timers = timers.filter(function (x) { return x.at > clock; });
    due.forEach(function (x) { x.fn(); });
    paintChapters(clock - loopStart);
    requestAnimationFrame(frame);
  }

  // Enter the timeline at `at` ms: replay what already happened instantly, schedule the rest.
  function seek(at) {
    timers = [];
    reset();
    loopStart = clock - at;
    SCRIPT.forEach(function (s) {
      if (s[0] <= at) s[1](at - s[0]);
      else later(function () { s[1](0); }, s[0] - at);
    });
    later(function () { seek(0); }, LOOP_MS - at);
  }

  // Still frame (reduced motion): the script applied up to `at`, frozen.
  function still(at) {
    frozen = true;
    timers = [];
    reset();
    SCRIPT.forEach(function (s) { if (s[0] <= at) s[1](at - s[0]); });
    frozen = false;
    paintChapters(at, true);
  }

  // ---- chapters ----
  function chapterAt(ms) { var i = 0; while (i + 1 < CH.length && ms >= CH[i + 1]) i++; return i; }
  var lastCh = -1;
  function paintChapters(ms, stillFrame) {
    if (!chapters.length) return;
    var i = chapterAt(ms);
    chapters.forEach(function (b, k) {
      var end = k + 1 < CH.length ? CH[k + 1] : LOOP_MS;
      var p = k < i ? 1 : k > i ? 0 : (stillFrame ? 1 : Math.max(0, Math.min(1, (ms - CH[k]) / (end - CH[k]))));
      b.firstElementChild.firstElementChild.style.transform = "scaleX(" + p.toFixed(3) + ")";
    });
    if (i !== lastCh) {
      chapters.forEach(function (b, k) { if (k === i) b.setAttribute("aria-current", "step"); else b.removeAttribute("aria-current"); });
      lastCh = i;
    }
  }
  // Still frame per chapter: the moment that best tells it.
  var STILLS = [1500, 6600, 11000, STILL_AT, 21000];
  chapters.forEach(function (b, k) {
    b.addEventListener("click", function () {
      if (reduce) { still(STILLS[k]); return; }
      seek(CH[k] + (k ? 150 : 0));
    });
  });

  function setPaused(p) {
    paused = p;
    Sound.hold(p);
    stage.classList.toggle("paused", p);
    if (toggle) {
      toggle.setAttribute("aria-pressed", String(manual));
      toggle.querySelector("span").textContent = t(manual ? "stage.play" : "stage.pause");
    }
  }

  // Arcs and bubbles use px: redraw them on resize.
  var rs;
  window.addEventListener("resize", function () {
    clearTimeout(rs);
    rs = setTimeout(function () {
      svg.querySelectorAll("g").forEach(function (g) {
        var p = g.querySelector("path");
        p.setAttribute("d", arcPath(anchor(g._ends[0]), anchor(g._ends[1])));
        var e = p.getPointAtLength(p.getTotalLength()), c = g.querySelector("circle");
        c.setAttribute("cx", e.x); c.setAttribute("cy", e.y);
      });
      bubbles.querySelectorAll(".bb").forEach(function (b) { var a = clampX(anchor(b.getAttribute("data-from"))); b.style.left = a.x + "px"; b.style.top = a.y + "px"; placeBubble(b, a); });
    }, 120);
  });

  // Language change: re-render the frame in the new language.
  document.addEventListener("langchange", function () {
    if (reduce) return still(STILL_AT);
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
    document.querySelectorAll("[data-sound]").forEach(function (b) { b.hidden = true; });
    return still(STILL_AT);
  }
  if (toggle) toggle.addEventListener("click", function () { manual = !manual; setPaused(manual || !visible); });
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (es) { visible = es[0].isIntersecting; setPaused(manual || !visible || document.hidden); }).observe(stage);
  }
  document.addEventListener("visibilitychange", function () { setPaused(manual || document.hidden || !visible); });
  last = performance.now();
  parkCursor();
  seek(0);
  requestAnimationFrame(frame);
})();
