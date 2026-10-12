/* Language (ES/EN) and CTAs. No dependencies, no analytics, no network. */
(function () {
  "use strict";
  var DICT = window.I18N, CFG = window.LANDING_CONFIG || {};

  // The language is the one the page was written in: "/" is Spanish and "/en/" is English, two real URLs
  // (with hreflang) that the ES/EN links switch between. It is never guessed from the browser: a crawler
  // that renders in en-US must see the same text a Spanish reader gets.
  function pickLang() {
    return document.documentElement.lang === "en" ? "en" : "es";
  }

  function applyCtas() {
    document.querySelectorAll("[data-cta]").forEach(function (a) {
      var kind = a.getAttribute("data-cta");
      if (kind === "signup") a.setAttribute("href", CFG.SIGNUP_URL || "#");
      if (kind === "login") a.setAttribute("href", CFG.LOGIN_URL || "#");
      a.setAttribute("rel", "noopener");
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
    applyCtas();
    document.dispatchEvent(new Event("langchange"));
  }

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
