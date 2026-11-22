# Puestos dentro del departamento

"Algunos archivos deberían servirse como RAG para que cada agente busque lo que le toca según su rol… gerente de
departamento, y luego asistente de gerente que no tiene sus mismas skills o acceso." Un **puesto** es un cargo dentro de
un departamento ("Gerente de Contabilidad", "Asistente de gerente", "Especialista"). No sustituye al **oficio** (la
plantilla de rol: herramientas, autonomía, `default_skills`) ni a la descripción de trabajo del organigrama
(`orgchart.Job`): se suma a ellos y decide dos cosas, **qué habilidades** recibe el agente y **qué carpetas consulta**.

Código: `backend/internal/positions` (tipos `types.go`, reglas puras `rules.go`, servicio `service.go`, plantillas
`templates.go`, `MemStore`), `backend/internal/infrastructure/postgres/positions.go`, migración
`830_office_positions.sql` (+ `migrations/down/830_office_positions_down.sql`), drive `backend/internal/drive/library.go`,
habilidades `backend/internal/skills/assign.go` (vía `position`), API `backend/internal/api/positions.go`, cableado
`backend/cmd/server/positions.go`. UI: `frontend/src/components/positions/*` (pantalla "Puestos" en `/d/{office}/puestos`,
editor y selector de carpetas en árbol), `frontend/src/components/agent/PositionSection.tsx` ("Su puesto"),
`frontend/src/lib/positions.ts`, `positionsLogic.ts` (probado), `mock/positions-mock.ts`. API: sección 55 de `08-api.md`.

## Modelo

- **Puesto** (`office_positions`): departamento (`office_id`), nombre (único por departamento, 80 caracteres), `reports_to`
  (otro puesto del mismo departamento, sin ciclos), `template` (`manager | assistant | specialist` o vacío) y el
  **paquete**: `skills` (ids de la biblioteca de habilidades: de la empresa o de ese departamento) y `library` (carpetas
  con nivel `read | edit | none`; `none` es una exclusión, el `!ruta` de las reglas). Límites: 30 puestos por
  departamento, 40 habilidades y 40 carpetas por puesto. General no es un departamento: no tiene puestos.
- **Asignación** (`agent_positions`): como mucho un puesto por agente, y solo de **su** departamento. Guarda el
  departamento en el que se dio: si el agente pasa a otro departamento, en la siguiente lectura el puesto deja de
  aplicarse al instante, la asignación se borra y queda auditado (`position.unassigned`, `reason: moved_department`).
- **Biblioteca vacía = sin biblioteca**: el agente ve lo de siempre (compatibilidad total). Un recorte que deja "nada"
  se guarda explícito (`/Shared` en `none`), nunca como vacío.

## Biblioteca del puesto (lo que el agente busca y lee)

La biblioteca actúa **solo sobre la base que el agente ya tiene por su departamento**, y nunca más allá:

| Base | Sin puesto | Con biblioteca |
|---|---|---|
| Su departamento `/Offices/<suyo>` | Editar | solo lo que la biblioteca incluye (Leer o Editar), menos sus exclusiones |
| `/Shared` | Leer | solo lo que la biblioteca incluye (puede subir a Editar) |
| Lo compartido hacia su departamento (D4) | Leer | solo lo que la biblioteca incluye; **siempre** solo Leer |
| Su escritorio `/Agents/<él>` | Editar | **igual** (nunca cambia) |
| Sus proyectos | Editar | **igual** |
| Reglas puestas a mano (`drive_grants`) | — | **igual**: ganan cuando son al menos tan profundas como la entrada de la biblioteca con que se cruzan |
| Otros departamentos, `/Clients`, otros escritorios | cerrado | cerrado: una entrada que apunte ahí se ignora |

Implementación: `scopeOf` carga la biblioteca (`drive.AgentLibraries`, que implementa `positions.Service`); las entradas
fuera de la base se descartan al cargar (un compartido revocado no abre nada). En la clasificación de zonas el orden es
`baseZone → applyPosition → applyRule (manuales) → aislamiento de cliente → personLimit`; un camino de la base que la
biblioteca no cubre es `ZonePositionOut` (no se lista, no se lee, no se busca, no se recuerda). `readableRoots` reemplaza
las raíces de base (`/Shared`, su oficina, lo compartido) por las entradas de la biblioteca con sus exclusiones, así
`fs.search` (palabra, significado e híbrida), `fs.recent`, `fs.list` y la memoria larga (`memory/retrieve.go`, que usa
`ReadableRoots` y `ReadableFilter`) quedan en el mismo alcance. Escribir fuera de la biblioteca pide aprobación, como
fuera de su área.

**Motivo claro sin filtrar al agente:** leer (o buscar en) algo fuera de su biblioteca responde al agente igual que un
archivo inexistente (`not_found: no such file`) y deja en la auditoría `drive.read_outside_position` con el agente, la
ruta, el puesto y `reason: outside_position`. Un error al leer el puesto deja al agente sin nada legible (falla cerrado).

## Habilidades del puesto

El agente recibe, en este **orden de prioridad**: asignadas directamente > del puesto > del oficio (`default_skills`); a
igual origen, por nombre. El tope de `MaxRunSkills` (12) y de caracteres se aplica en ese orden, así que un puesto nunca
desplaza una habilidad que una persona asignó, y el oficio queda al final. Una habilidad de otro departamento nunca
llega (ni se puede guardar en el puesto: responde `skill_invalid`, sin revelar que existe). `skill.load` acepta las del
puesto como las demás. Si la fuente de puestos falla, se omiten sus habilidades (menos, nunca más).

## Regla de subordinación

Un puesto que reporta a otro no puede tener **ninguna habilidad ni carpeta ni nivel** que su superior no tenga
(`positions.Exceeds`; una biblioteca vacía cuenta como la base en ambos lados, así un asistente "sin biblioteca" bajo un
gerente con biblioteca **sí** lo supera).

- Al crear o editar un subordinado que supera a su superior: `400 exceeds_superior` con `details` (qué sobra).
- Al recortar a un superior, sus subordinados (y los de ellos) se recortan en la misma transacción (`positions.Clamp`:
  en cada carpeta el menor de los dos niveles, conservando las exclusiones del superior). Con `dry_run` la API devuelve
  antes qué pierde cada uno (`impact`) sin guardar nada; la pantalla lo muestra y pide confirmar. Cada recorte queda
  auditado (`position.cut`).
- Al borrar un puesto, sus subordinados pasan a reportar al superior del borrado (ya cabían en él) y quien lo ocupaba
  queda sin puesto.
- Las reglas a mano de un agente son una excepción explícita de una persona: se muestran como "Manual" y no cuentan
  para la regla (que es entre puestos).

## Plantillas de arranque

Gerente (todo el departamento en Editar, `/Shared` en Leer, habilidades de gestión de la biblioteca de arranque si
existen), Asistente de gerente (departamento y `/Shared` en Leer, agenda/actas/estado semanal) y Especialista
(departamento en Editar, `/Shared` en Leer, sin habilidades: las trae su oficio). Las de subordinado ya vienen
recortadas al gerente del departamento. Se crean con un clic (nombre en el idioma de la persona) y se editan después.

## Permisos, sellado y auditoría

| Acción | Quién |
|---|---|
| Ver puestos de un departamento y el puesto de un agente | `agents:read`; un departamento **privado** responde 404 a quien no es miembro (administradores incluidos, D3) |
| Crear, editar, borrar, asignar, quitar | admin/owner o responsable (lead) de ese departamento; nunca un agente, un token ni el sistema (`humans_only`) |

Auditoría: `position.created|updated|cut|deleted|assigned|unassigned` y `drive.read_outside_position`. Aislamiento entre
organizaciones: RLS por `org_id` en ambas tablas y filtros en cada consulta; `MemStore` separado por organización.
**Tiempo real:** ni oficinas ni habilidades emiten eventos en vivo en este repo, así que los puestos tampoco (la pantalla
recarga tras cada cambio).

**Historial (F4):** el nombre del puesto entra en la instantánea del agente (`position`); asignar o quitar
(`PUT|DELETE /agents/{id}/position`) deja una versión, y "volver a como estaba" nunca lo restaura (se concede a mano).
Editar el paquete de un puesto no crea versiones de sus agentes (está auditado en el puesto). Las rutas
`/offices/{id}/positions…` caen en las rutas de alcance de organización del gancho.

## Pruebas

- `go test ./internal/positions/` — reglas (nivel efectivo, exceso, recorte), permisos, sellado, validación, cascada con
  vista previa, plantillas, mover de departamento, borrado, aislamiento entre organizaciones.
- `go test ./internal/drive/ -run Library` — sin biblioteca = igual que hoy; con biblioteca encuentra solo lo suyo +
  escritorio y proyectos; nunca abre otro departamento; niveles; reglas a mano; `personLimit`; falla cerrado; auditoría.
- `go test ./internal/skills/ -run Position` — prioridad directas > puesto > oficio y tope.
- `go test ./internal/api/ -run "Position|EveryAgentChange"` — HTTP, permisos, 404 privado, transparencia, versiones.
- `positions_pg_test.go` (RLS, re-padre al borrar, nombres únicos) escrito pero **no ejecutado**: sin `TEST_DATABASE_URL`.
- Frontend: `positionsLogic.test.mjs`, `panelTabs.test.mjs`.

## No verificado / pendiente

- La UI no se probó en un navegador (solo `tsc`, pruebas de lógica, `check:i18n` y `build`).
- Con varias réplicas, dos cambios simultáneos al mismo árbol de puestos pueden cruzarse (el candado es por proceso).
- Exportar puestos en una plantilla de oficina (F3) no está incluido.
