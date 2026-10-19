# Reducir el costo del LLM por tarea

Medido en corridas en vivo: cada llamada `run_task`/`plan` promediaba ~76k tokens de entrada y ~39k de salida incluso para pedidos triviales ("calcula el margen bruto"); un pedido trivial se descomponia en varias tareas; y el tope por solicitud solo se aplicaba despues de la llamada que lo cruzaba (sobregiro de 40-100 %).

Este documento explica por que, que se cambio y cuanto se espera ahorrar. **Todas las cifras de "despues" salen de tamanos de prompt medidos en pruebas** (`agent-runtime/tests/test_cost_control.py`, sin llamar a ningun modelo); las de "antes" que no se pueden reproducir sin gastar tokens estan marcadas como *inferidas*.

## 1. Por que la entrada era ~76k tokens

El prompt que construye el runtime para una tarea trivial (agente con `fs`, `code`, `web`, escritorio con escritura, 6 memorias recuperadas) pesa **~3.9k tokens** (sistema 346 + usuario 2 939 + esquema JSON 649). Ni de lejos 76k. El resto viene de multiplicar ese prompt:

| Causa | Evidencia | Efecto |
|---|---|---|
| **Iteraciones del agente CrewAI** (`max_iter` por defecto = 25, `max_retry_limit` = 2). Un agente sin herramientas debe responder en una pasada, pero CrewAI espera el formato ReAct (`Final Answer:`); una respuesta JSON "limpia" provoca reintentos de formato y cada iteracion reenvia toda la conversacion acumulada. | `crewai_engine._kickoff_once` creaba `Agent(...)` sin tope; `Agent.model_fields['max_iter'].default == 25` (CrewAI 1.15). 76k / ~4.3k = ~17 pasadas *(inferido)*. | **Causa principal.** |
| Reintento completo si el JSON es invalido (`crew.kickoff()` otra vez) y failover al siguiente proveedor. | codigo de `_kickoff_once` / `_kickoff`. | x2 en el peor caso. |
| `max_tokens` de 4096, **8192 forzado** para DeepSeek/Kimi (`max(kw, 8192)`) y sin limite por tipo de llamada. | `_llm_kwargs`. | Salida de 39k = pasadas x hasta 8k *(inferido)*. |
| Manuales de herramientas siempre presentes: reglas de Office (xlsx/docx/pdf), `fs.edit`, busqueda con citas, lectura de PDF/imagenes, memoria. | `workspace_lines`: ~1.2k tokens por llamada aunque la tarea sea un calculo. | ~30 % del prompt. |
| Esquema JSON de `TaskResult` con la descripcion larga de `ToolRequest.args` (duplicaba los manuales) y `title` en cada nodo. | `json_instructions`. | 649 tokens. |
| Contexto sin tope razonable: dependencias 8 000 tokens, memorias 6 x 500 caracteres, habilidades cargadas hasta 4 x 26 000 caracteres. | `security.py`. | Hasta decenas de miles de tokens en tareas con dependencias. |
| El planificador descomponia pedidos triviales: 1 llamada de plan + N llamadas de tarea + sintesis. | observado en vivo. | Costo x(N+2). |
| Reserva de presupuesto basada en un rango por tarea (entrada max ~10k, salida 4096) mientras la llamada real llevaba decenas de miles. | `reserveFor` / `estimate.py`. | Sobregiro del tope. |

## 2. Que se cambio

### Runtime (`agent-runtime/app`)
- `cost_control.py` (nuevo): topes de salida por tipo de llamada, topes del bucle CrewAI, politica de razonamiento y guarda previa al envio.
- `crewai_engine.py`: `Agent(max_iter=3, max_retry_limit=1, reasoning=False)`; cada llamada declara su `kind` y su `max_tokens`; se elimino el piso de 8192; esquema JSON compacto; modo opcional `LLM_DIRECT_CALL` (una sola completion, sin envoltorio ReAct).
- `security.py`: los manuales de Office / edicion / busqueda / vision se envian solo si la tarea (o sus archivos de entrada, o las imagenes adjuntas) los puede usar; topes mas bajos de contexto (dependencias 4 000 tokens, memorias 4 x 300 caracteres y 1 600 en total, habilidades 3 x 12 000 y 20 000 en total); texto de memoria mas corto.
- `fast_path.py` (nuevo): atajo del planificador.
- `models.py`: campo aditivo `max_cost_usd` en todas las peticiones; descripcion corta de `ToolRequest.args`.
- `estimate.py`: el rango en vivo usa los mismos topes y un sobrecosto de prompt medido (2 500 tokens, antes 700).

### Backend (`backend/internal/application`)
- `call_guard.go` (nuevo): antes de cada llamada **en vivo** (estimacion `token_heuristic`) se recalcula la reserva con el payload real: `(tokens del JSON + 2 600 de sobrecosto) x precio de entrada + max_tokens del tipo x precio de salida`, con `model_prices.json`. Si no cabe en el tope de solicitud/agente/organizacion la llamada **se pausa** (`budget.exceeded`, reanudable) *antes* de enviarse. La llamada lleva `max_cost_usd` = reserva x 1.25 para que el runtime la rechace tambien (HTTP 402 `call_budget_exceeded`) si su estimacion exacta lo supera; la tarea queda en espera ("call_budget"), no fallida.
- `costcontrol.go`: `reserveOrPauseSized`. `orchestrator.go`, `synthesis.go`: run-task, consulta y sintesis usan la guarda. `ports.go`: `max_cost_usd` (aditivo, omitido si 0).
- `breakdown.go` (`GET /costs/breakdown`, `GET /requests/{id}/cost`): tokens de entrada/salida en el total, por tarea, por operacion y por modelo, y `by_call` (llamada por llamada, solo por solicitud, maximo 500).

## 3. Topes de salida por tipo (variables de entorno)

| Tipo | Variable | Por defecto | Antes |
|---|---|---|---|
| plan | `MAX_TOKENS_PLAN` | 3 000 | 4 096 (8 192 DeepSeek) |
| plan_phases | `MAX_TOKENS_PLAN_PHASES` | 2 048 | 2 048 (8 192 DeepSeek) |
| plan_phase | `MAX_TOKENS_PLAN_PHASE` | 4 096 | 6 144 (8 192 DeepSeek) |
| run_task | `MAX_TOKENS_RUN_TASK` | 4 000 | 4 096 (8 192 DeepSeek) |
| consult | `MAX_TOKENS_CONSULT` | 800 | 4 096 (8 192 DeepSeek) |
| synthesize | `MAX_TOKENS_SYNTHESIZE` | 6 000 | 4 096 (8 192 DeepSeek) |
| review / summarize / route / chat | `MAX_TOKENS_REVIEW` ... | 1 500 / 2 000 / 300 / 350 | igual (ahora configurables) |

El backend lee los mismos nombres con los mismos valores por defecto (`call_guard.go`), de modo que ambos lados calculan el mismo peor caso.

Razonamiento / "thinking": **no se pide** salvo `LLM_REASONING=true`. Sin esa variable, un alias de solo razonamiento (`deepseek-reasoner`) se sustituye por el modelo normal del mismo proveedor, OpenAI gpt-5 recibe `reasoning_effort=minimal` (o-series `low`), y para Anthropic nunca se envia `thinking`.

## 4. Atajo del planificador (`PLANNER_FAST_PATH`, por defecto activo)

Un pedido es **simple** si cumple todo: <= 280 caracteres (`FAST_PATH_MAX_CHARS`) y <= 2 frases; sin lista, sin conectores de varios pasos (luego, despues, ademas, then, also...), sin palabras de entregable grande (propuesta, proyecto, campana, contrato, "prepara todo"...) ni comandos `[[...]]`; toca como maximo un area de la oficina y nombra a lo mas un agente. Entonces `plan()` devuelve **una tarea** para el agente dueno del area (el asistente si no hay area) **sin llamar al modelo** (`usage` en cero, modelo `fast-path`). Ante la duda va al planificador normal. `PLANNER_FAST_PATH=false` restaura el comportamiento anterior. El simulador conserva sus guiones.

## 5. Guarda de presupuesto antes de cada llamada

1. Backend: `sizeReservation` (arriba). Estimacion = entrada real + `max_tokens` x precio; si cruza el tope, pausa antes de llamar. Solo en vivo: simulacion y estimaciones `fallback` no cambian.
2. Runtime: `preflight` estima el prompt realmente armado (system + descripcion + esquema + envoltorio, chars/3.5) y el `max_tokens`; si `max_cost_usd` > 0 y la estimacion lo supera, **no envia** a ese proveedor (prueba el siguiente, p. ej. uno mas barato) y, si ninguno cabe, responde 402 `call_budget_exceeded` con los numeros. `BUDGET_PREFLIGHT=false` la apaga.
3. Limite conocido: la guarda es por *intento*. Un reintento por JSON invalido (raro) puede costar otro intento completo; el gasto real siempre se concilia despues con el mayor entre runtime y backend (sin cambios).

## 6. Antes / despues

Tarea trivial (`test_trivial_task_prompt_is_much_smaller_than_before`), tokens estimados a 4 caracteres por token:

| Componente | Antes | Despues |
|---|---|---|
| Prompt de sistema | 346 | 346 |
| Prompt de usuario (manuales, memoria, contexto) | 2 939 | 1 712 |
| Esquema JSON en el prompt | 649 | 439 |
| **Total por pasada** | **3 934** | **2 498 (-36 %)** |

Con `PROMPT_TRIM=0` se restauran los manuales completos (comparacion y escape).

Efecto del bucle (el factor dominante, estimado con el prompt medido; el "antes" real fue 76k/39k):

| | Antes | Despues |
|---|---|---|
| Pasadas por llamada (peor caso) | hasta 25 + reintento JSON | <= 3 + 1 de cierre (`CREW_MAX_ITER`), 1 con `LLM_DIRECT_CALL` |
| Entrada de una llamada tipica (pasadas x prompt) | ~76 000 (medido) | ~2 500 x 1-2 = **2 500-5 000** (<= ~10 000 en el peor caso de 4 pasadas con crecimiento) |
| Salida de una llamada | ~39 000 (medido), tope 8 192 por pasada | **<= 4 000** por pasada (run_task), tipica 1 000-2 000 |
| Pedido trivial | 1 plan + N tareas + sintesis | 1 tarea (sin llamada de plan) |

Estimacion global para un pedido trivial en vivo: de ~(1+N) llamadas de ~115k tokens a **1 llamada de ~5k tokens**: **reduccion del orden del 95 %** en tokens; para pedidos complejos, ~85-90 % (por el tope de iteraciones y de salida) mas el 36 % de prompt. Estas cifras deben confirmarse con una corrida en vivo controlada (no se hizo ninguna aqui).

## 7. Pendiente / recomendaciones

- Validar en vivo (una sola corrida) y, si `usage` baja a ~1 pasada, activar `LLM_DIRECT_CALL=true` por defecto: elimina el envoltorio ReAct de CrewAI por completo. Se dejo apagado porque el camino directo no se pudo probar contra un proveedor real sin gastar tokens.
- `infrastructure/runtime/client.go` (fuera de alcance) trata el 402 como error 4xx no reintentable; el backend lo detecta por el texto `call_budget_exceeded`. Seria mas limpio mapearlo a un error tipado.
- La guarda del planificador (`ReservePlanner`, 6k+6k tokens fijos) y del chat no usa el payload real todavia.
- Cache de prompt (Anthropic/DeepSeek) del bloque estatico de sistema: no implementado.
