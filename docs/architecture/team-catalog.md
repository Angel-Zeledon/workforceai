# Catálogo de equipos listos para contratar

Un equipo de negocio completo que se ve antes de contratar y se instala sin duplicar a nadie. Plan:
`docs/superpowers/plans/2026-11-21-equipos-listos.md`.

## Qué es un equipo

Un equipo es **solo datos** (`backend/internal/catalog/data/teams/*.json`, esquema `aiw.team/1`, versión por equipo).
No repite nada que ya exista: apunta a otras piezas.

| Campo | Para qué |
| --- | --- |
| `agents[]` `{role, does_key}` | Una persona por rol del catálogo de roles (`internal/roles`) y qué hará en este equipo. El rol pone herramientas, autonomía inicial, techos, habilidades por defecto y el nombre. |
| `week[]` `{template, day, why_key}` | Plantillas de flujo (`/workflow-templates`) de la primera semana, con el día (1 a 5) y por qué toca ese día. |
| `department_key` | Nombre sugerido del departamento destino. |
| `packs[]` | Paquetes de onboarding a los que conviene (`agency`, `commerce`, `general`, `professional_services`). |
| `name_key`, `for_key`, `solves_key` | Nombre, para quién es y qué resuelve en una frase. Textos es/en en `i18n`. |

`catalog.Load` valida al arrancar (un archivo malo es un error de compilación de datos, no de ejecución): rol existente
y sin repetir, plantilla y paquete existentes, día 1-5, textos es y en completos, sin campos desconocidos.

Equipos de esta versión: Cierre contable del mes (`month_close`), Cobranza (`collections`), Propuesta comercial
(`proposal`), Agencia de servicios (`agency`) y Tienda online (`online_store`). Son equipos de negocio, no de software.

## Vista previa para esta organización

`application.PreviewTeam(equipo, agentes de la organización, oficina de cada agente, destino, idioma)` es una función
pura. Por cada persona del equipo:

- `existing`: ya hay un agente con ese rol **en el departamento destino** (se muestra quién es). No se contrata otra vez.
- `new`: se contratará. Trae el **nombre sugerido** con la misma regla que `HireForRole`: la identidad semilla del rol
  mientras su id y su nombre estén libres; si no, el siguiente nombre libre de su repertorio. Dentro del mismo equipo
  los nombres sugeridos no se repiten.

El mismo rol en **otro** departamento no cuenta: el equipo trae a su propia persona al departamento elegido. Además
devuelve las habilidades por defecto (unión de `default_skills` de los roles).

## API

Todo bajo `/api/v1`, a cargo de `internal/api/teams_catalog.go`. La ruta `POST /teams` (crear departamento por
profesión, E1) sigue igual; estas son otras rutas del mismo prefijo.

| Ruta | Permiso | Qué hace |
| --- | --- | --- |
| `GET /teams?pack=&locale=` | `agents:read` | Tarjetas de la galería con cuántas personas llegarían y cuántas ya están (en el destino por defecto de cada equipo). |
| `GET /teams/{key}?office_id=&locale=` | `agents:read` | Vista previa completa: equipo, destino, personas (`new`/`existing`), habilidades, primera semana con nombre de cada plantilla, `can_install` y `blocked_reason` (`nothing_new`, `office_full`, `plan_limit`). |
| `POST /teams/{key}/install` `{office_id?}` | `agents:write` | Contrata solo a quien falta y devuelve `created` y la vista previa resultante (201 si contrató a alguien, 200 si ya estaba completo). |

`agents:write` es el mismo permiso de `POST /agents/from-template`: hoy lo tienen administradores y dueños.

### Instalar

1. Destino: `office_id` explícito, o si se omite, el departamento con el nombre sugerido si existe y la persona lo
   gestiona, o General. `general` es siempre válido.
2. Cerrojo por organización: dos instalaciones a la vez no contratan dos veces el mismo rol.
3. Topes antes de crear a nadie: la oficina (20 agentes) y el plan; si no caben todos, 409 o `plan_limit` y no se
   contrata a nadie.
4. Por cada rol nuevo se usa `Orchestrator.HireForRole`, el mismo camino del alta por rol: sin conexiones, autonomía
   inicial del rol bajo su techo, auditoría `agent.created_from_template` y evento de agente creado. Luego
   `OrgChart.AssignAgent` lo pone en el departamento.
5. Auditoría del equipo: `team.installed` con versión, destino, agentes creados y cuántos ya estaban.
6. **Habilidades por defecto**: vienen con el rol (`default_skills`, sin fila de asignación), como siempre; la
   habilidad tiene que existir y estar activa en la organización. Instalar un equipo no crea ni activa habilidades.
7. **Plantillas**: ya están disponibles en `/workflow-templates`; el equipo solo ordena las de la primera semana. Al
   contratar al equipo completo dejan de aparecer como "faltan roles".

Instalar dos veces no duplica: la segunda vez todo es `existing` y la respuesta es 200 con `created: []`.

## Departamentos y privacidad (D3)

- Un departamento privado del que la persona **no es miembro** responde como inexistente (404) en la vista previa y al
  instalar, también a un administrador. Tampoco se sugiere como destino por defecto.
- Instalar en un departamento exige gestionarlo (administrador o responsable) y poder verlo.
- La vista previa lee solo los agentes de la organización de quien llama. Si el agente que ya cubre un rol es el
  agente privado de otra persona, el rol cuenta como cubierto pero su nombre no se muestra.
- La oficina de cada agente se lee sin caché (`offices.Service.Fresh`) para que una instalación recién hecha ya cuente.

## Interfaz

- Sección **Equipos** del menú (`/teams`), siempre disponible, no solo en el onboarding.
- Galería de tarjetas (para quién es, qué resuelve, quiénes llegan, cuántos ya tienes). `?pack=` pone primero los
  equipos del paquete de negocio.
- Vista previa (`/teams?team=<key>`): **Llegan estas personas**, **Ya tienes a…**, **Esto harán la primera semana**,
  habilidades, y **Contratar este equipo** con elección de departamento (General y los que la persona gestiona).
- El paso de paquete del onboarding enlaza a la misma vista (`/teams?pack=<paquete>`, en otra pestaña para no perder lo
  elegido).
- Sin métricas de ahorro: la pantalla habla del trabajo (personas, flujos, habilidades), nunca de cuánto ahorra.

## Pendientes

- Un equipo no crea el departamento sugerido: si no existe, se contrata en General o en el que se elija.
- Si fallara `AssignAgent` a mitad de una instalación, el agente ya contratado queda en General y una segunda
  instalación contrataría otro en el departamento. Es un fallo de almacenamiento, no de lógica; se registra en el error.
- Idiomas: el catálogo está en es y en. Otros idiomas de la interfaz reciben español en los textos del equipo.
- Equipos de empresa propios: guardar un departamento como plantilla y llevarlo a otra empresa es F3
  (`office-templates.md`); usa esta misma vista previa e instalación.
