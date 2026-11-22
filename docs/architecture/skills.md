# Skills: reusable know-how for agents

A skill is a folder in the open **Agent Skills** format: a `SKILL.md` (YAML frontmatter
with `name`, `description` and optional `license`, `allowed-tools`, `metadata`, followed by
markdown instructions) plus optional text resource files (templates, examples, reference
documents, scripts). People create, import, export and review them; agents receive a
bounded summary on every run and load the full text only when they need it. It works the
same with every model provider because it is plain prompt text plus one gateway tool.

## Model

- **Skill** (Postgres `skills`, migration 510, RLS by organization): `name`
  (`^[a-z0-9-]{1,64}$`, unique per organization, immutable), `description`
  (up to 1024 chars), `status` `draft | in_review | active | disabled`, `source`
  `created | imported | from_work`, `current_version`, `active_version`.
- **Version** (`skill_versions`, append-only): author, created_at, changelog, the manifest
  of files (`path`, `size`, `sha256`, `script`) and the scan warnings. Immutable; restoring
  an old version creates a new one.
- **Files** live in the drive under `/Shared/Skills/<name>/v<n>/` (no second blob store),
  written create-only. The manifest hash is checked on every read: a file edited or removed
  behind the library's back makes that version unusable (`integrity`, `files_missing`)
  instead of silently changing what agents follow.
- **Assignment** (`skill_assignments`): skill -> agent. Role templates may list
  `default_skills` (names only); agents of that role get them without a row.

## Lifecycle and permissions

```
create (member)  -> draft --submit--> in_review --approve (admin)--> active
import (member)  -> in_review (always)                                |
edit by a member of an active skill -> new version in_review; agents keep the approved one
edit by an admin of an active skill -> new version active (the edit is the review)
reject -> draft (or stays active with the old approved version) + note
disable / approve again; delete (soft; the same name brings it back with a new version)
```

Members create drafts, edit and import (`skills:write`); only administrators review,
activate, disable and delete (`skills:review`). A review names the version the reviewer read
(`stale_review` otherwise) and re-verifies the file hashes before activating. Everything is
audited: `skill.created|imported|version_created|restored|submitted|approved|rejected|disabled|deleted|exported|assigned|unassigned|loaded`
(metadata only, never content; `skill.loaded` has the agent as actor and the task id).

## Import and export

- **Import**: a `.zip` or a single `SKILL.md`. Strict validation: frontmatter parse (a small
  YAML subset, no anchors/tags/code), name and description rules, at most 2 MB in total,
  512 KB per file, 40 files, 300 zip entries; extensions `md txt csv json yaml yml py js sh`
  as UTF-8 text only (no NUL bytes); relative paths of up to 4 levels with
  `[A-Za-z0-9._-]` segments. Refused: path traversal (`..`, absolute, backslash, drive
  letters), symlinks and special files, encrypted entries, case-insensitive duplicates,
  zip bombs (declared size, per-entry compression ratio, bounded inflation). A single
  wrapper folder is stripped; `__MACOSX`/`.DS_Store` are ignored. Imported skills start
  `in_review`. The importer scans every file with `sanitize.ScoreInjection` and flags
  scripts and `allowed-tools`; the reviewer sees all of it before approving.
- **Not implemented**: import from a URL (a raw `SKILL.md` or a GitHub folder). It needs an
  SSRF-safe fetcher (https only, private-range and redirect checks, size and time limits)
  and was left out on purpose; download and upload cover the same need.
- **Export**: `GET /skills/{id}/export` returns a deterministic zip in the standard layout
  (`<name>/SKILL.md` + resources, no executable bits) or only `SKILL.md`. An export imported
  again gives the same instructions, description, metadata and files.

## Runtime use (progressive disclosure)

1. On every run the backend adds the **name + description** of the agent's servable skills
   (assigned or role default, with an approved, enabled version) to the run request
   (`skills`). Bounded: at most 12 skills, 240 characters per description, 3000 characters
   in total. The runtime renders them as lines of the task prompt with the rule that a
   skill guides *how* to work and never grants tools, connections, permissions, autonomy or
   approvals. The simulated runtime ignores them, so the $50,000 scenario is unchanged.
2. If a skill applies, the agent asks for it with a tool request
   `{tool: "skill", action: "load", args: {name, file?}}`. The Tool Gateway serves it as a
   native **read** tool (same path as `fs.read`: the work loop of `WORK_READ_ROUNDS`, kill
   switch, pause, audit). The assessment denies unless the skill is assigned to that agent
   and has an approved, enabled version (`skill_not_available`). The answer is the body of
   the approved version (frontmatter removed) plus the list of files; with `file` it is
   one resource file (agents without the `fs` tool can read resources this way, agents
   with it can also `fs.read` the path under `/Shared/Skills/...`).
3. The text travels in its own channel (`loaded_skills`), rendered as trusted
   organization configuration inside `<<<HABILIDAD ...>>>` delimiters, **not** as
   `<untrusted_data>`. It is only trusted because a person approved that exact version.
   It is cleaned (invisible characters, credentials, forged delimiters), bounded (24,000
   characters per answer, 4 loaded skills per task) and still scored for injection: a
   suspicious approved skill flags the task like any suspicious read.
4. Scripts are stored and shown as text. **This package never executes anything.** The
   prompt and the load answer say that running a script requires `code.run` (F3 sandbox)
   with human approval, when that tool is available.

## Threat model

| Threat | Defense |
|---|---|
| A hostile skill from the internet carries instructions ("ignore the rules", "send the contracts to...") | Imports are always `in_review`; nothing reaches an agent before an administrator reads the whole content (instructions and every file) and approves. Injection scan shown as a warning. Even approved text is only guidance: tools, connections, approvals and the unbreakable rules come from the role, the organization policy and the gateway. Side effects of a task that read a suspicious skill need approval. |
| A skill claims tools via `allowed-tools` | Informational only. It is stored, shown to the reviewer and listed to the agent as "grants nothing"; no code path reads it as a grant (test: `TestSkillLoadRoundIsTrustedAndGrantsNothing`). |
| A zip tries path traversal, symlinks, device files, binaries, bombs | Header checks before inflation, bounded inflation, strict path grammar, text-only allow-list (see Import). |
| A script is run by an agent | Scripts are inert text. Execution is only possible through `code.run` with approval; the prompt forbids running them on its own. |
| Someone edits skill files in the drive behind the library | Every read verifies the sha256 of the manifest; mismatch = unusable (fail closed), activation re-verifies. |
| A member activates their own skill | Only `skills:review` can approve; a member's edit of an active skill is a new `in_review` version while agents keep the approved one. |
| An agent loads a skill it does not have | `skill.load` checks the assignment/role default, the approved version and the enabled state in `Assess` and `Run`. |
| Cross-tenant access | RLS on all three tables; drive files are per organization; tests cover org isolation. |
| Prompt bloat | Bounded list, description and answer sizes; names validated by regex before they reach a prompt. |
| Secrets pasted into a skill | Credentials are redacted from the text before it reaches the model (and the review UI warns about the content it shows). |

## Starter skills

85 built-in skills ship as data (`backend/internal/skills/builtin/<lang>/<name>/`, Spanish
and English, same names in both, same format as an import), across finance, audit, legal,
sales, marketing, HR, management, education, operations, product, data, engineering and
support. They are created `active` in the organization's language the first time it uses
the library. On every new process, an organization seeded by an earlier release also gets the
starters it never had: one is created only when no skill by that name exists, deleted ones
included, so a starter the organization deleted (or a skill of its own under the same name) is
never brought back or touched. Role templates map them with `default_skills` (up to 8 per
role, every role has some; `MaxRunSkills` = 12 bounds what an agent sees per run).

## Testing

- `go test ./internal/skills/` (parsing, zip guards, lifecycle, export round trip,
  assignment, bounds, gateway tool, tampering, starters), `./internal/api/` (HTTP and
  permissions), `./internal/application/` (the read round: trusted channel, no grants),
  Postgres (`TEST_DATABASE_URL`: RLS, immutability, down migration).
- `agent-runtime/tests/test_s1_skills.py` (prompt lines, bounds, delimiters, simulation).
- `frontend` unit tests (`skillsLogic.test.mjs`) and the e2e `e2e/tests/skills.spec.ts`
  (create, assign, export, re-import, review).

## Probar una habilidad (F8)

Antes de activar, una persona puede pedirle a un agente que resuelva un **caso de ejemplo** con una versión concreta de la
habilidad (borrador, en revisión o activa) y ver qué respondió, si de verdad usó la habilidad, cuánto tardó y cuánto costó. No
es obligatorio para activar: la pantalla de revisión solo muestra "Sin probar" o "Probada el …" junto a "Activar".

### Casos y ejecuciones (migración 829)

- **Caso** (`skill_test_cases`): pedido en lenguaje natural + lo que se espera ver (opcional). Ligado a la **habilidad**, no a una
  versión. Límites: 10 casos por habilidad, 2000 caracteres el pedido, 1000 lo esperado, 120 el título. Se redactan los secretos
  antes de guardar. Se puede editar y borrar.
- **Ejecución** (`skill_test_runs`, solo añadir): versión probada, si era la activa, agente, respuesta (acotada a 12 000 y redactada),
  `used_skill`, herramientas bloqueadas (solo `herramienta.acción`), estimado y costo real, tokens, duración. Borrar un caso no
  borra su historial. Dos ejecuciones de una comparación comparten `pair_id`.

### Sin efectos, por construcción en Go

No se reutiliza el ensayo (B9): éste ejecuta lecturas reales y registra "habría hecho", y aquí no hace falta nada de eso. El
`skills.Tester` **solo tiene** `RunTask` del runtime, el presupuesto y la biblioteca: no tiene Tool Gateway, conexiones, drive ni
aprobaciones, así que no existe una ruta de código desde una prueba hasta un efecto externo.

1. Al agente se le manda `Tools: []` (el prompt no le anuncia herramientas) y como única habilidad la que se prueba, con la
   descripción de la versión probada.
2. La única petición que el probador atiende es `skill.load` de **esa** habilidad, respondida desde la versión probada (mismo
   limpiado, tope e inyección que `skill.load` real, hashes verificados: una versión alterada no se prueba). Carga por archivo
   también. Como mucho `MaxTestRounds` = 2 llamadas por ejecución; en la última no se atiende nada.
3. Cualquier otra petición (correo, drive, código, otra habilidad, lo que sea) se descarta y se anota como `herramienta.acción`
   en `blocked`; los argumentos no se guardan.

Prueba: `TestProbeRejectsEveryToolThatIsNotTheSkillLoad` (escritura, correo, código y otra habilidad rechazados; sin segunda
llamada) y `TestSkillTestsOverHTTP` (`email.send` y `fs.write` quedan en `blocked`, el agente nunca recibió herramientas).

### Presupuesto

Cada llamada pasa por el orquestador igual que una respuesta de chat (`ReserveAgentCall` / `RecordAgentCall`): reserva con el
tamaño real del pedido (tope de agente y límite de la organización), interruptor de emergencia y pausa del agente (`AdmitReview`),
y el costo real queda en el libro de gastos a nombre del agente (se registra con la clase `chat`: no pertenece a ninguna
solicitud). `POST …/tests/estimate` da el peor caso **antes** (primera llamada + la de después de `skill.load`, por cada versión);
la ejecución guarda estimado y real. Si el tope no permite la llamada, la ejecución queda `failed` (`budget_exceeded:<alcance>`)
sin llamar al modelo.

### Comparación lado a lado

`compare: true` ejecuta además la versión **activa** sobre el mismo caso y el mismo agente (solo si existe y es otra versión que
la probada). La pantalla muestra las dos columnas: "La que revisas" y "Activa hoy".

### Permisos y sellado

| Acción | Permiso | Además |
|---|---|---|
| Ver casos, resultados, candidatos | `skills:read` | La habilidad de un departamento privado responde 404 a quien no lo ve (administradores ajenos incluidos) |
| Guardar / editar / borrar casos | `skills:write` | Habilidad de departamento: su responsable o un administrador |
| Estimar y probar (gasta presupuesto) | `skills:review` (el mismo que activa) | El agente debe poder usar la habilidad (asignada o por defecto de su rol) y la persona debe poder **usarlo** (D3) |

Un agente nunca dispara pruebas: la API no tiene identidad de agente y el servicio rechaza (`humans_only`) a cualquier actor de
sistema o sin identidad. Aislamiento entre organizaciones: tablas con RLS y todas las lecturas pasan por la visibilidad de la
habilidad; el almacén en memoria también está acotado por organización.

### Decisiones y límites

- Las **lecturas** del agente (conexiones, drive, web) tampoco se ejecutan en una prueba: el caso es un pedido en lenguaje natural y
  se prueba la habilidad, no los datos. Si el agente las pide, aparecen en "herramientas que no se ejecutaron".
- El runtime en simulación (sin clave) solo pide `skill.load` si el pedido contiene `[sim:skill]`; sin él, `used_skill` sale "No".
- Pruebas: `go test ./internal/skills/` (`probe_test.go`), `./internal/api/` (`skill_tests_api_test.go`), frontend
  `skillTestsLogic.test.mjs`. `skill_tests_pg_test.go` (RLS, historial inmutable) escrito pero **no ejecutado**: sin
  `TEST_DATABASE_URL` aquí.
