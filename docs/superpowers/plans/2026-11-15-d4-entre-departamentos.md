# D4. Entre departamentos — plan de implementación

**Goal:** un departamento comparte un archivo o una carpeta con otro, en solo lectura y con la aprobación de un
responsable del departamento dueño; lo recibido aparece en "Compartido conmigo". La columna Estado del Drive del
departamento gana "Borrador" y "Enviado".

**Spec:** `docs/superpowers/specs/2026-11-09-empresa-departamentos-design.md` §4.3 y §4.5 (fase D4).
**Fuera de D4:** "Pedir a otro departamento" (pedido en la cola de otro departamento) queda para E4.

## Decisiones del equipo

1. Los pedidos de compartir **no usan** la bandeja de aprobaciones (allí deciden por `AgentID` y aquí no hay agente):
   tienen su tabla y sus rutas, y el responsable los ve en el Drive de su departamento.
2. Deciden los **responsables** (`Office.Leads`) del departamento dueño; si no tiene responsables, cualquier miembro.
   Si quien pide puede decidir, el pedido nace activo.
3. Solo lectura. Se comparte un archivo o una carpeta **dentro** de `/Offices/<o>/` (no la raíz del departamento).
   Destino: otro departamento que no sea General ni el mismo.
4. "Enviado" y "Borrador" son etiquetas que pone la persona (`enviado`, `borrador`); cuando un agente escribe una
   versión nueva se quita `enviado` (igual que `revisado`). Prioridad en la columna: Para revisar > Enviado > Borrador.

## Contrato de la API (backend y frontend lo siguen al pie de la letra)

Tipo `Share` (JSON):
`{ id, path, kind: "file"|"folder", name, owner_office_id, target_office_id, status: "pending"|"active"|"rejected"|"revoked", requested_by, requested_at, decided_by?, decided_at?, can_decide: bool, can_revoke: bool }`

- `POST /drive/shares` `{ path, target_office_id }` → `201 Share`. Exige `edit` sobre `path`.
  Errores: `404` sin acceso o destino desconocido; `403 read_only_folder` con solo lectura; `400 not_in_office` si
  `path` no está estrictamente dentro de `/Offices/<o>/`; `400 same_office`; `400 general_office`;
  `409 already_shared` si ya hay uno `pending` o `active` para el mismo `path` y destino.
- `GET /drive/shares?office=<id>&dir=out|in[&status=…]` → `Share[]`.
  `out`: los pedidos que salen de `office` (solo sus miembros). `in`: los **activos** que llegan a `office` (solo
  sus miembros). Quien no es miembro recibe `404`.
- `GET /drive/shares?path=<p>` → los `pending`/`active` de ese archivo o carpeta (para el panel "Quién puede verlo";
  exige `read` sobre `path`).
- `POST /drive/shares/{id}/decision` `{ decision: "approve"|"reject" }` → `200 Share`. `403 not_decider`;
  `404` si la persona no es miembro del dueño; `409 not_pending`.
- `DELETE /drive/shares/{id}` → `204`; lo pueden hacer quien lo pidió y quienes deciden. Pasa a `revoked`.

Acceso:
- Persona: miembro del departamento destino de un share `active` → `read` sobre `path` y todo lo que cuelga de él
  (nunca `edit`). Todo lo demás de D1 no cambia (privado sigue en `404`).
- Agente: un agente del departamento destino lee lo compartido con su departamento (zona de solo lectura), siempre
  recortado por la persona por la que trabaja (`personLimit`). Nada de escritura.
- Revocar o rechazar quita el acceso en la siguiente petición (sin caché).
- Auditoría: pedir, decidir y revocar dejan una entrada en el registro de auditoría si el paquete ya ofrece una forma
  directa de escribirla.

## Tareas

### Tarea 1 (backend)
- Migración `821_drive_office_shares.sql` (+ `backend/migrations/down/821_drive_office_shares_down.sql`): tabla
  `drive_office_shares` (`id`, `org_id`, `path`, `kind`, `owner_office_id`, `target_office_id`, `status`,
  `requested_by`, `requested_at`, `decided_by`, `decided_at`), índice por `(org_id, target_office_id, status)`.
- Store en memoria y Postgres siguiendo el patrón del drive (añadir casos a `drive/drivetest/suite.go`).
- Servicio de shares en `internal/drive` (crear, decidir, revocar, listar) que pide a un adaptador los
  responsables/miembros de una oficina (implementado en `cmd/server` sobre `offices.Model`, como `officePeople`).
- Acceso: ampliar `personAccess` y `baseZone`/`zoneAll` según el contrato.
- Rutas en `internal/api/drive.go`. Etiqueta `enviado`: `reviewAfterWrite` también la quita.
- Pruebas: compartir y aprobar, rechazar, revocar, pedir siendo responsable (activo al nacer), no miembro (404),
  solo lectura (403), raíz del departamento (400), General y mismo departamento (400), duplicado (409), acceso de
  persona del destino (lee, no edita, no ve el resto del departamento dueño, buscar no devuelve lo no compartido),
  agente del destino (lee lo compartido y nada más; recortado por persona), revocado deja de leerse.

### Tarea 2 (frontend)
- `lib/drive.ts`: cliente de las rutas de arriba; mock en `lib/mock` para el modo demo.
- Árbol: sección "Compartido conmigo" al final de `DeptTree` (`dir=in`). Abrir una carpeta o un archivo compartido
  muestra la lista y el panel en solo lectura; sin "Pedir algo sobre esta carpeta" ni acciones de escritura.
- Menú "⋯": "Compartir con otro departamento…" (diálogo con selector de departamento) y "Marcar como enviado",
  "Marcar como borrador", "Quitar estado".
- Encima de la lista, para quien decide: "Pedidos para compartir" con Aprobar / Rechazar; para quien pidió, su pedido
  "Pendiente de aprobación".
- Panel del archivo: "Quién puede verlo": departamentos con acceso y "Dejar de compartir" si `can_revoke`.
- Estado: chips "Borrador" y "Enviado" con la prioridad de la decisión 4; lógica pura en `lib/deptLogic.ts` con
  pruebas en `deptLogic.test.mjs`.
- Textos en es/en/pt (`node scripts/check-i18n.mjs` en verde).

### Tarea 3 (docs)
- `docs/architecture/equipos-oficinas.md` (D4 hecha y lo que falta), `08-api.md` (rutas nuevas) y la guía
  `landing/guide-src/departments.json`.

Pruebas sin Docker: `go test ./...` en `backend/`; `npm run test:unit` y `npx tsc --noEmit -p .` en `frontend/`.
El equipo del dueño tiene poca memoria: no levantar servidores de desarrollo ni correr varias suites a la vez.
