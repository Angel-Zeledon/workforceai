# Puntaje de calidad y reputación por empleado (B11)

Un puntaje **orientativo** (0 a 100) por agente, calculado solo con señales que la plataforma ya registra, con la
metodología visible en cada respuesta. **No cambia autonomía ni permisos** (eso es B1): ningún endpoint de este módulo
escribe sobre el agente.

Código: `backend/internal/quality` (puntaje puro, servicio, casos dorados), `backend/internal/infrastructure/postgres/quality.go`
(consultas), `backend/internal/api/quality.go` (HTTP), migración `789_quality_golden.sql` (+ `migrations/down/789_quality_golden_down.sql`).
UI: `frontend/src/components/quality/`.

## Señales y metodología (versión 1)

| Componente | Peso | Mínimo | Fuente (tabla existente) | Tasa |
|---|---|---|---|---|
| `approvals` | 0.30 | 5 | `approvals` resueltas en la ventana | aprobadas / (aprobadas + rechazadas por una persona) |
| `tasks` | 0.25 | 5 | `tasks` en `done` o `failed` | hechas / (hechas + fallidas) |
| `review` | 0.15 | 5 | `tasks.output.review` (revisión Q1) | salidas aceptadas sin repetir el trabajo / salidas revisadas |
| `retries` | 0.10 | 5 | `events` tipo `task.retrying` | 1 - reintentos por tarea terminada (mínimo 0) |
| `golden` | 0.20 | 3 | `quality_golden_runs` (nueva) | ejecuciones que pasaron / ejecuciones |

- **Ventana móvil** de 7 a 90 días (`?window_days=`, por omisión 30). Aprobaciones se fechan por `resolved_at`, tareas por
  `finished_at`, eventos y ejecuciones por su sello de tiempo.
- **Muestra mínima**: un componente con menos observaciones que su mínimo **no cuenta** y sus pesos se reparten entre los
  demás, así que una señal ausente nunca se toma por mala. Si el total de observaciones de los componentes que cuentan es
  menor de 10 (o no cuenta ninguno), la respuesta es `status: "insufficient_data"` con `score: null`: no se inventa precisión.
- El puntaje es un entero; la reputación es una etiqueta sobre él: `excellent` (90+), `good` (75+), `fair` (50+),
  `needs_attention`. Nunca se muestra sin puntaje.
- **Tendencia**: puntaje de la ventana actual menos el de la anterior (misma duración). `up` / `down` si el cambio es de 5
  puntos o más, `stable` si no, `unknown` si alguna de las dos ventanas no tiene puntaje.
- No se cuentan como rechazo del agente los rechazos del sistema (nota que empieza por `sin respuesta` o `superseded:`:
  tiempo de espera agotado o reinicio del servidor).

Límites conocidos (honestos): las **ediciones** de una aprobación no existen hoy como señal, así que no forman parte del
puntaje; los **reintentos** solo se cuentan con Postgres (el almacén en memoria no lista eventos, y entonces el componente
queda fuera); la revisión Q1 es opt-in por proyecto, así que `review` suele tener poca muestra.

## Casos dorados

Un conjunto pequeño de la organización por agente (máx. 50): nombre, entrada y hasta 10 comprobaciones de texto
(`contains` / `not_contains`, sin distinguir mayúsculas). No hay juez LLM nuevo. Una **ejecución** se registra de dos maneras:

- `{"output": "..."}`: el servidor evalúa las comprobaciones; el texto **no se guarda** (solo aciertos y total).
- `{"passed": true|false}`: el veredicto viene de fuera (una persona o un arnés externo).

Las ejecuciones no dependen de la FK del caso: borrar un caso no reescribe el historial detrás de un puntaje. Un caso
inactivo no acepta ejecuciones.

## API (`/api/v1`)

| Método | Ruta | Permiso | Notas |
|---|---|---|---|
| GET | `/quality/agents?window_days=` | `agents:read` | `{methodology, generated_at, agents[]}`; ordenado de mayor a menor puntaje, los que no tienen puntaje al final |
| GET | `/agents/{id}/quality?window_days=` | `agents:read` | `{methodology, generated_at, agent}`; 404 si el agente no existe |
| GET / POST | `/agents/{id}/golden-cases` | `agents:read` / `agents:write` | POST `{name, input, checks[{kind,value}], active?}` -> 201 |
| PUT / DELETE | `/golden-cases/{id}` | `agents:write` | PUT reemplaza los campos editables; DELETE -> 204 |
| GET / POST | `/golden-cases/{id}/runs` | `agents:read` / `agents:write` | POST `{output}` o `{passed}`, opcional `task_id`, `note` -> 201; 409 si el caso está inactivo |

`agent` = `{agent_id, name, role, status, score, level, components[{key, weight, effective_weight, rate, sample, min_sample,
included}], sample, trend, trend_delta, previous_score, counts, golden_cases}`. `methodology` = `{version, window_days,
components[{key, weight, min_sample}], min_total_sample, trend_delta, advisory: true}`.

## Datos y seguridad

- Migración 789: `quality_golden_cases` y `quality_golden_runs`, RLS por organización (`tenant_isolation`), concedidas a
  `app_user`. El puntaje se calcula con consultas sobre `approvals`, `tasks` y `events`; no hay tabla de puntajes.
- Escrituras auditadas (sin la entrada del caso ni la salida): `quality.case_created`, `quality.case_updated`,
  `quality.case_deleted`, `quality.run_recorded` (entidad `quality_case`).
- Sin secretos: no se guardan salidas de agentes ni credenciales; el contenido del caso lo escribe una persona.
- Sin Postgres (demo) los casos viven en memoria y las señales salen del almacén de la aplicación; el modo simulado del
  frontend responde "datos insuficientes" porque no hay historial real que puntuar.
