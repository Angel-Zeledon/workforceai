# Ayudantes temporales (multiagente sin puesto): diseño

Estado: pendiente de aprobación del dueño. Fecha: 2026-11-23.
Marcas: **[dueño]** lo pidió el dueño; **[propuesta]** lo decidió quien escribe y se puede corregir.

## 1. Para qué

[dueño] Otras apps de agentes lanzan varios agentes sin darles un rol. Queremos ofrecer eso sin que choque con nuestra
distribución por empleo ni resulte ambiguo. [dueño] Los dos casos a la vez:

1. **Con puesto:** un empleado reparte un trabajo grande entre ayudantes ("Elena, revisa estos 40 contratos").
2. **Sin puesto:** un pedido que no es de ningún departamento, repartido entre ayudantes ("investiga estas 12 ciudades").

## 2. La regla que quita la ambigüedad

Un **empleado** responde por un puesto. Un **ayudante** es una parte de un pedido. **Todo ayudante tiene un empleado
responsable**, que junta los resultados, pide las aprobaciones y firma. En el caso 2 lo es la asistente (rol `assistant`)
o, si la empresa no tiene, el primer agente de la oficina de la persona. [propuesta]

## 3. Qué no es (límites)

- No se crean empleados permanentes sin puesto.
- Los ayudantes no salen en Equipo, Agentes, organigrama, Chats, tienda ni en el límite `MaxAgents`. No se les escribe.
- Un ayudante no lanza ayudantes, no pide aprobaciones y no hace acciones externas (enviar, publicar, pagar, escribir en
  conexiones). Lee y prepara borradores.
- No guardan memoria ni tienen carpeta propia. Nada queda de ellos salvo el resultado y el registro.
- Nunca se lanzan solos: la persona siempre ve cuántos son y el costo estimado, y acepta. (Primera versión.)
- Nada de "ahorra N personas" en la interfaz.

**Sería un fracaso si** alguien no supiera a quién reclamarle un resultado, si un pedido gastara más de lo aceptado, o si
un ayudante lograra algo que su responsable no puede.

## 4. Cómo se ve

Propuesta, en la conversación, antes de lanzar:
```
Elena propone repartirlo entre 8 ayudantes
40 contratos, 5 por ayudante. Elena revisa y firma el informe.
Costo estimado: $0.08 a $0.20   (tope: $0.20)
[Lanzar 8 ayudantes]  [Que lo haga Elena sola]  Cambiar cantidad ▾
```
En marcha (lista, bloque del pedido y panel En curso):
```
Revisa estos 40 contratos              Elena + 8 ayudantes   6 de 8
  ✓ Contratos 1 a 5      listo        ● Contratos 31 a 35   leyendo
  ✓ Contratos 6 a 10     listo        ! Contratos 36 a 40   falló  [Reintentar]
  Después: Elena junta los resultados          [Detener todo]
```
- **Para:** `Toda la oficina` · cada empleado · separador · `Ayudantes (sin puesto)`. Al elegirlo, la caja dice "Para algo
  que no es de un departamento. Lo coordina Sofía."
- Cada parte abre su resultado. El informe final es del responsable y enlaza a las partes.
- **Oficina 3D, en los 5 estilos:** figuras con ropa neutra gris y piel humana normal, junto al escritorio del
  responsable; hasta 6 visibles y "+N". Sin nombre propio: "Ayudante 3". Se van al terminar. Con render por software o
  movimiento reducido solo sale la etiqueta "+8 ayudantes".
- Celular: la misma información, en lista. Textos en es, en y pt.

## 5. Formas de construirlo

| | A. Parte con responsable (elegida) | B. Agentes reales temporales | C. Reparto dentro del runtime |
|---|---|---|---|
| Qué es | Una `Task` normal del responsable con datos de ayudante | Filas de `agents` que se borran | Un solo paso que hace varias llamadas |
| A favor | Permisos, presupuesto, auditoría, reinicio y paralelo ya funcionan por `AgentID` | Modelo "puro" | Poco código |
| En contra | Hay que distinguirlas en la interfaz | Toca listas, límites, memoria, chats, oficina; riesgo de dejar restos | Invisible, sin tope por parte, se pierde al reiniciar |

Elegida **A** [propuesta]: `AgentID` sigue siendo el empleado, así que es imposible que el ayudante tenga más permisos
que él.

## 6. Cómo funciona

**Datos.** `Task` gana `Helper *TaskHelper {Index, Total, Label, GroupID}` (JSON aditivo; `nil` = tarea de siempre). Nueva
tabla `helper_groups` (org, request, responsable, cantidad, tope en USD, estado, quién lo aceptó), con RLS y migración de
bajada. `GET /requests/{id}` y los eventos `task.*` incluyen `helper`.

**Proponer.** Nuevo `POST /v1/split` en el runtime: recibe el pedido y la lista de adjuntos y devuelve partes
independientes o "no conviene". Si hay N archivos o una lista clara, el reparto es por reglas, sin LLM. Solo se propone
con 4 o más partes independientes; si dependen entre sí, se sigue el plan normal. La simulación responde determinista.

**Aceptar.** `POST /requests/{id}/helpers` (permiso `conversations:write` y poder pedirle trabajo al responsable). Crea el
grupo y, con `AmendRun`, las tareas más una tarea final de unión del responsable que depende de todas. Idempotente.

**Ejecutar.** El `Scheduler` actual, con `MAX_PARALLEL_PER_ORG`. `buildRunRequest` para una tarea con `Helper`: persona
neutra, sin memoria ni recuerdos, solo herramientas de lectura que ya tenga el responsable, solo su parte del material. El
backend rechaza cualquier `tool_request` de escritura o externo aunque el runtime lo pida (no basta el prompt), y anula
consultas y `help_request`: lo que falte va en el resultado y lo resuelve el responsable.

**Unir.** Los resultados de los ayudantes entran a la tarea final como datos no confiables, con el presupuesto de contexto
que ya existe (`fitDependencies`). Con muchas partes se une por tandas. Revisión de calidad y aprobaciones: solo sobre el
resultado del responsable.

**Costo.** Estimación con `/v1/estimate` (rango). El máximo aceptado es **tope duro del grupo**: al alcanzarlo se detienen
las partes que faltan y el responsable entrega lo que haya, diciendo qué faltó. Cuenta además contra los topes de
siempre (pedido, agente, departamento, plan). El gasto se anota al responsable y a la persona que pidió.

**Fallos y control.** Una parte que falla se reintenta una vez. Se puede reintentar una, detener una o detener todo
(`POST /requests/{id}/helpers/stop`). Un reinicio del servidor retoma las partes sin repetir las terminadas.

**Límites por plan** [propuesta]:

| | Free | Pro | Max |
|---|---|---|---|
| Ayudantes a la vez | 3 | 6 | 8 |
| Partes por pedido | 6 | 20 | 50 |

"A la vez" nunca pasa de `MAX_PARALLEL_PER_ORG` (8 por defecto).

**Registro.** `helpers.proposed / launched / part_failed / stopped / completed` en auditoría: quién aceptó, cuántos, tope
y gasto real. **Interruptor** `HELPERS_ENABLED`, apagado en el servidor hasta probarlo; con él apagado nada cambia.

## 7. Orden de trabajo (sale todo junto)

1. Backend: datos, grupo, ejecución restringida, tope, parar y reintentar. Con simulación.
2. Runtime: `/v1/split` y persona neutra.
3. Interfaz: propuesta, bloque en marcha, "Ayudantes (sin puesto)" en Para.
4. Oficina 3D en los 5 estilos.
5. Guía y documentación (`chat-routing.md`, `07-seguridad-costos.md`, `equipos-oficinas.md`).

## 8. Cómo se comprueba

- Un ayudante no puede escribir, enviar, publicar, pedir aprobación ni lanzar otro, aunque el runtime lo intente.
- Un archivo que el responsable no puede leer tampoco lo lee el ayudante; una empresa nunca ve partes de otra.
- El tope detiene el grupo y el gasto real no lo pasa más que las llamadas ya en vuelo.
- Texto malicioso en el resultado de una parte no cambia lo que hace el responsable.
- Reinicio a mitad, parte que falla, detener todo, doble clic en Lanzar, y 50 partes.
- `HELPERS_ENABLED` apagado: las pruebas actuales pasan sin cambios.
- Todo con simulación. **No se corre con el modelo real sin permiso del dueño** (gasta saldo).

## 9. Queda a quien construye

Textos exactos, forma del selector de cantidad, tamaño de cada tanda al unir y aspecto fino de las figuras, dentro de
blanco, azul #0A66C2, IBM Plex Sans y sin nada infantil.
