# E1 "Alta y agentes por rol": plan de implementación

Fecha: 2026-11-03. Diseño: `docs/architecture/equipos-oficinas.md` §3, §4, §6, §10, §11 (fila E1) y
`docs/architecture/equipos-colaboracion.md` §5 y §7. E0 ya está hecha (commit 5e55648).

## Objetivo

Quien se registra dice **para quién es** (solo para mí / mi equipo / mi empresa con varios equipos) y **a qué se
dedica** (profesión). Su organización arranca solo con los agentes de su trabajo y el asistente, no con los 7 fijos.
Las invitaciones llevan equipo (departamento) y profesión; quien entra invitado llega a su equipo con sus agentes y una
bienvenida de su equipo. Con dominio verificado y autoingreso activo, quien se registra con ese correo entra a la
empresa existente en vez de crear otra. La bienvenida cambia según el caso: solo, responsable o miembro.

Un equipo es un **departamento** existente (`orgchart`). Las oficinas con permisos, la visibilidad de agentes y el
dueño de cada agente son E2: no se hacen aquí.

## Lo que hay hoy (resumen de la exploración)

- `auth.Service.Register` crea organización + usuario + membresía owner. `api.ensureSeeded` siembra los 7 agentes
  (`roles.SeedAgents()`) en la primera petición de una organización sin agentes.
- Invitaciones (`auth/invitations.go`, tabla `invitations`, mig. 300): correo + rol, límite de asientos (`SeatLimit`).
- Autoingreso por dominio (`sso/autojoin.go` + `auth.autoJoin`): solo después de que la dirección se demuestra (enlace
  de verificación o SSO). Hoy la persona igual crea su propia organización al registrarse.
- Departamentos, puestos de personas (`Placement`) y trabajos de agentes (`Job`) en `orgchart`; plantillas de industria
  (`catalog/data/industries`) crean departamentos y agentes.
- Frontend: `AuthScreen` (login/registro/invitación), `WelcomeOverlay` (bienvenida única), `OnboardingWizard`
  (3 pasos, desde el menú de admin), `MembersDialog` (miembros e invitaciones), recorridos en `components/tour`.

## Diseño técnico

### Datos (migración 809_team_signup.sql + down)

- `org_setup(org_id PK, audience solo|team|company, locale, created_at, setup_done_at)`: existe solo para organizaciones
  creadas con el alta nueva. Su presencia es lo que decide "no sembrar los 7 agentes".
- `member_profiles(org_id, user_id, profession, department_id, team_name, lead, via, invited_by, provisioned_at,
  created_at, updated_at)`: profesión y equipo de cada persona en cada organización.
- `signup_intents(user_id PK, audience, profession, org_name, team_name, locale, created_at)`: lo que eligió quien se
  registra para entrar por dominio, hasta que abre el enlace de verificación.
- `invitations` gana `department_id`, `profession`, `lead`.
- Sin RLS en las tres tablas nuevas, igual que `memberships`: se leen antes de conocer la organización (verificación,
  aceptar invitación) y solo las toca el módulo `auth`.

### Backend

1. `roles/professions.go`: catálogo de profesiones (legal, sales, accounting, marketing, operations, hr, direction,
   other) → roles sugeridos (siempre con `assistant`) y nombre de equipo por defecto (es/en/pt).
   `roles.SeedAgentsFor(roles, locale)` da los agentes semilla (id y nombre fijos) de esos roles.
2. `auth`: `RegisterInput` gana `audience`, `profession`, `team_name`, `join`. Sin `audience` el alta se comporta como
   antes (compatibilidad con integraciones y pruebas). Con `audience` guarda `org_setup` y el perfil del dueño.
   `join=true` + dominio con autoingreso + verificación activa: crea solo el usuario y una intención; al verificar entra
   a la empresa (perfil `via=domain`); si el autoingreso ya no aplica, crea su propia organización con la intención.
   `SignupCheck(email)` (POST público `/auth/signup/check`, limitado por IP) dice si el correo puede entrar a una
   empresa existente (solo nombre de la empresa, solo si su autoingreso está activo y la verificación está encendida).
   `CreateInvitation` acepta `department_id`, `profession`, `lead` (departamento validado con `Config.DepartmentExists`)
   y respeta el límite de asientos de siempre. `AcceptInvitation` crea el perfil del invitado. Métodos para leer
   perfiles, `org_setup`, marcar el alta de equipos hecha y elegir equipo/profesión propios.
3. `application.HireForRole`: agrega el agente de un rol con su identidad semilla si está libre, o como
   `HireFromTemplate`.
4. `orgchart.AssignAgent(ctx, agent, dept)`: pone un agente en un departamento sin tocar el resto de su trabajo.
5. `api`:
   - `ensureSeeded`: organización con `org_setup` → solo el asistente; sin `org_setup` → los 7 de siempre.
   - `ensureMember` (una vez por persona y proceso): si su perfil no está aprovisionado, crea los agentes que faltan de
     su profesión (topes de 20 y del plan), crea su equipo pendiente (`team_name`), la pone en su departamento (puesto),
     la hace responsable si `lead` y el departamento no tiene, y pone los agentes nuevos en ese departamento.
   - `GET /me/welcome`: tipo (`solo`, `owner`, `lead`, `member`, `classic`), equipo, responsable, compañeros, agentes
     sugeridos, quién invitó, si falta el alta de equipos y si puede elegir equipo.
   - `POST /me/team` `{department_id?, profession?}`: quien entró por dominio o SSO elige su equipo y su profesión.
   - `POST /teams` `{name, profession, head_me}` (org:manage): equipo = departamento + agentes de su profesión.
   - `POST /teams/setup/done` (org:manage): el alta de equipos del responsable queda hecha.

### Frontend

- `lib/teamsLogic.ts` (lógica pura + pruebas `node --test`) y `lib/teamsApi.ts`.
- Registro: "¿Para quién es?" (3 tarjetas), "¿A qué te dedicas?", nombre del equipo (equipo) y de la empresa
  (equipo/empresa); al escribir el correo se consulta `/auth/signup/check` y, si la empresa ya existe, se ofrece entrar.
- `WelcomeOverlay` con variantes: solo, responsable (abre el alta de equipos), responsable de equipo, miembro (bienvenida
  de su equipo; elige equipo si entró por dominio). `classic` = la de siempre.
- `TeamSetupWizard`: equipo (nombre + invitar ahora o después) o empresa (equipos de una lista, propios o plantilla de
  industria, y responsable de cada uno).
- `MembersDialog`: invitar con equipo, profesión y "responsable".
- Textos en `i18n/{es,en,pt}.json`.

## Pasos (TDD: prueba que falla, luego código)

1. `roles`: profesiones + `SeedAgentsFor` (pruebas en `roles_test.go`).
2. Migración 809 + down; `auth` store (memoria y Postgres) con perfiles, `org_setup`, intenciones y columnas nuevas de
   invitaciones (pruebas de contrato).
3. `auth.Register` con `audience`/`profession`; entrada por dominio; `SignupCheck` (pruebas de servicio y HTTP).
4. Invitaciones con equipo y profesión; perfil al aceptar (pruebas).
5. `application.HireForRole`, `orgchart.AssignAgent` (pruebas).
6. `api`: siembra, aprovisionamiento, `/me/welcome`, `/me/team`, `/teams`, `/teams/setup/done` (pruebas de API).
7. Frontend: lógica + API + registro + bienvenida + alta de equipos + invitaciones + i18n.
8. Docs: `equipos-oficinas.md` §11, `08-api.md`. Verificación completa.

## Decisiones mías (el dueño puede cambiarlas)

- **Alta sin "¿para quién es?" = la de antes.** Solo las organizaciones creadas con el formulario nuevo arrancan con
  los agentes de su profesión; una llamada a la API sin `audience` (integraciones, pruebas, cuentas de prueba
  `/auth/trial`) sigue creando los 7. Así nada de lo existente cambia.
- **Agentes por profesión** (siempre + asistente): abogacía → Legal; ventas → Ventas, Análisis; contabilidad →
  Contabilidad, Análisis; marketing → Ventas, Análisis (no hay plantilla de marketing todavía); operaciones →
  Operaciones, Project manager; RR. HH. → Recursos Humanos; dirección → Análisis, Project manager; otra → solo el
  asistente.
- **Entrar a un equipo sin aprobación en E1.** Un equipo todavía no da acceso a nada (eso llega con E2), así que quien
  entró por dominio elige su equipo directamente y queda en la auditoría. La aprobación del responsable llega con E2.
- **Entrar por dominio solo con autoingreso activo y verificación de correo encendida.** Si la empresa verificó su
  dominio pero no activó el autoingreso, se crea una organización propia como siempre (no se revela nada).
- **El responsable invitado entra con rol `member`** y la marca de responsable de su equipo (jefe del departamento);
  los poderes de responsable llegan con E2.
- **En E1 todos siguen viendo todos los agentes de la empresa**: lo que cambia es que la empresa solo tiene los de los
  trabajos de su gente. La visibilidad por persona y oficina es E2.
- **Plantillas de trabajo con roles que faltan** (pedido del coordinador): no se ocultan. La galería avisa qué roles
  faltan y que esos pasos los hará la asistente; al instanciar, cada paso va a un agente de su rol (aunque tenga otro
  id) o a la asistente, y queda auditado. Antes ya caían en la asistente, pero sin aviso y sin buscar por rol.

## Estado (2026-11-03)

Hecho: pasos 1 a 8. Pruebas Go de `roles`, `auth`, `orgchart`, `application`, `api`; lógica del frontend en
`src/lib/teamsLogic.test.mjs` (no está en el script `test:unit` para no tocar `package.json`, que edita otro agente).
Sin Postgres de pruebas local: las pruebas de contrato en Postgres (`TestPGProfilesContract`) se saltan.
