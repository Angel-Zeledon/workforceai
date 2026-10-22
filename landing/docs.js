/*
 * Docs, guide and trust pages: progressive enhancement only (every page works without this file).
 *   - Search dialog over the prebuilt index (/docs-search.<lang>.json): Ctrl/Cmd+K or "/" to open.
 *   - Copy buttons on code blocks; heading anchors copy their link.
 *   - Scroll-spy for the "On this page" rail; drawer behavior for the sidebar on phones.
 * No dependencies, no tracking, nothing leaves the page except the fetch of the static index.
 */
(() => {
  "use strict";
  const doc = document;
  const root = doc.documentElement;
  root.classList.add("js");
  const lang = root.lang === "en" ? "en" : "es";
  const T = {
    es: {
      search: "Buscar en la documentación", placeholder: "Busca páginas, secciones, endpoints…", empty: "Sin resultados para", hint: "Prueba con otra palabra o revisa la ortografía.",
      suggestions: "Sugerencias", results: "Resultados", nav: "navegar", open: "abrir", close: "cerrar", copy: "Copiar", copied: "Copiado",
      copyCode: "Copiar código", codeCopied: "Código copiado", linkCopied: "Enlace copiado", loading: "Cargando índice…", failed: "No se pudo cargar el índice de búsqueda.",
      count: (n) => (n === 1 ? "1 resultado" : `${n} resultados`),
    },
    en: {
      search: "Search the docs", placeholder: "Search pages, sections, endpoints…", empty: "No results for", hint: "Try another word or check the spelling.",
      suggestions: "Suggestions", results: "Results", nav: "navigate", open: "open", close: "close", copy: "Copy", copied: "Copied",
      copyCode: "Copy code", codeCopied: "Code copied", linkCopied: "Link copied", loading: "Loading index…", failed: "The search index could not be loaded.",
      count: (n) => (n === 1 ? "1 result" : `${n} results`),
    },
  }[lang];
  const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  const live = doc.querySelector("[data-live]");
  const say = (msg) => { if (!live) return; live.textContent = ""; setTimeout(() => { live.textContent = msg; }, 30); };
  const svg = (p) => `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
  const ICON_COPY = svg('<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6.5a2 2 0 0 0-2-2h-7a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h2"/>');
  const ICON_OK = svg('<path d="m5 12.5 4.5 4.5L19 7.5"/>');
  const escHtml = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); return true; } catch {
      const ta = doc.createElement("textarea");
      ta.value = text; ta.setAttribute("readonly", ""); ta.style.cssText = "position:fixed;top:-1000px;opacity:0";
      doc.body.appendChild(ta); ta.select();
      let ok = false; try { ok = doc.execCommand("copy"); } catch { ok = false; }
      ta.remove(); return ok;
    }
  }

  // ---------- code blocks: copy ----------
  for (const fig of doc.querySelectorAll(".dx-code")) {
    const bar = fig.querySelector(".dx-code-bar");
    const code = fig.querySelector("code");
    if (!bar || !code) continue;
    const btn = doc.createElement("button");
    btn.type = "button"; btn.className = "dx-copy";
    btn.innerHTML = `${ICON_COPY}<span>${T.copy}</span>`;
    btn.setAttribute("aria-label", T.copyCode);
    btn.addEventListener("click", async () => {
      if (!(await copyText(code.innerText.replace(/\n$/, "")))) return;
      btn.classList.add("is-done"); btn.innerHTML = `${ICON_OK}<span>${T.copied}</span>`; say(T.codeCopied);
      clearTimeout(btn._t); btn._t = setTimeout(() => { btn.classList.remove("is-done"); btn.innerHTML = `${ICON_COPY}<span>${T.copy}</span>`; }, 1800);
    });
    bar.appendChild(btn);
  }

  // ---------- heading anchors: navigate and copy the link ----------
  let toastTimer;
  const toast = (msg) => {
    let el = doc.querySelector(".dx-toast");
    if (!el) { el = doc.createElement("div"); el.className = "dx-toast"; el.setAttribute("aria-hidden", "true"); doc.body.appendChild(el); }
    el.textContent = msg; el.classList.add("is-on"); say(msg);
    clearTimeout(toastTimer); toastTimer = setTimeout(() => el.classList.remove("is-on"), 1600);
  };
  for (const a of doc.querySelectorAll(".dx-anchor")) {
    a.addEventListener("click", (e) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.button) return;
      const href = location.href.split("#")[0] + a.getAttribute("href");
      copyText(href).then((ok) => ok && toast(T.linkCopied));
    });
  }

  // ---------- "On this page": collapsed on narrow screens, scroll-spy on wide ones ----------
  const toc = doc.querySelector(".dx-toc");
  const wide = matchMedia("(min-width: 1200px)");
  const syncToc = () => { if (toc) toc.open = wide.matches || toc.dataset.userOpen === "1"; };
  if (toc) {
    syncToc();
    wide.addEventListener?.("change", syncToc);
    toc.querySelector("summary").addEventListener("click", (e) => { if (wide.matches) { e.preventDefault(); return; } toc.dataset.userOpen = toc.open ? "0" : "1"; });
    toc.addEventListener("click", (e) => { if (!wide.matches && e.target.closest("ol a")) { toc.open = false; toc.dataset.userOpen = "0"; } });
  }
  const tocLinks = toc ? [...toc.querySelectorAll("ol a")] : [];
  const heads = tocLinks.map((a) => doc.getElementById(decodeURIComponent(a.hash.slice(1)))).filter(Boolean);
  if (heads.length) {
    let current = null, ticking = false;
    const spy = () => {
      ticking = false;
      const line = 110;
      let idx = 0;
      for (let i = 0; i < heads.length; i++) if (heads[i].getBoundingClientRect().top - line <= 0) idx = i;
      if (innerHeight + scrollY >= doc.documentElement.scrollHeight - 4) idx = heads.length - 1;
      const link = tocLinks[idx];
      if (link === current) return;
      if (current) { current.classList.remove("is-active"); current.removeAttribute("aria-current"); }
      current = link; current.classList.add("is-active"); current.setAttribute("aria-current", "location");
    };
    addEventListener("scroll", () => { if (!ticking) { ticking = true; requestAnimationFrame(spy); } }, { passive: true });
    addEventListener("resize", spy, { passive: true });
    spy();
  }

  // ---------- sidebar drawer (phones/tablets) ----------
  const side = doc.getElementById("docs-nav");
  const menuBtn = doc.querySelector(".dx-menu");
  const scrim = doc.querySelector(".dx-scrim");
  const desktop = matchMedia("(min-width: 960px)");
  if (side && menuBtn) {
    menuBtn.setAttribute("role", "button");
    menuBtn.setAttribute("aria-expanded", "false");
    let lastFocus = null;
    const focusables = () => [...side.querySelectorAll("a[href], button:not([disabled])")].filter((el) => el.offsetParent !== null);
    const open = () => {
      lastFocus = doc.activeElement;
      root.classList.add("dx-nav-open");
      menuBtn.setAttribute("aria-expanded", "true");
      side.setAttribute("role", "dialog"); side.setAttribute("aria-modal", "true");
      (side.querySelector('[aria-current="page"]') || focusables()[0])?.focus({ preventScroll: true });
    };
    const close = (restore = true) => {
      if (!root.classList.contains("dx-nav-open")) return;
      root.classList.remove("dx-nav-open");
      menuBtn.setAttribute("aria-expanded", "false");
      side.removeAttribute("role"); side.removeAttribute("aria-modal");
      if (restore) (lastFocus || menuBtn).focus({ preventScroll: true });
    };
    menuBtn.addEventListener("click", (e) => { e.preventDefault(); root.classList.contains("dx-nav-open") ? close() : open(); });
    side.querySelector(".dx-side-close")?.addEventListener("click", (e) => { e.preventDefault(); close(); });
    scrim?.addEventListener("click", (e) => { e.preventDefault(); close(); });
    side.addEventListener("keydown", (e) => {
      if (!root.classList.contains("dx-nav-open")) return;
      if (e.key === "Escape") { e.preventDefault(); close(); return; }
      if (e.key !== "Tab") return;
      const f = focusables(); if (!f.length) return;
      if (e.shiftKey && doc.activeElement === f[0]) { e.preventDefault(); f[f.length - 1].focus(); }
      else if (!e.shiftKey && doc.activeElement === f[f.length - 1]) { e.preventDefault(); f[0].focus(); }
    });
    desktop.addEventListener?.("change", () => close(false));
    if (location.hash === "#docs-nav") history.replaceState(null, "", location.pathname + location.search);
    // Keep the current page visible inside a long sidebar without moving the page itself.
    const cur = side.querySelector('.dx-pages [aria-current="page"]');
    if (cur && desktop.matches) { const r = cur.offsetTop - side.clientHeight / 2; if (r > 0) side.scrollTop = r; }
  }

  // ---------- search ----------
  const triggers = [...doc.querySelectorAll("[data-search]")];
  if (!triggers.length || !("HTMLDialogElement" in window)) return;
  for (const k of doc.querySelectorAll("[data-kbd]")) k.textContent = isMac ? "⌘K" : "Ctrl K";
  for (const t of triggers) t.hidden = false;

  const norm = (s) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
  let data = null, loading = null;
  const load = () => loading || (loading = fetch(`/docs-search.${lang}.json`, { credentials: "same-origin" })
    .then((r) => { if (!r.ok) throw new Error(r.status); return r.json(); })
    .then((j) => { data = j; for (const it of j.items) { it._t = norm(it.t); it._h = norm(it.h); it._x = norm(it.x); } return j; })
    .catch(() => { loading = null; return null; }));

  const dlg = doc.createElement("dialog");
  dlg.className = "dx-search";
  dlg.setAttribute("aria-label", T.search);
  dlg.innerHTML = `<div class="dx-search-box">
      <div class="dx-search-field">
        ${svg('<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.2-4.2"/>')}
        <input type="search" role="combobox" aria-expanded="true" aria-controls="dx-results" aria-autocomplete="list" autocomplete="off" spellcheck="false" placeholder="${T.placeholder}" aria-label="${T.search}">
        <button type="button" class="dx-search-esc" aria-label="${T.close}">Esc</button>
      </div>
      <div class="dx-search-body">
        <p class="dx-search-status" aria-live="polite"></p>
        <ul id="dx-results" role="listbox" aria-label="${T.results}"></ul>
      </div>
      <div class="dx-search-foot" aria-hidden="true"><span><kbd>↑</kbd><kbd>↓</kbd> ${T.nav}</span><span><kbd>↵</kbd> ${T.open}</span><span><kbd>Esc</kbd> ${T.close}</span></div>
    </div>`;
  doc.body.appendChild(dlg);
  const input = dlg.querySelector("input");
  const list = dlg.querySelector("ul");
  const status = dlg.querySelector(".dx-search-status");
  let results = [], active = -1;

  function score(it, terms) {
    let s = 0;
    for (const t of terms) {
      let v = 0;
      if (it._h && (it._h.startsWith(t) || it._h.includes(" " + t))) v = 14; else if (it._h.includes(t)) v = 9;
      if (it._t.startsWith(t) || it._t.includes(" " + t)) v = Math.max(v, it.h ? 6 : 16); else if (it._t.includes(t)) v = Math.max(v, it.h ? 4 : 10);
      if (it._x.includes(t)) v += it._x.includes(" " + t) || it._x.startsWith(t) ? 3 : 1.5;
      if (!v) return 0;
      s += v;
    }
    return s;
  }
  function mark(text, terms, max) {
    let start = 0;
    if (max && text.length > max) {
      const n = norm(text);
      const pos = Math.min(...terms.map((t) => { const i = n.indexOf(t); return i < 0 ? Infinity : i; }));
      start = Number.isFinite(pos) ? Math.max(0, pos - 50) : 0;
      if (start > 0) { const sp = text.indexOf(" ", start); start = sp > -1 && sp - start < 20 ? sp + 1 : start; }
      text = (start > 0 ? "…" : "") + text.slice(start, start + max) + (start + max < text.length ? "…" : "");
    }
    const n = norm(text);
    const ranges = [];
    for (const t of terms) { let i = n.indexOf(t); while (i > -1 && t) { ranges.push([i, i + t.length]); i = n.indexOf(t, i + t.length); } }
    ranges.sort((a, b) => a[0] - b[0]);
    let out = "", at = 0;
    for (const [a, b] of ranges) { if (a < at) continue; out += escHtml(text.slice(at, a)) + "<mark>" + escHtml(text.slice(a, b)) + "</mark>"; at = b; }
    return out + escHtml(text.slice(at));
  }
  function render(q) {
    const terms = norm(q).split(/[\s/,.:;()]+/).filter((t) => t.length > 0);
    list.innerHTML = ""; active = -1;
    if (!data) { status.textContent = loading ? T.loading : T.failed; return; }
    if (!terms.length) {
      results = data.items.filter((it) => !it.h && /\/(docs|guia|guide|confianza|trust)\/$/.test(it.u));
      status.textContent = T.suggestions;
    } else {
      results = data.items.map((it) => [it, score(it, terms)]).filter(([, s]) => s > 0).sort((a, b) => b[1] - a[1]).slice(0, 24).map(([it]) => it);
      status.textContent = results.length ? T.count(results.length) : "";
      if (!results.length) { list.innerHTML = `<li class="dx-search-empty" role="presentation"><b>${T.empty} “${escHtml(q.trim())}”</b><span>${T.hint}</span></li>`; return; }
    }
    list.innerHTML = results.map((it, i) => `<li role="option" id="dx-opt-${i}" aria-selected="false"><a href="${escHtml(it.u)}" tabindex="-1">
        <span class="dx-r-meta"><span class="dx-r-coll">${escHtml(data.labels[it.c] || "")}</span>${it.h ? `<span class="dx-r-page">${escHtml(it.t)}</span>` : ""}</span>
        <span class="dx-r-title">${mark(it.h || it.t, terms)}</span>
        ${it.x ? `<span class="dx-r-text">${mark(it.x, terms, 150)}</span>` : ""}
      </a></li>`).join("");
    setActive(0);
  }
  function setActive(i) {
    const opts = list.querySelectorAll('[role="option"]');
    if (!opts.length) { input.removeAttribute("aria-activedescendant"); return; }
    active = (i + opts.length) % opts.length;
    opts.forEach((o, j) => o.setAttribute("aria-selected", j === active ? "true" : "false"));
    input.setAttribute("aria-activedescendant", opts[active].id);
    opts[active].scrollIntoView({ block: "nearest" });
  }
  const go = (a) => { if (!a) return; dlg.close(); location.href = a.href; };
  input.addEventListener("input", () => render(input.value));
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setActive(active + 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive(active - 1); }
    else if (e.key === "Enter") { e.preventDefault(); go(list.querySelector('[aria-selected="true"] a')); }
    else if (e.key === "Escape") { e.preventDefault(); dlg.close(); } // a search field would otherwise just clear itself
  });
  list.addEventListener("mousemove", (e) => { const li = e.target.closest('[role="option"]'); if (li) { const i = [...list.children].indexOf(li); if (i !== active) setActive(i); } });
  list.addEventListener("click", (e) => { const a = e.target.closest("a"); if (a) { e.preventDefault(); go(a); } });
  dlg.querySelector(".dx-search-esc").addEventListener("click", () => dlg.close());
  dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close(); });
  dlg.addEventListener("close", () => { root.classList.remove("dx-search-open"); lastTrigger?.focus({ preventScroll: true }); });

  let lastTrigger = null;
  const openSearch = (from) => {
    if (dlg.open) return;
    lastTrigger = from || doc.activeElement;
    root.classList.add("dx-search-open");
    dlg.showModal();
    input.select();
    render(input.value);
    load().then(() => render(input.value));
  };
  for (const t of triggers) { t.addEventListener("click", () => openSearch(t)); t.addEventListener("pointerenter", load, { once: true }); }
  addEventListener("keydown", (e) => {
    const k = e.key.toLowerCase();
    if ((e.metaKey || e.ctrlKey) && k === "k") { e.preventDefault(); dlg.open ? dlg.close() : openSearch(); return; }
    if (k === "/" && !dlg.open && !e.metaKey && !e.ctrlKey && !e.altKey) {
      const el = doc.activeElement;
      if (el && (el.isContentEditable || /^(input|textarea|select)$/i.test(el.tagName))) return;
      e.preventDefault(); openSearch();
    }
  });
})();
