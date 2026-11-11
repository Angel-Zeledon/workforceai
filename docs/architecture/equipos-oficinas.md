# Trabajo en equipo: oficinas por equipo, agentes por rol y permisos entre compañeros

Investigación y diseño del 2026-11-02. Pregunta del dueño: cómo mejorar el trabajo entre personas de la misma empresa;
si al crear la cuenta hay que preguntar si es solo para una persona o para una empresa; cómo colaboran compañeros con
oficinas distintas; cómo cada uno tiene solo los agentes de su trabajo (un abogado no necesita un agente de ventas); y
cómo se da acceso de lectura, de edición o ninguno a carpetas y archivos de un compañero.

Estado: **propuesta**. Las decisiones del dueño están en la sección 10. Los flujos de colaboración (encargos,
circuitos, ausencias, invitados), el motor de permisos paso a paso y los casos difíciles están en
`equipos-colaboracion.md`.

---

## 0. La idea en una frase

**Una empresa, varias oficinas, cada persona con su mesa.** La empresa es el edificio; cada equipo (Legal, Ventas,
Contabilidad…) es una **oficina** con sus personas, sus agentes, sus carpetas y su memoria; cada persona tiene además
**sus propios agentes y su carpeta privada**. Un agente nunca ve más de lo que puede ver la persona que le pide algo. Se
colabora entre oficinas **tocando la puerta**: pedir a otra oficina (a una persona o a su agente) con aprobación de quien
la lleva.

```
 Empresa (edificio, recepción común)
 ├─ Oficina Legal   (privada)   personas: Ana (responsable), Luis · agentes: Elena (legal), Sofía (asistente)
 │   └─ /Oficinas/Legal/…       memoria de Legal
 ├─ Oficina Ventas  (abierta)   personas: Marta, Pablo · agentes: Valeria (ventas), Nadia (análisis)
 │   └─ /Oficinas/Ventas/…
 └─ Cada persona: su mesa, /Personas/<persona>/ (privada) y "mis agentes" (privados hasta que los comparta)
```

---

## 1. Lo que hay hoy (inventario del 2026-11-02)

Todo está pensado para **una sola oficina por empresa**:

| Área | Hoy | Problema para equipos |
|---|---|---|
| Registro | Pide correo, contraseña, nombre y empresa. Crea la empresa con 7 agentes fijos para todos | No pregunta si es para una persona o un equipo, ni a qué se dedica |
| Bienvenida | Asistente de 3 pasos (tipo de negocio, idioma, resumen diario) + plantillas de industria (despacho de abogados, etc.) que crean departamentos y agentes | Solo a nivel empresa; quien entra invitado ve lo mismo que el dueño |
| Organización | Roles owner/admin/member/viewer; departamentos (árbol con jefe y centro de costo); personas en el organigrama | Los departamentos son solo para mostrar y para presupuesto: no dan ni quitan acceso. Las reglas de delegación existen pero no se aplican |
| Oficina 3D | Una por empresa (`office_layout` por organización); presencia de personas, cercanía abre un chat | No hay oficinas por equipo ni pisos |
| Agentes | Pertenecen a la empresa; todos los ven todos; tope de 20 por empresa en todos los planes | No hay dueño, ni agentes personales, ni compartir con alguien |
| **Chat con agentes** | Un hilo por agente **para toda la empresa**; el mensaje se guarda como `user` sin autor; leído/no leído por empresa | La abogada y el vendedor ven las conversaciones del otro con el mismo agente, y no se sabe quién escribió qué |
| Chat entre personas | Canales de empresa, departamento, proyecto, grupo y mensajes directos, con agentes que responden si se los menciona | Sin adjuntos |
| Archivos (personas) | Acceso por rol en toda la empresa | No hay carpetas privadas ni permisos por carpeta o archivo; cualquier miembro lee y sobrescribe todo |
| Archivos (agentes) | Zonas (su mesa, proyectos asignados, `/Shared`, `/Clients`) + permisos de lectura por carpeta (sin pantalla) | El alcance depende del agente, no de quién le pide. Los adjuntos del chat van a `/Shared` y los ve todo el mundo |
| Aprobaciones y avisos | Las deciden admin/owner; push a toda la empresa | No llegan a la persona que corresponde |
| Costos | Por agente, proyecto, departamento y empresa | No hay costo por persona; todos ven todo |
| Memoria | Empresa, agente y cliente | No hay memoria de equipo ni personal |
| Proyectos | Tienen creador; los agentes "están" por sus tareas | Las personas no son miembros de proyectos |

Ya existe y se aprovecha: departamentos, presencia, chat entre personas con canales de departamento, aislamiento por
cliente (el mismo patrón sirve para oficinas), registro de quién pidió cada ejecución y aprobación, SSO/SCIM con
autoingreso por dominio, invitaciones con límite de asientos.

---

## 2. Qué hace la competencia (resumen)

| Producto | Modelo | Lo que vale copiar | Lo que no |
|---|---|---|---|
| **Dust** | Un espacio de trabajo con *spaces* abiertos o restringidos y *pods* (salas de equipo con personas y agentes) | Los datos viven en un espacio y el espacio decide qué agentes y personas los usan; tareas asignables a persona o agente | — |
| **Microsoft 365 Copilot** | Agentes privados al crearlos; "puede chatear" o "puede editar"; publicar a la tienda de la empresa con aprobación del admin | Escalera de visibilidad; el agente respeta el acceso de cada usuario | Sobre-compartir: Copilot encuentra lo que la gente no sabía que tenía abierto (Microsoft vende herramientas para arreglarlo) |
| **Gemini Enterprise / Glean** | Galería/biblioteca de agentes; por defecto se crean pero no se comparten | Resultados recortados a los permisos de quien pregunta; en sitios públicos, solo contenido público; las acciones de escritura esperan aprobación | Gemini: lo subido al conocimiento del agente lo lee cualquiera que lo use |
| **ChatGPT / Claude (Team, Enterprise)** | Proyectos públicos o privados; "ver" o "editar" | Conocimiento del proyecto compartido, **chats privados salvo que se compartan** | — |
| **Notion (agentes)** | El agente usa sus propios permisos | Pregunta en el alta "personal / equipo" y luego tamaño y función | **Fuga documentada**: un usuario puede sacar información a la que no tiene acceso a través del agente |
| **Sintra / Lindy / Marblism** | Espacio con dueño y pocos roles | Créditos de la empresa con tope por persona (Lindy) | Un solo dueño sin roles (Sintra); asientos que se cobran solos al mencionar al agente (Lindy); alta por separado que crea espacios sueltos |
| **Gather / Kumospace / Sococo** | Pisos, áreas por equipo, salas con puerta | **Tocar la puerta** (Sococo: cualquiera dentro abre, caduca a los 5 min y queda constancia); estados automáticos por calendario | — |
| **Google Drive / Box / SharePoint** | Lector, comentarista, editor; herencia hacia abajo | Pedir acceso con nota → el dueño elige nivel y caducidad → aviso al que pidió | Romper la herencia por archivo vuelve el permiso inmanejable (SharePoint) |

Errores de los demás que hay que evitar: **agentes sin control** (el 94% de responsables de TI dice que la proliferación
de agentes aumenta el riesgo), **agentes con permisos propios** (Notion), **agentes huérfanos** cuando su dueño se va,
**cambios de política que no se aplican hacia atrás**, y **cobros por créditos que nadie entiende**.

Precio de referencia: la competencia cobra 12-30 USD por persona y mes; nuestro Pro cuesta 29 USD por 10 personas.
Hay margen para que el trabajo en equipo sea una razón para pasar a Pro y Max.

---

## 3. Modelo: empresa → oficina → persona

- **Empresa:** facturación, plan, administradores, directorio de agentes, políticas, recepción común.
- **Oficina = equipo.** **Se construye sobre los departamentos que ya existen**: un departamento con oficina activada
  gana miembros con acceso, agentes, carpeta, memoria, presupuesto (los límites por departamento ya existen) y su sala
  3D. Para la persona es un solo concepto, "oficina". El organigrama sigue igual.
  - **Abierta** (por defecto): cualquiera de la empresa puede entrar a mirar (lectura); los miembros trabajan.
  - **Privada:** solo sus miembros la ven por dentro; desde fuera se ve la puerta y se puede tocar.
  - Roles dentro de la oficina: **responsable** (aprueba lo de su oficina, gestiona miembros, agentes y permisos) y
    **miembro**. Los admin de la empresa pueden todo, y queda auditado.
- **Persona:** pertenece a una o varias oficinas; tiene **su mesa**, su carpeta privada `/Personas/<persona>/`, **sus
  agentes** y su bandeja (tareas, aprobaciones, puertas tocadas, pedidos de acceso).
- **Empresas que ya existen:** se crea una oficina "General", abierta, con todas las personas, los agentes y
  `/Shared`. Nada cambia hasta que creen una segunda oficina.

---

## 4. Agentes por rol, con dueño

- Cada agente tiene **dueño**: una persona (agente personal) o una oficina (agente del equipo). Nunca un grupo como
  único dueño. Si su dueño se va, pasa al responsable de la oficina (no queda huérfano).
- **Escalera de visibilidad** (como Microsoft, Glean y Dust):
  1. **Privado:** solo su dueño.
  2. **Oficina:** lo usan los miembros de la oficina.
  3. **Compartido con personas:** "puede chatear" (por defecto) o "puede editar".
  4. **Directorio de la empresa:** lo puede "contratar" o consultar cualquiera. En Max, publicar ahí puede requerir
     aprobación del admin.
- **Por rol:** al elegir profesión (§6) se sugieren los agentes que tienen sentido. Una abogada parte con Legal y
  Asistente; un vendedor con Ventas y Análisis. Los demás están en el directorio, no en su oficina.
- **Panel de agentes del admin:** dueño, oficina, último uso, agentes sin uso, duplicados, traspasos.
- El tope de 20 agentes por empresa hoy es igual en todos los planes; con agentes por persona y por oficina hay que
  revisarlo por plan (decisión §10.3).

---

## 5. Permisos: quién ve y edita qué

### 5.1 Tres niveles, nada más

**Ninguno · Leer (ver y comentar) · Editar.** Ser responsable o dueño queda implícito.

- Se dan sobre **oficina → carpeta → archivo** y **se heredan hacia abajo**: quien lee la oficina lee sus carpetas.
- La única excepción es la **carpeta restringida**, que corta la herencia (solo quien se nombra), es explícita y se ve
  con una insignia. No se permite romper la herencia archivo por archivo (el problema de SharePoint): para un archivo
  suelto se comparte el archivo.
- **Carpetas por defecto:**

  | Carpeta | Quién | Nivel |
  |---|---|---|
  | `/Personas/<persona>/` | su dueño | Editar; nadie más (ni sus agentes compartidos, salvo que ella lo decida) |
  | `/Oficinas/<oficina>/` | miembros | Editar |
  | `/Oficinas/<oficina>/` | resto de la empresa | Leer si la oficina es abierta; Ninguno si es privada |
  | `/Proyectos/<p>/` | miembros del proyecto (personas, ahora sí) | Editar |
  | `/Compartido/` | toda la empresa | Leer; Editar para member+ (como hoy) |
  | `/Clientes/<c>/` | quien atiende al cliente | igual que hoy, más el aislamiento por cliente |

- **Compartir** un archivo o carpeta con una persona: Leer o Editar, con **caducidad opcional** (30 días por defecto si se
  activa).
- **Pedir acceso** (como Drive): si alguien abre algo que no puede ver, aparece "Pedir acceso" con una nota. Al dueño o
  responsable le llega a su bandeja; responde Leer, Editar o No, con caducidad opcional, y se avisa a quien lo pidió. Un
  pedido sin respuesta caduca y queda constancia.
- **Adjuntos del chat:** dejan de ir a `/Shared`. Van a la carpeta de la oficina de la conversación o a la carpeta
  personal en un chat privado.

### 5.2 Los agentes respetan a la persona

**Acceso efectivo del agente = lo que el agente tiene permitido ∩ lo que puede la persona que le pide.**

- El agente de una abogada no lee la carpeta privada de otra abogada aunque esté en su oficina.
- Si un compañero usa mi agente compartido, el agente trabaja con **los permisos del compañero**, no con los míos.
- Las tareas programadas o autónomas actúan en nombre de un **patrocinador** (el dueño del agente) con su techo.
- En sitios compartidos (un canal de toda la empresa) el agente usa solo lo que pueden leer todos los presentes.
- Se comprueba **en cada llamada a herramienta**, no solo al empezar la tarea, para que una instrucción escondida en un
  documento no pueda saltárselo.
- Un agente puede tener **menos** que la persona (por ejemplo, solo lectura aunque ella pueda editar), nunca más.
- Si no puede ver algo, lo dice y ofrece "Pedir acceso" o "Preguntar a la oficina de Legal" (§7).

### 5.3 Lo que sale de un archivo restringido

Si un agente produce algo a partir de contenido restringido (un resumen de un contrato de Legal):

- el resultado **hereda la restricción más fuerte** de sus fuentes y lleva la marca "derivado de: Legal / Contratos";
- guardarlo o enviarlo a un sitio más visible necesita aprobación del dueño de la fuente;
- las citas a contenido que el lector no puede ver aparecen como "fuente restringida", sin el texto.

### 5.4 Auditoría

Cada lectura de contenido restringido y cada escritura queda registrada con **qué agente, para qué persona y qué
archivo**, incluidas las lecturas de texto y vistas previas (hoy solo se audita la descarga). El admin tiene una vista de
"quién o qué agente tocó qué", un informe de lo compartido con mucha gente y un informe de accesos que caducan.

---

## 6. Alta y bienvenida

1. **"¿Para quién es?"** con tres respuestas:
   - **Solo para mí** → una oficina personal, sin pasos de equipo. Puede crecer después a equipo.
   - **Para mi equipo** → una oficina con nombre ("Despacho", "Ventas"), invitar compañeros ahora o después.
   - **Para mi empresa (varios equipos)** → elegir oficinas de una lista (las plantillas de industria ya crean
     departamentos) o crearlas, y quién es responsable de cada una.
2. **"¿A qué te dedicas?"** → profesión (abogacía, ventas, contabilidad, marketing, operaciones, RR. HH., dirección,
   otra). Define los agentes sugeridos para su mesa y su oficina. **Los 7 agentes fijos para todos dejan de existir**:
   cada oficina arranca con los de su trabajo.
3. **Invitaciones con oficina y profesión:** "Invitar a Luis a Legal como miembro, abogado". Luis entra a su oficina con
   sus agentes sugeridos y una bienvenida de su equipo, no la del dueño. Con un dominio verificado, quien se registra con
   el correo de la empresa **entra a la empresa existente** (pidiendo entrar a una oficina) en vez de crear otra empresa
   suelta (el error de Lindy).
4. El recorrido guiado cambia según el caso: solo, miembro o responsable.

---

## 7. Colaborar entre oficinas

- **Tocar la puerta** (como Sococo): en la oficina 3D o desde el directorio, para pedir algo a otra oficina. Abre
  cualquier miembro presente; si no hay nadie, queda en la bandeja del responsable; caduca y deja constancia.
- **Consulta a otra oficina** (por ejemplo, Ventas pregunta a Legal "¿este contrato tiene penalización?"):
  - el pedido llega a la **cola de Legal** y el agente de Legal prepara la respuesta **con los permisos de Legal**;
  - si la respuesta usa contenido que quien pregunta no puede ver, **una persona de Legal la aprueba** (o la edita)
    antes de enviarla; si solo usa contenido visible para él, sale directa;
  - todo queda en el hilo, con las fuentes marcadas.
- **Encargos entre personas y agentes:** una tarea con su contexto (archivos, conversación), asignable a una persona o a
  un agente de otra oficina, con plazo y estado, visible en la bandeja de quien la recibe. Las delegaciones entre agentes
  que ya existen (consultar, traspasar, reasignar) se respetan, pero cruzar a otra oficina pasa a ser un encargo.
- **Salas compartidas:** un proyecto o canal con personas y agentes de varias oficinas (el canal de proyecto que ya
  existe gana carpeta y miembros). Cada uno ve lo que su permiso le deja.
- **Conversaciones:** cada persona tiene **su propio hilo** con cada agente (privado por defecto) y puede **compartirlo**
  con alguien o con la oficina. La sala de la oficina tiene su hilo común.

---

## 8. Oficina 3D

- **Un edificio:** una recepción común y una sala por oficina (pisos o alas según el tamaño). Cada oficina tiene su
  propio diseño en el editor (hoy hay uno por empresa) y sus propios estilos (`office-editor.md`).
- Las oficinas privadas se ven con la puerta cerrada; las abiertas se pueden visitar mirando (sin tocar).
- Cada persona tiene su mesa con sus agentes personales al lado.
- Estados de presencia (disponible, concentrado, en reunión, fuera), que ya existen, más los automáticos por calendario.

---

## 9. Plan y costos

- **Costo por persona:** el registro de costos gana la persona que pidió; informes por persona, oficina y agente; topes
  por oficina (ya existen por departamento) y, en Max, por persona.
- **Quién ve los costos:** cada persona los suyos; el responsable los de su oficina; el admin todos.
- **Por plan** (propuesta):
  - **Gratis:** una oficina (uso personal o equipo pequeño), los tres niveles de permiso.
  - **Pro:** varias oficinas, agentes personales, compartir y pedir acceso, tocar la puerta, encargos entre oficinas.
  - **Max:** oficinas privadas ocultas, caducidad de accesos, aprobación para publicar agentes al directorio, reglas de
    aprobación por agente y acción, topes por persona, exportar la auditoría.

---

## 10. Decisiones del dueño

1. **Oficina = departamento** (un solo concepto, reutilizando lo que existe). Recomendado.
2. **Oficina abierta por defecto** (las empresas pequeñas colaboran más así; privada a un clic). Recomendado.
3. **Topes de agentes por plan** (hoy 20 por empresa en todos los planes). Propuesta: contar por empresa y subir el tope
   en Pro y Max. Las cifras las decide el dueño.
4. **Consultas entre oficinas aprobadas por una persona** cuando la respuesta usa contenido que quien pregunta no puede
   ver. Recomendado.
5. **Chats con agentes privados por persona por defecto** (se pueden compartir). Recomendado: es lo que hacen ChatGPT,
   Claude y Copilot, y hoy todo el mundo ve las conversaciones de todos.

---

## 11. Fases

Tamaños: S (días), M (1-2 semanas), L (varias semanas).

| Fase | Qué | Tamaño | Qué se nota |
|---|---|---|---|
| **E0. Saber quién es quién** (**implementada** 2026-11-02, ver `chat-history.md` sección E0) | Autor en cada mensaje; hilo propio por persona y agente; leído/no leído por persona; push y avisos a la persona; "mis aprobaciones"; persona en el registro de costos | M | Cada uno ve sus conversaciones; se sabe quién pidió qué. Es la deuda más grande y lo que todo lo demás necesita |
| **E1. Alta y agentes por rol** (**implementada** 2026-11-03, ver abajo y `08-api.md` §41) | "¿Para quién es?", profesión, agentes sugeridos por rol, invitaciones con oficina y profesión, entrar por dominio a la empresa existente | S-M | La abogada no ve un agente de ventas; los invitados llegan a su equipo |
| **E2. Oficinas** (**implementada** 2026-11-03, ver abajo y `08-api.md` §42) | Departamento → oficina (miembros, responsable, abierta/privada), dueño y visibilidad de agentes, directorio, oficina "General" para las empresas existentes, diseño 3D por oficina, memoria de oficina y personal | L | Cada equipo tiene su espacio, sus agentes y su memoria |
| **E3. Permisos de archivos** (**parcial**, D1 hecha 2026-11-09, D3 hecha 2026-11-10, ver abajo) | Tres niveles con herencia, carpetas personal y de oficina, carpeta restringida, compartir con caducidad, pedir acceso, acceso efectivo = agente ∩ persona en cada herramienta, marca de derivado, adjuntos fuera de `/Shared`, pantalla de permisos, auditoría de lecturas | L | Leer, editar o nada por carpeta y archivo, y los agentes lo respetan |
| **E4. Colaborar entre oficinas** | Tocar la puerta, consulta a otra oficina con aprobación, encargos a personas o agentes, bandeja personal, compartir conversaciones, salas de proyecto con miembros | M | Ventas le pide a Legal sin ver lo que no debe |
| **E5. Gobierno** | Aprobaciones al responsable de la oficina (por fin se usa `approver_id`), reglas por agente y acción, topes y costos por persona, panel de agentes, controles de Max | M | Cada responsable gobierna su oficina; el admin ve el conjunto |

**E3 parcial: D1, carpetas de departamento (2026-11-09).** Hecho: raíz `/Offices/<departamento>` con plantilla por profesión; personas (miembro edita, oficina abierta lee, privada no se ve ni siendo admin); agentes (editan en su oficina, nada en otras, nunca más que la persona, `person_no_access`); `POST /teams` crea el departamento como oficina privada con sus carpetas; adjuntos del chat por defecto en `/Offices/<depto del agente con quien se chatea>/Adjuntos/<día>` (sin departamento: `Compartido > Chat`; la persona puede elegir otra carpeta); la exportación completa de la organización incluye todos los departamentos. Falta: pedir acceso, compartir con caducidad, carpeta restringida, carpetas personales, marca de derivado, pantalla de permisos y auditoría de lecturas. Hoy los tres niveles con herencia y el acceso efectivo agente ∩ persona solo se aplican dentro de `/Offices`, y "adjuntos fuera de `/Shared`" solo ocurre cuando el agente tiene departamento.

**D2 hecha (2026-11-09): el Drive del departamento.** Hecho: en cuentas de empresa el menú corto (Departamento, Pendientes, Agentes y oficinas, Ajustes, resto en "Más"); `/d` abre el primer departamento de la persona (sin departamento, aviso, y "Crear departamentos" para admins; `/d/<id>` ajeno, "no disponible"); árbol de carpetas, lista Nombre · Último cambio · Estado, filtros Todo / Para revisar / Cambiado hoy, panel del archivo con aprobar/rechazar, vista previa y versiones, menú "⋯" y "Pedir algo sobre esta carpeta…" con selector de agente (solo los que la persona puede usar); carpeta y archivo en la URL. "Para revisar" = el agente pide aprobación sobre ese archivo, o un agente escribió su última versión y nadie lo marcó como revisado (botón "Marcar como revisado" en el panel: etiqueta `revisado`, que el servidor quita cuando un agente escribe una versión nueva); "Propuesto" = archivo nuevo que se crea al aprobar. Las cuentas de empresa con departamento entran en `/d` (una vez por pestaña; volver a "/" desde "Más" lleva a la oficina). Backend: `POST /messages` acepta `folder` (solo en `agent:<id>`); las aprobaciones de fs llevan `context.path` y `context.action` (también en `GET /approvals`, la bandeja y tras un reinicio). Falta (D4): estados "Borrador"/"Enviado" y "Compartido conmigo". Guía de usuario: `landing/guide-src/departments.json`.

**D3 hecha (2026-11-10): sellar el resto del departamento.** Plan: `docs/superpowers/plans/2026-11-09-d3-sellar-departamentos.md`. Regla central: `offices.Model.CanSeeWork`. El trabajo de un departamento privado solo lo ven sus miembros, **también si quien mira es admin** (el admin puede entrar como miembro, y queda auditado; la exportación completa de la organización de D1 sigue incluyéndolo). Lo oculto se comporta como si no existiera: `404` o no aparece en las listas. Una persona con acceso por compartido o directorio chatea con el agente en su propio hilo y ve sus propios pedidos, no el resto del trabajo del departamento.

- **Pedidos y tareas:** las rutas de trabajo, los comandos de `/commands/run` y el resumen de ausencia están sellados. Un pedido es visible si la persona ve el trabajo de todos sus agentes **o es quien lo pidió** (quien pide siempre ve su pedido); un pedido sin tareas lo ven solo quien lo pidió y los admin. Los proyectos se muestran con los nodos sellados tapados; si todos sus nodos de agente son privados, `404`. Las vistas previas de hilos y los conteos están sellados. "Guardar como plantilla" devuelve `403` si el proyecto mezcla departamentos (una plantilla copia nodos enteros; tapar rompería dependencias). Lotes (`/project-batches`): `404` si todo está oculto, `403` si está mezclado (no se aplica a medias).
- **Eventos en vivo:** solo llegan a los miembros del departamento y a quien hizo el pedido. Los eventos de conexiones y habilidades de un departamento privado llevan `office_id` y siguen la misma regla. Los eventos de un departamento privado conservan su línea en `/activity` (filtrada por `CanSeeWork`) pero no entran al registro común de eventos. Paquete: `backend/internal/officeaudience/`.
- **Chats:** los hilos compartidos (`agent:<general>`, oficina), `/export`, `/compact` y el historial que recibe el modelo no incluyen los mensajes de agentes privados que la persona no ve. Los mensajes propios de una persona con un agente que ya no puede usar quedan visibles, solo lectura.
- **Memoria (migración 818):** `memory_docs` suma `scope='office'` y `office_id`. Lo que aprenden los agentes de un departamento se guarda en ese departamento y solo lo recuerdan sus agentes; General sigue escribiendo `org`. Un informe cuyos agentes son todos de un departamento se guarda en él; un informe mixto que incluya un departamento privado **no se indexa** (no se podrá recordar), y si ya estaba guardado como memoria de empresa se suprime. Las escrituras fallan cerradas si no se sabe el departamento. Al arrancar, toda la memoria de empresa (`scope='org'`) escrita por agentes de un departamento **privado** pasa a ese departamento, sin tope (`SealOfficeMemory`); el re-índice de las tareas e informes más nuevos sigue acotado, y los informes más viejos que el tope solo se corrigen (suprimir o mover), sin re-indexarse. Un documento de departamento solo se ve leyendo ese departamento, nunca por ser su autor. Cuando un agente de un departamento privado trabaja para una persona que no es miembro (un agente compartido o del directorio), no recuerda ni encuentra la memoria del departamento, aunque esa persona sea admin; el trabajo propio del agente (sin persona) sí.
- **Habilidades (migración 819):** `skills.office_id` (`""` = toda la empresa). Los archivos de una habilidad de departamento viven en `/Offices/<departamento>/Skills/<nombre>/vN` (las carpetas de D1 las sellan), no en `/Shared/Skills`. `/learn` crea la habilidad en el departamento del agente de la tarea (si es de General, de toda la empresa). Asignar a un agente una habilidad de otro departamento devuelve `400`. Un admin que no es miembro de un departamento privado no puede crear habilidades en él (`403`). Importar crea siempre habilidades de toda la empresa. El departamento de una habilidad no cambia (`400 office_immutable`).
- **Conexiones (migración 820):** `connections.office_id`. Solo los agentes de ese departamento pueden usarla (`400 agent_other_office` al conceder; el Tool Gateway rechaza un permiso viejo de un agente de otro departamento). El responsable la gestiona sin `connections:manage`, pero cambiar a dónde va la credencial (`base_url`, `host`, `port`, `user`) sin `connections:manage` obliga a reenviar el secreto en la misma llamada (`400 credential_required`); quitar `read_only` requiere `connections:manage`. Conceder escritura, aprobar permisos pendientes y reanudar tras el freno siguen siendo de admin; el responsable puede **co-firmar la segunda aprobación** de un permiso de escritura de su departamento (la primera sigue necesitando `connections:grant:write`). Todos sus eventos en vivo (alta, cambios de estado, prueba, revocación y rotación) llevan `office_id`: los de una conexión de un departamento privado solo llegan a sus miembros. Los buzones de datos y las importaciones por enlace siguen siendo de toda la empresa.
- **Aprobaciones:** el responsable del departamento decide sus aprobaciones aunque no tenga `approvals:decide` (solo en su departamento); si no hay responsable que alcance el mínimo, deciden los admin que ven ese trabajo. Quien hizo el pedido **ve** (no decide) su aprobación. Las aprobaciones de un departamento privado no van a correos, Slack ni Teams de la empresa, solo a los responsables (correo y push). La app de Slack de dos vías tampoco las publica en el canal de la empresa (si no se puede saber la oficina, no publica nada); `/oficina pendientes` lista solo las aprobaciones cuyo trabajo ve la persona, un botón sobre una aprobación oculta responde como si no existiera y el responsable decide desde Slack aunque no tenga `approvals:decide`. "Permitir siempre a cualquier agente" requiere `approvals:decide`; el responsable solo puede permitir siempre para ese mismo agente. Editar un correo del outbox entre los dos votos se rechaza con `409 partially_approved`. Cancelar un proyecto retira sus aprobaciones pendientes. Los lotes aplican la misma regla de `404`/`403`. Cada aprobación trae `can_decide` para esa persona.
- **Arranque:** todo el cableado de departamentos (`wireDepartments` en `cmd/server/offices.go`: alcance de memoria, aprobadores, Slack, conexiones, carpetas, habilidades y audiencia de eventos) se instala antes de que `orch.Recover` reanude los pedidos que quedaron a medias; no hay ventana sin sellar (`TestDepartmentsAreWiredBeforeRecover`).

**Falta tras D3:** un departamento privado sin responsable y sin admin miembro puede dejar aprobaciones sin decidir (exigir un responsable es decisión de producto); permisos de archivos pendientes de E3 (pedir acceso, compartir con caducidad, carpeta restringida, carpetas personales, marca de derivado, pantalla de permisos, auditoría de lecturas); estados "Borrador"/"Enviado" y "Compartido conmigo" (D4); buzones de datos, enlaces importados y calendarios de reuniones siguen de toda la empresa; una habilidad o un documento de memoria no sigue al agente que cambia de departamento; los informes mixtos con un departamento privado no se recuerdan; las rutas de aceptar o rechazar propuestas de confianza no revisan el departamento del agente; las listas de `/conversations` y `/activity` filtran después de paginar (una página puede salir vacía con `X-Has-More`). Guía de usuario: `landing/guide-src/departments.json`, sección "Lo que queda dentro del departamento".

**E1 implementada (2026-11-03).** Plan: `docs/superpowers/plans/2026-11-03-e1-alta-agentes-por-rol.md`.

- **Registro:** "¿Para quién es?" (solo para mí / mi equipo / mi empresa) y "¿A qué te dedicas?" (8 profesiones). Una
  oficina personal no pide nombre de empresa; "mi equipo" pide empresa y nombre del equipo (por defecto, el de la
  profesión).
- **Agentes por rol:** las organizaciones creadas con el registro nuevo arrancan con el asistente y los agentes de la
  profesión de cada persona (abogacía → Legal; ventas → Ventas y Análisis; contabilidad → Contabilidad y Análisis;
  marketing → Ventas y Análisis; operaciones → Operaciones y Project manager; RR. HH. → Recursos Humanos; dirección →
  Análisis y Project manager; otra → solo el asistente). Las organizaciones que ya existían, el demo, las cuentas de
  prueba y las altas por API sin `audience` siguen con los 7 de siempre.
- **Equipo = departamento.** "Mi equipo" crea el departamento con su responsable al entrar. "Mi empresa" abre el alta de
  equipos: equipos de una lista (uno por profesión), propios o de una plantilla de industria, y responsable de cada uno
  (yo, invitar a alguien o después). Cada equipo trae los agentes de su trabajo.
- **Invitaciones con equipo, profesión y "responsable"**, con el límite de asientos de siempre. La persona invitada entra
  a su equipo (puesto en el organigrama, jefa del equipo si es responsable), con sus agentes y la bienvenida de su
  equipo (quién la invitó, responsable, compañeros, sus agentes).
- **Entrar por dominio:** si la empresa verificó su dominio y activó el autoingreso, el registro lo ofrece; la cuenta se
  crea sin organización y, al confirmar el correo, entra a esa empresa y elige su equipo y su trabajo en la bienvenida.
  Si la empresa ya no lo permite, se crea su propia oficina.
- **Bienvenida distinta por caso:** solo, responsable de la empresa (lleva al alta de equipos), responsable de equipo,
  miembro y miembro sin equipo (elige equipo). El alta de equipos también queda en el menú de administración hasta
  terminarla.
- **Pendiente para E2:** la visibilidad de agentes por persona u oficina (en E1 todos ven todos los agentes de la
  empresa, pero la empresa solo tiene los de los trabajos de su gente), la aprobación del responsable para entrar a un
  equipo (en E1 se entra directo y queda auditado) y los poderes del responsable.

**E2 implementada (2026-11-03).** Plan: `docs/superpowers/plans/2026-11-03-e2-oficinas.md`.

- **Oficinas:** un admin convierte un equipo (departamento) en oficina, abierta por defecto o privada. Entran las personas
  de ese equipo y su jefe queda como responsable; el responsable añade y quita miembros, nombra otros responsables y la
  abre o cierra. Una oficina sin responsable la gestionan los admin. Una oficina que todavía es dueña de agentes no se
  puede cerrar. Pantalla: Organización → Oficinas.
- **"General" para todas las empresas:** oficina virtual, abierta, con todas las personas; los admin la llevan. Las
  empresas que ya existían no notan nada hasta que crean una segunda oficina o cambian la visibilidad de un agente.
- **Agentes con dueño y visibilidad:** cada agente es de una persona o de una oficina, con la escalera privado / su
  oficina / compartido con personas (puede chatear o puede editar) / directorio de la empresa. La lista de agentes, sus
  fichas y el chat directo respetan quién puede verlo y usarlo; en una oficina abierta los de fuera ven sus agentes pero
  no los usan. Si el dueño se va, el agente pasa al responsable de su oficina.
- **Directorio de agentes:** Organización → Directorio de agentes: mis agentes, los de mis oficinas, compartidos
  conmigo, los de toda la empresa y los de otras oficinas abiertas (solo mirar), con buscador y "Quién lo usa".
- **Memoria de oficina y personal:** notas de la oficina (sus miembros) y "Mi memoria" (solo la persona); los agentes
  las tienen en cuenta en sus tareas. El responsable puede **pasar una nota a toda la empresa** (queda registrado).
- **Diseño 3D por oficina:** en la oficina 3D aparece un selector de oficina cuando hay más de una; cada oficina tiene su
  propio diseño, que arregla su responsable. Las privadas ajenas salen con candado.
- **Pendiente:** el chat de toda la oficina, los proyectos, los eventos en vivo y las menciones en canales todavía no
  filtran por visibilidad; en la oficina 3D de una oficina se ven todos los personajes (cambia el diseño, no quién
  está); la aprobación del responsable para entrar a un equipo; contratar un agente propio desde el directorio; los
  permisos de archivos por carpeta para personas (E3).

Orden recomendado: **E0 → E1 → E2 → E3 → E4 → E5**. E0 y E1 se notan enseguida y no rompen nada. E3 va de la mano de la
fase 4 de `archivos-agentes.md` (fuentes): las dos tocan permisos de carpetas y se deben diseñar juntas.

## Fuentes

Dust (access controls, agents management, pods, pricing), Microsoft (compartir agentes en Copilot, buenas prácticas de
SharePoint para Copilot, Restricted Content Discovery, plan contra sobre-compartir), Google (compartir agentes en Gemini
Enterprise, roles de Drive, pedidos de acceso, accesos que caducan), Glean (permisos de agentes, biblioteca), Notion
(permisos de agentes personalizados, teamspaces), OpenAI (Business FAQ, proyectos, workspace agents), Claude
(visibilidad de proyectos), Lindy, Relevance, Sintra, Marblism, ClickUp, CrewAI, Devin, Gather, Kumospace, Sococo
(tocar la puerta), Box y Dropbox (roles de carpetas), encuesta de OutSystems 2026 sobre proliferación de agentes.
Detalle y enlaces en el informe de investigación del 2026-11-02; algunos precios de terceros sin verificar.
