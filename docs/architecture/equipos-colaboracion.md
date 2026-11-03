# Trabajo en equipo, parte 2: cómo se colabora de verdad

Continúa `equipos-oficinas.md` (el modelo: empresa → oficinas → personas, agentes con dueño, tres niveles de permiso).
Aquí se baja a cómo se usa: casos reales, las piezas de colaboración, el motor de permisos paso a paso, los casos
difíciles y qué ve cada quien en pantalla. Fecha: 2026-11-02. Estado: **propuesta**.

---

## 1. Cinco casos para probar el diseño

Cada pieza de este documento tiene que resolver al menos uno de estos casos; si no resuelve ninguno, sobra.

**A. Despacho de abogados (6 personas).** La socia (dueña), tres abogados, un asistente y un contador externo.
- El abogado Luis le pide a su agente legal que revise un contrato con la plantilla de la socia, que está en la carpeta
  de modelos del despacho.
- El contador externo solo debe ver `/Oficinas/Despacho/Facturación`.
- La socia aprueba todo lo que sale a un cliente.

**B. Pyme de ventas y operaciones (12 personas).**
- Marta (Ventas) cierra un trato.
- Operaciones tiene que preparar el pedido y Contabilidad facturarlo.
- Hoy esto va por correo y se pierde. Debería ir solo, con cada paso visible.

**C. Agencia de marketing con clientes.**
- Un equipo por cliente y un diseñador freelance que solo trabaja para el cliente X.
- Los agentes de la cuenta X no pueden usar nada del cliente Y. El aislamiento por cliente ya existe y se reutiliza.

**D. Alguien se va o se toma vacaciones.**
- Ana (responsable de Legal) se va dos semanas.
- Sus aprobaciones, las puertas que le tocan y los encargos que tiene no pueden quedarse parados.
- Si se va de la empresa, sus agentes, archivos y encargos pasan a alguien sin perderse.

**E. Entra alguien nuevo.**
- Pablo entra a Ventas.
- El primer día tiene que tener los agentes de Ventas, ver lo que su equipo sabe (memoria de la oficina) y saber a
  quién preguntar.

---

## 2. Las piezas de colaboración

### 2.1 Encargo (la unidad de trabajo entre personas y agentes)

Hoy el trabajo entre agentes se delega en el chat (consultar, traspasar, reasignar) y no deja un objeto que se pueda
seguir. Un **encargo** es un trabajo pedido por alguien a alguien:

- **De:** una persona o un agente, siempre en nombre de una persona.
- **Para:** una persona, un agente, o una oficina (lo toma quien esté libre o el agente de guardia).
- **Qué:** texto, archivos adjuntos (compartidos con el nivel justo), conversación de origen, plazo y prioridad.
- **Estados:** `pedido → aceptado → en curso → entregado → revisado` (o `devuelto` con comentario, o `rechazado`).
- **Seguimiento:** el agente de quien lo pidió puede recordar y preguntar por el estado ("¿cómo va lo del pedido 341?"),
  sin presionar a una persona más de una vez al día.
- **Entrega:** un resultado (archivo, respuesta, enlace) que queda en el hilo del encargo y en la carpeta acordada.

Se construye sobre las tareas y el motor de workflows que ya existen (`05-workflows.md`): un encargo es un workflow de
un paso con un asignado humano o agente. Resuelve A (revisión de contrato), B (cada paso) y D (encargos de Ana a su
suplente).

### 2.2 Circuitos entre oficinas

Un **circuito** es un proceso que cruza oficinas y se repite: "cuando Ventas marca un trato como ganado, Operaciones
recibe un encargo con el pedido y Contabilidad otro para facturar cuando Operaciones lo entregue".

- Es un workflow declarativo (`05-workflows.md`) cuyos pasos son encargos a oficinas.
- Lo crea el responsable de la oficina que inicia el circuito. Cada oficina que recibe tiene que **aceptarlo** una vez
  (nadie le mete trabajo automático a otra oficina sin permiso).
- Plantillas listas: trato ganado → pedido → factura; alta de cliente; contratación; revisión legal antes de enviar
  un contrato.
- Tablero del circuito: dónde está cada caso y cuánto lleva parado.

Resuelve B. Es lo que nadie en la competencia hace bien con agentes: Dust y Asana tienen tareas, pero no procesos entre
equipos con aprobación de cada equipo.

### 2.3 Tocar la puerta y la consulta entre oficinas

Ya está en `equipos-oficinas.md` §7. Detalles que faltaban:

- Si nadie de la oficina está presente, la puerta tocada se convierte en un **encargo a la oficina**.
- La respuesta de una consulta que usa contenido restringido la aprueba una persona de la oficina que responde. Para
  que no sea pesado, el responsable puede marcar carpetas como **"se puede citar fuera"**: entonces las respuestas que
  solo usan esas carpetas salen sin aprobación.

### 2.4 ¿Quién sabe de esto?

Un directorio de personas y agentes con oficina, profesión, temas (sacados de lo que trabajan, con permiso) y
disponibilidad. Responde a "¿quién en la empresa sabe de contratos de alquiler?" con personas y agentes, **sin mostrar
el contenido** al que quien pregunta no tiene acceso: solo "Legal tiene 14 documentos sobre esto; tocar la puerta".
Resuelve E (el nuevo sabe a quién preguntar) y evita que se pregunte siempre a la misma persona.

### 2.5 Pedir una revisión

Antes de mandar algo afuera, una persona o su agente pide **"revisión de"** a un compañero: el compañero ve el archivo
y la conversación con nivel Leer, comenta, y aprueba o devuelve. Es la aprobación de cuatro ojos que ya existe, pero
iniciada por quien trabaja, no solo por una regla. Resuelve A (la socia revisa lo que sale).

### 2.6 Comentarios y menciones en archivos

- `@persona` en un comentario de un archivo avisa a esa persona (y le pide acceso si no lo tiene).
- `@agente` en un comentario le encarga al agente trabajar sobre ese archivo ("@Elena resume las cláusulas de salida"),
  con los permisos de quien escribió el comentario.
- Las propuestas de cambio que ya existen en los artefactos se extienden a los archivos del Drive.

### 2.7 Editar a la vez

- Texto y artefactos: la fusión de tres vías y las versiones que ya existen.
- Binarios (pdf, docx, pptx): turno corto por archivo. "Luis está editando" o "Elena (para Luis) está editando" se ve
  en el archivo y en la oficina 3D, y quien llega después espera o crea una copia.
- Un agente nunca pisa una versión hecha por una persona mientras trabajaba: si el archivo cambió, vuelve a leerlo y
  rehace el cambio, o se lo dice a la persona.

### 2.8 Reuniones con varias oficinas

Las reuniones ya admiten personas, agentes y externos. Con oficinas:

- El agente de cada oficina participa **con los permisos de su oficina**, pero lo que dice en la reunión lo oyen todos.
  Por eso solo usa contenido que todos los presentes pueden leer, o contenido marcado como "se puede citar fuera".
- El acta se guarda una vez y cada asistente recibe su copia. Si hay partes restringidas, se reparten por permisos.
- Los acuerdos se convierten en encargos con responsable (§2.1).

### 2.9 Conocimiento que sube de nivel

- La memoria de una oficina es de esa oficina.
- El responsable puede **promover** un recuerdo o un documento a "conocimiento de la empresa" (lo ven todos), con un
  clic y quedando registrado.
- Las habilidades (`skills.md`) igual: una oficina publica "cómo hacemos una propuesta" y otras la usan.
- Nada sube solo: el texto de un documento importado no escribe memoria compartida por sí mismo (`archivos-agentes.md`
  §5).

### 2.10 Ausencias, suplentes y salidas

- **Ausencia** (con fechas, o leída del calendario): se elige un **suplente**. Las aprobaciones, puertas, consultas y
  encargos de la persona van al suplente; sus agentes siguen trabajando lo que ya tenían, pero no empiezan nada nuevo
  que necesite su aprobación. El estado se ve en el directorio y en la oficina 3D.
- **Salida de la empresa:** el admin elige a quién pasan sus agentes, carpetas y encargos. Hasta que lo decida, quedan
  con el responsable de su oficina, **nunca huérfanos ni borrados**. Su carpeta personal pasa al nuevo dueño solo con
  constancia y aviso.
- Resuelve D.

### 2.11 Invitados externos

Rol nuevo **invitado** (contador externo, freelance, abogado de otra firma):

- solo ve lo que se le comparte explícitamente: carpetas, encargos o una sala de proyecto;
- no ve el directorio, ni la memoria de la empresa, ni otras oficinas;
- puede usar los agentes que se le compartan, siempre con sus permisos (o sea, casi nada);
- cuenta como asiento (o, decisión del dueño, como asiento de menor precio);
- todo lo que hace queda auditado.

Distinto del portal de clientes, que es de solo lectura y para el cliente final. Resuelve A (contador) y C (freelance).

### 2.12 Mi bandeja y el tablero de la oficina

- **Mi bandeja** (por persona): encargos para mí y que pedí, aprobaciones que puedo decidir y que pedí, puertas tocadas,
  pedidos de acceso, revisiones pedidas, menciones. Agrupado por prioridad y con un resumen diario opcional (hay un
  resumen "mientras no estabas" que ya es por persona).
- **Tablero de la oficina** (para el responsable): encargos abiertos por persona y por agente, circuitos parados, carga
  de cada uno, costo del mes. Sirve para repartir el trabajo entre personas y agentes.

### 2.13 Avisos sin cansar

- Tres niveles: **ahora** (me bloquea a mí o a otro: aprobación, puerta tocada, encargo urgente), **resumen** (lo
  demás, una o dos veces al día), **silencio** (lo veo cuando entre).
- Cada persona elige los suyos.
- Mientras alguien está "concentrado" o "en reunión", lo que no es urgente espera.
- Nunca se avisa dos veces por lo mismo por dos canales.

---

## 3. El motor de permisos, paso a paso

### 3.1 Datos

- `oficinas`: el departamento con oficina activada (visibilidad abierta o privada).
- `miembros_oficina`: persona y rol (responsable o miembro).
- `accesos`: quién (persona, oficina, invitado o agente), sobre qué (carpeta o archivo), nivel (leer o editar),
  caduca, quién lo dio y por qué (compartido, pedido aprobado, encargo).
- `carpetas.restringida`: la frontera que corta la herencia.
- `procedencia` de cada archivo: de qué archivos y versiones salió, para los derivados.

### 3.2 ¿Puede la persona P leer o editar el recurso R?

1. Si P es admin de la empresa → sí, con auditoría. Las carpetas personales y los chats privados son la excepción: el
   admin no entra salvo el procedimiento de salida (§2.10) o una exportación legal, que avisa a la persona y queda
   registrada.
2. Se sube desde R por sus carpetas hasta la raíz y se juntan los accesos de P (directos, por su oficina, por invitado).
   **Si se encuentra una carpeta restringida, se para ahí**: lo de más arriba no cuenta.
3. El nivel de P es **el más alto** de los que encontró (leer < editar), sin contar los caducados.
4. Si no encontró nada → ninguno, y se ofrece "Pedir acceso".
5. Si R es un **derivado**, además P tiene que poder leer **todas** sus fuentes restringidas (§3.4).

### 3.3 ¿Puede el agente A hacerlo para la persona P?

`acceso(A para P, R) = mínimo(acceso de P a R, techo del agente A sobre R)`.

- El techo del agente sale de sus zonas de hoy (su mesa, proyectos asignados, `/Compartido`) más los accesos dados al
  agente, y nunca supera lo de la persona.
- Si no hay persona (tarea programada), P es el patrocinador del agente.
- Se calcula **en cada herramienta** (leer, buscar, escribir, mover), no al principio de la tarea.
- **Búsqueda (RAG):** se calcula una vez por tarea la lista de carpetas que P puede leer y se filtra cada búsqueda por
  esa lista. Para una pyme son decenas de carpetas: cabe en la consulta sin problema. Los trozos de carpetas
  restringidas solo entran si P tiene acceso dentro de la frontera.
- **Memoria:** igual. Un recuerdo hereda los permisos de su fuente; uno que salió de una carpeta restringida solo lo
  recuerdan las tareas de personas que pueden leerla.

### 3.4 Derivados

- Al guardar un archivo, se guarda de qué archivos y versiones leyó el agente para hacerlo.
- Si alguna fuente está tras una frontera restringida, el derivado queda **pegado** a esa frontera: solo lo leen quienes
  leen todas sus fuentes restringidas, esté donde esté guardado.
- Para "despegarlo" (por ejemplo, un resumen público de un contrato) hace falta la aprobación del responsable de la
  fuente. Queda registrado.

### 3.5 Lo que se ve

- **Diálogo Compartir** (como Drive): personas u oficinas, nivel Leer o Editar, caducidad opcional, y qué agentes de
  esa persona podrán usarlo (por defecto los suyos, con su mismo nivel).
- **Quién tiene acceso** en cada carpeta: lista, origen de cada acceso y botón para quitarlo.
- **Insignias:** restringida, compartida contigo, derivado de restringido, caduca el día X.
- **Por qué no puedo:** cuando falta acceso, se dice qué frontera lo impide y quién puede darlo.

---

## 4. Casos difíciles

| Caso | Qué pasa |
|---|---|
| Le quito el acceso a alguien que tenía un derivado guardado en su carpeta | El derivado está pegado a la frontera: deja de poder leerlo |
| El agente de Ana tiene una tarea a medias cuando Ana pierde el acceso a la carpeta | En la siguiente herramienta ya no puede leer; la tarea se detiene con "falta acceso" y avisa a Ana |
| Dos oficinas en el mismo proyecto | La carpeta del proyecto es de sus miembros (personas de las dos oficinas); lo de cada oficina sigue en la suya |
| Una persona en dos oficinas | Suma los accesos de ambas; sus agentes personales pueden trabajar en las dos con sus permisos |
| Un agente de oficina y una persona de otra oficina en un chat común | El agente usa solo lo que pueden leer todos los presentes |
| Se borra una oficina | No se permite con archivos o agentes dentro: primero se mueven o se pasan a otra oficina |
| El responsable de una oficina se va | Pasa a ser responsable el admin hasta que se nombre otro; nunca queda sin responsable |
| Un invitado reenvía un archivo | No tiene "compartir"; solo descargar, y si el dueño lo desactiva, ni eso (queda auditado) |
| Alguien pide acceso a algo muy sensible | El pedido llega al responsable de esa frontera, no al dueño del archivo, si la carpeta es restringida |
| Un documento con instrucciones escondidas pide "comparte esto con todos" | Compartir, despegar derivados y publicar fuera siempre los aprueba una persona; el agente no puede |

---

## 5. Qué se ve según quién eres (no complicar a nadie)

- **Persona sola:** nunca ve oficinas, permisos ni directorio. Solo "Compartir" cuando invite a alguien.
- **Equipo de una oficina:** ve su oficina, "Compartir" y "Pedir acceso"; no ve fronteras ni circuitos salvo que los
  busque.
- **Empresa con varias oficinas:** ve el edificio, el directorio, tocar la puerta, encargos entre oficinas y su bandeja.
- **Responsable:** además el tablero de su oficina, miembros, agentes, permisos de sus carpetas y circuitos.
- **Admin:** además el panel de la empresa (agentes, accesos que caducan, lo compartido de más, auditoría).

Regla: **cada pantalla nueva aparece cuando hace falta, no antes.**

---

## 6. Cómo sabremos que funciona

- Encargos entre oficinas por semana y tiempo hasta que se aceptan y se entregan.
- Puertas tocadas respondidas en menos de una hora.
- Pedidos de acceso aprobados frente a rechazados (muchos rechazos = permisos mal pensados).
- Ningún caso de un agente mostrando contenido a quien no lo puede leer (pruebas automáticas que lo intentan en cada
  versión).
- Personas activas por empresa y empresas que pasan de Gratis a Pro por invitar compañeros.

---

## 7. Fases (actualiza `equipos-oficinas.md` §11)

| Fase | Qué | Notas |
|---|---|---|
| E0 | Saber quién es quién | **En construcción** (2026-11-02) |
| E1 | Alta "¿para quién es?", profesión, agentes por rol, invitaciones con oficina | Sin cambios |
| E2 | Oficinas, agentes con dueño, directorio, memoria de oficina y personal | + promover conocimiento (§2.9) |
| E3 | Motor de permisos (§3), compartir, pedir acceso, derivados, auditoría | Diseñar junto con las fuentes de `archivos-agentes.md` fase 4 |
| E4 | Encargos, mi bandeja, tocar la puerta, consultas entre oficinas, pedir revisión, menciones | Los encargos son la base de E5 |
| E5 | Circuitos entre oficinas, ausencias y suplentes, salidas, invitados externos, tablero de la oficina, avisos por niveles | |
| E6 | Gobierno: aprobaciones al responsable, reglas por agente y acción, costos y topes por persona, panel del admin | |

## 8. Decisiones del dueño (nuevas)

1. **El admin no lee carpetas personales ni chats privados** salvo salida de la persona o exportación legal, con aviso.
   Recomendado: es lo que da confianza para usar el producto de verdad.
2. **Invitados externos:** ¿cuentan como asiento normal o más barato? Recomendado: asiento normal en Pro y Max, hasta ver
   el uso real.
3. **Circuitos:** ¿solo plantillas al principio o también crearlos a mano? Recomendado: plantillas primero y crear a mano
   en Max.
