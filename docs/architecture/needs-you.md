# Te necesitan (F5)

Una sola lista con todo lo que espera a la persona que mira **y que ella puede resolver**. Antes estaba repartido entre
la bandeja de aprobaciones, las preguntas de los agentes, las sugerencias del panel y las propuestas de confianza del
perfil de cada agente. Es la entrada "Te necesitan" del menú (antes "Pendientes"), con su número; la bandeja rápida de
aprobaciones (`/approvals/inbox`) sigue a un clic desde la página. Sin migración.

Código: `backend/internal/needsyou` (agregador puro), `backend/internal/api/needs_you.go` (fuentes y rutas),
`trust.Service.Pending`/`MayDecide`; frontend `src/components/needsyou/NeedsYouView.tsx` (página `/needs-you`),
`src/lib/needsYou.ts` (store y API), `src/lib/needsYouLogic.ts` (+ `.test.mjs`), `src/lib/useNeedsYouBadge.ts`,
`src/lib/mock/needsyou-mock.ts`. API: `08-api.md` §46.

## Qué entra (y la regla de cada fuente)

Cada fuente **reutiliza la regla que su pantalla ya aplica**; el agregador no tiene reglas propias de visibilidad.

| Tipo (`kind`) | Qué | Quién lo ve aquí | Dónde se resuelve |
|---|---|---|---|
| `approval` | Aprobaciones pendientes | Quien la ve (`seesApproval`) **y** puede decidirla ahora: `application.DecidesApproval` (regla de la oficina: responsable del departamento o, sin responsables, admins; `approvals:decide`; rol mínimo; sin autoaprobación; otra persona en la doble aprobación). Es lo mismo que "Las que puedo decidir" de la bandeja | `/approvals/{id}` |
| `question` | Preguntas abiertas de los agentes | `seesQuestion` (D3) y `tasks:create`; las que son **para ella** (lanzó el trabajo) y, si tiene `tasks:manage`, las que nadie lanzó (van a dueños y admins) | `/approvals/inbox` |
| `suggestion` | Sugerencias (`initiatives`) pendientes | `requests:create` y `agentOK` (D3), como `GET /initiatives` | `/dashboard` o desde la lista |
| `trust` | Propuestas de subir o bajar autonomía | `agents:write` + `approvals:decide` (como las rutas) y la regla del servicio (`MayDecide`: admin o dueño, nunca un agente), de agentes cuyo trabajo ve | Perfil del agente (tarjeta de confianza) |

Un principal que no es persona (agente, token, sistema: `application.PersonID` vacío) recibe la lista vacía. Sin
cuentas (demo) la persona actúa como dueña, igual que la bandeja.

**Decisión:** una pregunta que un agente hizo a *otra* persona no aparece en la lista de un admin aunque podría
responderla: es de esa persona (los avisos `help.notice` van igual). La sigue viendo en la bandeja de Pendientes.

**D4 (compartir entre departamentos):** todavía no existe estado "pendiente de compartir"; cuando exista será una
fuente más.

## Orden y urgencia

Urgencia `high` > `normal` > `low`, y dentro de cada una **la más antigua primero**:

- Trabajo bloqueado (aprobación, pregunta): `high` si es de riesgo alto, doble aprobación o espera desde hace 24 h o
  más (el primer recordatorio de las preguntas también es a las 24 h); si no, `normal`.
- Sugerencias y propuestas de confianza no bloquean nada: `low`.

`limit` (1..500, defecto 200) corta la lista, no los conteos (`truncated: true`).

## Acciones desde la lista

Solo lo seguro, y siempre por la ruta de siempre (con todas sus reglas y auditoría):

- Aprobación de riesgo bajo o medio (`direct: true`): **Aprobar** / **Rechazar** (`POST /approvals/{id}/decision`).
  Riesgo alto o doble aprobación: solo **Rechazar** y "Ver detalle"; aprobarla pide su pantalla.
- Sugerencia: **Aceptar** (inicia una solicitud normal) / **Descartar**.
- Pregunta y propuesta de confianza: solo el enlace (responder necesita contexto; cambiar autonomía mira la evidencia).

Al resolver algo aquí sale de la lista al momento y se vuelve a pedir al servidor. Si otra persona lo resolvió antes,
la acción falla, se avisa y la lista se recarga.

## Tiempo real

Sin canal nuevo. `store.apply` llama a `refreshNeedsYou` con los eventos que cambian lo que espera a alguien
(`approval.requested|resolved|progress`, `question.asked|resolved`, `task.needs_input`, `trust.proposal`,
`agent.autonomy_changed`, `initiative.raised` y el nuevo aditivo `initiative.resolved`) y con `hello`. Se agrupan
(500 ms) y se recarga el contador, o la lista entera si la página está abierta. Como red de seguridad, el contador se
vuelve a leer al reconectar y cada 2 minutos con la pestaña visible (p. ej. una propuesta de confianza que el sistema
reemplaza no emite evento). La audiencia de cada evento ya respeta los departamentos privados (D3); de todos modos la
lista siempre la arma el servidor.

## Añadir una fuente

1. En `api/needs_you.go`, una función `ny<Algo>(r, v) needsyou.Source` con un `kind` nuevo que aplique **la regla de su
   pantalla** (visibilidad D3 con `v.ws`, permiso con `v.can`, regla de quién decide) y devuelva `Item` con `target`.
2. Añadirla a `collectNeedsYou`. Si su evento en vivo es nuevo, añadirlo a `isNeedsYouFrame`.
3. El frontend muestra un tipo desconocido con la etiqueta genérica y su enlace (`needsYou.kind.other`); para acciones
   directas o un título propio, añadirlo a `needsYouLogic.ts`.

Lo que levante cualquier señal con `InitiativeService.Raise` (por ejemplo el vigilante de tareas colgadas) ya entra
solo como `suggestion`, con su `signal` y `params`.

## Lecturas por petición

La regla de la oficina se lee una vez por petición (`offices.Once`, como la bandeja). Una lectura por fuente
(aprobaciones pendientes, preguntas abiertas, sugerencias pendientes, propuestas pendientes); la lista añade títulos
de tareas, nombres de proyectos, agentes y departamentos, que el contador (`/needs-you/count`) no lee.

## Qué no se verificó

- No se vio en un navegador ni con Playwright (regla del proyecto: sin servidores ni navegador). Verificado:
  pruebas de Go (`needsyou`, `trust`, `application`, `api`), `go vet`, `tsc`, `check-i18n`, `npm run test:unit` y
  `npm run build`.
- Con Postgres no se corrió nada nuevo: no hay SQL nuevo (`ListProposals` con agente vacío ya existía).
