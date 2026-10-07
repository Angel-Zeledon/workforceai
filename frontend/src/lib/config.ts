// En producción (detrás de Caddy, mismo dominio) no se define ninguna variable:
// la API es relativa (/api/v1) y el WebSocket se deriva de window.location.
// En dev, docker-compose.yml / .env.local pueden sobrescribirlas con NEXT_PUBLIC_*.
const envApi = process.env.NEXT_PUBLIC_API_URL;
const envWs = process.env.NEXT_PUBLIC_WS_URL;

function deriveWsUrl(): string {
  if (typeof window === "undefined") return "/ws"; // SSR/build: no se usa, solo el navegador abre el socket
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export const API_URL = (envApi || "/api/v1").replace(/\/$/, "");
export const WS_URL = envWs || deriveWsUrl();
export const MOCK = process.env.NEXT_PUBLIC_MOCK === "true";
