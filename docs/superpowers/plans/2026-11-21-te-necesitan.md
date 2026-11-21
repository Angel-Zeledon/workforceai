# F5. Bandeja única "Te necesitan" — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Pasos con checkbox (`- [ ]`).

**Goal:** una sola lista con todo lo que espera a la persona que mira y que ella puede resolver: aprobaciones que
puede decidir, preguntas de los agentes que le tocan, sugerencias pendientes y propuestas de confianza que puede
decidir. Con contador en el menú, en tiempo real y sin filtrar nada de un departamento privado ajeno.

**Architecture:** un agregador puro (`backend/internal/needsyou`) recibe **fuentes** (`Source{Kind, Collect}`) y
devuelve la lista ordenada (urgencia, luego la más antigua) y los conteos. Cada fuente vive en la API
(`internal/api/needs_you.go`) y **reutiliza la regla que su pantalla ya aplica**: `visibleApprovals`/`seesApproval` +
`application.DecidesApproval` (con `offices.Once`: la regla de la oficina se lee una vez por petición);
`seesQuestion` + quién responde; `agentOK` + `requests:create` para sugerencias; `trust` (rol admin, `agents:write`
+ `approvals:decide`) + `ws.agent` para propuestas. Añadir una fuente nueva = una función más en la lista. Sin
migración. Tiempo real: el frontend vuelve a pedir el contador/lista al recibir eventos existentes
(`approval.*`, `question.*`, `task.needs_input`, `trust.proposal`, `agent.autonomy_changed`, `initiative.raised`) y
uno aditivo `initiative.resolved` (mismo bus, misma audiencia por agente).

**Tech Stack:** Go (chi), Next.js 15 / React 19, zustand, `node --test`.

## Global Constraints

- Solo lo que la persona ve **y** puede resolver; lo de un departamento privado ajeno nunca aparece (ni en el
  contador). Un principal que no es persona (agente, token, sistema) recibe la lista vacía.
- Sin servidor/demo sin cuentas: la persona actúa como dueña (igual que la bandeja).
- Una fuente que falla no tumba la bandeja: se omite y se nombra en `unavailable` (nunca muestra de más).
- Textos es/en/pt; nunca métricas de ahorro; estilo sobrio (blanco, #0A66C2).

## Tareas

- [ ] **1. Agregador puro** (`internal/needsyou`): `Item`, `Source`, `Collect(ctx, full, sources...)`, `Urgency`,
  orden y conteos. Pruebas: orden urgencia/antigüedad, fuente que falla, límite.
- [ ] **2. Fuentes y rutas** (`internal/api/needs_you.go`, `trust.Service.Pending`): `GET /needs-you?limit=` y
  `GET /needs-you/count`. Pruebas API: miembro no ve nada del departamento privado ajeno (aprobación, pregunta,
  sugerencia, propuesta); admin no responsable no ve la aprobación de Legal y el responsable sí; viewer sin nada;
  conteo = lista.
- [ ] **3. Evento `initiative.resolved`** al aceptar/descartar una sugerencia.
- [ ] **4. Frontend**: `lib/needsYouApi.ts`, `lib/needsYouLogic.ts` (+ test), store con recarga por eventos,
  página `/needs-you`, entrada "Te necesitan" del menú con contador, mock.
- [ ] **5. Docs**: `docs/architecture/needs-you.md`, `08-api.md`.
