# Ensayo general (B9)

> "Modo simulacro: los agentes muestran qué habrían enviado o pagado, sin hacerlo; sirve para validar antes de subir autonomía."

En un ensayo los agentes trabajan **de verdad** (planifican, leen, razonan, redactan el informe) pero **ninguna acción con efectos
secundarios se ejecuta**: cada envío, pago o escritura que habrían hecho (o dejado esperando aprobación) queda registrado como una
entrada "habría hecho" y la persona marca, para cada una, si **la habría aprobado o rechazado**. Esas marcas son la evidencia para
subir (o no) la autonomía del agente.

## Cómo se entra

| Alcance | Cómo | Permiso |
|---|---|---|
| **Agente** | `PUT /agents/{id}/rehearsal {"enabled": true}` (botón "Modo ensayo" en el panel del agente o en el centro de seguridad) | encender: `controls:pause`; **apagar** (devolverle efectos reales): `controls:release` (owner) |
| **Solicitud** | `POST /requests {"text": "…", "rehearsal": true}` (campo "Ensayar" en el centro de seguridad) | `requests:create` |

El indicador de solicitud se guarda en `RunMeta.Rehearsal` (JSON, sin migración): una solicitud reanudada tras un reinicio sigue
siendo un ensayo. El interruptor por agente vive en `agent_rehearsal` y sobrevive a un reinicio de la demo (es configuración).

## Qué pasa con cada llamada a herramienta

| Tipo | En ensayo |
|---|---|
| Lectura (conexión, nativa, web, visión) | **se ejecuta normal**, para que el resultado sea realista |
| Escritura por conexión (email, calendario, Slack, GitHub…) | se registra; **no** se ejecuta, programa, retiene ni entra a la bandeja de salida |
| Escritura nativa (drive `fs.*`) | se registra (`Assess` es puro; `Run` nunca se llama) |
| Herramienta simulada (legado) con efecto | se registra; no se audita como `tool.executed` |
| `code.run` (sandbox) | se registra como "habría pedido aprobación"; no se prepara ni corre |

No se pide **ninguna aprobación** durante un ensayo: la decisión humana es la marca posterior. Para cada entrada se guarda lo que
habría decidido la ejecución real (`would_decision`):

- `allowed`: la habría hecho sola (p. ej. un borrador reversible de un agente autónomo);
- `needs_approval`: habría esperado a una persona (siempre para envíos; con los `flags` de la tarjeta de aprobación real:
  `always_approval`, `autonomy`, `tainted_task`, `injection_suspected`, `recipient_outside_allowlist`…);
- `denied`: un control o regla la habría bloqueado (`deny_reason`: interruptor de emergencia, solo lectura, permiso, límite,
  secreto detectado, `policy:<regla>`…).

Se calcula con **las mismas comprobaciones** que una llamada real (controles, permisos, límites, escaneo de secretos, razones de
aprobación, motor de políticas), sin efectos: no cuenta contra los límites ni los contadores de anomalías.

## La garantía: imposible causar un efecto externo

Dos barreras independientes:

1. **Orquestador** (`application/rehearsal.go`, `handleToolsFrom`): con el alcance en ensayo, toda solicitud con efecto va a
   `rehearseTool` antes de llegar a cualquier ejecutor o aprobación. También se comprueba **después** de una aprobación pendiente
   (gateway, herramienta simulada y `code.run`): si el agente entró en ensayo mientras esperaba, la aprobación **no** gana.
2. **Tool Gateway** (`gateway/rehearsal.go`), que no confía en el orquestador:
   - `GatewayCall.Rehearsal` o el `RehearsalPolicy` (interruptor del agente, solicitud marcada o `RunMeta.Rehearsal`) convierten
     cualquier llamada con efecto en una vista previa **antes** de mirar la aprobación: con `ApprovedArgsHash` válido tampoco se
     ejecuta (`Decision: "rehearsed"`, `Status: "not_executed"`).
   - **Falla cerrado**: si no puede saber el estado (error del almacén), trata la llamada como ensayo.
   - Un envío **ya aprobado y retenido** (ventana de 60 s) de un agente que entra en ensayo no sale: vuelve a la cola
     (`requeued:rehearsal_mode`) y aprobarlo desde la bandeja responde `rehearsal_mode` mientras siga en ensayo.

Tests: `gateway/rehearsal_test.go` (`TestRehearsalNeverExecutesAWriteEvenIfApproved`, borrador que se permitiría, lecturas siguen,
denegaciones en la vista previa, política del agente y fallo cerrado, envío retenido re-encolado),
`application/rehearsal_test.go` (de punta a punta: interruptor durante la espera de aprobación, solicitud en ensayo con pago
simulado y lecturas reales, veredictos y resumen) y `api/rehearsal_api_test.go` (informe, veredictos, permisos, el interruptor gana
a una aprobación).

## Datos (migración 794)

- `agent_rehearsal (org_id, agent_id, enabled, set_by, updated_at)`.
- `rehearsal_actions (org_id, id, request_id, task_id, agent_id, tool, action, source, scope, would_decision, deny_reason, risk,
  reversibility, account, recipients, amount, flags, preview, args_hash, verdict, verdict_by, verdict_at, created_at)`.

Ambas con RLS forzado por organización; `Reset` borra las acciones (datos de ejecución) y conserva los interruptores. Bajada:
`backend/migrations/down/794_rehearsal_down.sql`. `preview` es una vista **saneada** de los argumentos (máx. 12 claves, secretos
redactados, cadenas truncadas a 160, valores anidados resumidos); nunca se guardan los argumentos completos ni credenciales. El
evento `rehearsal.recorded` no lleva la vista previa (sólo ids, herramienta, acción y `would_decision`). El importe sale de
`policy.MaxAmount` (las claves de importe que ya reconoce el motor de políticas), no del texto libre.

## Veredictos y evidencia para ascensos (B1)

`POST /rehearsal/actions/{id}/verdict {"verdict":"would_approve"|"would_reject"|""}` (`approvals:decide`). Sólo personas: los
agentes no tienen identidad HTTP ni herramienta para esto. `GET /rehearsal/agents` devuelve por agente el interruptor y la cuenta
`would_approve / would_reject / undecided`: es la entrada que un futuro B1 ("ascensos por confianza") puede leer. B1 **no existe**
en este repositorio; nada aquí cambia autonomía ni permisos por sí solo.

## UI

- Panel del agente: botón **Modo ensayo** (`RehearsalToggle`) junto a Pausar; etiqueta "Ensayo" en la oficina 3D sobre el puesto.
- Centro de seguridad: tarjeta **Ensayo general** (pedir algo en ensayo, agentes con su evidencia, últimas acciones agrupadas por
  solicitud con "Lo aprobaría / Lo rechazaría").
- Informe de la solicitud: sección **"Ensayo: lo que habría hecho (no se ejecutó nada)"** (`RehearsalReport`).
- Modo demo (`NEXT_PUBLIC_MOCK=true`): `lib/mock/rehearsal-mock.ts`; en el escenario de $50,000 el envío de la propuesta queda
  registrado y no se "envía" si la solicitud o Ventas están en ensayo.

Todo texto visible en `es/en/pt` (`rehearsal.*`).

## Controles

El ensayo no relaja nada: el interruptor de emergencia, la pausa, el modo solo lectura y el horario siguen deteniendo el trabajo
(las lecturas de un ensayo pasan por los mismos controles), los topes de gasto aplican a las llamadas al modelo y todo queda en la
auditoría (`tool.rehearsed`, `policy.decision`, `rehearsal.agent_changed`, `rehearsal.verdict`). La revisión del plan sigue
pidiéndose igual que en una solicitud real.

## No verificado

- Tests de Postgres (`rehearsal_pg_test.go`, migración 794 arriba/abajo, RLS) escritos pero **no ejecutados** aquí: no había
  `TEST_DATABASE_URL` ni Docker disponible.
- Sin e2e de Playwright ni capturas de la UI en este cambio; verificado con `tsc`, tests unitarios y `check:i18n`.
- Proyectos y reuniones crean solicitudes por `SubmitPlan`: el interruptor por agente les aplica; no hay un "ensayo de proyecto"
  propio.
