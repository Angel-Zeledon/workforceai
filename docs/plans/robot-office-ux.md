# Plan: dirigir literalmente una oficina de robots

Fecha: 2026-10-07. Estado: propuesta para el dueño; no hay código en este plan.
Prosa en español; identificadores, endpoints, claves JSON y `data-testid` en inglés.
Las estimaciones de esfuerzo son juicio propio (días-persona de 1 dev frontend, salvo que se diga otra cosa), no medición.

Objetivo: que usar la app se sienta como **ser el jefe de una oficina de robots**: caminas por ella, le hablas al robot de su escritorio, le pasas trabajo en la mano, firmas en tu mesa de aprobaciones y ves quién levanta la mano. Hoy se siente más como una aplicación con una escena 3D decorativa al lado.

Se apoya en (y no duplica) las ideas de `docs/prompts/next-features.md`: B1 ascensos por confianza, B2 proactivos, B3 "mientras dormías", B4 rituales, B5 reuniones, B17 aprobaciones en 1 minuto.

---

## 1. Diagnóstico: dónde se siente "software" y no "oficina"

| # | Observación | Evidencia |
|---|---|---|
| D1 | **Mandar trabajo es escribir en una barra global**, no hablarle a alguien. La barra (con sugerencias fijas) va al canal `office` y el router decide quién responde. | `components/CommandBar.tsx` (`SUGGESTIONS`), `docs/architecture/chat-routing.md` §2 |
| D2 | **Hablar con un robot concreto exige 2 pasos y un panel de 9 pestañas** (chat, estado, tareas, chats, memoria, reportes, archivos, actividad, perfil) detrás de una rueda de secciones. Es potente pero es "ficha de CRM", no "acercarse al escritorio". | `components/AgentPanel.tsx` (`TABS`, `TAB_ICON`), `components/SectionWheel.tsx` |
| D3 | **Las aprobaciones viven en una bandeja flotante** de la interfaz; en la escena existe una "Zona de aprobaciones" pintada en el suelo, pero sólo es un letrero (y con texto en español fijo, fuera de i18n). | `components/ApprovalsTray.tsx`, `components/office/OfficeEnv.tsx:318` (`FloorSign` "Zona de aprobaciones") |
| D4 | **La sala de reuniones es decoración**: nadie la usa. Los personajes sí caminan, pero sólo para visitar a otro en una consulta. | `OfficeEnv.tsx` `MeetingRoom` (:259), `Character.tsx:241-254` (`visit` en `consult`) |
| D5 | **El jefe no está en la escena.** No hay avatar del usuario ni "tu escritorio"; `USER_POS` existe sólo como punto de llegada de las líneas. | `lib/meta.ts` `USER_POS`, `components/office/Links.tsx` `endpoint()` |
| D6 | **Gestionar al equipo es un menú de administración** (conexiones, plantillas, configuración), no "contratar, ascender o mandar a capacitación". La autonomía es un campo (`suggest/approve_each/rules/autonomous`). | `components/admin/AdminMenu.tsx`, `06-permisos-autonomia.md` §3 |
| D7 | **Los proyectos son otra vista** (`nav-projects`) con su grafo; no hay "pizarrón" en la oficina que muestre en qué está el equipo. | `components/Shell.tsx` (`ProjectsView`, `ProjectPill`), `workflow-visualization.md` |
| D8 | **Los robots no avisan "con el cuerpo" de forma uniforme**: hay iconos por estado (✓, alerta, ámbar) y un rebote en la etiqueta, pero no un gesto claro de "te necesito" que se vea desde lejos. | `Character.tsx:328-333` (`checkG`, `alertG`, `warnG`), `LabelLayer.tsx` (`ac-bounce` si `attention`) |
| D9 | **El aspecto es de personas** (pelo, piel, corbata) aunque el producto habla de "empleados de IA"; el usuario pide "robots". | `lib/meta.ts` `ROLE_META` (`skin`, `hair`, `hairStyle`, `tie`) |

Lo que **ya está bien y se conserva**: toda animación deriva de eventos reales (`agent.state_changed`, `chat.message`, `approval.*`), chat 1:1 `agent:<id>` con regla dura de "sólo ese agente", modo dashboard como alternativa completa, `reduceMotion` en preferencias (`lib/preferences.ts`).

---

## 2. La metáfora, concreta

Eres el jefe. Tu oficina tiene escritorios con robots, una **mesa del jefe** (tu bandeja de firmas), una **sala de juntas**, un **pizarrón** y una **puerta de recursos humanos**. Reglas de la metáfora:

1. **Todo se hace "sobre" algo físico**: un robot, su escritorio, tu mesa, el pizarrón. La barra global queda como atajo ("hablar con toda la oficina").
2. **Los robots te buscan a ti**, no al revés: cuando necesitan algo levantan la mano / encienden su baliza y, si lo permites, caminan hasta tu mesa con el papel.
3. **Tú nunca pierdes el control**: los robots piden, nunca firman; el botón rojo de la oficina (kill-switch) es un objeto visible en la escena además del de la interfaz.
4. **Siempre hay una vista de lista** equivalente (dashboard) y todo funciona con teclado y con movimiento reducido.

### Ideas de interacción (catálogo)

| Idea | Lo que haces | Lo que ves |
|---|---|---|
| Hablarle a un robot | Clic en su escritorio (o en él) | La cámara se acerca, aparece un **globo de chat anclado al robot** (no un panel lateral) con su conversación `agent:<id>`; el panel completo sigue a un clic ("Ver ficha") |
| Pasar trabajo en la mano | Arrastras una **tarjeta** (de una sugerencia, de un correo, de un artefacto o escrita) sobre un robot | El robot "la recoge", responde en su globo si es suyo o propone a quién pasarla (reglas de fuera de competencia ya existentes) |
| Mesa del jefe | Clic en tu mesa (o atajo `A`) | Pila de papeles = aprobaciones pendientes; cada una con quién la pide, qué, riesgo, costo; firmar / rechazar |
| Robots que levantan la mano | Nada: miras la oficina | Baliza en la cabeza (ámbar = te espera, rojo = bloqueado, verde = terminó) y mano arriba; visible desde cualquier ángulo y en el minimapa |
| Llevarte el papel | Opcional ("los robots me traen los papeles") | El robot camina a tu mesa con un sobre y vuelve; el sobre queda en la pila |
| Stand-up | A la hora configurada, o botón "Junta ahora" | Los robots caminan a la sala de juntas; cada uno dice en su globo qué hizo y qué hará (B4) |
| Pizarrón | Clic en el pizarrón de la pared | Proyectos activos como columnas con tarjetas por robot; clic abre `ProjectsView` en ese proyecto |
| Recursos humanos | Clic en la puerta de RR. HH. | Contratar (desde plantillas de rol, A5), "ascender" (B1: subir autonomía con historial), "mandar a capacitación" (editar instrucciones/memoria), dar de baja |
| Botón rojo | Clic en el botón rojo de la pared | Mismo `freeze`/`lockdown` que hoy, con confirmación; la oficina se oscurece y los robots se quedan quietos |
| Turno día/noche | Automático por hora real (opcional) | Luz de la escena según la hora local; de noche los robots "duermen" en su base salvo trabajo programado (B3 al volver) |

### Aspecto: ¿personas o robots?

| Opción | Pros | Contras |
|---|---|---|
| A. Mantener personas | Cero trabajo; la oficina ya se ve bien | No cumple el pedido "oficina de robots"; personas con piel y pelo sugieren que son humanos |
| B. **Piel "robot" como preferencia** (recomendada para empezar) | Reutiliza el esqueleto y las poses de `Character.tsx` (cambian materiales y la cabeza: visor/antena en vez de pelo/piel; `ROLE_META.color` como chapa); el usuario elige | Dos estilos que mantener; las poses "humanas" (sentarse, teclear) se ven algo extrañas en robots |
| C. Rediseño completo a robots | Identidad clara y diferenciadora; permite gestos propios (baliza, antena, pantalla-cara con expresiones) | 2-3 semanas de modelado/animación; rompe capturas de la landing y tests visuales |

Recomendación: B ahora (wave 1), decidir C con datos de uso.

---

## 3. Propuesta priorizada en 3 olas

Convenciones por ítem: **Haces / Ves / Contratos / Esfuerzo / Riesgos / Reglas**. "Contrato nuevo" se marca con ⚠ y requiere visto bueno.

### Ola 1: ganancias rápidas (1-2 semanas en total)

**1.1 Clic en el robot = hablarle (globo de chat anclado).**
- Haces: clic en un robot. Ves: globo con su chat `agent:<id>` junto a él, campo para escribir, "Ver ficha" abre el `AgentPanel` actual.
- Contratos: ninguno; usa `POST /messages` con `conversation: "agent:<id>"` y `chat.message`/`chat.typing` existentes.
- Esfuerzo: 3 días. Riesgos: solapamiento con etiquetas (reutilizar `LabelSolver`). Reglas: el globo es un diálogo accesible (`role="dialog"`, foco, `Esc`); con `reduceMotion` sin zoom de cámara.

**1.2 Baliza "te necesito" + mano arriba.**
- Ves: baliza de color en la cabeza para `awaiting_approval`, `blocked`, `error`, `completed`; contador en la barra superior "3 robots te esperan" que al pulsarlo recorre la cámara de uno en uno.
- Contratos: ninguno (`agent.state_changed`). Esfuerzo: 2 días. Riesgos: ruido visual con muchos agentes (máximo 1 baliza por robot, prioridad a `awaiting_approval`). Reglas: la información también va como texto en la etiqueta (no sólo color), `aria-live` educado en el contador.

**1.3 Mesa del jefe (la "Zona de aprobaciones" se vuelve tu mesa).**
- Haces: clic en tu mesa o tecla `A`. Ves: pila de papeles con las aprobaciones, mismo `ApprovalCard` (doble aprobación, rol requerido, sin autoaprobación intactos).
- Contratos: ninguno (`approval.requested/progress/resolved`). Esfuerzo: 2-3 días. Riesgos: que se pierda la bandeja actual → se mantiene `ApprovalsTray` como atajo y en dashboard. Reglas: los robots nunca firman; la decisión sigue en `POST /approvals/{id}/decision` con el usuario autenticado. Arreglar de paso el texto fijo de `OfficeEnv.tsx:318` a i18n.

**1.4 Piel robot como preferencia (opción B).**
- Haces: Ajustes de la oficina → Estilo: Personas / Robots. Ves: mismos personajes con cabeza de visor y antena, chapa del color del rol.
- Contratos: ninguno (preferencia local en `lib/preferences.ts`). Esfuerzo: 3-4 días. Riesgos: rendimiento (mismo número de mallas o menos). Reglas: e2e por `data-testid` no dependen del aspecto.

**1.5 Lenguaje de jefe en la interfaz.** Textos: "Pásale trabajo a…", "Te esperan", "Firmar", "Contratar robot". Sólo i18n es/en, sin cambiar claves de API. Esfuerzo: 1 día. Riesgo: decidir el tono (ver decisiones).

### Ola 2: media (3-6 semanas)

**2.1 Arrastrar tarjetas de trabajo sobre un robot.**
- Haces: arrastras una sugerencia, un correo leído, un artefacto o una tarjeta escrita a mano sobre un robot. Ves: el robot la "recoge"; si no es de su competencia, propone a quién pasarla (flujo de fuera de competencia existente, `chat-routing.md` §7).
- Contratos: reutiliza `POST /messages` (`agent:<id>`); para artefactos, adjunto existente (`artifact_attachments`). ⚠ Si se quiere asignar una tarea **sin** pasar por el chat, haría falta `POST /requests` con `assignee_hint` (aditivo).
- Esfuerzo: 6-8 días. Riesgos: drag & drop en 3D y en táctil; alternativa obligatoria por teclado ("Asignar a…" en menú contextual). Reglas: el texto de correos/documentos arrastrados sigue siendo dato delimitado, nunca instrucción.

**2.2 Stand-up diario en la sala de juntas (= B4, primera mitad).**
- Haces: configuras la hora o pulsas "Junta ahora". Ves: los robots caminan a la sala (ya saben caminar: `Character.tsx` `route`) y cada uno habla en su globo: hecho / sigue / bloqueos.
- Contratos: ⚠ nuevo `schedule` tipo `standup` reutilizando `schedules` + evento `meeting.started/ended` (aditivo) para que la escena sepa cuándo mover a todos. Costo: una llamada de resumen por robot → respeta topes y se estima antes.
- Esfuerzo: 6-8 días (backend 3, frontend 4). Riesgos: costo en modo live; texto inventado → el resumen sale sólo de tareas, actividad y auditoría reales. Reglas: en modo solo lectura y kill-switch la junta no se dispara; con `reduceMotion`, sin caminata (aparecen sentados).

**2.3 Pizarrón de proyectos en la pared.**
- Haces: clic en el pizarrón. Ves: columnas por proyecto con tarjetas por robot y semáforo; clic abre el proyecto.
- Contratos: ninguno (`project.*` existentes). Esfuerzo: 4-5 días. Riesgos: legibilidad en 3D → el pizarrón es un `Html` 2D anclado. Reglas: equivalente en dashboard ya existe (`ProjectsView`).

**2.4 Los robots te traen los papeles (opcional).** Animación de caminar a tu mesa al crearse una aprobación. Sin contratos. 2 días. Desactivado por defecto con muchas aprobaciones o `reduceMotion`.

**2.5 Avatar del jefe y botón rojo físico.** Tu avatar en tu mesa (`USER_POS`), el botón rojo en la pared que abre el mismo diálogo de `freeze`/`lockdown`. Sin contratos. 3 días. Riesgo: activación accidental → confirmación igual que hoy y permiso `controls:*` igual.

### Ola 3: ambiciosa (2-3 meses, sólo tras validar olas 1-2)

**3.1 Recursos humanos: contratar, ascender, capacitar, dar de baja.**
- Contratar = plantillas de rol (A5, `POST /agents/from-template` ⚠ ya planeado). Ascender = B1 (autonomía ganada con historial de aprobaciones; siempre con tu visto bueno, emite `agent.autonomy_changed` y queda en auditoría). Capacitar = editar instrucciones/memoria con diff. Baja = desactivar (no borrar historial).
- Esfuerzo: depende de A5 y B1 (no se suma aquí). Reglas: un robot nunca cambia su propia autonomía (`06-permisos-autonomia.md` §3, invariante 3).

**3.2 Robots proactivos que se acercan (= B2).** Un robot con "presupuesto de iniciativa" camina a tu mesa con una propuesta ("el margen bajó 3 %, ¿lo reviso?"). Nunca ejecuta sin aprobación. ⚠ Contrato de B2.

**3.3 "Mientras dormías" como timelapse (= B3).** Al volver, la oficina reproduce en 60 s lo ocurrido usando el registro de eventos. ⚠ Requiere eventos con orden y replay (hoy `events/hub.go` sin `seq`, ver roadmap C4).

**3.4 Rediseño completo a robots (opción C)**, turno día/noche por hora real y sonidos opcionales (teclear, campanita de aprobación), si las decisiones del dueño lo piden.

---

## 4. Cómo se respetan las reglas no negociables

- **Los agentes nunca aprueban**: ninguna interacción nueva añade un camino de decisión; la mesa del jefe usa el mismo endpoint y las mismas comprobaciones (`approvals.go` `Decide`: rol, doble aprobación, sin autoaprobación).
- **Kill-switch, solo lectura, pausa de agente, topes**: la escena los refleja (oficina oscura, robots quietos, baliza gris "en pausa") y las acciones nuevas (stand-up, proactivos) se bloquean igual que las existentes.
- **Auditoría**: todo lo que cambie estado (asignar, ascender, junta) pasa por endpoints auditados; las animaciones no generan eventos.
- **i18n es/en**: todo texto nuevo con claves en `es.json`/`en.json` y `npm run check:i18n`.
- **Accesibilidad**: cada objeto clicable de la escena tiene equivalente en teclado y en dashboard; `reduceMotion` quita caminatas, zoom y balizas animadas (quedan estáticas); los colores llevan texto.

---

## 5. Decisiones que te tocan

1. **Aspecto**: personas, robots como preferencia (recomendado para empezar) o rediseño completo.
2. **Personalidad de los robots**: ¿siguen siendo "Valeria, Tomás…" con tono humano, robots con nombre y número ("V-01 Ventas"), o mezcla? Afecta textos, landing y capturas.
3. **¿Los robots caminan hasta ti?** (traer papeles, proactivos): más vida, más distracción.
4. **Sonidos**: ¿existen? (desactivados por defecto si sí).
5. **Stand-up**: ¿diario automático (con costo en modo live) o sólo a demanda?
6. **Lenguaje de jefe**: tuteo y tono ("Fírmalo", "Pásale trabajo") por país (los tonos regionales ya existen en la org).

---

## 6. Cómo sabremos que es más amigable (sin métricas inventadas)

Proponemos medir, antes y después de cada ola, en sesiones de prueba con 5-8 personas del público objetivo (despachos y pymes hispanas) y con telemetría local opcional (sin analítica de terceros; sólo con consentimiento):

| Criterio | Cómo se mide |
|---|---|
| Tiempo hasta la primera tarea aprobada (desde abrir la app) | Cronometrado en sesión; y en la app, diferencia entre el primer `request.received` y el primer `approval.resolved` aprobado por usuario nuevo |
| Pasos para acciones comunes (hablar con un robot, asignar, aprobar) | Conteo de clics/teclas por tarea guionada, antes vs después |
| ¿Saben quién los necesita? | Pregunta en sesión: "¿qué robot te espera y por qué?" a los 10 s de mirar la oficina; % de aciertos |
| Uso de la oficina vs dashboard | Proporción de tiempo en `mode=office` (preferencia local), sólo como señal |
| Errores y arrepentimientos | Rechazos inmediatos tras aprobar, cancelaciones, uso de "deshacer" (cuando exista) |
| Percepción | Escala corta (SEQ, 1-7) tras cada tarea guionada y comentario libre |

Criterio de paso de ola: mejora en tiempo y pasos en la mayoría de los participantes sin empeorar los aciertos de "¿quién te espera?". Los números concretos se fijan con la línea base de la primera sesión, no antes.
