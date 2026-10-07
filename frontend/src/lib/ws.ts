import { MOCK, WS_URL } from "./config";
import { useStore } from "./store";
import type { WsFrame } from "./types";

let started = false;
let stopped = false;

export function startRealtime() {
  if (started) return () => {};
  started = true;
  stopped = false;
  const { apply, setConnected, loadAll } = useStore.getState();

  if (MOCK) {
    let off = () => {};
    import("./mock/engine").then(({ mockBackend }) => {
      if (stopped) return;
      loadAll().then(() => {
        if (stopped) return;
        setConnected(true);
        off = mockBackend.connect((f) => apply(f));
      });
    });
    return () => { stopped = true; started = false; off(); };
  }

  let ws: WebSocket | null = null;
  let retry = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const schedule = () => {
    if (stopped) return;
    const delay = Math.min(10000, 800 * 2 ** Math.min(retry++, 5));
    timer = setTimeout(open, delay);
  };
  const open = () => {
    if (stopped) return;
    try { ws = new WebSocket(WS_URL); } catch { schedule(); return; }
    ws.onopen = () => { retry = 0; setConnected(true); loadAll(); };
    ws.onmessage = (ev) => {
      try { apply(JSON.parse(ev.data as string) as WsFrame); } catch { /* ignore malformed frame */ }
    };
    ws.onclose = () => { setConnected(false); schedule(); };
    ws.onerror = () => { try { ws?.close(); } catch { /* noop */ } };
  };
  loadAll();
  open();
  return () => {
    stopped = true; started = false;
    if (timer) clearTimeout(timer);
    try { ws?.close(); } catch { /* noop */ }
  };
}
