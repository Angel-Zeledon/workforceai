/* Service worker: offline app shell + Web Push notifications.
 * It NEVER caches API responses or anything cross-origin: approvals and every
 * other business datum always come live from the authenticated API. */
const SHELL = "wfai-shell-v1";
const LABELS = "wfai-labels-v1";
const LABELS_KEY = "/__labels.json";
const SHELL_URLS = ["/approvals", "/manifest.webmanifest", "/icon.svg"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(SHELL).then((c) => Promise.allSettled(SHELL_URLS.map((u) => c.add(u)))).then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SHELL && k !== LABELS).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/") || url.pathname === "/ws") return; // never cached
  if (req.mode === "navigate") {
    // Network first; offline falls back to the cached approvals shell.
    event.respondWith(
      fetch(req).catch(() => caches.open(SHELL).then((c) => c.match("/approvals")).then((r) => r || Response.error())),
    );
    return;
  }
  // Static, content-hashed assets: cache first.
  if (url.pathname.startsWith("/_next/static/") || SHELL_URLS.includes(url.pathname)) {
    event.respondWith(
      caches.open(SHELL).then(async (c) => {
        const hit = await c.match(req);
        if (hit) return hit;
        const res = await fetch(req);
        if (res.ok) c.put(req, res.clone());
        return res;
      }),
    );
  }
});

// The page sends translated strings once; the worker keeps them to render notifications.
self.addEventListener("message", (event) => {
  const d = event.data;
  if (d && d.type === "labels" && d.labels && typeof d.labels === "object") {
    event.waitUntil(caches.open(LABELS).then((c) => c.put(LABELS_KEY, new Response(JSON.stringify(d.labels)))));
  }
});

async function readLabels() {
  try {
    const r = await (await caches.open(LABELS)).match(LABELS_KEY);
    return r ? await r.json() : {};
  } catch {
    return {};
  }
}

self.addEventListener("push", (event) => {
  let m = {};
  try { m = event.data ? event.data.json() : {}; } catch { m = {}; }
  event.waitUntil((async () => {
    const l = await readLabels();
    const risk = ["low", "medium", "high"].includes(m.risk) ? m.risk : "medium";
    const id = typeof m.approval_id === "string" ? m.approval_id : "";
    // Only title + risk + id arrive; details are read after signing in.
    await self.registration.showNotification(String(m.title || l.fallbackTitle || ""), {
      body: l["risk_" + risk] || risk,
      tag: id || "approval",
      icon: "/icon.svg",
      badge: "/icon.svg",
      data: { url: id ? "/approvals/" + encodeURIComponent(id) : "/approvals" },
    });
  })());
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  let url = event.notification.data && event.notification.data.url;
  if (typeof url !== "string" || !url.startsWith("/") || url.startsWith("//")) url = "/approvals";
  event.waitUntil((async () => {
    const all = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const c of all) {
      if (new URL(c.url).origin === self.location.origin && "navigate" in c) {
        await c.navigate(url);
        return c.focus();
      }
    }
    return self.clients.openWindow(url);
  })());
});
