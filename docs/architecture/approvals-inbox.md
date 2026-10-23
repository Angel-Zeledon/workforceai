# Aprobaciones en 1 minuto (B17)

Bandeja rápida de aprobaciones **de todos los proyectos**: una tarjeta a la vez con lo esencial, teclado, gestos y
agrupación donde la API ya lo permite. Página `/approvals/inbox` (`frontend/src/components/approvals/ApprovalsInbox.tsx`),
enlazada desde la lista móvil (`/approvals`, "Modo rápido") y desde la bandeja de la oficina cuando hay dos o más pendientes.
Backend: `GET /approvals/inbox` (`backend/internal/application/approval_inbox.go`, `backend/internal/api/approval_inbox.go`).
Sin migración.

## Qué muestra cada tarjeta

Quién (agente), qué (acción), riesgo, costo de IA de la tarea hasta ahora (`task_cost_usd`, del libro de uso por tarea),
proyecto y, si ya existe, el **resumen del abogado del diablo** (B8) leído de su caché: la bandeja nunca genera una revisión
(no gasta). Con `E` se abre el detalle; en riesgo alto sin revisión aparece el control existente para pedirla.

Orden: riesgo más alto primero; dentro del mismo riesgo, la más antigua primero.

## Teclado y gestos

| Acción | Teclado | Táctil |
|---|---|---|
| Aprobar | `A` | deslizar a la derecha |
| Rechazar | `R` | deslizar a la izquierda |
| Siguiente / anterior | `J` / `K` (o flechas) | botones |
| Detalle | `E` | botón |

**Riesgo alto nunca se aprueba con un gesto o una tecla sin confirmación explícita** (`needsConfirm`): deslizar o pulsar `A`
abre un diálogo "¿Aprobar una acción de riesgo alto?". Riesgo alto incluye la doble aprobación (`IsHighRiskApproval`).
Rechazar no pide confirmación (es lo seguro) pero sí tiene ventana para deshacer.

## Deshacer

`POST /approvals/{id}/decision` no tiene deshacer (ver `approvals-notify.md`). Por eso la ventana de 5 s es **del cliente**:
la decisión se **retiene en el dispositivo** y se envía al vencer; "Deshacer" la cancela antes de enviarla. No se agregó
ningún retraso ni estado en el servidor. Al salir de la página o esconder la pestaña, lo retenido se envía en ese momento.
Si el navegador se cierra de golpe dentro de la ventana, la decisión puede no llegar y la aprobación **sigue pendiente**
(falla hacia lo seguro). Si el envío falla (rol insuficiente, ya decidida, auto-aprobación) la tarjeta vuelve y se avisa.

## Agrupación ("aprobar las N")

Solo donde `POST /approvals/batch` existe: todas las pendientes de **una acción en un proyecto** (el proyecto se resuelve
por la solicitud de la tarea con `Projects.ProjectIDForRequest`). Un grupo es `batchable` si tiene 2 o más y **todas son de
riesgo bajo** (nunca riesgo medio, alto ni doble aprobación). El botón abre una confirmación con la lista y envía
`expected_count`: si llegó otra del mismo tipo, la API responde `409` y no aprueba a ciegas; la bandeja se recarga. El lote
no tiene ventana de deshacer (se dice en la confirmación).

## "Aprende cuándo molestar": solo una sugerencia explicable

Por cada tipo de acción, con las aprobaciones resueltas de los últimos 30 días:

- al menos **20 decisiones humanas** (las resueltas por un permiso permanente no cuentan),
- **más del 95 %** aprobadas,
- **nunca** si alguna fue de riesgo alto.

Entonces la bandeja muestra: "En los últimos 30 días se aprobó el 97,5 % (39 de 40) de «draft_reply». Podrías subir el
umbral de avisos o dar un permiso permanente en el proyecto." **No cambia ningún ajuste**: la persona decide si va a
Seguridad → Avisos (`min_risk`) o a la pestaña Aprobaciones del proyecto. La cuenta es de la organización y no de la persona:
los rechazos no registran quién rechazó, y contar solo las aprobaciones de alguien inflaría su porcentaje.

## Reglas que se mantienen

Toda decisión pasa por los endpoints existentes, así que aplican roles, doble aprobación, "los agentes nunca aprueban",
maker-checker, interruptor de emergencia/solo lectura en la ejecución posterior y la auditoría. `GET /approvals/inbox` es
de solo lectura (`approvals:read`); decidir sigue pidiendo `approvals:decide`.

## Modo demo

`frontend/src/lib/mock/inbox-mock.ts`: tres gastos iguales de bajo riesgo en "Cierre de octubre · Ferretería López"
(grupo con "aprobar las 3"), una publicación de riesgo medio, un contrato de riesgo alto con revisión del abogado del diablo
ya generada, un pago de riesgo alto sin proyecto, y un historial que dispara una sugerencia (`draft_reply`, 39 de 40).
Las aprobaciones reales del simulador se suman a la bandeja.

## Pendiente / no verificado

- No hay prueba e2e de Playwright del gesto de deslizar; la lógica está cubierta por `inboxLogic.test.mjs`.
- La bandeja carga todo el historial de aprobaciones de la organización para la sugerencia; con mucho volumen convendría
  una consulta acotada a 30 días.
