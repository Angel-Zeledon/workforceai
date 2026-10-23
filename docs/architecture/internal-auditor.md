# Auditor interno: el sello visible (B7)

El auditor interno (`internal_auditor`, plantilla `backend/internal/roles/templates/internal_auditor.json`) revisa el trabajo
de otros empleados y cruza cifras entre entregables (por ejemplo, la utilidad neta del estado de resultados contra el
patrimonio del balance). Esta nota cubre la parte que faltaba: **mostrar** su veredicto.

## De dónde sale el veredicto

No hay lógica nueva de auditoría. El veredicto es lo que el auditor declara en su salida:

- `output.metrics.audit_status` = `verified | inconsistency | unverified` (regla `AUDITOR_TASK_RULES` en
  `agent-runtime/app/security.py`; el auditor simulado lo calcula con `cross_check` en `agent-runtime/app/review.py`).
- El backend lo endurece antes de guardarlo: un `verified` con menos de dos referencias de evidencia baja a `unverified`
  (`enforceAuditEvidence`, `backend/internal/application/review.go`).
- `output.findings` lleva las cifras que no coinciden (o las que sí), `output.evidence` las referencias
  `task:<id>.metrics.<clave>=<valor>`, y `metrics.audit_compared` / `metrics.audit_inconsistencies` los conteos.

La salida de una tarea ya viaja completa en `GET /tasks` y `GET /tasks/{id}` (`metrics` es un mapa libre), así que el
estado ya llegaba al frontend; nadie lo leía.

## Qué se agregó

| Pieza | Dónde |
|---|---|
| `application.TaskAuditOf` / `AuditOfTasks`: convierten la salida en el sello. Solo tareas `done`; un valor desconocido se lee como `unverified` (nunca dice más de lo que el auditor probó). Hallazgos acotados a 20, evidencia a 40. | `backend/internal/application/audit_stamp.go` |
| `GET /requests/{id}/audit`: todas las auditorías terminadas de la solicitud y un estado agregado | `backend/internal/api/router.go` |
| `audit` en cada nodo de proyecto que sea una auditoría terminada (aditivo, `omitempty`) | `backend/internal/projects/types.go`, `view.go` |
| Sello en el informe (`RequestAuditStamp` en `ReportView`), en el panel del nodo (`NodeDrawer`) y en el detalle de tareas del dashboard | `frontend/src/components/quality/AuditStamp.tsx` |
| Helpers puros (mismas reglas que el backend, fallback si el backend es anterior) | `frontend/src/lib/auditLogic.ts` (+ `auditLogic.test.mjs`) |

### Estado agregado de una solicitud

1. `inconsistency` si alguna auditoría encontró una.
2. `verified` solo si **todas** verificaron.
3. `none` si no hubo auditorías (la UI no muestra nada).
4. `unverified` en cualquier otro caso.

### Textos visibles

"Verificado" / "Inconsistencia" / "Sin verificar" (es), "Verified" / "Inconsistency" / "Not verified" (en), con su
explicación en el `title` del sello. "Sin verificar" se explica siempre en la tarjeta: no significa que el trabajo esté
mal, solo que no hubo cifras comparables o faltó evidencia. En una inconsistencia se listan los hallazgos; la evidencia
se abre a pedido.

## Modo demo (`NEXT_PUBLIC_MOCK=true`)

- Proyecto "Cierre financiero del trimestre": al terminar, `b4` (estado de resultados) sale **Verificado**, `c1`
  (conciliar balance contra resultados) sale con **Inconsistencia** en la utilidad neta y `c2` (cumplimiento fiscal) sale
  **Sin verificar** (`demoAudit` en `frontend/src/lib/projects/mock.ts`).
- Solicitudes del chat: la propuesta de $50,000 muestra una inconsistencia de margen, la caída de ventas una
  verificación y el plan de contratación "sin verificar" (`demoRequestAudit` en `frontend/src/lib/mock/engine.ts`).
  Las cifras son de demostración.

## Límites

- El sello solo repite lo que el auditor declaró; compara claves de `metrics`, no prosa (ver `docs/plans/large-workflows.md`).
- Un informe ya generado no guarda el sello: se calcula al leer, desde las tareas de la solicitud. Si una tarea se
  borrara, el sello cambiaría con ella.
- Sin auditor contratado en la organización no hay tareas de auditoría y no aparece ningún sello.
