# F6. Vigilante de trabajo atascado — plan breve

**Objetivo:** detectar trabajo que dejó de avanzar (tarea en `running` sin progreso, tarea lista que nadie ejecuta tras un reinicio sin reanudar) y ofrecer a una persona **reintentar / reasignar / cancelar** sin gastar tokens al detectar y sin doble ejecución.

## Decisiones

1. **Progreso** = lo más reciente de: `tasks.started_at` (cada intento lo escribe), la última entrada del libro de uso (`usage`) de la tarea (cada llamada al runtime que termina deja una) y, para la tarea que este proceso está ejecutando, el momento en que empezó su ejecución o terminó su última espera. Una tarea lista (`pending`, dependencias hechas) de un pedido que **ningún proceso ejecuta** cuenta desde su creación o el fin de su última dependencia.
2. **No es atasco** lo que espera legítimamente: estados `awaiting_approval` y `needs_input`, pedido `paused` (tope de gasto), y cualquier espera que pase por `YieldSlot` (pausa del agente / kill-switch, pausa u horario del proyecto, aprobación, pregunta) o por la cola de la organización (`acquireSlot`). Se marca en el "manejador de ejecución" de la tarea.
3. **Umbral por organización** dentro de las señales de iniciativa (`signals.stalled_work`, JSONB ya existente → **sin migración**): `task_minutes` 30 (5..1440), `project_minutes` 120 (15..4320; las tareas de proyecto esperan detrás del trabajo interactivo y los proyectos grandes encadenan muchas).
4. **Detección** en `Orchestrator.Stalled` (datos guardados + estado en memoria del proceso). Un pedido sin trabajador aporta una sola candidata (reanudarlo cubre a todas). Barrido cada `WATCHDOG_INTERVAL` (5m; `off`), sin modelo.
5. **Al detectar:** sugerencia `stalled_task` del agente dueño vía `InitiativeService.Raise` (presupuesto + duplicados 24 h; clave = tarea + inicio del episodio), evento `task.stalled` y auditoría `task.stalled` una vez por episodio. La regla D3 sale sola: la sugerencia y el evento llevan el agente.
6. **Recuperación** (`Orchestrator.RecoverStalled`), solo personas, solo si la tarea sigue atascada:
   - *Ejecución viva en este proceso:* la tarea corre bajo un supervisor con contexto cancelable; la acción deja una orden y cancela ese contexto; **la misma goroutine** reencola la tarea por `runTask` (guardas, presupuesto, aprobaciones, kill-switch, pausa, solo lectura) o la bloquea. Nunca hay una segunda ejecución.
   - *Sin trabajador:* se modifica la tarea guardada y el pedido se reanuda con `track` + `recoverRequest`, el mismo camino y el mismo cerrojo de la reanudación durable: si `Recover` ya lo tomó, la acción responde 409.
   - Atomicidad: cerrojo `stall:<tarea>` (`Locker`, Redis en producción) + marca "recuperando" en el proceso; la segunda persona recibe 409.
   - Reintentar: `requests:create`. Reasignar (mismo departamento, mismo rol, `canTake`, reglas de delegación) y cancelar: `tasks:manage`. Cancelar exige la lista exacta de dependientes activos que se cancelan (`confirm_task_ids`); si cambió, 409 con la lista nueva.
   - Aceptar la sugerencia = reintentar (acción registrada por señal en `InitiativeService`), no un pedido nuevo.
7. **API:** `GET /stalled-work`, `POST /stalled-work/scan`, `GET /tasks/{id}/stall`, `POST /tasks/{id}/stall/{retry|reassign|cancel}`; D3 con `workTask`.
8. **UI:** componente `StallActions` (botones + modal de cancelación con dependientes) en la sugerencia, en la tarea del panel del agente y en el nodo del proyecto; umbrales en la tarjeta de señales.

## Tareas

- [ ] Ajustes `stalled_work` (validación, defectos al leer) + pruebas.
- [ ] Supervisor de ejecución, marcas de espera, detección con reloj falso + pruebas (no detecta aprobación / pregunta / pausa).
- [ ] `RecoverStalled` vivo y sin trabajador (reintento idempotente, reasignar, cancelar en cascada, sin doble ejecución con `Recover`) + pruebas.
- [ ] `Watchdog` (barrido, sugerencia, evento, auditoría, cierre de sugerencias resueltas) + pruebas de duplicados y presupuesto.
- [ ] API + pruebas (permisos, aislamiento entre orgs y departamentos privados).
- [ ] Cableado (`cmd/server`), frontend (api, lógica, componente, i18n es/en/pt) + `node --test`.
- [ ] `docs/architecture/watchdog.md`, 08-api, initiative.md; `go test`, `go vet`, `npm run build`.
