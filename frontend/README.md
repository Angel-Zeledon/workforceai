# AI Workforce OS - Frontend

Next.js (App Router, TS) + React Three Fiber + zustand + Tailwind. Modo oficina 3D y modo dashboard.

## Correr
    npm install
    npm run dev:mock      # sin backend: simula el escenario de $50,000 (NEXT_PUBLIC_MOCK=true)
    npm run dev           # contra el backend real
    npm run build && npm start

Variables (ver `.env.example`): `NEXT_PUBLIC_API_URL` (default http://localhost:8080/api/v1),
`NEXT_PUBLIC_WS_URL` (default ws://localhost:8080/ws), `NEXT_PUBLIC_MOCK=true` para el modo mock.
Son variables de build: en Docker se pasan como `--build-arg`.

## Docker
    docker build -t aiw-frontend --build-arg NEXT_PUBLIC_API_URL=http://localhost:8080/api/v1 .
    docker run -p 3000:3000 aiw-frontend

## Estructura
- `src/lib/store.ts` store zustand; `apply()` aplica todos los eventos WS del SPEC.
- `src/lib/ws.ts` cliente WS con reconexion (backoff) y recarga REST al reconectar.
- `src/lib/mock/engine.ts` backend simulado (REST + frames WS con los mismos payloads).
- `src/components/office/` escena 3D (personajes procedurales, escritorios, lineas/burbujas de mensajes).
- `src/components/dashboard/` dashboard.
