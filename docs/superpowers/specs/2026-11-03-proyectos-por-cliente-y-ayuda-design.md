# Proyectos para muchos clientes y agentes que piden ayuda

Fecha: 2026-11-03. Estado: aprobado por el dueño en el chat ("haz eso que tenga sentido lógico y de negocio").

## Por qué

El dueño quiere usar Proyectos así: arma una vez un trabajo ("Contactar clientes", "Onboarding de cliente") y lo
lanza **para muchos clientes**. Mientras corre, quiere **ver en qué va cada uno** y, si un agente se traba a mitad de
un paso, que **pare y le pregunte** antes de seguir. Hoy le cuesta entender Proyectos: hay mucho, pero repartido en 9
vistas con nombres técnicos, y no hay forma de que un agente pregunte algo y siga con la respuesta.

## Lo que ya existe (base)

- Proyecto = una solicitud del orquestador con objetivos → flujos → nodos (`backend/internal/projects`, API §16 de
  `docs/architecture/08-api.md`). Estados de nodo calculados en `projects/view.go`.
- Plantillas de proyecto con parámetros (`text|number|list`) y repetición por lista (`repeat`, `templates_data.go`).
  "Guardar como plantilla" existe.
- Clientes (`backend/internal/customers`, `/customers`): carpeta `/Clients/<nombre>`, agentes por cliente, y
  `customer_id` en el proyecto con aislamiento de artefactos (`projects/customer_refs.go`). El asistente de nuevo
  proyecto (`NewProjectWizard`) no lo envía todavía.
- Recuperación de un nodo fallido: reintentar, omitir o replanificar (`projects/recovery.go`); intervención `steer` y
  reasignar (`application/intervene.go`).
- Aprobaciones con bandeja (`ApprovalsTray`, `approvals-inbox.md`), avisos y push por persona (E0), canales de aviso
  (`backend/internal/notify`).

## Parte 1: lanzar una plantilla para varios clientes (un "lote")

Para el usuario la palabra es **lote**: "Lote · Contactar clientes · 30 clientes".

1. **Dónde se lanza:** en la plantilla (biblioteca y "Usar plantilla") hay dos botones: "Usar para un cliente" (o
   sin cliente, como hoy) y **"Lanzar para varios clientes"**. También desde un proyecto terminado: "Repetir con otros
   clientes" (lo guarda como plantilla si no lo es).
2. **Elegir clientes:** lista de clientes existentes con búsqueda, filtros y seleccionar todo; o **importar una lista**
   (CSV/Excel: nombre, correo, teléfono, empresa, notas). Antes de crear nada se muestra la vista previa: cuáles ya
   existen, cuáles se crearán y filas con error. Los clientes nuevos se crean como clientes normales (con su carpeta).
3. **Parámetros:** los de la plantilla se rellenan una vez para todo el lote. Los textos pueden usar los datos del
   cliente (`{{cliente.nombre}}`, `{{cliente.correo}}`, `{{cliente.empresa}}`, `{{cliente.notas}}`); si a un cliente
   le falta un dato que la plantilla usa, la vista previa lo marca antes de lanzar.
4. **Costo y límites antes de lanzar:** estimación total (rango por cliente × número de clientes) y comparación con el
   presupuesto; si supera el umbral de confirmación que ya existe, se pide confirmar. Hay un máximo de clientes por
   lote según el plan.
5. **Un proyecto por cliente:** cada cliente recibe su propio proyecto (con `customer_id`, su carpeta y su
   aislamiento), así uno trabado o fallido no frena a los demás. El lote los agrupa.
6. **Ritmo:** el lote corre como mucho K proyectos a la vez (los demás quedan "en cola") para no disparar el costo ni
   los límites del proveedor de IA.
7. **Vista del lote:** una tabla, una fila por cliente: cliente · avance (%) · paso actual y qué agente lo hace ·
   estado (en cola, en curso, **necesita tu ayuda**, terminado, falló, cancelado) · costo. Arriba un resumen (12
   terminados, 15 en curso, 3 necesitan tu ayuda) y filtros por estado. Acciones: pausar o reanudar el lote, cancelar
   un cliente, reintentar los fallidos, abrir el proyecto de un cliente. El lote aparece en Proyectos como una tarjeta
   propia y en el indicador de proyectos de la oficina.

## Parte 2: un agente puede pedir ayuda a mitad de un paso

Estado nuevo de tarea y de nodo: **"Necesita tu ayuda"** (en código `needs_input`).

1. **El agente pregunta:** el runtime da al agente una herramienta para pedir ayuda cuando le falta algo que no puede
   conseguir o decidir solo (un dato, un archivo, una decisión entre opciones, una contradicción en la información).
   La pregunta dice qué pasó, qué necesita y, si aplica, opciones. Se pide en vez de inventar o de fallar.
2. **Qué se detiene:** solo esa tarea (deja de llamar al modelo y no gasta) y lo que depende de ella. El resto del
   proyecto y los otros clientes del lote siguen.
3. **Avisos:** un aviso por pregunta a la persona que corresponde (quien lanzó el proyecto o el lote; si no está, el
   responsable del equipo), por los canales que ya tiene (push, correo, Slack/Teams según su configuración). La
   pregunta queda además en la **bandeja de pendientes**.
4. **Bandeja de pendientes:** la bandeja de aprobaciones pasa a ser "Pendientes" con dos tipos de elemento:
   aprobaciones y preguntas. Se agrupan por lote y por proyecto, se pueden filtrar y se pueden **responder varias a la
   vez** con la misma respuesta (p. ej. 3 clientes sin teléfono: "usa el correo").
5. **Cómo se responde:** con texto, con un archivo, eligiendo una de las opciones, reasignando la tarea a otro agente u
   **omitiendo** la tarea. Al responder, la tarea sigue desde donde paró con la respuesta incluida; queda en el
   historial quién respondió y qué.
6. **Omitir con sentido:** antes de omitir, la app calcula qué depende de esa tarea.
   - Si nada esencial depende de ella: muestra lo que se saltará y pide confirmar.
   - Si sin ella el proyecto no puede terminar (todos los caminos al resultado final pasan por ella): no deja omitir
     sin más y lo explica: "Si omites esto, estos N pasos no se pueden hacer y el proyecto de este cliente no se
     termina". Ofrece responder, reasignar o **cancelar el proyecto de ese cliente**.
7. **Si nadie responde:** recordatorio a las 24 h y luego cada día; la fila del lote muestra hace cuánto espera. El
   agente nunca decide solo por falta de respuesta.

## Parte 3: ver en qué va cada uno (mínimo de esta entrega)

- En el detalle de un nodo se ven los **archivos que produjo** esa tarea.
- La vista de proyecto abre en una vista simple de seguimiento: lista de tareas con agente, estado, desde cuándo y sus
  archivos, y "Necesita tu ayuda" arriba. Las vistas avanzadas (mapa, línea de tiempo, kanban…) siguen disponibles.
- Una vista general en la oficina para "estar al tanto de todo" se diseña aparte, en una conversación posterior.

## Metas, no metas y anti-metas

- **Metas:** lanzar una plantilla para muchos clientes en un paso; que un cliente trabado no frene a los demás; que el
  agente pregunte en vez de inventar o fallar; responder rápido, también en grupo; omitir solo cuando tiene sentido.
- **No metas:** que el agente decida solo cuando nadie responde; enviar algo fuera de la empresa sin aprobación (las
  aprobaciones siguen igual); rediseñar la oficina 3D.
- **Anti-metas (sería un fracaso aunque funcione):** avisos que cansan; omitir algo y que el proyecto se rompa sin
  avisar; costos sorpresa por lanzar 100 clientes; que el usuario no entienda qué tiene que hacer con una pregunta.

## Restricciones

- Reutilizar el motor de proyectos, las plantillas, los clientes, la recuperación de nodos y la bandeja existentes; no
  crear un segundo motor.
- Permisos y aprobaciones sin cambios; la persona que responde necesita poder ver ese proyecto.
- Textos en es, en y pt; estilo visual de la app; pruebas en backend, runtime y frontend.
- Migraciones aditivas, como las demás.

## Decisiones tomadas después de la revisión

- **Límite de proyectos activos del plan (decisión del dueño):** un lote cuenta como **un solo** proyecto activo,
  tenga los clientes que tenga. Sigue habiendo un máximo de clientes por lote y K proyectos corriendo a la vez por
  lote (valores por plan, configurables).
- **Quién hace el trabajo (decisión del coordinador):** los agentes del equipo de la empresa. Si un cliente ya tiene
  agentes asignados (los de cliente que ya existen), se usan esos. Un lote nunca crea agentes nuevos.
- **Qué es un cliente y cómo se reconoce al importar (decisión del coordinador):** un cliente puede ser una persona o
  una empresa. Se agregan al cliente campos opcionales de contacto: persona de contacto, correo y teléfono (la
  empresa es su nombre). Al importar, una fila es "el mismo cliente" si coincide el correo; si no trae correo, el
  número fiscal; si no, el nombre normalizado. La vista previa muestra cada coincidencia y deja corregirla antes de
  lanzar.
- **Omitir cuando el proyecto tiene varios objetivos (decisión del coordinador):** si omitir impide **todo** el
  resultado (ningún objetivo puede terminar), no se deja omitir y se explica por qué. Si solo impide algunos
  objetivos, se permite con confirmación que nombra los objetivos que quedarán sin hacer, y el proyecto termina como
  **"terminado en parte"** (estado nuevo, visible en la fila del lote).
- **Avisos sin cansar (decisión del coordinador, respetando "un aviso por cada uno"):** cada pregunta avisa. Si
  llegan varias del mismo lote en pocos minutos, se juntan en un solo aviso ("5 clientes necesitan tu ayuda"),
  mientras en Pendientes cada una sigue apareciendo por separado. Recordatorios: a las 24 h, luego cada día durante 3
  días y después un resumen semanal por lote.

## Lo que queda a criterio de quien construye

- Nombres técnicos, forma exacta de las tablas y eventos, cómo el runtime expone la herramienta de pedir ayuda.
- K (proyectos a la vez por lote) y el máximo de clientes por lote por plan, con valores por defecto razonables
  configurables.
- El diseño visual de la tabla del lote y de la bandeja, dentro del estilo de la app.
