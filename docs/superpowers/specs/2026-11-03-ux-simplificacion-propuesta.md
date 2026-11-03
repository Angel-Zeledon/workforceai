# Una app más fácil de entender: auditoría y propuesta

Fecha: 2026-11-03. Estado: **borrador para que el dueño apruebe**. No se cambió código.

Pedido del dueño: "que sea más entendible y conceptualmente fácil de identificar cada cosa en la app; hacer todo menos
complejo, manteniendo nuestras features, complejidad y lógica, pero haciéndolo a nivel de UI más fácil de entender".

Regla de esta propuesta: **no se quita ninguna función ni se cambia cómo trabaja el sistema por dentro.** Lo que cambia
es cómo se llaman las cosas, dónde están, cuántas se ven a la vez y cómo se ve cada estado.

Capturas tomadas hoy en el modo demo: `docs/screenshots/ux-audit-2026-11-03/` (01 a 09). También se revisaron las de
`chats-pedidos-2026-11-03/` y `e2-oficinas-2026-11-03/`.

---

## 1. En una página

La app tiene mucho y casi todo funciona. El problema no es que falten cosas: es que **la misma idea aparece con varios
nombres, en varios lugares y con colores distintos**, y que cada pantalla muestra todo a la vez.

Los 5 problemas más importantes:

1. **Pendientes está escondido y repartido.** La bandeja "Pendientes" no está en el menú y se abre fuera de la app (sin
   barra lateral ni forma de volver). Las aprobaciones aparecen además en **9 lugares más**, cada uno con su propio número
   (en la demo, al mismo tiempo: 1 en el menú, 6 en Pendientes, 0 en el Dashboard y "2 con más de 24 h" en Sugerencias).
2. **Las mismas palabras para cosas distintas, y palabras distintas para lo mismo.** "Oficina" significa 5 cosas (la vista
   3D, un equipo, los ajustes de pantalla, la configuración del negocio y el chat con todos). "Plantilla" significa 4
   (de proyecto, de trabajo, de industria y de puesto). "Agente" y "empleado" se usan mitad y mitad (251 y 90 textos).
   "Equipo", "oficina" y "departamento" son el mismo grupo de personas visto desde tres pantallas.
3. **Cada estado tiene un nombre y un color distinto según la pantalla.** "Terminado" se dice Listo, Terminado, Hecha y
   Completado. "Necesita tu ayuda" es azul en Proyectos y ámbar en Chats. "Lista" quiere decir "lista para empezar" en
   Proyectos y "Listo" quiere decir "terminado" en Chats.
4. **Pantallas con demasiado a la vez.** El proyecto tiene 10 pestañas (Kanban, Carriles, Delegación…); el panel del
   agente, 10 secciones; el Dashboard, 11; Organización, 6 más un enlace. Un nuevo no sabe por dónde empezar.
5. **Lo nuevo de hoy quedó enterrado.** Los permisos de aprobación del agente están dentro de "Perfil"; las carpetas que
   ve, dentro de "Archivos"; los Clientes (que son la base de los lotes) solo existen dentro del Dashboard; y "permisos"
   aparece en 5 pantallas distintas.

La propuesta, en tres ideas:

- **12 conceptos base**, cada uno con un nombre, un ícono y una frase. Se eliminan los sinónimos.
- **Un menú agrupado** con Pendientes como sección propia, Clientes visible y lo de configuración fuera del día a día.
- **Patrones únicos**: un solo indicador de estado (7 estados, mismos colores en toda la app), un solo panel lateral de
  detalle, una sola bandeja de Pendientes y un botón "¿Qué es esto?" junto a cada concepto.

---

## 2. Inventario: qué hay hoy

### 2.1 El marco de la app

| Zona | Qué tiene hoy | Dónde está en el código |
|---|---|---|
| Menú lateral | Oficina (con el número de aprobaciones), Chats, Proyectos, Organización, Reuniones, Archivos, Habilidades, Tienda; abajo Ajustes | `lib/navBridge.ts` (`SECTIONS`), `components/nav/AppShell.tsx` |
| Barra de arriba | Título y empresa; selector "Oficina / Dashboard"; "Primeros pasos"; meta del día; "?" (tour); monedas; "En vivo / Reconectando"; engranaje "Administración" | `components/nav/AppShell.tsx`, `ActivationChecklist.tsx`, `admin/AdminMenu.tsx` |
| Avisos bajo la barra | Cuenta de prueba, controles de seguridad, proveedor de IA, costos | `TrialBanner`, `ControlsBanner`, `ProviderAlert`, `CostOverlays` |
| Ayudas de primera vez | Bienvenida con 3 pasos, franja "Así se trabaja aquí" en cada pantalla, tour guiado por pantalla, "Primeros pasos" (3 tareas) | `onboarding/WelcomeOverlay`, `nav/ui2.tsx` (`FirstRunHint`), `tour/`, `ActivationChecklist` |
| Pendientes | `/approvals/inbox` (tarjetas una a una) y `/approvals` (lista, pensada para móvil). **Ninguna de las dos está dentro del marco de la app** | `app/approvals/`, `components/approvals/` |

### 2.2 Las pantallas principales

| Pantalla | Qué hay dentro | Cuántas pestañas o secciones |
|---|---|---|
| **Oficina** (inicio) | Vista 3D; "Actividad en vivo"; bandeja de aprobaciones flotante; "Mientras no estabas"; "Editar oficina"; "1 en línea"; atajos; barra para pedir | 7 cosas flotando sobre la vista 3D (captura 02) |
| **Dashboard** (el otro modo de la Oficina) | Resumen, Clientes, Pedidos y plan, Tareas, Agentes, Conversaciones, Aprobaciones, Reportes, Archivos, Calidad, Costos y errores | 11 (captura 09) |
| **Panel del agente** (al hacer clic en uno) | Chat, Estado, Tareas, Con el equipo, Memoria, Reportes, Archivos (+ carpetas que puede ver), Actividad, Perfil (+ permisos, confianza, calidad, habilidades), Auditoría de pensamiento | 10, escondidas en un menú desplegable "Chat ▾" (captura 08) |
| **Chats** | Grupos Agentes / Personas / Pedidos; panel del pedido con Resumen, Pasos, Archivos, Informe, Cómo lo trabajaron | 3 grupos + 5 pestañas del pedido (ya simplificado hoy) |
| **Proyectos** | Lista de proyectos y lotes; plantillas; "Biblioteca de plantillas"; asistente nuevo proyecto; asistente de lote | — |
| **Detalle del proyecto** | Seguimiento, Salud, Mapa, Línea de tiempo, Kanban, Carriles por agente, Delegación, Aprobaciones, Cambios de plan, Costos; arriba Pausar, Cancelar, Compartir con el cliente, Guardar como plantilla | 10 + una línea de ayuda que explica en qué orden usarlas |
| **Borrador de proyecto** | Plan editable; "Costo probable (P50)", "Costo pesimista (P90)", "modelo deepseek-chat · confianza baja"; revisión de calidad; validación | (captura 04) |
| **Organización** | Organigrama, Oficinas, Directorio de agentes, Departamentos, Puestos de agentes, Delegación; enlace "Plantillas por industria"; botón principal "Nuevo departamento" | 6 + 1 enlace |
| **Reuniones** | Reuniones, Salas, Rituales, Calendario, Debates | 5 |
| **Archivos** | Carpetas de la empresa y de los agentes; "Qué agentes la ven" | — |
| **Habilidades** | Lista con filtro por estado, editor, revisión | — |
| **Tienda** | Tienda, Inventario, Historial | 3 |
| **Ajustes** | Ajustes de la oficina, Contratar por profesión, Plantillas de trabajo, Marca del portal del cliente, Conexiones, Configura tu oficina, Aprobaciones, Seguridad, Inicio de sesión único, Detener todo, Seguridad de la cuenta, Datos y privacidad, Mis costos, Miembros, Cerrar sesión; buscador | 15 filas (captura 06) |
| **Conexiones** (dentro de Ajustes) | Conexiones, Buzones de datos, Permisos por empleado, Qué puede hacer cada empleado, Registro de uso, Límites. Hoy otro agente agrega **bases de datos** | 6 |
| **Seguridad** (dentro de Ajustes) | Estado general, solo lectura, detener por herramienta, pausar empleados, revisión de plan, **correos para aprobar**, topes de gasto, horario, alertas | ~10 bloques |

---

## 3. Mapa de conceptos

Marcas: **S** = sinónimos para lo mismo · **D** = una palabra para dos o más cosas · **T** = palabra técnica a la vista ·
**P** = hay que entenderlo antes de poder usar algo.

| Concepto (lo que es) | Nombres con que aparece (es / en) | Dónde | Marcas |
|---|---|---|---|
| Un empleado de IA | agente (251 textos), empleado (90), "miembro de tu equipo", "Ver empleado" / agent, employee | En todas partes; "Contratar por profesión", "Permisos por empleado", "Pausar empleados" | **S** |
| Una persona de la empresa | persona, compañero, miembro, usuario / person, member | Chats → Personas, Miembros e invitaciones, Oficinas | S |
| Un grupo de personas y agentes (Legal, Ventas) | oficina, equipo, departamento / office, team, department | Organización → Oficinas ("Convertir un **equipo** en **oficina**"), Departamentos, Organigrama, botón "Nuevo departamento" | **S**, **P** |
| La vista 3D | Oficina / Office | Menú, título, bienvenida | **D** (ver abajo) |
| "Oficina" además significa… | la empresa entera ("Toda la oficina", "Archivos de la oficina"), la apariencia de la app ("Ajustes de la oficina": idioma, etiquetas), el contexto del negocio ("Configura tu oficina"), un equipo (Oficinas) | Chats, Archivos, Ajustes, Organización | **D** ×5 |
| "Equipo" además significa… | todos tus agentes ("Tu equipo de empleados de IA"), un grupo de personas, la configuración ("Ajustes del equipo") | Bienvenida, Organización, menú de administración | **D** ×3 |
| Algo que le pides a la app | pedido (80), solicitud, encargo, trabajo, "tu primera tarea" / request | Chats (pedido), Dashboard ("Pedidos y plan"), Primeros pasos ("Pide tu primera tarea"), mensajes de agentes ("Estructurar la solicitud") | **S** |
| Una parte del trabajo que hace un agente | tarea (166), paso (55), nodo / task, step, node | Proyecto ("13 tareas" en borrador y "3 de 13 pasos" en la tarjeta), panel del pedido ("Pasos"), panel del agente ("Tareas"), mensajes de límite ("máximo de **nodos**") | **S**, T |
| Un trabajo grande con plan | proyecto, plan, objetivos, flujos, fases / project | Proyectos ("Trabajo grande con objetivos, flujos en paralelo…") | T, P |
| El mismo proyecto para muchos clientes | lote / batch | Proyectos ("Lanzar para varios clientes") | P |
| Una receta reutilizable | plantilla de proyecto (Proyectos), **plantilla de trabajo** (Ajustes: "Plantillas de equipos listas para poner a trabajar"), **plantilla de industria** (crea departamentos y agentes), **plantilla de puesto** (Contratar por profesión), receta, flujo / template, workflow, recipe | Proyectos, Ajustes ×2, Organización (enlace) | **D** ×4, P |
| Cómo hace algo tu empresa | habilidad, skill, SKILL.md / skill | Habilidades, panel del agente, errores de importación, Rituales ("crea la skill desde Skills") | S, T |
| Lo que entrega un pedido | informe, reporte, entregable, resultado, artefacto / report, deliverable | Chats ("Informe"), panel del agente ("Reportes"), Dashboard ("Reportes"), Archivos ("entregables") | **S** |
| Lo que espera por ti | Pendientes, aprobaciones, "por aprobar", preguntas, bandeja, "Mientras no estabas", "correos para aprobar", "Necesita tu ayuda", "Esperando a una persona" | 10 lugares (ver problema 1) | **S**, P |
| Qué puede hacer un agente sin preguntarte | permisos, autonomía, nivel, excepciones, piso de seguridad, "sugerir … autónomo" (Primeros pasos) vs "Me pregunta todo / Solo lo importante / Trabaja solo" (panel) | Panel del agente → Perfil, Ajustes → Aprobaciones, Ajustes → Seguridad, Conexiones → Permisos por empleado y Qué puede hacer cada empleado, Proyecto → Aprobaciones | **S**, P |
| Para quién trabajas | cliente, "Distrito de clientes", portal del cliente / customer | **Solo** dentro de Dashboard → Clientes; también en lotes y "Compartir con el cliente" | P (sin casa propia) |
| Una cuenta que los agentes usan | conexión, integración, herramienta, buzón de datos, importar desde un enlace (y ahora base de datos) / connection | Ajustes → Conexiones (6 pestañas) | S |
| Lo que el agente recuerda | memoria (de agente, de oficina, "Mi memoria", de cliente) / memory | Panel del agente, Organización → Oficinas | D |
| Moneda de juego | monedas, meta del día, tienda | Barra de arriba, Tienda, bienvenida | P (se explica 3 veces) |
| Palabras técnicas a la vista | `email.send`, `draft_reply`, P50/P90, "modelo deepseek-chat", "confianza baja", Kanban, Carriles, Delegación, "Auditoría de pensamiento", "Abogado del diablo", "Modo ensayo", Rituales, SCIM, nodos | Tarjeta de aprobación (captura 02), Pendientes (captura 05), borrador (captura 04), panel del agente (captura 08) | **T** |
| Estado de un agente | Inactivo + Disponible a la vez (captura 08), Pensando, Esperando, Bloqueado, Completado, Error… (10 estados) | Panel del agente, Dashboard → Equipo | D (Inactivo suena a apagado) |

---

## 4. Problemas, ordenados por impacto

### Alto impacto

**P1. Pendientes no tiene casa y cada lugar cuenta por su lado.**
- La bandeja "Pendientes" (`/approvals/inbox`) no está en el menú y se abre sin la barra lateral: quien entra no tiene
  cómo volver (captura 05). La única forma de llegar es un enlace en Chats o en el panel del pedido.
- Las aprobaciones se ven además en: el número sobre "Oficina" en el menú, la bandeja flotante de la Oficina, el
  mostrador 3D de aprobaciones, "Mientras no estabas", Dashboard → Aprobaciones, la pestaña Aprobaciones de cada
  proyecto, la barra del chat ("Hay 1 aprobación esperando"), Seguridad → "Correos para aprobar" y las Sugerencias del
  equipo. Son diez lugares con tarjetas distintas.
- En la demo se ven cuatro números distintos al mismo tiempo (1, 6, 0 y "2 de más de 24 h"). Aunque parte sea de los
  datos de prueba, muestra que cada pantalla cuenta por su cuenta.
- La sugerencia de Pendientes manda a "Seguridad → Avisos" para dejar de preguntar, cuando hoy existe "Aprobar y no
  volver a preguntar" y Ajustes → Aprobaciones.

**P2. "Oficina", "equipo" y "plantilla" significan varias cosas.** Ver el mapa (sección 3). Es la causa principal de que
el dueño se enrede: para alguien nuevo, "Ajustes de la oficina" y "Configura tu oficina" son la misma cosa, y no lo son.
En Organización (captura e2-01) el botón principal es "Nuevo departamento", la pestaña es "Oficinas" y el selector dice
"Elige un equipo": tres palabras para una sola idea en una sola pantalla.

**P3. Los estados no se parecen entre pantallas.**

| Lo que pasa | Pedido (Chats) | Proyecto | Tarea del proyecto | Fila del lote | Tarea (panel/Dashboard) | Agente |
|---|---|---|---|---|---|---|
| Aún no empieza | — | Borrador / Planificando / **Lista** | Pendiente / **Lista** / Esperando | En cola | Pendiente | Inactivo |
| Trabajando | En curso (azul) | En curso | En curso | En curso | En curso | Trabajando / Pensando |
| Te necesita (pregunta) | Necesita tu ayuda (**ámbar**) | — | Necesita tu ayuda (**azul**) | Necesita tu ayuda (**azul**, en la tarjeta del lote) | — | Esperando |
| Te necesita (aprobación) | Espera tu aprobación | Esperando a una persona | Esperando aprobación | — | Esperando aprobación | Esperando aprobación |
| Terminó | **Listo** | Terminado | **Hecha** | Terminado | Hecha | Completado |
| Falló | Falló | Fallido | Fallida / Bloqueada | Falló | Fallida / Bloqueada | Error / Bloqueado |

Hay al menos 6 tablas de colores de estado en el código (`lib/meta.ts`, `projects/shared.tsx` ×3, `chats/chatsUi.tsx`,
`customersLogic.ts`, `presence/PeopleLayer.tsx`, `ui.tsx`). "Lista" (proyecto listo para empezar) y "Listo" (pedido
terminado) se confunden a simple vista.

**P4. Pantallas con demasiado a la vez.**
- Proyecto: 10 pestañas con nombres de gestor de proyectos (Kanban, Carriles por agente, Delegación, Línea de tiempo) y
  una línea de ayuda que explica en qué orden usarlas: si hace falta instrucción, sobran pestañas.
- Panel del agente: 10 secciones escondidas en un desplegable. Lo más nuevo y delicado (permisos de aprobación) está
  dentro de "Perfil", debajo de la personalización del muñeco.
- Oficina: siete cosas flotando sobre la vista 3D más la bienvenida, más la franja de ayuda, más "Primeros pasos"
  (captura 02). Con el panel del agente abierto hay **dos** barras para escribir a la vez ("Escribe a la oficina" y
  "Escríbele a Sofía", captura 08).
- Dashboard: es otra app entera dentro de la Oficina (11 secciones) que repite Chats, Pendientes, Archivos y Clientes.

**P5. Cosas importantes sin casa visible.**
- **Clientes** solo existe en Dashboard → Clientes ("Distrito de clientes"), y el Dashboard solo se abre con el
  selector de arriba, que en el móvil no se ve. Sin embargo los lotes y "Compartir con el cliente" dependen de ellos.
- **Permisos** está en 5 lugares: panel del agente → Perfil, Ajustes → Aprobaciones, Ajustes → Seguridad, Conexiones →
  "Permisos por empleado" y "Qué puede hacer cada empleado", Proyecto → Aprobaciones.
- **Contratar un agente** está en Ajustes ("Contratar por profesión"), no donde están los agentes (Organización).

### Impacto medio

**P6. Caminos duplicados.** El engranaje de arriba ("Administración") repite casi todo Ajustes (oficina, contratar,
plantillas, miembros, conexiones, seguridad, detener todo). Agentes se ven en 5 sitios (Oficina, Chats, Dashboard →
Agentes, Organización → Organigrama, Directorio y Puestos). Plantillas en 3 sitios (Proyectos, Biblioteca, Ajustes →
Plantillas de trabajo).

**P7. Palabras técnicas a la vista.** `email.send` en la tarjeta de aprobación; «draft_reply» en Pendientes; P50/P90,
nombre del modelo y "confianza baja" en el borrador; "máximo de nodos"; "Descargar SKILL.md"; "Abogado del diablo";
"Modo ensayo"; "Auditoría de pensamiento"; "Rituales"; "Kanban".

**P8. Íconos sin regla.** Proyectos usa una carpeta y Archivos un documento (al revés de lo que uno espera); en el panel
del agente "Archivos" sí usa carpeta. Organización y el chat "Toda la oficina" usan el mismo ícono de personas. Chats
dibuja sus propios íconos (`chats/chatsUi.tsx`, `GLYPH`) en vez de usar los de la app (`components/icons.tsx`).

**P9. Ayudas que compiten.** Bienvenida, franja de ayuda por pantalla, tour, "Primeros pasos" y meta del día hablan a la
vez y repiten lo mismo (las monedas se explican en tres sitios). "Primeros pasos" pide elegir la libertad "desde
sugerir hasta autónomo", con los nombres viejos; el panel ya usa "Me pregunta todo / Solo lo importante / Trabaja solo".

**P10. Estados vacíos desiguales.** Algunos tienen frase y botón (Proyectos, Pedidos en Chats); otros solo dicen "Aún
no tiene reportes" o "Bandeja vacía" sin decir qué hacer para que aparezca algo.

### Impacto bajo (pero se nota)

- "Inactivo" junto a "Disponible" en el mismo agente (captura 08): parece apagado cuando solo está libre.
- "Usar para un cliente" en plantillas internas como "Presupuesto anual" (no hay cliente).
- Las pestañas usan tres estilos distintos (con borde, sin borde, tipo interruptor) según la pantalla.
- El selector "Oficina / Dashboard" se esconde en pantallas pequeñas.

---

## 5. Propuesta

### 5.1 Glosario: 12 conceptos base

Regla de colores: **el color dice el estado; el ícono dice qué es.** Cada concepto tiene un ícono fijo y un tono suave
fijo que solo se usa en su ícono (no en estados), para no chocar con los colores de estado de 5.4.

| # | Concepto | Ícono | Tono del ícono | Frase (la que sale en "¿Qué es esto?") |
|---|---|---|---|---|
| 1 | **Agente** | cara de robot (el de la marca, en pequeño) | verde azulado (marca) | Un empleado de IA con un puesto (contador, abogada…). Hace lo que le pides y te pregunta lo delicado. |
| 2 | **Persona** | silueta | gris pizarra | Alguien de tu empresa con cuenta en la app. |
| 3 | **Equipo** | grupo de siluetas | gris pizarra | Personas y agentes que trabajan juntos (Legal, Ventas), con sus carpetas y su memoria. Abierto o privado. |
| 4 | **Pedido** | portapapeles | índigo | Todo lo que le pides a tus agentes, pequeño o grande. Siempre tiene un estado y un resultado. |
| 5 | **Tarea** | casilla con visto | índigo claro | Una parte de un pedido o de un proyecto, hecha por un agente. |
| 6 | **Proyecto** | bandera | violeta | Un pedido grande con plan: varias tareas, presupuesto y aprobaciones. Un **lote** es el mismo proyecto para varios clientes a la vez. |
| 7 | **Plantilla** | molde (rectángulos apilados) | violeta claro | Un proyecto guardado para repetirlo cuando quieras, para uno o para varios clientes. |
| 8 | **Habilidad** | capas (el actual) | ámbar suave | Cómo se hace algo en tu empresa. La enseñas una vez y los agentes la usan siempre. |
| 9 | **Pendiente** | campana | ámbar | Algo que espera por ti: una **pregunta** de un agente o una **aprobación**. |
| 10 | **Archivo** | carpeta (pasa a ser solo de Archivos) | azul | Documentos y hojas tuyas y de tus agentes. El **informe** es el resumen que deja cada pedido terminado. |
| 11 | **Cliente** | maletín | verde | Persona o empresa para la que trabajas. Tiene su carpeta, sus proyectos y su enlace de avance. |
| 12 | **Conexión** | enchufe (el actual) | gris azulado | Una cuenta que tus agentes pueden usar: correo, calendario, una API, **una base de datos**. Conectar no es dar permiso. |

Dos palabras de lugar, que no son conceptos:

- **Oficina** = solo la vista 3D donde ves a tus agentes trabajar. Cada equipo tiene ahí su sala.
- **Ajustes** = todo lo que se configura una vez.

**Palabras que se eliminan de lo que ve el usuario** (siguen en el código si hace falta):

| Se quita | Se dice |
|---|---|
| empleado, miembro de tu equipo (para IA) | agente |
| departamento; oficina (como grupo) | equipo |
| "Toda la oficina", "Archivos de la oficina" | "Todos los agentes", "Archivos" |
| "Ajustes de la oficina" | "Apariencia e idioma" |
| "Configura tu oficina", "Ajustes del equipo" | "Tu negocio" (tipo, tono, reglas) |
| solicitud, encargo, trabajo (como cosa pedida) | pedido |
| paso, nodo | tarea |
| plantilla de trabajo | plantilla (una sola biblioteca, en Proyectos) |
| plantilla de industria | "Empezar con un tipo de empresa" (crea equipos y agentes) |
| plantilla de puesto, "Contratar por profesión" | "Contratar agente" (se elige un puesto) |
| skill, SKILL.md | habilidad, "archivo de la habilidad" |
| reporte, entregable, artefacto | informe (o archivo, según el caso) |
| bandeja, "por aprobar", "Esperando a una persona" | Pendientes, "Te necesita" |
| autonomía, sugerir/autónomo | "Qué puede hacer sin preguntarte" con los 3 niveles nuevos |
| Inactivo (agente) | Libre |
| Distrito de clientes | Clientes |
| Kanban, Carriles por agente, Delegación, Línea de tiempo | Tablero, Por agente, Quién pasó qué, Calendario del plan (dentro de "Otras vistas") |
| P50 / P90, nombre del modelo, "confianza baja" | "Costo probable: $0,03 (hasta $0,06)"; lo técnico en "Ver cómo se calculó" |
| Abogado del diablo | Segunda opinión |
| Modo ensayo | Probar sin enviar |
| Auditoría de pensamiento | Cómo lo pensó |
| Rituales | Reuniones fijas |
| `email.send`, `draft_reply` y otros códigos | el nombre en palabras ("Enviar correo", "Borrador de respuesta") |

> **Decisión para el dueño (D1).** El diseño de equipos (`docs/architecture/equipos-oficinas.md` §3) eligió "oficina"
> como palabra para el grupo. Esta propuesta recomienda **"equipo"** porque "Oficina" ya es la pantalla de inicio y el
> resto de la app ya habla de "equipo" ("Convertir un equipo en oficina", "Elige un equipo"). Si prefieres "oficina",
> entonces la pantalla 3D debería llamarse distinto (por ejemplo "Inicio") y "equipo" desaparece. Lo que no funciona es
> tener las dos.

### 5.2 Navegación propuesta

**Antes**

```
Menú lateral                    Barra de arriba
 Oficina  (1 = aprobaciones)     Oficina | Dashboard   (selector, oculto en móvil)
 Chats                           Primeros pasos · Meta del día · ? · Monedas · En vivo
 Proyectos                       ⚙ Administración  (repite Ajustes)
 Organización
 Reuniones                      Fuera del menú
 Archivos                        Pendientes (/approvals/inbox, sin barra lateral)
 Habilidades                     Clientes (solo en Dashboard)
 Tienda                          Conexiones, Contratar, Plantillas de trabajo (en Ajustes)
 ─────
 Ajustes
```

**Después**

```
Menú lateral                                   Barra de arriba
 TU DÍA                                          Título · ¿Qué es esto?
  Oficina          (vista 3D | vista lista)      Primeros pasos (solo hasta completarlos)
  Pendientes   (3) ← el único número             Monedas (incluye la meta del día)
  Chats                                          Avatar → Mi cuenta · Detener todo · Salir
  Proyectos        (incluye plantillas y lotes)  "Sin conexión" solo cuando se corta
 TU EMPRESA
  Agentes y equipos  (contratar, puestos, organigrama, equipos, directorio)
  Clientes
  Archivos
  Reuniones
 ENSEÑAR Y CONECTAR
  Habilidades
  Conexiones       (incluye bases de datos)
 ─────
  Tienda
  Ajustes
```

Qué cambia y por qué:

1. **Pendientes pasa al menú**, dentro del marco de la app, con el único contador. El número deja de estar sobre
   "Oficina".
2. **Dashboard deja de ser otra app**: el selector se llama "Vista 3D / Vista lista" y la vista lista se queda con lo
   que es suyo (resumen, agentes, tareas, calidad, costos). Clientes, Pedidos, Aprobaciones, Archivos e Informes llevan
   a sus pantallas propias en vez de repetirlas.
3. **Organización → "Agentes y equipos"**, con 4 pestañas: **Agentes** (directorio + "Contratar agente" + puestos),
   **Equipos** (los de hoy en Oficinas, con su memoria), **Organigrama**, **Reglas de delegación** (avanzado). La
   pestaña Departamentos se une a Equipos (un departamento es un equipo sin espacio propio todavía).
   "Empezar con un tipo de empresa" queda como botón dentro de Equipos.
4. **Clientes sube al menú** (hoy solo en el Dashboard). Si la empresa no tiene clientes, la entrada muestra el estado
   vacío que explica para qué sirven y cómo importar una lista.
5. **Conexiones sube al menú** (hoy en Ajustes), como "Enseñar y conectar" junto a Habilidades: las dos son "lo que tus
   agentes saben y pueden usar". Incluye la nueva conexión a bases de datos como un tipo más de conexión, con la misma
   tarjeta, el mismo asistente y la misma pregunta de "qué agentes pueden usarla". Sus 6 pestañas pasan a 3:
   **Conexiones** (cuentas, buzones de datos, importar desde un enlace, bases de datos), **Quién puede usar qué** (une
   "Permisos por empleado" y "Qué puede hacer cada empleado"), **Uso y límites**.
6. **El engranaje de arriba desaparece**; se reemplaza por el avatar con "Mi cuenta", "Detener todo" (sigue a un clic)
   y "Salir". Todo lo demás está en Ajustes.
7. **Ajustes queda en 3 grupos y 11 filas**: *Tu empresa* (Tu negocio, Apariencia e idioma, Miembros e invitaciones,
   Plan y uso, Marca del portal del cliente) · *Seguridad* (Aprobaciones, Seguridad y límites, Inicio de sesión único,
   Detener todo) · *Tu cuenta* (Seguridad de la cuenta, Mis costos, Datos y privacidad). Salen de Ajustes: Contratar
   (a Agentes y equipos), Plantillas de trabajo (a Proyectos), Conexiones (al menú). El buscador de Ajustes se queda y
   sigue encontrando todo.
8. **Tienda** baja junto a Ajustes: es un extra, no parte del trabajo.

En el móvil el menú es el mismo, en el cajón lateral; Pendientes además en la barra inferior si se agrega una.

### 5.3 El panel del agente y el proyecto, más cortos

**Panel del agente: de 10 secciones a 5**, como pestañas visibles (no en un desplegable):

| Nueva | Qué junta |
|---|---|
| **Chat** | Chat (igual que hoy). Al abrir el panel, la barra de abajo de la Oficina se oculta: una sola barra para escribir. |
| **Trabajo** | Estado + Tareas + Informes + Actividad; abajo "Cómo lo pensó" (hoy Auditoría de pensamiento) y "Con otros agentes" (hoy Con el equipo), plegados |
| **Qué puede hacer** | Permisos de aprobación (los 3 niveles + excepciones), Carpetas que puede ver, Conexiones que puede usar, Pausar, Probar sin enviar |
| **Qué sabe** | Habilidades asignadas, Memoria |
| **Perfil** | Puesto, apariencia, confianza y calidad |

**Proyecto: de 10 pestañas a 4**, con "Seguimiento" siempre primero:

| Nueva | Qué junta |
|---|---|
| **Seguimiento** | Igual que hoy; arriba un aviso si hay Pendientes o Cambios de plan por revisar, con botón |
| **Plan** | Mapa por defecto y un selector "Otras vistas": Tablero (Kanban), Por agente (Carriles), Calendario del plan (Línea de tiempo), Quién pasó qué (Delegación), Salud |
| **Costos** | Igual que hoy |
| **Permisos** | Hoy "Aprobaciones": lo que se aprobó para todo el proyecto o el lote y las aprobaciones de este proyecto (que también están en Pendientes) |

"Cambios de plan" deja de ser pestaña: aparece como aviso en Seguimiento cuando hay alguno, y su historial dentro de
Plan. La línea de ayuda "Salud → Mapa → Kanban…" ya no hace falta.

### 5.4 Patrones únicos

**1. Un solo indicador de estado.** Un componente con 7 estados para todo lo que avanza (pedido, proyecto, tarea, fila de
lote, agente):

| Estado | Color | Ícono | Agrupa los estados internos |
|---|---|---|---|
| **Por empezar** | gris claro | círculo vacío | draft*, planning, ready, pending, queued, waiting |
| **En curso** | azul | círculo girando | running, thinking, working, reviewing, talking |
| **Te necesita** | ámbar | campana | needs_help, needs_input, needs_approval, awaiting_approval, waiting_human. El texto dice cuál: "Te necesita · una pregunta" o "· una aprobación" |
| **En pausa** | gris | dos barras | paused |
| **Listo** | verde | visto | done, completed. "Listo en parte" (partial) con medio visto |
| **Con problema** | rojo | triángulo | failed, blocked, error |
| **Cancelado** | gris tachado | raya | cancelled, skipped (este último "No se hizo") |

\* Borrador es un estado aparte solo de los proyectos (gris claro, "Borrador"), porque ahí se edita el plan.

Para los agentes: Libre (gris), En curso (azul), Te necesita (ámbar), Con problema (rojo), En pausa (gris).
El estado de las **personas** (Disponible, En reunión, Ausente…) se queda como está, pero con un punto, no con una
etiqueta, para no mezclarse con el del agente.

**2. Un solo panel lateral de detalle.** El del pedido (hecho hoy en Chats) es el modelo: se abre a la derecha, se
cierra con la X o Esc, en el móvil es pantalla completa con "Volver", y la dirección de la página lo refleja para que el
botón Atrás funcione. Se usa igual para: pedido, tarea de un proyecto (hoy `NodeDrawer`), pendiente, cliente y archivo.

**3. Una sola forma de Pendientes.** Una tarjeta de pendiente (pregunta o aprobación) que se ve igual en la bandeja de
la Oficina, en "Mientras no estabas", en el proyecto, en el chat y en Pendientes; siempre con: quién pide, qué, por qué
lo pide (la regla), y los botones Aprobar / Rechazar / "Aprobar y no volver a preguntar" o Responder. **Un solo número**
para toda la app, de una sola fuente. "Correos para aprobar" de Seguridad se vuelve un filtro de Pendientes.

**4. "¿Qué es esto?".** Un pequeño botón junto al título de cada pantalla y de cada concepto (Pedido, Lote, Habilidad,
Plantilla…) que abre la frase del glosario, un ejemplo y "Ver dónde se usa". Hoy ya existe `ConceptLegend` (agentes,
habilidades, plantillas): se amplía a los 12 conceptos y se reutiliza.

**5. Estados vacíos con la misma forma.** Siempre: ícono del concepto, una frase de qué es, una frase de cómo aparece algo
aquí y un botón para hacerlo. Ejemplo para Clientes: "Aquí verás las personas y empresas para las que trabajas. Agrega
una o importa tu lista (Excel o CSV) para lanzar un proyecto a todos a la vez. [Importar lista] [Agregar cliente]".

**6. Las mismas pestañas y botones en todas partes.** Un solo estilo de pestañas (`nav/ui2.tsx`), un solo juego de
íconos (`components/icons.tsx`, también en Chats) y la regla de íconos del glosario.

**7. Nada técnico a la vista.** Toda acción se muestra con su nombre en palabras; si no hay traducción, se muestra
"Acción del agente" y el código va en "Ver detalle". Lo técnico (modelo, P50/P90, intentos, códigos de error) vive en
un plegable "Ver cómo se calculó / Detalles técnicos".

### 5.5 Modo simple y modo completo

Sí tiene sentido, sin quitar nada: es solo qué se ve **abierto** al entrar.

- **Simple (por defecto para cuentas nuevas y para miembros):** menú con *Tu día* y *Tu empresa*; Clientes aparece en
  cuanto hay uno o se lanza un lote; Habilidades y Conexiones dentro de "Más". En el proyecto solo Seguimiento abierto y
  el resto a un clic. En el panel del agente, Chat y Trabajo. En la Oficina 3D solo la barra para pedir, la tarjeta de
  Pendientes y "Mientras no estabas" cuando corresponde; "Actividad en vivo" empieza plegada.
- **Completo (administradores o quien lo active en Apariencia e idioma):** todo abierto como en 5.2 y 5.3.
- El buscador de Ajustes y el "¿Qué es esto?" llevan siempre a todo, esté plegado o no.

### 5.6 El primer día

Hoy compiten bienvenida, franja de ayuda, tour, Primeros pasos y meta del día. Propuesta: **una sola guía**, los
"Primeros pasos", con 4 tareas que enseñan los conceptos en orden, y el resto aparece después:

1. **Cuéntale de tu negocio** (Tu negocio: tipo, tono e idioma). Enseña: tus agentes trabajan con tu contexto.
2. **Haz tu primer pedido** desde la barra o un atajo. Se abre el panel del pedido: enseña Pedido, Tarea e Informe.
3. **Decide tu primer pendiente** (el atajo "Propuesta de $50.000" siempre pide una aprobación). Enseña Pendientes y
   "Aprobar y no volver a preguntar".
4. **Elige qué puede hacer cada agente sin preguntarte** (los 3 niveles). Enseña permisos con los nombres nuevos.

Después, de a uno y solo cuando tocan: conectar una cuenta (cuando un pedido la necesite), crear un proyecto desde una
plantilla, agregar clientes, enseñar una habilidad. La bienvenida queda en una sola pantalla con un botón ("Empezar") y
el tour como enlace dentro de "?". Las monedas se explican una sola vez, en la Tienda.

---

## 6. Cambios priorizados

Riesgo: **bajo** = solo textos o colores; **medio** = mueve cosas de lugar o junta componentes, con pruebas que ajustar;
**alto** = cambia la estructura de pantallas grandes.

### Tanda 1: rápidos y de alto impacto (1 a 3 días)

| # | Cambio | Archivos | Riesgo |
|---|---|---|---|
| 1.1 | **Pendientes en el menú y dentro de la app**: nueva ruta dentro del marco (`/pendientes` o `/approvals/inbox` bajo `(app)`), entrada en el menú con el contador; el número deja de estar sobre Oficina. `/approvals/inbox` y `/approvals` siguen funcionando (redirigen) | `lib/navBridge.ts`, `components/nav/AppShell.tsx`, `app/(app)/…` nueva página, `app/approvals/inbox/page.tsx` | medio (pruebas de navegación y de la bandeja) |
| 1.2 | **Un solo contador de pendientes** (preguntas + aprobaciones) usado por menú, Oficina, Dashboard, chat y "Mientras no estabas" | `lib/store.ts` o un selector nuevo en `lib/inboxLogic.ts`; `AppShell.tsx`, `ApprovalsTray.tsx`, `dashboard/Dashboard.tsx`, `away/AwayHost.tsx` | medio |
| 1.3 | **Glosario aplicado a los textos** en es/en/pt: agente (no empleado), equipo (no departamento/oficina como grupo, si se aprueba D1), tarea (no paso/nodo), informe (no reporte), "Apariencia e idioma", "Tu negocio", "Contratar agente", "Segunda opinión", "Probar sin enviar", "Cómo lo pensó", "Libre", "Por empezar" (no "Lista"), etc. Solo textos, sin cambiar claves | `lib/i18n/es.json`, `en.json`, `pt.json` | bajo (revisar pruebas que buscan textos) |
| 1.4 | **Nada técnico a la vista**: nombre en palabras para cada acción (`email.send`, `draft_reply`…), costo "probable (hasta …)" sin P50/P90 ni modelo, "máximo de tareas" | `ApprovalsTray.tsx`, `approvals/ApprovalsInbox.tsx`, `projects/DraftEditor.tsx`, i18n | bajo |
| 1.5 | **Colores de estado iguales en todas partes** como primer paso: "Te necesita" siempre ámbar (hoy azul en Proyectos y en la tarjeta del lote), "Listo/Terminado/Hecha" un solo verde y un solo nombre | `projects/shared.tsx`, `projects/ProjectsView.tsx` (BatchCard), `lib/meta.ts`, `chats/chatsUi.tsx`, i18n | bajo |
| 1.6 | **Permisos al frente en el panel del agente**: sacar "Permisos" de Perfil y ponerlo como sección "Qué puede hacer" junto con "Carpetas que puede ver" (sin rehacer el panel todavía) | `components/AgentPanel.tsx` | bajo |
| 1.7 | **Una sola barra para escribir**: al abrir el panel del agente, ocultar la barra de la Oficina | `office/OfficeView.tsx` | bajo |
| 1.8 | **Primeros pasos con los nombres nuevos** de los niveles de permiso y el texto de la sugerencia de Pendientes apuntando a "Aprobar y no volver a preguntar" y a Ajustes → Aprobaciones | i18n (`act.autonomy.*`, `approvalInbox.tip`) | bajo |

### Tanda 2: medianos (1 a 2 semanas)

| # | Cambio | Archivos | Riesgo |
|---|---|---|---|
| 2.1 | **Componente único de estado** (7 estados, tabla de la sección 5.4) y reemplazo de las pastillas de cada pantalla | nuevo `components/ui/Status.tsx`; usar en `projects/shared.tsx` (StatusPill, StatePill), `chats/chatsUi.tsx`, `ui.tsx` (StateBadge, TaskBadge), `projects/BatchView.tsx`, `dashboard/Dashboard.tsx`, `lib/meta.ts` | medio (muchas pruebas miran colores o `data-status`; conservar los `data-*`) |
| 2.2 | **Menú agrupado** (Tu día / Tu empresa / Enseñar y conectar), Clientes y Conexiones al menú, Tienda abajo; engranaje reemplazado por el avatar con Mi cuenta / Detener todo / Salir | `lib/navBridge.ts`, `nav/AppShell.tsx`, `admin/AdminMenu.tsx`, nuevas páginas `app/(app)/clients`, `app/(app)/connections` que reutilizan `customers/CustomerDistrict.tsx` y `connections/ConnectionsView.tsx` **sin tocar su contenido** (coordinar con quien construye bases de datos) | medio |
| 2.3 | **Ajustes en 3 grupos y 11 filas**; salen Contratar, Plantillas de trabajo y Conexiones (con redirección desde `/settings?s=…` para no romper enlaces) | `settings/SettingsView.tsx`, i18n | medio |
| 2.4 | **Proyecto en 4 pestañas** (Seguimiento, Plan con "Otras vistas", Costos, Permisos) y "Cambios de plan" como aviso | `lib/projects/store.ts` (`PROJECT_TABS`), `projects/ProjectDetail.tsx` | medio (pruebas `pv-tab-*`) |
| 2.5 | **Panel del agente en 5 pestañas visibles** (Chat, Trabajo, Qué puede hacer, Qué sabe, Perfil) | `components/AgentPanel.tsx`, `SectionWheel.tsx` | medio |
| 2.6 | **Tarjeta única de pendiente** reutilizada en Oficina, Mientras no estabas, proyecto, chat y Pendientes; "Correos para aprobar" como filtro de Pendientes | `ApprovalsTray.tsx` (ApprovalCard), `approvals/QuestionCard.tsx`, `away/AwayHost.tsx`, `projects/ApprovalsBatch.tsx`, `security/SecurityCenter.tsx` | medio |
| 2.7 | **"¿Qué es esto?"**: ampliar `ConceptLegend` a los 12 conceptos y ponerlo junto a los títulos (`PageHeader`) | `concepts/ConceptLegend.tsx`, `nav/ui2.tsx`, i18n `concepts.*` | bajo |
| 2.8 | **Estados vacíos con la misma forma** en todas las listas | `nav/ui2.tsx` (EmptyState) y las vistas que usan `Empty` de `ui.tsx` | bajo |
| 2.9 | **Un solo juego de íconos** y la regla del glosario (carpeta solo para Archivos, bandera para Proyectos, campana para Pendientes, maletín para Clientes) | `components/icons.tsx`, `chats/chatsUi.tsx`, `lib/navBridge.ts` | bajo |

### Tanda 3: grandes (2 a 4 semanas)

| # | Cambio | Archivos | Riesgo |
|---|---|---|---|
| 3.1 | **"Agentes y equipos"** en 4 pestañas (Agentes con Contratar, Equipos uniendo Oficinas y Departamentos, Organigrama, Reglas de delegación) | `org/OrgPage.tsx`, `org/OfficesPanel.tsx`, `org/DepartmentEditor.tsx`, `org/AgentDirectory.tsx`, `admin/HireFromTemplateDialog.tsx`, `templates/IndustryTemplates.tsx` | alto (depende de D1; el modelo por dentro no cambia) |
| 3.2 | **Una sola biblioteca de plantillas** en Proyectos: unir "Plantillas de trabajo" (Ajustes) y "Plantillas de proyecto"; ambas lanzan un pedido o un proyecto | `templates/TemplateGallery.tsx`, `templates/TemplateRunDialog.tsx`, `projects/TemplateLibrary.tsx` | alto (dos fuentes de datos distintas en el backend: se muestran juntas, no se fusionan) |
| 3.3 | **Dashboard como "vista lista" de la Oficina**: se queda con Resumen, Agentes, Tareas, Calidad y Costos; lo demás enlaza a su pantalla | `dashboard/Dashboard.tsx`, `nav/AppShell.tsx` | medio-alto |
| 3.4 | **Panel lateral único** (el del pedido) para tarea de proyecto, pendiente, cliente y archivo | `chats/PedidoPanel.tsx` (extraer el marco), `projects/NodeDrawer.tsx`, `customers/CustomerView.tsx`, `files/FileViewerPanel.tsx` | alto |
| 3.5 | **Modo simple / completo** y **primer día** de la sección 5.5 y 5.6 | `lib/preferences.ts`, `nav/AppShell.tsx`, `nav/ActivationChecklist.tsx`, `onboarding/WelcomeOverlay.tsx`, `office/OfficeView.tsx`, `tour/` | medio |
| 3.6 | **Conexiones en 3 pestañas** (Conexiones, Quién puede usar qué, Uso y límites), con bases de datos como un tipo más | `connections/ConnectionsView.tsx`, `GrantMatrix.tsx`, `CapabilitiesTab.tsx` | medio; **hacerlo después** de que termine el trabajo de bases de datos para no pisarlo |

---

## 7. Lo que no cambia

- Ninguna función se quita: todas las vistas del proyecto, permisos, lotes, oficinas/equipos, reuniones, tienda,
  seguridad y conexiones siguen ahí, a lo sumo un clic más adentro.
- Ni el motor, ni la base de datos, ni los permisos por dentro. Los nombres internos (`department`, `office`,
  `needs_input`, `node`) se quedan en el código; solo cambia lo que se lee en pantalla.
- Los enlaces viejos siguen funcionando (redirecciones).
- El estilo visual (blanco, acentos azules, IBM Plex Sans, esquinas de ~10 px).

## 8. Decisiones para el dueño

1. **D1. ¿"Equipo" u "oficina" para el grupo de personas y agentes?** Recomendación: equipo (ver 5.1).
2. **D2. ¿Conexiones y Clientes en el menú principal?** Recomendación: sí los dos; Conexiones agrupado con Habilidades.
3. **D3. ¿Modo simple por defecto para cuentas nuevas?** Recomendación: sí, con un interruptor en Apariencia e idioma.
4. **D4. ¿Empezamos por la tanda 1?** Es casi toda de textos y colores, y arregla Pendientes, que es lo que más confunde.

## 9. Capturas de esta auditoría

En `docs/screenshots/ux-audit-2026-11-03/` (modo demo):

- `01-oficina-primer-ingreso.png`: bienvenida sobre la oficina; detrás ya hay una aprobación con `email.send`.
- `02-oficina-todo-a-la-vez.png`: siete cosas flotando sobre la vista 3D.
- `03-proyectos-lista.png`: "Usar para un cliente" también en plantillas internas.
- `04-proyecto-borrador.png`: P50/P90, nombre del modelo y "confianza baja".
- `05-pendientes-sin-menu.png`: Pendientes sin barra lateral; «draft_reply»; la sugerencia manda a "Seguridad → Avisos".
- `06-ajustes.png`: "Ajustes de la oficina" y "Configura tu oficina" juntos; "Plantillas de trabajo".
- `07-mientras-no-estabas-otra-bandeja.png`: otra bandeja de aprobaciones, alertas y problemas.
- `08-panel-del-agente.png`: "Inactivo" y "Disponible" a la vez; dos barras para escribir; secciones en un desplegable.
- `09-dashboard-otra-app-dentro.png`: 11 secciones; "0 aprobaciones" mientras la Oficina muestra 1.

## Decisiones del dueño (2026-11-03)

1. **"Oficina" es la palabra para el grupo** de personas y agentes (Legal, Ventas…), no "equipo" ni "departamento".
   Encaja con E2: cada oficina tiene su propio espacio 3D, así que la oficina es el grupo y su lugar en la vista 3D.
   "Equipo" y "departamento" dejan de aparecer en pantalla (el organigrama interno puede seguir usando departamentos en
   el código). El glosario y la navegación de esta propuesta se ajustan: "Agentes y equipos" pasa a "Agentes y oficinas".
2. **Clientes y Conexiones van al menú principal.**
3. **Modo simple por defecto para cuentas nuevas**, con un interruptor visible para ver todo; las cuentas existentes ven
   todo como hoy.
4. Se empieza por la primera tanda, con las decisiones 1-3 incluidas.
