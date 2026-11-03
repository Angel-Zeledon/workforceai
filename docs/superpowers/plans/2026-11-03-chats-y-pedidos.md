# Chats en orden y pedidos: plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Sin commits (lo decide el dueño).

**Goal:** Un chat por "con quién hablas" y cada trabajo como un "pedido" que se abre en un panel al costado, sin salir del chat.

**Architecture:** La lógica pura (estado de un pedido, filas de la barra lateral, búsqueda en tres grupos, una tarjeta por pedido, URL `?pedido=`) vive en `frontend/src/lib/pedidosLogic.ts` y se prueba con `node --test`. La pantalla (`ChatsView.tsx`) deja de cambiar el chat a la "conversación del trabajo": abre `PedidoPanel.tsx` (drawer derecho; pantalla completa en móvil) con pestañas Resumen / Pasos / Archivos / Informe / Cómo lo trabajaron. Los datos salen de lo que ya existe: `useChats` (conversaciones de pedido, archivar, buscar), `useStore` (requests, tasks, reports, approvals, questions), `GET /requests/{id}` (pasos) y `GET /conversations/{id}/messages` (cómo lo trabajaron). Sin cambios de almacenamiento en el backend.

**Tech Stack:** Next.js 15, React 19, zustand, Tailwind; Go (solo textos).

**Spec:** `docs/superpowers/specs/2026-11-03-chats-y-pedidos-design.md`

## Global Constraints

- Un solo nombre: "pedido" (en: "request", pt: "pedido") para todo trabajo pedido; nada de "solicitud", "trabajo" ni "conversación del trabajo" en la UI de chats.
- URL del panel: `/chats/...?pedido=<id del pedido>`; Atrás del navegador lo cierra; `/chats/<agente>` sigue funcionando; `?conv=<id de conversación>` (enlace viejo a una conversación de trabajo) abre su pedido.
- "Necesita tu ayuda" = pregunta abierta (`useStore.questions`, del trabajo de Proyectos) o tarea `needs_input`; si no hay datos, no se inventa.
- Textos en `es/en/pt.json`; releer el archivo justo antes de escribir y no borrar claves ajenas.
- Estilo: blanco, acentos azules, IBM Plex Sans, esquinas ~10px.
- No tocar `components/projects/**`, `ApprovalsTray.tsx`, `components/office/**`, runtime, `.env`, landing.

## Review Focus

- Pedido sin `request` cargado en el store (historial viejo): la fila y el panel muestran el título y "estado desconocido", no se rompen. (Task 1: `pedidoStatus(undefined, …) === "unknown"`.)
- Mismo pedido con varios mensajes en el hilo (plan, archivos, informe): una sola tarjeta, en el último. (Task 1: `pedidoCardAnchors`.)
- Abrir `/chats?pedido=X` directo y luego Atrás: se cierra el panel sin salir de Chats; cerrar con la X tras abrir desde la lista vuelve a la URL anterior. (Task 1: `pedidoHref` conserva otros parámetros.)
- Búsqueda sin acentos ("informe" encuentra "Infórme", "conciliacion" → "Conciliación") en agentes, personas y pedidos. (Task 1: `searchGroups`.)
- Archivado: un pedido archivado no aparece en la lista activa pero sí con "Ver archivados" y sigue abriéndose por URL. (Task 1: `pedidoRows` con `archived`.)

---

### Task 1: lógica pura de pedidos

**Files:**
- Create: `frontend/src/lib/pedidosLogic.ts`, `frontend/src/lib/pedidosLogic.test.mjs`
- Modify: `frontend/package.json` (script `test:unit`)

**Interfaces (Produces):**
- `type PedidoStatus = "running" | "needs_help" | "needs_approval" | "paused" | "done" | "failed" | "unknown"`
- `pedidoStatus(req?: {status}, tasks: {status}[], openQuestions: number, pendingApprovals: number): PedidoStatus` — orden: failed > done > needs_help > needs_approval (request `awaiting_approval|awaiting_confirmation`, tarea `awaiting_approval`, aprobación pendiente) > paused > running (`planning|running`); sin request → unknown.
- `needsAttention(s) : boolean` (needs_help | needs_approval).
- `pedidoAgents(tasks, participants): string[]` (agentes de las tareas en orden; si no hay, participantes sin `user`).
- `pedidoRows(convs, ctx, { archived }): PedidoRow[]` — solo `kind === "request"` con `request_id`, del archivado pedido; los que necesitan atención primero, luego por `updated_at` desc. `PedidoRow = { id, convId, title, status, agents, updatedAt, unread, archived, projectId }`.
- `pedidoCardAnchors(msgs: {id, request_id?}[]): Set<string>` — id del último mensaje de cada pedido.
- `searchGroups(q, { agents: {key,name,sub}[], people: {id,title,sub}[], pedidos: PedidoRow[], hits: ConvRowLike[] }): { agents: string[]; people: string[]; pedidos: string[] }` — sin acentos ni mayúsculas; los aciertos del backend por texto de mensaje suman: `office` → `general`, `agent` → su agente, `request` → su pedido.
- `pedidoFromSearch(search): { pedido: string; conv: string }`, `pedidoHref(pathname, search, id: string | null): string` (pone/quita `pedido`, quita `conv`, conserva el resto).
- `foldText(s): string`.

- [ ] Escribir `pedidosLogic.test.mjs` con los casos de la Review Focus y los del orden de estados; correr `node --test src/lib/pedidosLogic.test.mjs` → falla (módulo inexistente).
- [ ] Implementar `pedidosLogic.ts`; correr → pasa; sumarlo a `test:unit`.

### Task 2: panel del pedido

**Files:** Create `frontend/src/components/chats/PedidoPanel.tsx`; Modify `frontend/src/lib/chats.ts` (cargar detalle `GET /requests/{id}` con sus tareas en `useStore.tasks`).

**Interfaces:** `PedidoPanel({ requestId, onClose, onGone })`; pestañas `summary | steps | files | report | work` (`data-testid="pedido-tab-<x>"`). Archivos = `files` de los mensajes con ese `request_id` (hilos cargados, chat en vivo y conversación interna), sin repetir ruta, abiertos con `useFileViewer.open`. Informe = `ReportView`. Cómo lo trabajaron = mensajes de la conversación interna, solo lectura. Acciones de la conversación (renombrar, archivar, eliminar) en la cabecera. "Abrir proyecto" si `project_id`. Si necesita ayuda/aprobación: aviso con "Ir a Pendientes" (`/approvals/inbox`). Esc cierra.

- [ ] Construir; `npx tsc --noEmit` limpio.

### Task 3: pantalla de Chats

**Files:** Modify `frontend/src/components/chats/ChatsView.tsx`, `PeopleChatsList.tsx` (que acepte la lista filtrada y no imprima su título cuando busca), `frontend/src/components/chat/commands/CommandCard.tsx` (`/chats?pedido=<id>`), `frontend/src/components/tour/tours/chats.ts`.

- Barra lateral: **Agentes** (Toda la oficina + un chat por agente), **Personas**, **Pedidos** (filas de `pedidoRows`, con estado, quién y cuándo; archivar/ver archivados).
- Buscador: resultados en los tres grupos (`searchGroups`).
- Una tarjeta por pedido en el hilo (`pedidoCardAnchors`): estado + "Abrir pedido" (+ "Ir a Pendientes" si hace falta). `data-testid="chats-request"` se conserva.
- Se quita la selección "conversación de trabajo" del panel derecho y el modal de informe; el panel del pedido la reemplaza. URL con `pedidoHref` y `popstate`.

- [ ] Construir; `npx tsc --noEmit`.

### Task 4: un solo nombre

**Files:** `frontend/src/lib/i18n/{es,en,pt}.json`, `frontend/src/lib/mock/engine.ts`, `frontend/src/lib/mock/commands-mock.ts`, `backend/internal/application/chat_local.go` (`plan_outro`, `report_ready`, `task_failed`), `backend/internal/push/service.go` (URL `/chats?pedido=<id>`).

- [ ] Script de Node que lee cada JSON justo antes de escribir y cambia solo los valores de las claves listadas; `node scripts/check-i18n.mjs` sin problemas nuevos.
- [ ] `go build ./... && go test ./internal/application/ ./internal/push/`.

### Task 5: verificación y documentación

- [ ] `npx tsc --noEmit`, `npm run test:unit`, `node scripts/check-i18n.mjs`.
- [ ] Ver la interfaz (Docker postgres 5437, backend 8084, frontend 3004, simulación) y capturas en `docs/screenshots/chats-pedidos-2026-11-03/`; detener y borrar lo propio.
- [ ] Actualizar `docs/architecture/chat-history.md` (sección "Pedidos").
