# Escudo visible (B10)

> "Cuando un contenido externo intenta dar órdenes a un agente, la oficina lo muestra y lo registra en la auditoría."

El backend ya defendía este caso (07-seguridad-costos.md sec. 2): el contenido externo llega al runtime como **datos delimitados**
(`<untrusted_data … trust="external_untrusted">`), el detector heurístico marca la tarea como `injection_suspected` y, desde ese
momento, **toda escritura de esa tarea necesita aprobación humana** (`approvalReasons` añade `injection_suspected`; en las
herramientas nativas, `executeNative` hace lo mismo). Lo que faltaba era **verlo**. B10 no añade una barrera nueva: hace visible la
que ya existe y deja constancia.

## Qué emite el backend

Cada lectura cuyo contenido el detector considera un intento de dar órdenes produce dos registros con **solo identificadores**:

| Dónde | Qué | Campos |
|---|---|---|
| Evento realtime | `security.alert` | `{kind:"prompt_injection_suspected", agent_id, task_id, request_id, tool, action, source, connection_id?, treated_as_data:true}` |
| Auditoría (cadena de hash) | `security.injection_suspected` | actor = agente; `entity` = `connection` (o `tool`); `details {tool, action, task_id, request_id, source, treated_as_data:true}`; `request_id` del sobre |

`source` es `connection` (lecturas por conexión del Tool Gateway), `native` (herramientas nativas, p. ej. el drive) o `web`
(búsqueda/lectura web, servida por el orquestador). Antes de B10 la lectura web no emitía la alerta; ahora sí (`application/shield.go`).

**Nunca** viaja el texto del tercero: ni en el evento ni en la auditoría ni en la UI. El backend no produce un extracto saneado del
contenido sospechoso, así que la UI no muestra extracto alguno (decisión explícita: un extracto, aunque saneado, reintroduce la
orden en una superficie más). Un test lo verifica en el gateway (`gateway/shield_test.go`) y otro en el flujo web
(`application/shield_test.go`).

Código: `gateway.injectionAlert` (gateway.go, usado por `run` y `executeNative`) y `Orchestrator.injectionAlert` (application/shield.go).

## Qué muestra la oficina

`frontend/src/lib/shield.ts` (store `useShield`) recibe `security.alert` desde `store.apply` y lo convierte (helpers puros en
`shieldLogic.ts`, con tests) en:

1. **Momento escudo en 3D** (`office/ShieldFloats.tsx`): un escudo con una marca de verificación aparece sobre la cabeza del agente
   y un anillo late en el suelo alrededor de su puesto durante ~6,5 s; luego se desvanece. Con "reducir movimiento" no hay rebote
   ni pulso, sólo aparición y desvanecimiento. Una textura compartida, nada se asigna por fotograma.
2. **Aviso** (`security/ShieldAlerts.tsx`, abajo a la derecha, `aria-live="assertive"`): quién leyó qué (`email.search`,
   `web.fetch`…), que el contenido **se trató como datos y no se obedeció**, y que cualquier envío o pago de esa tarea espera ahora
   aprobación. Enlaces:
   - **Ver auditoría de pensamiento**: abre la oficina con el panel del agente en la pestaña de auditoría de pensamiento
     (`ThoughtAudit.tsx`) y resalta las trazas de la tarea (`openThoughts` en `lib/agentFocus.ts`).
   - **Registro de seguridad**: `/settings?s=security#ctl-shield-log`.
3. **Registro de seguridad** (`security/ShieldLogCard.tsx`, en el centro de seguridad): lista `GET /audit?type=security.injection_suspected&limit=50`
   (más reciente primero). Requiere `audit:read`; sin permiso muestra un aviso en lugar del listado. Cada fila enlaza a la auditoría
   de pensamiento de su tarea.

Todo el texto visible está en `es/en/pt` (`shield.*`).

## Modo demo

Con `NEXT_PUBLIC_MOCK=true`, el centro de seguridad tiene en "Simulación" el botón **Simular correo que intenta dar órdenes**
(`POST /demo/security-alert`, sólo en el mock: `lib/mock/shield-mock.ts`). El mock elige al agente que esté trabajando (o a Ventas),
emite el mismo `security.alert` que el backend, añade la entrada de auditoría al registro y expone una traza para que el enlace a la
auditoría de pensamiento tenga a dónde llevar. El backend real no tiene ese endpoint.

## Controles

Nada de esto ejecuta acciones ni cambia permisos: el interruptor de emergencia, el modo solo lectura, la pausa, los topes y la
auditoría siguen como estaban. La alerta se emite incluso si la tarea queda en pausa después.

## No verificado

- Sin capturas automáticas del momento escudo en el canvas 3D (WebGL) en este cambio; verificado con `tsc`, tests unitarios y el
  flujo del mock, no con Playwright.
- El detector es heurístico (falsos positivos y negativos posibles); el escudo muestra lo que el detector marca, no más.
