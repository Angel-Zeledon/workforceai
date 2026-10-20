# Abogado del diablo (B8)

Antes de decidir una aprobación de **riesgo alto**, la persona puede abrir una segunda opinión que defiende la postura contraria y expone qué podría salir mal. Es **solo consultiva**: no aprueba, no rechaza, no bloquea y no tiene botones de decisión. Los agentes siguen sin poder aprobar (ni pedir esta revisión: `Generate` rechaza a agentes y al sistema con `403`).

## Cuándo aplica

`application.IsHighRiskApproval` lee lo que el motor de políticas ya dejó en la aprobación: `risk == "high"` **o** `required_approvals >= 2` (el motor sólo exige doble aprobación a acciones de riesgo alto). No hay heurística nueva. Una aprobación de otro riesgo responde `400`; una ya decidida sin revisión previa responde `409`.

## Flujo

1. La tarjeta muestra una sección plegable "Abogado del diablo" (`DevilsAdvocate.tsx`). Al abrirla por primera vez llama `POST /approvals/{id}/counterargument`.
2. El servicio (`application/devils_advocate.go`) revisa el caché; si no hay revisión, comprueba el interruptor de emergencia y la pausa del agente (`AdmitReview`), reserva presupuesto con `ReservePlanner` (tamaño real del payload + tope de salida `MAX_TOKENS_COUNTERARGUMENT`, por defecto 1200), llama al runtime y registra el consumo con `RecordPlannerUsage` (libro de costos, topes por agente y por organización, aviso de "sin saldo").
3. El resultado se guarda **una vez por aprobación** (`approval_counterarguments`, la primera escritura gana). Repetir el clic, otro navegador u otra réplica devuelven el caché (`cached: true`) sin llamar ni cobrar de nuevo; los clics concurrentes se serializan por aprobación.
4. Se audita `approval.counterargument` (modelo, costo) o `approval.counterargument_failed` (motivo).

El modo solo lectura no aplica: la revisión no tiene efectos secundarios. El horario de operación tampoco: quien la pide es una persona, no un agente que inicia trabajo.

## Degradación

Cualquier fallo (runtime sin la capacidad, error del modelo, respuesta vacía, tope de gasto, interruptor de emergencia) devuelve un código estable y **no** toca la aprobación, que sigue decidible:

| Código | HTTP | Causa |
|---|---|---|
| `unavailable` | 503 | sin capacidad en el runtime, error del modelo o respuesta vacía |
| `blocked` | 423 | interruptor de emergencia, agente en pausa o controles no disponibles |
| `budget_exceeded` | 402 | un tope (agente u organización) no permite la llamada |

La causa técnica queda en el log y la auditoría; el cliente sólo recibe el código. Un fallo no se cachea: se puede reintentar.

## API

| Endpoint | Permiso | Respuesta |
|---|---|---|
| `GET /approvals/{id}/counterargument` | `approvals:read` | revisión en caché, o `404 not_generated` |
| `POST /approvals/{id}/counterargument` | `approvals:decide` | genera (una vez) o devuelve el caché. Cuerpo opcional `{"locale"}` |

```json
{"approval_id":"…","counterargument":"…","risks":["…"],"checks":["…"],"advisory":true,"cached":false,
 "cost_usd":0.004,"model":"…","provider":"…","locale":"es","generated_by":"ana","created_at":"…"}
```

Todo está acotado por organización (RLS en `approval_counterarguments`, PK `(org_id, approval_id)`). El idioma queda fijado por la primera generación.

## Contrato con el runtime

`POST /v1/counterargument` (`agent-runtime/app/devils_advocate.py`). Entrada: `{approval: {action, tool, title, details, risk, agent_role, required_approvals}, locale, tone}` más la política de proveedores habitual. Salida: `{counterargument, risks[], checks[], usage}`; sin campo de veredicto, y todo lo que salga pasa por `redact_any`.

- Sólo viaja la **tarjeta de la aprobación**, que ya está saneada; el backend la redacta otra vez (`sanitize.RedactSecrets`) y la acota (título 300, detalle 4000 caracteres). Nunca viajan credenciales, ids de conexión ni el `Context` de la aprobación.
- El texto de la tarjeta es **dato no confiable**: el prompt lo delimita con `wrap_untrusted` y le ordena al modelo tratar cualquier orden dentro de él como un hallazgo (riesgo), no como instrucción.
- Simulación: `sim_counterargument` es determinista (sin LLM, costo simulado pequeño) y también marca órdenes escondidas en el detalle. Motor en vivo: `CrewAIEngine.counterargument`.

## Costos y límites

Se estima como una llamada de planificador (`UsageCounterargument`, sobrecarga fija 1200 tokens) y se asienta en el libro con el tipo `plan` (sin solicitud: cuentan el tope del agente asistente y el de la organización). No existe cuenta separada.

## Qué no cubre

No se re-genera si la aprobación cambia (no cambia: el estado sólo pasa de pendiente a decidida). No hay edición ni borrado desde la API. La revisión no se envía por correo ni por notificaciones push.

## Archivos

Backend: `domain/counterargument.go`, `application/devils_advocate.go`, `api/devils_advocate.go`, `infrastructure/{memory,postgres}/counterarguments.go`, migración `788_approval_counterarguments.sql` (+ `migrations/down/`). Runtime: `app/devils_advocate.py`. Frontend: `components/approvals/DevilsAdvocate.tsx`, claves `approvals.devil.*`.
