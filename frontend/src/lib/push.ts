import { call } from "./api";

// Web Push client. The backend only ever pushes title + risk + approval id;
// the endpoint and keys go to the API and nowhere else.

export interface PushConfig { enabled: boolean; public_key?: string }

export const pushSupported = () =>
  typeof window !== "undefined" && "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;

export const getPushConfig = () => call<PushConfig>("GET", "/push/config");

function keyBytes(b64url: string): Uint8Array {
  const pad = "=".repeat((4 - (b64url.length % 4)) % 4);
  const raw = atob((b64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

export async function currentSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.ready;
  return reg.pushManager.getSubscription();
}

/** Asks permission, subscribes this browser and registers it with the backend. */
export async function enablePush(publicKey: string): Promise<"granted" | "denied"> {
  const perm = await Notification.requestPermission();
  if (perm !== "granted") return "denied";
  const reg = await navigator.serviceWorker.ready;
  const sub = (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) as BufferSource }));
  await call("POST", "/push/subscriptions", sub.toJSON());
  return "granted";
}

export async function disablePush(): Promise<void> {
  const sub = await currentSubscription();
  if (!sub) return;
  await call("DELETE", "/push/subscriptions", { endpoint: sub.endpoint });
  await sub.unsubscribe();
}

/** Gives the service worker the translated notification texts. */
export async function sendLabels(labels: Record<string, string>): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  const reg = await navigator.serviceWorker.ready;
  reg.active?.postMessage({ type: "labels", labels });
}
