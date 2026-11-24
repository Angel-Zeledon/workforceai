# Aislamiento por oficina: inventario

Regla (un solo punto: `application/office_scope.go` + `offices.Model.WorkOffice`): la oficina del pedido viaja en el
contexto; el agente usa esa oficina si trabaja en ella y, si no (o se desconoce), solo su base. Nunca otra.

| # | Sitio (lee memoria, archivos, notas, habilidades o historial) | Cómo queda cubierto | Test |
|---|---|---|---|
| 1 | Memoria larga `memory.Recall` / `retrieve` (docs de oficina) | `AgentOffice(ctx)` = oficina efectiva | `TestIsolation_RuntimeRequestNeverHasTheOtherOfficesSecrets` (application) |
| 2 | Notas privadas del agente (`ScopeAgent`) | se descartan si trabaja para otra oficina; lo aprendido ahí se guarda con esa oficina | mismo test (notas declaradas) |
| 3 | Memoria clave/valor `ListMemory` + `last_task` (orchestrator) | alcance `agent@<oficina>` | mismo test (`last_task` en `context.memory`) |
| 4 | Notas de equipo `RecallNotesFor` | oficina efectiva | `TestIsolation_TeamNotes` (cmd/server) |
| 5 | Archivos `fs.*` (`drive.AgentFS` vía `AgentOffices`) | oficina efectiva; compartidos y permisos de quien pide igual que hoy | `TestIsolation_Files` (cmd/server; incluye instrucción maliciosa y oficina desconocida) |
| 6 | Habilidades `AgentSkills` (oficina, puesto, propias) | `here()` usa oficina efectiva | SIN TEST propio (ver abajo) |
| 7 | Conexiones de oficina (`connections.AgentOffice`) | misma función | cubierto por 5 (misma función) |
| 8 | Historial del chat que ve el agente (`chatHistory`) | filtra por oficina; sin resumen /compact en turnos de oficina | `TestIsolation_ChatHistory` (application) + `TestMessagesOfficeAPI` |
| 9 | Pedido, tareas y mensajes | heredan `office_id`; reparto solo con agentes de la oficina | primer test (planner sin agentes de la otra oficina) + `TestMessagesOfficeAPI` |
| 10 | Llamadas de herramientas sin contexto (aprobadas después) | `Gateway.RequestOffice` la saca del pedido | SIN TEST propio (ver abajo) |
| 11 | Consulta agente a agente | usa el contexto del pedido de origen (sin memoria propia del consultado) | sin test propio: la consulta no carga memoria del consultado |
| 12 | Reunión/debate, proyecto, ayudantes, rutina | heredan el contexto del pedido; sin oficina = base | sin test propio; heredan el contexto |
| 13 | Quitar agente de la oficina | tareas no empezadas del pedido quedan bloqueadas | `TestRemoveOfficeBlocksPending` + `TestIsolation_RemovedAgentLosesTheOffice` |
| 14 | API: mensajes, historial, eventos, privadas | `office_id`, 404 si no puede entrar, filtro | `TestMessagesOfficeAPI` |
| 15 | Costo | `UsageEntry.OfficeID` | `TestUsageOffice` |

No cubierto / sin test propio: habilidades de oficina (`here()` en `skills.AgentSkills`, cambio hecho, sin test), la
restauración de la oficina en `Gateway.Execute` (hecha, sin test), vista previa/no leídos de la lista de hilos
(`/chats/threads`, `/conversations`: el texto de la última línea no se filtra por oficina), el resumen `/compact`
(se descarta en turnos de una oficina), proyectos/rutinas/reuniones (sin oficina => base del agente, no heredan
una oficina elegida) y los eventos de estado del agente (`agent.state`), sellados por su oficina base.
