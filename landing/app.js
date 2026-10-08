/* Language (ES/EN) and CTAs. No dependencies, no analytics, no network. */
(function () {
  "use strict";
  var DICT = window.I18N, CFG = window.LANDING_CONFIG || {};
  var KEY = "aiwos-lang";

  function store(get, v) {
    try { return get ? localStorage.getItem(KEY) : localStorage.setItem(KEY, v); } catch (e) { return null; }
  }

  function pickLang() {
    var q = new URLSearchParams(location.search).get("lang");
    if (q === "es" || q === "en") return q;
    var saved = store(true);
    if (saved === "es" || saved === "en") return saved;
    var nav = (navigator.languages && navigator.languages[0]) || navigator.language || "es";
    return /^en/i.test(nav) ? "en" : "es";
  }

  function applyCtas(lang) {
    var subject = encodeURIComponent((CFG.CONTACT_SUBJECT || {})[lang] || "");
    var mail = "mailto:" + (CFG.CONTACT_EMAIL || "") + "?subject=" + subject;
    document.querySelectorAll("[data-cta]").forEach(function (a) {
      var kind = a.getAttribute("data-cta");
      if (kind === "contact") a.setAttribute("href", CFG.CONTACT_EMAIL ? mail : (CFG.DEMO_URL || "#"));
      if (kind === "demo") {
        a.setAttribute("href", CFG.DEMO_URL || "#");
        a.setAttribute("rel", "noopener");
      }
    });
  }

  function t(lang, key) {
    var d = DICT[lang] || {};
    return d[key] != null ? d[key] : (DICT.es[key] != null ? DICT.es[key] : "");
  }

  function apply(lang) {
    document.documentElement.lang = lang;
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
    document.querySelectorAll(".lang button").forEach(function (b) {
      b.setAttribute("aria-pressed", String(b.getAttribute("data-lang") === lang));
    });
    applyCtas(lang);
    document.dispatchEvent(new Event("langchange"));
  }

  document.querySelectorAll(".lang button").forEach(function (b) {
    b.addEventListener("click", function () {
      var l = b.getAttribute("data-lang");
      store(false, l);
      apply(l);
    });
  });

  apply(pickLang());

  // Scroll-in entrances with the app's short motion (ui-in). Without JS or with reduced
  // motion everything is visible from the start.
  var still = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!still && "IntersectionObserver" in window) {
    document.documentElement.classList.add("motion");
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
      });
    }, { rootMargin: "0px 0px -8% 0px" });
    document.querySelectorAll(".reveal").forEach(function (el) { io.observe(el); });
  }
})();
