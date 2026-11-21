# F2. Equipos listos para contratar, con vista previa: plan

Fecha: 2026-11-21. Rama de trabajo aislada; sin migración (se reservó la 825 y no hace falta).

## Objetivo

Un catálogo de equipos de negocio (cierre contable, cobranza, propuesta comercial, agencia de servicios, tienda online)
que se ve antes de contratar y se instala sin duplicar. Un equipo no repite nada: apunta a roles del catálogo de roles
(ellos ponen herramientas, autonomía inicial, techos y habilidades por defecto), a plantillas de flujo de la primera
semana y a los paquetes de onboarding a los que conviene.

## Diseño

- **Datos**: `backend/internal/catalog/data/teams/*.json` (`aiw.team/1`), cargados y validados por `catalog` junto a
  paquetes y plantillas (rol existente y sin repetir, plantilla y paquete existentes, textos es/en completos, día 1-5).
- **Vista previa**: función pura `application.PreviewTeam` (equipo + agentes de la organización + oficina destino): cada
  persona del equipo es `new` (con nombre sugerido: la identidad semilla del rol si está libre, si no el siguiente
  nombre del repertorio) o `existing` (ya hay alguien con ese rol en el departamento destino). Habilidades = unión de
  `default_skills` de los roles.
- **API** (`api/teams_catalog.go`): `GET /teams`, `GET /teams/{key}` (`?office_id=`, `?pack=`), `POST /teams/{key}/install`
  `{office_id?}`. Ver: `agents:read`. Instalar: `agents:write`, igual que `POST /agents/from-template`.
- **Instalar** reutiliza `Orchestrator.HireForRole` (auditoría, sin conexiones, autonomía y techos del rol), pone al
  agente en el departamento con `OrgChart.AssignAgent`, respeta el tope de la oficina y del plan antes de crear nada, y
  corre bajo un cerrojo por organización: instalar dos veces no duplica. Las habilidades por defecto no necesitan fila
  (el rol las trae). Las plantillas ya están disponibles en `/workflow-templates`; la respuesta dice cuáles quedan.
- **Departamentos**: sin `office_id` se usa el departamento con el nombre sugerido si existe, si no General. Un
  departamento privado del que la persona no es miembro responde como inexistente (404), también a un admin (D3).
  Instalar en un departamento exige gestionarlo (admin o responsable).
- **UI**: sección "Equipos" en el menú (siempre disponible), tarjetas, vista previa con "Llegan estas personas", "Esto
  harán la primera semana", "Ya tienes a…" y "Contratar este equipo" con elección de departamento. El paso de paquete
  del onboarding enlaza a la galería filtrada por ese paquete.

## Pasos (TDD)

1. `catalog`: equipos como datos + pruebas de validación y de contenido.
2. `application`: `PreviewTeam` + pruebas (con y sin agentes, otro departamento, nombres sin repetir).
3. `api`: rutas + pruebas (vista previa, instalación idempotente, permisos, aislamiento, departamento privado, topes).
4. Frontend: lógica pura + pruebas `node --test`, API + demo, galería y vista previa, menú, onboarding, i18n es/en/pt.
5. Documento `docs/architecture/team-catalog.md`.
