# Plantillas de oficina: guardar un departamento y llevarlo a otra empresa (F3)

Un contador o consultor monta su oficina tipo (personas con sus instrucciones, habilidades, plantillas de trabajo,
tareas programadas y objetivos) y la replica en cada cliente. Plan: `docs/superpowers/plans/2026-11-21-plantillas-de-oficina.md`.
API: sección 50 de `08-api.md`.

La exportación de respaldo (`GET /org/export`, `governance.md`) es otra cosa: un ZIP con todos los datos para la propia
empresa. Una plantilla es lo contrario: solo la forma de trabajar, sin ningún dato.

## El archivo

`aiw.office-template`, `format_version: 1`, JSON legible. Es el **mismo modelo que un equipo del catálogo** (F2,
`team-catalog.md`): personas por rol con lo que hacen, nombre, para quién es y qué resuelve. Lo extiende con lo
propio de la oficina:

| Campo | Qué lleva |
| --- | --- |
| `kind` | `department` (un departamento) o `company` (General y los departamentos que quien exporta ve). |
| `departments[] {key, name}` | La estructura: claves internas del archivo (`general`, `d1`, `d2`…) y nombres. Nunca ids. |
| `agents[]` | `department`, `role`, `does`, `name`, perfil de trabajo (`instructions`, `creativity`, `tone`, `tags`), `responsibilities` y los nombres de las habilidades asignadas. **Sin autonomía.** |
| `skills[] {name, skill_md, files[]}` | La versión **activa** (aprobada) de las habilidades asignadas: el `SKILL.md` y sus archivos de texto. Las de inicio (`builtin`) y las que no tienen versión aprobada no viajan. |
| `workflows[]` | Plantillas de trabajo propias compartidas (G2), con los agentes de cada paso escritos como **rol**. |
| `schedules[]` | Tareas programadas (plantilla del catálogo, hora, zona, días, parámetros). |
| `goals[]` | Objetivos activos (F1), de la empresa o de un departamento del archivo. |

Topes: 3 MB, 12 departamentos, 20 personas (el tope de una oficina), 20 habilidades, 10 plantillas de trabajo,
20 programaciones, 10 objetivos; cada habilidad con los límites de una importación de habilidades (2 MB, 512 KB por
archivo, 40 archivos, solo texto).

## Lista blanca, no lista negra

El archivo se construye campo a campo con los tipos de `internal/officetemplates` (`types.go`): nada se copia de una
estructura interna. Lo que no tiene campo no puede viajar: secretos, conexiones, credenciales, tokens, memoria,
archivos del drive, personas y correos, clientes, auditoría, chats, pedidos, informes, ids internos, rutas locales y
la autonomía ganada.

Además, al exportar, `Scrub` deja fuera (y lo dice en `left_out`) cualquier texto libre con forma de secreto
(`sanitize.RedactSecrets`), de correo, o que se lee como instrucciones para saltarse las reglas
(`sanitize.ScoreInjection`, el detector de notas y objetivos); una habilidad con un secreto no viaja y los correos
dentro de las demás se tapan. Un agente privado de otra persona tampoco viaja. Luego el archivo pasa por la misma
validación que una importación: lo que se exporta siempre se puede importar.

Prueba: `TestOfficeTemplateExportCarriesTheWorkAndNothingPrivate` siembra autonomía subida, pedidos, tareas, una
entrada de auditoría con un token, un archivo del drive, un objetivo archivado y un correo, y comprueba que el
archivo no contiene ninguno de esos valores, ni el correo ni el id de la persona, ni ids de organización,
departamento, habilidad, objetivo o plantilla.

## Importar

`POST /office-templates/import` con el archivo. `officetemplates.Parse`:

- JSON estricto: campo desconocido en cualquier nivel (por ejemplo `autonomy`, `connections`, `id`, `path`) = 400;
  datos después del objeto = 400; más de 3 MB = 413; `format` distinto o `format_version` mayor = 400.
- Roles, tonos, creatividad y plantillas del catálogo deben existir; referencias a departamentos coherentes.
- Textos: `application.CleanInstructions` (instrucciones), y en todos los textos libres el rechazo de secretos, correos
  e inyección. Habilidades: `skills.Build` (frontmatter, rutas sin `..`, extensiones de texto, tamaños) y sin secretos.
- Plantillas de trabajo: `projects.ValidateTemplate` (ciclos, dependencias, tamaños) con los roles en lugar de agentes.

Un archivo válido se guarda en la organización (`office_templates`, migración 826, RLS) con su huella: el mismo
contenido importado dos veces queda una sola vez. Aparece en **Equipos → Tus plantillas**.

## Vista previa e instalación

Es la de F2. Cada departamento del archivo se resuelve con `teamDestination` (el mismo sellado D3):

- `general` → General.
- En una plantilla de departamento, el destino elegido o, si no, el departamento con el mismo nombre que la persona
  gestiona y ve, o General.
- En una de empresa, cada departamento va al que tiene su nombre; los que no existen van al destino elegido (General
  por defecto). **No se crean departamentos** (como F2): quien instala los crea antes si los quiere.

`application.PreviewTeam` calcula quién llega y quién ya está (ahora el mismo rol puede aparecer dos veces: cada
agente existente cubre un solo puesto). El nombre del archivo se conserva si está libre en la empresa nueva.

Instalar (`POST /office-templates/{id}/install`), bajo el mismo cerrojo por organización que los equipos:

1. Topes de oficina (20) y de plan para todas las personas nuevas antes de contratar a nadie.
2. Cada persona nueva con `Orchestrator.HireForRoleNamed` (= `HireForRole` con el nombre del archivo si está libre):
   sin conexiones, **autonomía inicial del rol bajo su techo** (periodo de prueba; la confianza se gana otra vez) y
   auditoría `agent.created_from_template`. `OrgChart.AssignAgent` la pone en su departamento; luego su perfil
   (instrucciones, creatividad, tono, etiquetas) y sus responsabilidades. A un agente que ya estaba no se le toca.
3. Habilidades: si la organización ya tiene una con ese nombre, se reutiliza; si no, `skills.ImportPackage` la crea
   **en revisión** (`in_review`, nunca activa), en el departamento si todos los que la usan están en uno, o de la
   empresa. Se asigna a las personas nuevas que la tenían; ningún agente la recibe hasta que una persona la aprueba.
4. Plantillas de trabajo: se crean con el agente de cada rol (el de la plantilla, o cualquiera de ese rol); la que ya
   existe con ese nombre no se repite.
5. Programaciones: se crean **apagadas** (respetan el tope de 20 y el del plan); una igual no se repite.
6. Objetivos: `goals.Import`, activos solo si caben en el tope de 10; si no, archivados. Uno con el mismo título en el
   mismo sitio no se repite. Los de la empresa necesitan un administrador.
7. Auditoría `office_template.installed` con recuentos.

Lo que no se pudo (un objetivo de empresa sin ser admin, una plantilla sin agente para un rol) va en `skipped` y el
resto se instala. Instalar dos veces no crea nada: la segunda respuesta es 200 con todo `existing`.

## Permisos

| Acción | Quién |
| --- | --- |
| Exportar un departamento | Su responsable o un administrador, y que lo vea: un privado solo sus miembros (404 a los demás, admins incluidos). General: admin. |
| Exportar la empresa | Administrador; los departamentos privados de los que no es miembro quedan fuera. |
| Importar, instalar, borrar | `agents:write` (admin y dueño, como contratar). Instalar en un departamento exige gestionarlo y verlo. |
| Ver "Tus plantillas" y la vista previa | `agents:read`. |

## Interfaz

- **Organización → Oficinas**: en cada oficina que la persona gestiona, "Guardar como plantilla".
- **Ajustes → Guardar la empresa como plantilla** (administradores).
- Ambos muestran antes de descargar **Esto incluye** (recuentos) y **Esto NO incluye nunca** (conexiones y
  contraseñas, memoria, archivos, personas y correos, clientes, conversaciones, historial, la confianza ganada) y lo
  que se dejó fuera por seguridad.
- **Equipos**: botón "Importar plantilla" (comprueba tamaño y formato antes de subir) y la sección "Tus plantillas"
  con la misma vista previa (`/teams?template=<id>`): personas por departamento, habilidades (para revisión),
  plantillas de trabajo, programaciones (apagadas) y objetivos (activo o archivado), e "Instalar esta plantilla".
- Lógica pura en `frontend/src/lib/officeTemplatesLogic.ts` (+ prueba), API en `officeTemplatesApi.ts`, modo demo en
  `mock/office-templates-mock.ts`; claves `otpl.*` en es/en/pt.

## Piezas

- `backend/internal/officetemplates`: formato (`types.go`), `Parse`/`Validate` (`parse.go`), `Scrub` (`build.go`),
  conversión con plantillas de trabajo y equipos (`convert.go`), `Library` y almacén en memoria (`store.go`).
- Postgres `infrastructure/postgres/office_templates.go`, migración `826_office_templates.sql`, rollback
  `backend/migrations/down/826_office_templates_down.sql`.
- `application.PreviewTeam` (roles repetidos), `Orchestrator.HireForRoleNamed`, `skills.ActivePackage` / `ByName` /
  `ImportPackage` (`skills/templates.go`), `goals.Service.Import`.
- API `backend/internal/api/office_templates.go`; cableado `cmd/server/office_templates.go`.

## Pendientes

- No se crean los departamentos que faltan ni se copia su visibilidad (abierto/privado): la privacidad la decide la
  empresa nueva. Sus personas van al destino elegido.
- Las plantillas de trabajo no son de un departamento: en una plantilla de departamento viajan las que usan a alguno
  de sus roles. Las programaciones igual, por los roles de su plantilla del catálogo.
- Las personas de un mismo rol en el archivo no recuerdan cuál era cuál al reinstalar: el idempotente cuenta puestos
  por rol en el destino, no identidades.
- El tope de 30 plantillas guardadas y el de objetivos se serializan por proceso (como F1).
- Las pruebas de Postgres (`office_templates_pg_test.go`) necesitan `TEST_DATABASE_URL`; no se corrieron en esta rama.
  La interfaz no se probó en un navegador.
