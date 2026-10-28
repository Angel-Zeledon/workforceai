/* Language (ES/EN), CTAs and the page's motion. No dependencies, no analytics, no network.
 * Everything here is an enhancement: the baked HTML is complete and readable without JS. */
(function () {
  "use strict";
  var DICT = window.I18N, CFG = window.LANDING_CONFIG || {};
  var root = document.documentElement;
  root.classList.add("js");

  // The language is the one the page was written in: "/" is Spanish and "/en/" is English, two real URLs
  // (with hreflang) that the ES/EN links switch between. It is never guessed from the browser.
  function pickLang() { return root.lang === "en" ? "en" : "es"; }

  function applyCtas() {
    document.querySelectorAll("[data-cta]").forEach(function (a) {
      var kind = a.getAttribute("data-cta");
      if (kind === "signup") a.setAttribute("href", CFG.SIGNUP_URL || "#");
      if (kind === "login") a.setAttribute("href", CFG.LOGIN_URL || "#");
      if (kind === "trial") a.setAttribute("href", CFG.TRIAL_URL || "#");
      a.setAttribute("rel", "noopener");
    });
  }

  function t(lang, key) {
    var d = DICT[lang] || {};
    return d[key] != null ? d[key] : (DICT.es[key] != null ? DICT.es[key] : "");
  }

  function apply(lang) {
    root.lang = lang;
    document.querySelectorAll("[data-i18n]").forEach(function (el) { el.textContent = t(lang, el.getAttribute("data-i18n")); });
    document.querySelectorAll("[data-i18n-html]").forEach(function (el) { el.innerHTML = t(lang, el.getAttribute("data-i18n-html")); });
    ["alt", "aria", "title"].forEach(function (kind) {
      var attr = kind === "aria" ? "aria-label" : kind;
      document.querySelectorAll("[data-i18n-" + kind + "]").forEach(function (el) {
        el.setAttribute(attr, t(lang, el.getAttribute("data-i18n-" + kind)));
      });
    });
    document.querySelectorAll("[data-i18n-attr]").forEach(function (el) {
      el.getAttribute("data-i18n-attr").split(",").forEach(function (pair) {
        var p = pair.split(":");
        el.setAttribute(p[0], t(lang, p[1]));
      });
    });
    applyCtas();
    document.dispatchEvent(new Event("langchange"));
  }

  if (DICT) apply(pickLang()); else applyCtas();

  var still = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var hasIO = "IntersectionObserver" in window;

  // ---- Scroll-in reveals with staggered children ----
  document.querySelectorAll("[data-stagger]").forEach(function (list) {
    Array.prototype.forEach.call(list.children, function (c, i) { c.style.setProperty("--i", i); });
  });
  document.querySelectorAll(".flow > li").forEach(function (c, i) { c.style.setProperty("--i", i); });
  if (!still && hasIO) {
    root.classList.add("motion");
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
      });
    }, { rootMargin: "0px 0px -10% 0px", threshold: 0.08 });
    document.querySelectorAll(".reveal").forEach(function (el) { io.observe(el); });
  }

  // ---- Header: solid once the page scrolls ----
  var header = document.querySelector("[data-header]");
  if (header) {
    var ticking = false;
    var onScroll = function () {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(function () { header.classList.toggle("scrolled", window.scrollY > 8); ticking = false; });
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
  }

  // ---- Active section in the desktop nav, with a sliding rail ----
  var nav = document.querySelector("[data-nav]");
  if (nav && hasIO) {
    var ind = nav.querySelector(".nav-ind");
    var links = {};
    nav.querySelectorAll('a[href^="#"]').forEach(function (a) { links[a.getAttribute("href").slice(1)] = a; });
    var current = null;
    var move = function (a) {
      if (current) { current.classList.remove("on"); current.removeAttribute("aria-current"); }
      current = a || null;
      if (!ind) return;
      if (!a) { ind.style.opacity = "0"; return; }
      a.classList.add("on");
      a.setAttribute("aria-current", "location");
      ind.style.opacity = "1";
      ind.style.transform = "translateX(" + (a.offsetLeft + 10) + "px) scaleX(" + ((a.offsetWidth - 20) / 100) + ")";
    };
    var visible = {};
    var sio = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { visible[e.target.id] = e.isIntersecting; });
      var pick = null;
      Object.keys(links).forEach(function (id) { if (visible[id] && !pick) pick = id; });
      move(pick ? links[pick] : null);
    }, { rootMargin: "-40% 0px -55% 0px" });
    Object.keys(links).forEach(function (id) { var s = document.getElementById(id); if (s) sio.observe(s); });
    window.addEventListener("resize", function () { if (current) move(current); });
  }

  // ---- Mobile menu (a <details>, so it also works without JS) ----
  var mnav = document.querySelector("[data-mnav]");
  if (mnav) {
    var close = function () { mnav.removeAttribute("open"); };
    mnav.querySelectorAll("a").forEach(function (a) { a.addEventListener("click", close); });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && mnav.open) { close(); mnav.querySelector("summary").focus(); }
    });
    document.addEventListener("click", function (e) { if (mnav.open && !mnav.contains(e.target)) close(); });
    window.addEventListener("resize", function () { if (window.innerWidth >= 1080) close(); });
  }

  // ---- Screenshot tabs (WAI-ARIA tabs; auto-advance until the visitor picks one) ----
  var show = document.querySelector("[data-show]");
  if (show) {
    var tabs = Array.prototype.slice.call(show.querySelectorAll('[role="tab"]'));
    var pans = tabs.map(function (b) { return document.getElementById(b.getAttribute("aria-controls")); });
    var auto = !still;
    var select = function (i, focus) {
      tabs.forEach(function (b, k) {
        var on = k === i;
        b.setAttribute("aria-selected", String(on));
        b.tabIndex = on ? 0 : -1;
        pans[k].classList.toggle("on", on);
        pans[k].setAttribute("aria-hidden", String(!on));
      });
      if (focus) tabs[i].focus();
    };
    var stopAuto = function () { auto = false; show.classList.remove("auto"); };
    tabs.forEach(function (b, i) {
      b.addEventListener("click", function () { stopAuto(); select(i); });
      b.addEventListener("keydown", function (e) {
        var k = { ArrowDown: 1, ArrowRight: 1, ArrowUp: -1, ArrowLeft: -1 }[e.key];
        if (e.key === "Home") k = -i; else if (e.key === "End") k = tabs.length - 1 - i;
        if (k == null) return;
        e.preventDefault(); stopAuto();
        select((i + k + tabs.length) % tabs.length, true);
      });
      var bar = b.querySelector(".tab-bar i");
      if (bar) bar.addEventListener("animationend", function () {
        if (!auto) return;
        var next = (i + 1) % tabs.length;
        select(next);
      });
    });
    select(0);
    if (auto && hasIO) {
      new IntersectionObserver(function (es) { show.classList.toggle("auto", auto && es[0].isIntersecting); }, { threshold: 0.35 }).observe(show);
    }
  }

  // ---- Parallel lanes: on narrow screens the diagram scrolls sideways; fade the edge until the end ----
  var lanes = document.querySelector(".lanes");
  if (lanes) {
    var lanesMore = function () { lanes.classList.toggle("lanes-more", lanes.scrollLeft + lanes.clientWidth < lanes.scrollWidth - 4); };
    lanes.addEventListener("scroll", lanesMore, { passive: true });
    window.addEventListener("resize", lanesMore);
    lanesMore();
  }

  // ---- Count-up for the product facts (the final number is already in the HTML) ----
  if (!still && hasIO) {
    var nums = document.querySelectorAll("[data-countup]");
    var cio = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (!e.isIntersecting) return;
        cio.unobserve(e.target);
        var el = e.target, end = +el.getAttribute("data-countup"), m = el.textContent.match(/^(\D*)(\d+)(\D*)$/);
        if (!m) return;
        var t0 = performance.now(), dur = 900 + end * 60;
        (function step(now) {
          var k = Math.min(1, (now - t0) / dur), v = Math.round(end * (1 - Math.pow(1 - k, 3)));
          el.textContent = m[1] + v + m[3];
          if (k < 1) requestAnimationFrame(step);
        })(t0);
      });
    }, { threshold: 0.6 });
    nums.forEach(function (n) { cio.observe(n); });
  }

  // ---- Pointer spotlight on the bento cards ----
  if (!still && window.matchMedia && window.matchMedia("(hover: hover)").matches) {
    document.querySelectorAll(".card").forEach(function (c) {
      c.addEventListener("pointermove", function (e) {
        var r = c.getBoundingClientRect();
        c.style.setProperty("--mx", (e.clientX - r.left) + "px");
        c.style.setProperty("--my", (e.clientY - r.top) + "px");
      });
    });
  }
})();
