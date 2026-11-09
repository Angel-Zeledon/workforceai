# Versión empresa: departamentos sellados con cara de Drive

Fecha: 2026-11-09. Estado: aprobado por el dueño para construir ("hazle y ya a todo"); las decisiones de detalle
son del equipo de desarrollo y están marcadas como tales.

## 1. La idea

El producto tiene dos caras sobre el mismo motor:

1. **Oficina personal** (lo que existe hoy, con la oficina 3D): una persona sola con su equipo de agentes. **No cambia.**
2. **Empresa**: cada departamento (Contabilidad, Legal, RH, Educación…) queda con uno o pocos profesionales humanos que
   supervisan, y los agentes hacen el trabajo de oficina del resto del equipo. En una empresa grande hay varios
   departamentos **sellados** entre sí.

La persona que trabaja en un departamento no quiere tableros ni números de ahorro (decisión del dueño): quiere su
trabajo, que vive en documentos. Por eso **la pantalla de inicio de un departamento es su Drive**: árbol de carpetas,
lista de archivos y panel del archivo, como Google Drive / OneDrive / SharePoint, que es lo que ya usan. Los agentes son
compañeros que trabajan dentro de esas carpetas.

## 2. Metas, no-metas y anti-metas

**Metas**
- Al registrarse como empresa se crean departamentos desde una plantilla de profesión (árbol de carpetas inicial y
  agentes incluidos) y cada uno tiene responsable.
- Un departamento es sellado: archivos, conocimiento, habilidades, conexiones, chats y aprobaciones no se ven ni se usan
  desde otro departamento.
- El trabajo diario sucede en el Drive del departamento: pedir algo en una carpeta, ver qué cambió y quién (persona o
  agente), revisar lo que espera decisión en el propio archivo.
- Lo que cruza de un departamento a otro entra por "Compartido conmigo", con aprobación del responsable dueño.
- Las aprobaciones de un departamento llegan a su responsable.

**No-metas (por ahora)**
- Control de cambios real dentro de .docx/.xlsx (los agentes entregan versiones nuevas y comentarios en el panel).
- Integraciones con SharePoint/Google Drive de la empresa (v1: solo importaciones genéricas; Drive/Dropbox/OneDrive
  quedan como última fase, ver memoria del dueño).
- Rediseñar la oficina personal.

**Anti-metas** (sería un fracaso aunque funcione)
- Mostrar "cuánto ahorras" o "equivale a N personas" en las pantallas de los empleados.
- Que un agente de Contabilidad pueda leer un archivo de Legal por cualquier camino (herramienta, búsqueda, memoria,
  chat, adjunto).
- Una interfaz nueva que haya que aprender: se copia el patrón estándar de Drive.

## 3. Lo que ya existe (inventario 2026-11-09)

- Registro con "¿Para quién es?" (solo / equipo / empresa) y profesión; alta de equipos con responsable
  (`SignupTeamFields.tsx`, `TeamSetupWizard.tsx`, `org_setup.audience`). Fase E1 de `docs/architecture/equipos-oficinas.md`.
- Oficinas = departamentos con miembros, responsable (`lead`), abierta/privada, agentes con dueño y visibilidad, notas
  de oficina (E2, `backend/internal/offices/`, migración 815).
- Drive de la organización con raíces `/Agents`, `/Projects`, `/Clients`, `/Shared` y permisos **por agente**
  (none/read/edit, migración 811). No hay carpetas de oficina ni permisos por persona.
- Memoria, habilidades, conexiones, chats y aprobaciones son de toda la organización. Las reglas de delegación guardan
  `approver_id` pero no se aplica.

## 4. Diseño

### 4.1 Departamento = oficina privada con carpeta propia

- En una cuenta de empresa, cada departamento es una oficina **privada por defecto** (decisión del equipo: en la cara
  empresa el aislamiento es lo que se vende; en "mi equipo" sigue abierta por defecto).
- Cada oficina tiene su raíz `/Oficinas/<oficina>/`. Permisos de `equipos-oficinas.md` §5.1: miembros Editar; resto de
  la empresa Ninguno si es privada, Leer si es abierta. Los admin de la empresa **administran** (crean departamentos,
  nombran responsables, ven nombres y tamaños) pero no leen contenido de un departamento privado salvo que se agreguen
  como miembros, y eso queda auditado (decisión del equipo).
- Acceso efectivo de un agente = permiso del agente ∩ permiso de la persona por la que trabaja, comprobado en cada
  llamada a `fs` (`equipos-oficinas.md` §5.2). Un agente de oficina sin persona (tarea programada) trabaja con el
  permiso de su oficina.

### 4.2 Sellado de los demás recursos

| Recurso | Regla |
|---|---|
| Conocimiento / memoria / búsqueda | alcance `office`: lo escrito por agentes de una oficina queda en esa oficina; la búsqueda solo devuelve lo de las oficinas de la persona y del agente (además de lo personal). Lo de alcance `org` sigue para "General" y para notas que el responsable publica a toda la empresa. |
| Habilidades | una habilidad puede ser de la empresa (todas) o de una oficina; solo se pueden asignar a agentes de esa oficina. |
| Conexiones | `connection_grants` por oficina además de por agente; el responsable gestiona las de su oficina. |
| Chats y pedidos | filtrados por la oficina del agente: los ve quien puede usar al agente. |
| Aprobaciones | van al responsable de la oficina del agente (fase E5). Admin de la empresa como respaldo si no hay responsable. |
| Costos | ya hay topes por departamento; el responsable ve los de su oficina. Nada de números de ahorro en la UI. |

### 4.3 La cara de empresa: el Drive del departamento

Ruta nueva `/d/<oficina>` (decisión del equipo), pantalla de inicio de los miembros de cuentas de empresa:

- **Barra superior:** nombre del departamento, migas de pan, buscador del departamento, selector de departamento si la
  persona está en varios.
- **Izquierda:** árbol de la carpeta del departamento; abajo "Compartido conmigo" (lo recibido de otros departamentos).
- **Centro:** lista de la carpeta con columnas Nombre · Último cambio (persona o agente, con insignia de agente) ·
  Estado (Para revisar / Borrador / Enviado). Filtros: Todo, Para revisar, Cambiado hoy. Clic derecho y menú "⋯":
  Abrir, Pedir a un agente…, Compartir con otro departamento…, Versiones, Mover, Renombrar, Eliminar.
- **Derecha:** panel del archivo seleccionado: vista previa, la pregunta o aprobación pendiente del agente con sus
  botones (usa las aprobaciones/preguntas que ya existen), actividad (quién tocó, con qué fuentes), versiones y "quién
  puede verlo".
- **Abajo:** "Pedir algo sobre esta carpeta…": crea un pedido normal al agente adecuado del departamento con la carpeta
  como contexto (la carpeta va en el pedido; el agente escribe ahí).
- El menú lateral de la app en cuentas de empresa se reduce a: Departamento (el Drive), Pendientes, Agentes del
  departamento, Ajustes. La oficina 3D, Chats y Proyectos quedan accesibles desde "Más" (decisión del equipo).
- Estilo: el de la app (blanco, azul #0A66C2, IBM Plex Sans), patrón Drive.

### 4.4 Plantillas por profesión

Al crear un departamento se elige profesión; crea el árbol inicial dentro de `/Oficinas/<oficina>/` y los agentes:

| Profesión | Carpetas | Agentes |
|---|---|---|
| Contabilidad | Clientes, Proveedores, Bancos y conciliaciones, Impuestos, Cierres mensuales, Políticas | Contabilidad, Análisis (los de E1) |
| Legal | Casos, Contratos, Plantillas de escritos, Plazos, Políticas | Legal |
| Recursos Humanos | Colaboradores, Reclutamiento, Planillas, Políticas, Capacitación | Recursos Humanos |
| Educación | Cursos, Exámenes, Rúbricas, Calificaciones | Asistente (rol docente nuevo queda para después) |
| Otras profesiones de E1 | Documentos, Políticas | los de E1 |

### 4.5 Entre departamentos

- "Compartir con otro departamento…" sobre un archivo o carpeta: queda pendiente hasta que el responsable del
  departamento dueño lo apruebe; luego aparece (solo lectura por defecto) en "Compartido conmigo" del otro.
- "Pedir a otro departamento" desde un pedido: crea un pedido en la cola del otro departamento; su agente responde con
  los permisos de ese departamento y una persona de allí aprueba si usa contenido no visible (E4).

## 5. Fases de construcción

| Fase | Contenido | Se nota |
|---|---|---|
| **D1. Carpetas de departamento** | `/Oficinas/<oficina>/`, permisos por persona (Ninguno/Leer/Editar con herencia), acceso efectivo agente ∩ persona en `fs`, adjuntos del chat a la carpeta de la oficina, plantilla de carpetas por profesión al crear oficina | Legal no ve archivos de Contabilidad |
| **D2. Cara Drive** | Ruta `/d/<oficina>`, árbol, lista con autor y estado, panel del archivo con pendientes, "Pedir algo sobre esta carpeta", menú reducido en cuentas de empresa | El departamento trabaja en su Drive |
| **D3. Sellado del resto** | memoria/búsqueda, habilidades, conexiones y chats por oficina; aprobaciones al responsable (E5) | Nada se filtra entre departamentos |
| **D4. Entre departamentos** | Compartir con aprobación, "Compartido conmigo", pedidos a otro departamento (E4) | Contabilidad pide un contrato a Legal sin ver lo demás |

Cada fase: pruebas de Go y del frontend, recorrido en navegador **sin Docker** (servidor Go nativo con almacén en
memoria + `next start`), guía de uso actualizada, commit.

## 6. Decisiones del equipo (para que el dueño las revise)

1. Departamentos privados por defecto en cuentas de empresa.
2. Los admin de la empresa no leen el contenido de un departamento privado sin hacerse miembros (auditado).
3. Ruta `/d/<oficina>` y menú reducido para cuentas de empresa; 3D, Chats y Proyectos en "Más".
4. Sin control de cambios dentro de Office en esta versión: versiones nuevas + comentarios en el panel.
5. Compartir entre departamentos en solo lectura por defecto y con aprobación del responsable dueño.
