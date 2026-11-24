# Aislamiento por oficina: inventario

Regla (un solo punto: `application/office_scope.go` + `offices.Model.WorkOffice`): la oficina del pedido viaja en el
contexto; el agente usa esa oficina si trabaja en ella y, si no (o se desconoce), solo su base. Nunca otra.

| # | Sitio (lee memoria, archivos, notas, habilidades o historial) | Cómo queda cubierto | Test |
|---|---|---|---|
| 1 | Memoria larga `memory.Recall` / `retrieve` (docs de oficina) | `AgentOffice(ctx)` = oficina efectiva | `TestIsolation_Recall` |
| 2 | Notas privadas del agente (`ScopeAgent`) | se descartan si trabaja para otra oficina; lo aprendido ahí se guarda con esa oficina | `TestIsolation_AgentNotes` |
| 3 | Memoria clave/valor `ListMemory` + `last_task` (orchestrator) | alcance `agent@<oficina>` | `TestIsolation_KeyValueMemory` |
| 4 | Notas de equipo `RecallNotesFor` | oficina efectiva | `TestIsolation_TeamNotes` |
| 5 | Archivos `fs.*` (`drive.AgentFS` vía `AgentOffices`) | oficina efectiva; compartidos y permisos de quien pide igual que hoy | `TestIsolation_Files` |
| 6 | Habilidades `AgentSkills` (oficina, puesto, propias) | `here()` usa oficina efectiva | `TestIsolation_Skills` |
| 7 | Conexiones de oficina (`connections.AgentOffice`) | misma función | cubierto por 5 (misma función) |
| 8 | Historial del chat que ve el agente (`chatHistory`) | filtra por oficina; sin resumen /compact en turnos de oficina | `TestIsolation_ChatHistory` |
| 9 | Pedido, tareas y mensajes | heredan `office_id`; reparto solo con agentes de la oficina | `TestIsolation_Routing` |
| 10 | Llamadas de herramientas sin contexto (aprobadas después) | `Gateway.RequestOffice` la saca del pedido | `TestIsolation_GatewayRestoresOffice` |
| 11 | Consulta agente a agente | usa el contexto del pedido de origen (sin memoria propia del consultado) | `TestIsolation_Consult` |
| 12 | Reunión/debate, proyecto, ayudantes, rutina | heredan el contexto del pedido; sin oficina = base | `TestIsolation_Debate` (contexto), resto por herencia |
| 13 | Quitar agente de la oficina | tareas no empezadas del pedido quedan bloqueadas | `TestRemoveOfficeBlocksPending` |
| 14 | API: mensajes, historial, eventos, privadas | `office_id`, 404 si no puede entrar, filtro | `TestMessagesOfficeAPI` |
| 15 | Costo | `UsageEntry.OfficeID` | `TestUsageOffice` |

No cubierto: ver informe final.
