# 10 - Workspaces de agente y artefactos

Estado: spec implementable (propuesta). Extiende, sin romper, `docs/SPEC.md` y `docs/architecture/01..09`. Todo es aditivo: sin artefactos la app se comporta exactamente como hoy.

Tema: como el humano trabaja con cada agente a nivel UI y como el agente produce y edita contenido (hojas, documentos, tableros, graficas, etc.) con Go como fuente de verdad.

Convenciones del repo que se respetan (verificadas en el codigo):
- Migraciones reales en `backend/internal/infrastructure/postgres/migrations/` (embebidas con `go:embed`, aplicadas en orden de nombre; ultima: `203_app_role.sql`). Ids `TEXT`, `org_id TEXT`, RLS con `app_current_org()` y `FORCE ROW LEVEL SECURITY`; el rol `app_user` recibe grants por `ALTER DEFAULT PRIVILEGES`. Esta feature agrega `204_artifacts.sql`.
- Herramientas: `backend/internal/tools` (`Tool`, `Registry`, `Executor`) + `backend/internal/policy` (deny > require_approval > allow; la autonomia `suggest|approve_each|rules|autonomous` ya existe en `policy/types.go`).
- Permisos: `backend/internal/auth/rbac.go` (`<recurso>:<accion>`, roles owner/admin/member/viewer).
- Frontend: Zustand (`lib/store.ts`), `AgentPanel.tsx` (aside de 420 px), `Shell.tsx`, `Dashboard.tsx` (secciones), estilo Animal Crossing (tokens Tailwind `bg/panel/panel2/line/ink/mute/accent`, `rounded-2xl border-2 border-line shadow-pop`, `ac-pop`, fuentes Nunito/Fredoka).
- E2E: los selectores `[data-testid^="agent-"]` y `[data-testid^="approval-"]` cuentan agentes y aprobaciones. **Ningun testid nuevo puede empezar con `agent-` ni `approval-`**; esta spec usa los prefijos `ws-`, `art-` y `dash-`.

Convenciones de esta spec:
- **Codigo en ingles**: todo identificador (componentes, tablas, columnas, endpoints, claves JSON, eventos WS, `data-testid`, ids de plantilla, nombres de herramientas/acciones) esta en ingles. La prosa de la spec es espanol. Los textos visibles ("Archivos", "Conciliacion bancaria", "Margen Acme", etiquetas de pestañas...) que aparecen en esta spec son **ejemplos localizables**, nunca literales de codigo (ver sec. 2.6, i18n).
- **Terminologia de proyectos/entregables**: la define `docs/architecture/workflow-visualization.md` (otro documento; al escribir esto aun no existe). Esta spec **no la redefine**: solo usa dos referencias opacas, `project_id` y `deliverable_id`, y propone un mapeo provisional con los terminos ya existentes en `05-workflows.md`: *project* ~ `request` / `workflow_run`; *deliverable* ~ `task` / `workflow_steps` de tipo `task` con un artefacto como salida. Al fusionar, **prevalece workflow-visualization.md** y se ajustan nombres en la sec. 5.8 sin cambiar la mecanica.

---

## 0. Decisiones (resumen)

| Tema | Decision |
|---|---|
| Modelo | El catalogo de artefactos es **global y componible**. Cualquier agente (o el humano, en el escritorio de cualquier agente) abre/crea cualquier tipo, varios a la vez, y un artefacto puede **incrustar** a otro (grafica o tabla dentro de un documento). |
| Rol | El rol del agente solo define **artefacto por defecto sugerido, plantillas y herramientas**. No restringe tipos. |
| Permisos | Por **capacidad** (`artifacts:read/create/write/comment/approve/export/delete`) y por **adjunto** (`read/propose/edit` por agente y artefacto). Nunca por tipo de rol ni por tipo de artefacto. |
| Fuente de verdad | Go. Contenido canonico en JSON propio versionado (`aiw.<kind>/1`), independiente de la libreria de UI (adaptadores en el frontend). |
| Concurrencia | **Versionado optimista + merge de 3 vias estructurado** (por celda / bloque / fila / tarjeta). **No CRDT** en Fase 1-5; Yjs queda como opcion Fase 6 solo para co-edicion multi-humano de documentos. |
| Agente produce | `tool_requests` con `tool:"artifacts"` (crear, aplicar ops, proponer, incrustar, exportar). Go valida, aplica politica/autonomia y versiona. El runtime sigue sin tocar nada. |
| Sugerencias | Documentos: marcas de cambio sugerido persistidas (como Word). Resto: overlay de propuesta. En ambos casos hay una fila en `artifact_proposals` (aceptar/rechazar, parcial). |
| Hojas | **Univer** (Apache-2.0) + `excelize` (Go) para xlsx/recalculo. |
| Documentos | **TipTap v3 core (MIT) sobre ProseMirror** + extensiones propias de cambios sugeridos y comentarios (las de TipTap Pro son de pago y no redistribuibles). `docx` + `mammoth` para docx. |
| Graficas | **Apache ECharts 5** con un `ChartSpec` propio (el agente nunca envia un `option` crudo). |
| Tabla / kanban | **TanStack Table v8 + @dnd-kit** (headless, hereda el look Animal Crossing). |
| PDF | **react-pdf / pdf.js** (visor). Generacion docx->pdf via Gotenberg (Fase 4). |
| Blobs | Interfaz `BlobStore` en Go, contenido direccionado por sha256; FS en dev, S3/MinIO en prod. |
| Proyectos | Cada artefacto puede ligarse a un `project_id` y a un `deliverable_id` (terminologia de workflow-visualization.md). El escritorio muestra **varios entregables en construccion en paralelo**, con progreso por pestaña, panel resumen del proyecto, navegacion entre entregables y **referencias entre artefactos** (celdas que leen otra hoja, notas que enlazan a una hoja). |
| Idioma | Codigo en ingles; textos visibles y plantillas **localizables** (`es` por defecto, `en`). |

---

## 1. Principios de diseno

1. **Escritorio = conjunto de pestañas.** El workspace de un agente es una lista de pestañas, cada una un artefacto de cualquier tipo. No hay "el editor del Contador".
2. **El tipo lo elige la tarea.** El planificador/agente decide `kind` segun lo que necesita; el rol solo ordena el menu "+" (sugeridos primero).
3. **Todo cambio es una version.** Humano o agente, directo o aceptado desde una propuesta: nueva fila inmutable en `artifact_versions` + evento + auditoria.
4. **Go decide si el agente escribe directo o propone** (autonomia, estado del artefacto, tamano del cambio, origen del contenido).
5. **El agente ve las ediciones humanas** como contexto de su siguiente tarea (cursor `last_seen_version` por agente y artefacto).
6. **Contenido de artefactos = datos no confiables** al ir al runtime (delimitado, con `trust`), igual que `external_content` (07 sec. 2).
7. **El look no cambia.** Mismos tokens, bordes redondeados, sombras `shadow-pop`, animacion `ac-pop`; los editores se tematizan con esos tokens.

---

## 2. UI: el escritorio del agente

### 2.1 Flujo y estados

Hoy: clic en un personaje -> `select(id)` -> `AgentPanel` (aside 420 px). Se conserva tal cual y se extiende con **tres tamanos** del mismo contenedor:

| Tamano (`data-size`) | Ancho | Cuando | Contenido |
|---|---|---|---|
| `dock` | 420 px (actual) | clic en agente | `AgentPanel` con nueva pestaña **Archivos** (lista de artefactos adjuntos/creados + boton "Abrir escritorio") |
| `expanded` | `min(1180px, 82vw)` | "Abrir escritorio", clic en un archivo, doble clic en el personaje, o evento `artifact.created` de ese agente con `open_in_workspace` | Riel izquierdo de 64 px (avatar, nombre, `StateBadge` compacto, boton cerrar) + `WorkspaceHost` |
| `full` | 100 % del area `main` | boton "Pantalla completa" / modo dashboard | `WorkspaceHost` sin riel de agente (selector "Escritorio de: ..." arriba) |

Animacion (todo en el estilo actual):
- Entrada de `dock` ya existe. De `dock` a `expanded`: transicion de `width` 360 ms con `cubic-bezier(.34,1.56,.64,1)` (la misma curva de `ac-pop`), contenido con `ac-pop` escalonado (riel, tabs, pane). Sin blur nuevo: se reutiliza `bg-panel/95 backdrop-blur`.
- **Camara**: `Rig` (en `OfficeScene.tsx`) interpola `controls.target` hacia el escritorio del agente (`ROLE_META[role].desk`) cuando `workspaceOpen`, y usa `camera.setViewOffset` para centrar al personaje en la zona visible a la izquierda del panel (offset = ancho del panel / 2). Al cerrar vuelve al target previo. Los limites de `Rig` (clamp de target, polar/azimuth) se respetan.
- **El personaje sigue vivo**: la animacion sigue derivando de eventos reales. `agent.state_changed` (working/reviewing/thinking...) ya basta; ademas `artifact.version_created` con autor agente dispara un "pulso de tecleo" de 1.2 s y `artifact.proposal_created` el estado `reviewing` visual (sin nuevos estados de dominio).
- **Chip de archivos** en `LabelLayer`: bajo la etiqueta del agente, un chip `N archivos` (icono del tipo mas reciente). Es un hijo del nodo `agent-<id>` pero con testid propio `ws-chip-<agentId>` (no empieza con `agent-`). Clic = abre el escritorio.
- `CommandBar` ya usa `paddingRight: selected ? 420 : 16`; pasa a leer la variable CSS `--panel-w` que `WorkspaceHost` actualiza (420 / ancho expandido / 0 en `full`).

### 2.2 Pestañas y paneles multiples

- Un agente puede tener **N pestañas abiertas** de tipos mezclados. Pestañas estilo carpeta (borde inferior con el color del rol activo, como las tabs actuales de `AgentPanel`), icono de tipo (`lucide-react`), punto de color si hay propuestas pendientes (ambar) o un agente editando (color del agente).
- Boton **"+"** (`NewArtifactMenu`): lista **todos** los tipos; los `suggestedFor` del rol llevan una estrella y van primero; debajo "Plantillas" (filtradas por rol pero buscables todas) y "Importar archivo" (xlsx, docx, csv, pdf).
- **Split**: el area admite 1 o 2 columnas; se arrastra una pestaña al borde derecho (dnd-kit) o boton `ws-split-toggle`. Maximo 2 columnas x 1 fila en Fase 3 (suficiente para "hoja a la izquierda, documento a la derecha").
- **Adjuntar al agente**: cada pestaña tiene un selector `Visible para <Agente>: ninguno / leer / proponer / editar` (`artifact_attachments`). Por defecto, un archivo que el humano abre en el escritorio de un agente queda en `read`; los creados por el agente en `edit`. Esto es lo que define que ve el agente en su siguiente tarea (sec. 6).
- Mismo artefacto abierto en escritorios de varios agentes: permitido (p. ej. Legal abre la hoja de Contabilidad). Es un solo artefacto; solo cambia el adjunto y la pestaña.
- Estado de pestañas por usuario y escritorio en `workspace_layouts` (sec. 5.6) con respaldo en `sessionStorage`.

### 2.3 Convivencia con el modo dashboard

- Se agrega la seccion **"Archivos"** al arreglo `SECTIONS` de `Dashboard.tsx`: biblioteca global con filtros (tipo, agente, cliente, estado, texto), tarjetas `Card` con icono de tipo, estado, version, ultimo autor (humano/agente) y badges de propuestas pendientes.
- Abrir una tarjeta monta `WorkspaceHost size="full"` en el area derecha del dashboard. Las pestañas viven en el store (`workspace.desks[deskId]`), **no** en el componente: cambiar de modo oficina/dashboard conserva todo. El dashboard muestra el escritorio `"me"` (personal, sin agente) y un selector para ver el de cualquier agente.
- Tarjeta de aprobaciones/`ApprovalsTray`: las aprobaciones ligadas a un artefacto (enviar propuesta, enviar contrato) muestran una miniatura y el boton "Ver version exacta" que abre el artefacto en la `version` fijada.
- La barra de comando sigue igual; `art-ask-agent` (sec. 6.3) es el atajo contextual.

### 2.3b Proyectos y entregables en paralelo

Caso guia: el Contador recibe el proyecto "Cierre financiero" con entregables **Balance general**, **Estado de resultados** y **Flujo de efectivo**; los tres se construyen **al mismo tiempo** (tareas paralelas del scheduler), cada uno en su propia hoja, mas una **nota de politicas contables** (doc) que enlaza a las hojas.

- **Una pestaña por entregable en construccion.** Cada pestaña de artefacto con `deliverable_id` muestra: anillo de progreso (`ws-tab-progress-<artifactId>`, 0-100), estado de construccion (`build_state`: `queued | building | ready_for_review | done | blocked`) y el color del agente que lo construye. Pestañas "building" tienen un pulso suave (`ac-bounce`); `ready_for_review` un punto ambar. Todo derivado de eventos (`artifact.progress_changed`, `task.*`), nunca inventado por la UI.
- **Panel resumen del proyecto** (pestaña fija "Project", `ws-project-summary`; icono de carpeta): nombre y estado del proyecto, lista de entregables (titulo, agente, progreso, `build_state`, artefactos asociados), grafo mini de dependencias (se **reutiliza el componente de workflow-visualization.md**; aqui solo se monta), y "Linked artifacts" (quien referencia a quien). Clic en un entregable lo abre/enfoca en pestaña.
- **Navegacion entre entregables**: barra superior del pane (`ws-deliverable-nav`) con anterior/siguiente dentro del proyecto y selector de entregable (`ws-deliverable-select`); atajos `Alt+[` / `Alt+]`. Los entregables de **otros agentes** del mismo proyecto aparecen en solo lectura salvo adjunto distinto (sec. 5.3 de permisos).
- **Referencias entre artefactos** (clic salta a destino con la region seleccionada): en una hoja, una celda que usa `AIW_REF` (sec. 5.9) muestra un chip "linked" y al `Ctrl+clic` abre la hoja origen en su celda; en un doc, el nodo/mark `artifact_link` abre el artefacto con `anchor` (`{sheet,range}`, `{bid}`, `{card}`); en el panel del proyecto, el grafo de enlaces. Si la fuente cambia: insignia "Source changed" (`ws-dependency-badge-<artifactId>`) hasta que el dependiente se recalcula (sec. 5.9).
- **Paralelismo y personaje**: el personaje sigue un solo estado (`working`), pero el chip `ws-chip-<agentId>` muestra "N building" y `artifact.agent_focus` puede apuntar a varios artefactos a la vez (una region por tarea). Cada tarea escribe en **su** artefacto (el `deliverable_id` define el dueño); escribir en el artefacto de otro entregable sigue las reglas de adjunto/autonomia (sec. 6.2).
- **Dashboard**: la seccion `files` agrupa por proyecto (acordeon), con progreso agregado por proyecto y por entregable.

### 2.4 Componentes (frontend)

```
frontend/src/components/workspace/
  WorkspaceHost.tsx        contenedor, tamano dock|expanded|full, camara, --panel-w
  WorkspaceTabs.tsx        pestañas + split + "+"
  NewArtifactMenu.tsx      todos los tipos, sugeridos del rol, plantillas, importar
  ArtifactPane.tsx         header (titulo, estado, version, adjunto, exportar, pedir al agente) + Editor
  AttachControl.tsx        read|propose|edit por agente
  ProposalBar.tsx          aceptar/rechazar todo, revisar una por una
  CommentsRail.tsx         hilos, @menciones, resolver
  VersionHistory.tsx       linea de tiempo humano/agente, diff, restaurar
  AgentPresence.tsx        "Tomas esta editando B2:D9" (cosmetico, efimero)
  ProjectSummary.tsx       panel resumen del proyecto (entregables, progreso, enlaces)
  DeliverableNav.tsx       anterior/siguiente/selector dentro del proyecto
  TabProgressRing.tsx      anillo de progreso por pestaña
  LinkedArtifacts.tsx      referencias entrantes/salientes y navegacion a ancla
  extensions/ArtifactLinkMark.ts   mark/nodo TipTap para enlazar a otro artefacto (+ AIW_REF en univerAdapter)
  registry.ts              ARTIFACT_KINDS (sec. 3)
  editors/{SheetEditor,DocEditor,TableEditor,BoardEditor,ChartEditor,PdfViewer,FormRenderer,InboxView,AgendaView}.tsx   (lazy con next/dynamic)
  adapters/{univerAdapter,tiptapAdapter,echartsAdapter}.ts
frontend/src/lib/artifacts.ts   slice Zustand + api + aplicar eventos artifact.*
```
### 2.6 i18n (localizable, `es` por defecto)

- Diccionarios `frontend/src/i18n/es.json` y `en.json` con claves en ingles (`workspace.openDesk`, `artifact.kind.sheet`, `artifact.status.in_review`, `workspace.tab.files`...). Helper `t(key, vars)` en `lib/i18n.ts` + `locale` en el store (persistido; defecto `es`, detectado de `navigator.language` si es `en`). Ningun texto visible se escribe literal en componentes nuevos.
- **Plantillas** (`backend/internal/artifacts/templates/*.json`): id y estructura en ingles; textos visibles como mapas por idioma y se resuelven al instanciar con el locale del usuario:
```json
{ "id": "bank_reconciliation", "kind": "sheet", "suggested_for": ["accounting"],
  "title": {"es": "Conciliación bancaria", "en": "Bank reconciliation"},
  "content": {"schema": "aiw.sheet/1", "sheets": [{"id": "s1", "name": {"es": "Conciliación", "en": "Reconciliation"},
    "cells": {"A1": {"v": {"es": "Concepto", "en": "Item"}}, "B1": {"v": {"es": "Monto", "en": "Amount"}}}}]} }
```
  Regla: valores `{"es":..,"en":..}` solo existen **en plantillas**; el artefacto instanciado guarda cadenas planas y su `locale`.
- `artifacts.locale` (`es|en`) y `RunContext.locale` (aditivo): el runtime redacta el contenido de los artefactos en ese idioma; el simulador (`sim_content.py`) mantiene ambos idiomas para el escenario demo.
- El backend devuelve codigos/ids estables (`status`, `build_state`, `kind`, `code` de errores); la traduccion es siempre del cliente.

`lib/types.ts` recibe los tipos de la sec. 5.1; `lib/ws.ts` enruta `artifact.*` a `lib/artifacts.ts`; `lib/api.ts` recibe los endpoints de la sec. 5.5; `lib/mock/engine.ts` debe simular los mismos (modo `dev:mock`).

---

## 3. Catalogo de artefactos y librerias

Nueve tipos de primera clase. Criterio de licencia: **MIT / Apache-2.0 / BSD / ISC** (redistribuibles dentro de codigo fuente vendido). Se descartan GPL/AGPL, "non-commercial", y paquetes por-desarrollador. Pesos aproximados (min+gzip, solo carga al abrir el tipo; **verificar con `npm view` y el analizador de bundle al instalar; fijar con lockfile**).

### 3.1 `sheet` - hoja de calculo (formulas, tablas, conciliaciones)

| Opcion | Licencia | Peso | Veredicto |
|---|---|---|---|
| **Univer** (`@univerjs/presets`, `preset-sheets-core`, `preset-sheets-formula`, `preset-sheets-data-validation`, `preset-sheets-conditional-formatting`, `preset-sheets-numfmt`, locale `es-ES`) | Apache-2.0 (nucleo). **Univer Pro** (colaboracion, import/export xlsx, graficas, pivot) es comercial | ~1-1.5 MB gz | **ELEGIDA**. Motor de formulas propio, canvas rapido, TS, i18n. |
| Handsontable | Licencia no comercial / comercial por desarrollador | ~400 KB | Descartada: no redistribuible en venta de fuente. |
| AG Grid Community | MIT (Enterprise de pago) | ~300-600 KB | Es grilla, sin formulas. Valida como `table`, pero se prefiere TanStack (control del look). |
| FortuneSheet (`@fortune-sheet/react`) | MIT | ~700 KB | **Plan B** si Univer falla el spike de tema/rendimiento. |
| x-spreadsheet | MIT | ~150 KB | Descartada: sin mantenimiento desde 2021, sin formulas reales. |
| Jspreadsheet CE | MIT | ~200 KB | Descartada: formulas basicas, UX limitada. |
| HyperFormula | GPLv3 / comercial | - | Descartada (viral). |
| SheetJS CE (npm `xlsx` 0.18.5) | Apache-2.0 | - | Descartada: version npm desactualizada con CVE conocidos; la reciente solo via CDN propio. |

- **Import/export xlsx y recalculo en Go**: `github.com/xuri/excelize/v2` (BSD-3, ~v2.9.x). Importa xlsx -> `aiw.sheet/1`; exporta `aiw.sheet/1` -> xlsx; `CalcCellValue` recalcula formulas de ediciones del agente para devolverle valores al runtime y para validar. `exceljs` (MIT) solo como plan B.
- **Riesgos**: API 0.x de Univer cambia entre minors (fijar version exacta, adaptador unico `univerAdapter.ts`); tematizado limitado (se aplican variables CSS y fuente Nunito, el lienzo de celdas usa paleta crema; spike de 2 dias en Fase 1); sin graficas incluidas (se usa `chart` incrustable); funciones de Excel no soportadas por Univer o excelize pueden diferir en el recalculo (se muestra `#NAME?` y se reporta en `findings`).

### 3.2 `doc` - documento rico (clausulas, cambios sugeridos, comentarios)

| Opcion | Licencia | Veredicto |
|---|---|---|
| **TipTap v3** (`@tiptap/react`, `@tiptap/pm`, `@tiptap/starter-kit`, `@tiptap/extension-table`, `-link`, `-underline`, `-placeholder`, `-character-count`) | MIT (nucleo). Extensiones **Pro** (Comments, Tracked changes, Collaboration history, AI) de pago, no redistribuibles | **ELEGIDA** + extensiones propias `Suggestion`, `Comment`, `Clause`, `Embed`. |
| ProseMirror directo | MIT | Es la base de TipTap; se usa via `@tiptap/pm` para pasos/transformaciones del diff. |
| Lexical | MIT | Sin cambios sugeridos nativos y menos ecosistema de marcas/pasos; no aporta sobre TipTap. |
| Plate (Slate) | MIT | **Plan B**: trae plugins de sugerencias y comentarios gratis, pero Slate + Tailwind 4/shadcn choca con nuestro Tailwind 3 y su manejo de documentos largos/IME es mas debil. Spike de 2 dias en Fase 2 compara costo de nuestras extensiones vs Plate. |
| BlockNote, CKEditor 5, OnlyOffice, Collabora | MPL+GPL(xl)/GPL-comercial/AGPL | Descartadas por licencia. |

- **docx**: importar con `mammoth` (BSD-2, ~1.8) en el navegador (docx -> HTML -> `DOMPurify` -> esquema ProseMirror, que descarta lo no permitido); exportar con `docx` (MIT, ^9) que soporta `InsertedTextRun`/`DeletedTextRun` (w:ins/w:del) y comentarios. Fase 5: parser propio de `w:ins/w:del/comments` con `jszip` + `fast-xml-parser` para **importar** cambios sugeridos existentes (mammoth los aplana).
- **Riesgos**: construir bien Suggestion/Comment (aceptar/rechazar, rebase de posiciones; mitigado: marcas con `sid` estable y ancla por `bid` de bloque, no por offset); fidelidad docx parcial (estilos complejos, campos, tablas anidadas; documentar los limites y mostrar aviso al importar); rendimiento >300 paginas (limite de 1 MB de JSON).

### 3.3 `table` - tabla de datos (dataset)

`@tanstack/react-table` ^8.20 (MIT, ~15 KB) + `@tanstack/react-virtual` ^3 (MIT) para filas virtualizadas. Columnas tipadas (`text|number|money|date|select|checkbox|link`), orden/filtro, edicion de celda, filas con `id` estable. Riesgo bajo; es headless, asi que el look Animal Crossing es nativo. Diferencia con `sheet`: sin formulas, esquema fijo, ideal para listados, matrices de riesgo, candidatos; se puede **convertir** a hoja y viceversa (accion "Abrir como hoja").

### 3.4 `board` - kanban / checklist / pipeline

`@dnd-kit/core` ^6.3 + `@dnd-kit/sortable` ^10 (MIT, ~15 KB) sobre componentes propios. Columnas con limite WIP opcional, tarjetas con asignado (agente o humano), etiquetas, fecha limite, checklist interno y `link` a otro artefacto. Una variante de vista `view:"checklist"` renderiza una sola columna con casillas (Operaciones). Riesgo bajo; accesibilidad de drag (usar sensores de teclado de dnd-kit).

### 3.5 `chart` - grafica

| Opcion | Licencia | Veredicto |
|---|---|---|
| **Apache ECharts 5** (`echarts` ^5.6, importacion modular + `SVGRenderer`/`CanvasRenderer`) | Apache-2.0 | **ELEGIDA** (~150-250 KB gz tree-shaken). SSR a SVG util para exportar a docx/pdf. |
| Recharts | MIT | Mas ligera pero menos tipos (sin mapas de calor, sankey, etc.). |
| Vega-Lite (`vega-embed`) | BSD-3 | Spec declarativa muy amigable para LLM, pero ~600+ KB y estilo menos controlable. |

El agente **no** envia un `option` de ECharts (permite funciones/HTML en tooltips): envia un `ChartSpec` propio (sec. 5.1) que `echartsAdapter.ts` compila a `option` con formateadores fijos. Los datos vienen de otro artefacto (`source`), asi la grafica se actualiza al cambiar la hoja. Notebooks (Fase 5) = documento con bloques `cell` (markdown, consulta a dataset, grafica); **no** se ejecuta codigo arbitrario (ni Python ni JS) en el navegador ni en Go.

### 3.6 `pdf` - visor PDF

`react-pdf` ^9 (MIT; trae `pdfjs-dist` 4.x, Apache-2.0, ~400 KB gz + worker de carga diferida). Solo lectura + anotaciones propias (resaltado y nota ancladas a pagina/rectangulo, guardadas en `content.annotations`, no dentro del PDF). `isEvalSupported:false`, sin scripting de PDF, sin enlaces externos activos (se muestran con dominio y confirmacion). Generar PDF: Gotenberg (MIT, contenedor aparte con LibreOffice/Chromium) desde docx en Fase 4. Riesgo: PDFs hostiles (limite 25 MB / 500 paginas; el render corre en worker).

### 3.7 `form` - formulario de aprobacion / captura

`react-hook-form` ^7.54 + `zod` ^3.24 (MIT) renderizando un `FormSpec` propio (campos `text|textarea|number|money|date|select|multiselect|checkbox|file|readonly_artifact`). Sirve para: (a) el agente pide datos que le faltan al humano (`request_input`), (b) aprobacion enriquecida (montos editables, comentarios obligatorios, checklist de verificacion) ligada a una `Approval`. Una `readonly_artifact` incrusta la version exacta que se aprueba. Riesgo bajo.

### 3.8 `inbox` - bandeja

Componente propio (lista virtualizada TanStack + panel de lectura). Items `{from, subject, snippet, received_at, source, triage:{label,priority,suggested_action,draft_artifact_id}}`. El cuerpo de mensajes externos se muestra como texto plano (sin HTML ni imagenes remotas, 07 T7). Los borradores de respuesta son artefactos `doc` incrustados. Hoy alimentada por la herramienta fake de email; en Fase 4+ por la integracion real.

### 3.9 `agenda` - agenda/calendario

`@fullcalendar/react` + `daygrid` + `timegrid` + `interaction` ^6.1 (MIT; **no** usar los plugins premium). Eventos `{id,title,start,end,attendees,location,status:'proposed'|'confirmed',source}`; propuestas del agente se ven punteadas (aceptar/rechazar = propuesta). Riesgo: peso (~80 KB gz); alternativa `@schedule-x/react` (MIT) si el look no encaja.

### 3.10 Utilidades transversales
`lucide-react` (ISC, iconos de tipo), `dompurify` ^3.2 (Apache-2.0, solo en importadores del navegador), `github.com/minio/minio-go/v7` (Apache-2.0, S3), `excelize/v2` (BSD-3). Descartadas por licencia: Syncfusion, DevExtreme, Kendo, Univer Pro, TipTap Pro, AG Grid Enterprise.

### 3.11 Incrustacion (composicion)

Un `doc` puede contener nodos `embed` que apuntan a otro artefacto; un `board` puede tener tarjetas con `link`; un `chart` tiene `source` hacia una `sheet`/`table`.

| Anfitrion | Incrustable | Vista |
|---|---|---|
| `doc` | `sheet` (rango), `table`, `chart`, `board` (instantanea), `pdf` (pagina), `form` (solo lectura) | `view` por tipo; `pinned_version: null` = en vivo, `n` = fija |
| `board` | cualquier artefacto como `link` de tarjeta | enlace con miniatura |
| `chart` | `sheet` (rango) o `table` como fuente | datos |

Reglas: grafo sin ciclos (se valida con `artifact_links`), profundidad maxima 3, el lector necesita `artifacts:read` sobre el destino (si no, se muestra un candado), tamano del embed no cuenta contra el limite del anfitrion. Al exportar: `embed` de hoja/tabla -> tabla docx; `chart` -> SVG/PNG renderizado con ECharts SSR; resto -> imagen miniatura + enlace.

---

## 4. Roles: artefacto por defecto, plantillas y herramientas (sugerencia, no limite)

`GET /artifact-kinds` devuelve el catalogo completo y, para el agente, `suggested` ordenado. Las columnas son **orden sugerido en el menu "+"**; cualquier agente puede abrir cualquiera.

| Rol | Default (se abre al empezar) | Tambien sugeridos | Plantillas | Herramientas tipicas |
|---|---|---|---|---|
| Contador (`accounting`) | `sheet` | `doc` (notas, memos), `chart`, `table` | Conciliacion bancaria, Estado de resultados, Analisis de margen | spreadsheet, calculator, import xlsx |
| Legal (`legal`) | `doc` | `table` (matriz de riesgos/clausulas), `sheet`, `pdf` | Contrato de servicios, NDA, Revision de clausulas | documents, docx redline |
| Ventas (`sales`) | `doc` (propuesta/cotizacion) | `board` (pipeline), `sheet` (cotizador), `chart` | Propuesta comercial, Cotizacion, Pipeline | documents, crm |
| Analista (`analyst`) | `chart` | `sheet`, `doc` (informe/notebook), `table` | Informe de analisis, Tablero de KPIs | spreadsheet, calculator |
| Operaciones (`operations`) | `board` (checklist/kanban) | `table`, `sheet` (capacidad), `agenda` | Checklist de operacion, Plan de capacidad | calendar, spreadsheet |
| Asistente (`assistant`) | `inbox` | `agenda`, `doc` (borradores), `form` | Bandeja del dia, Agenda semanal, Minuta | email, calendar |
| RR. HH. (`hr`) | `table` (candidatos) | `board` (reclutamiento), `doc` (oferta), `form` | Pipeline de contratacion, Oferta laboral | documents, calendar |
| Otros roles futuros: Marketing (`doc`, `board` calendario editorial, `chart`), Finanzas/Tesoreria (`sheet`, `chart`, `form`), Compras (`table`, `form`, `pdf`), Direccion (`chart` tablero, `form` aprobaciones), Soporte/TI (`board`, `table`, `doc`), Compliance (`table`, `pdf`, `doc`). | | | | |

Mecanica: `ROLE_META` (frontend) y el seed del agente en Go ganan `suggested_artifacts: string[]`; las plantillas son JSON embebidos (`backend/internal/artifacts/templates/*.json`) con `suggested_for: string[]` (informativo). Un admin puede opcionalmente fijar `agent_tools.constraints.artifact_kinds` para **restringir** (por defecto vacio = sin restriccion).

---

## 5. Contrato de datos

### 5.1 Esquemas (Go `domain.Artifact*`, TS `types.ts`)

```jsonc
// ArtifactMeta (lista y eventos)
{
  "id": "art_01J9...", "org_id": "000...001",
  "kind": "sheet",                       // sheet|doc|table|board|chart|pdf|form|inbox|agenda
  "schema_version": "aiw.sheet/1",
  "title": "Margen Acme - conciliacion",
  "status": "draft",                     // draft|in_review|approved|sent|archived
  "head_version": 7,
  "created_by": {"kind": "agent", "id": "accounting"},   // agent|user|system
  "last_author": {"kind": "user", "id": "u_1"},
  "task_id": "task_9", "request_id": "req_3", "customer_id": null,
  "project_id": "proj_1", "deliverable_id": "dlv_balance_sheet",     // opacos; terminologia en workflow-visualization.md
  "progress": 60, "build_state": "building",                          // queued|building|ready_for_review|done|blocked
  "locale": "es", "depends_on_artifacts": ["art_income_stmt"],        // derivado de artifact_links (source_of)
  "pending_proposals": 2, "tainted": false, "locked": false,
  "size_bytes": 18234,
  "attachments": [{"agent_id": "accounting", "mode": "edit"}, {"agent_id": "legal", "mode": "read"}],
  "created_at": "...", "updated_at": "..."
}
// Artifact = ArtifactMeta + { "content": <segun kind>, "version": 7 }
// ArtifactVersion
{ "artifact_id": "...", "version": 7, "base_version": 6,
  "author": {"kind":"agent","id":"accounting"}, "source": "agent_task",  // agent_task|human_edit|accept_proposal|import|restore|rebase|recalc|create
  "task_id": "task_9", "summary": "Margen real 24% (no 31%)", "content_hash": "sha256:...",
  "has_pending_suggestions": false, "created_at": "..." }
```

**Contenido canonico por tipo** (Go valida con structs + listas blancas; el frontend lo adapta a Univer/TipTap/ECharts):

```jsonc
// aiw.sheet/1  (celdas dispersas; "v" valor cacheado, "f" formula; el calculo fino es de Univer/excelize)
{ "schema":"aiw.sheet/1",
  "sheets":[{ "id":"s1","name":"Margen","rows":200,"cols":26,
    "cells":{ "A1":{"v":"Concepto"}, "B2":{"v":50000,"fmt":"$#,##0.00"},
              "B3":{"v":34500,"fmt":"$#,##0.00"}, "B5":{"f":"=(B2-B3)/B2","v":0.31,"fmt":"0.0%"} },
    "colw":{"A":220}, "frozen":{"rows":1,"cols":0}, "names":{"Ingresos":"Margen!B2"} }] }

// aiw.doc/1  (ProseMirror JSON; cada bloque lleva attrs.bid estable)
{ "schema":"aiw.doc/1",
  "doc":{"type":"doc","content":[
    {"type":"heading","attrs":{"bid":"b1","level":1},"content":[{"type":"text","text":"Contrato marco"}]},
    {"type":"clause","attrs":{"bid":"b7","number":"7","title":"Penalidades"},"content":[
       {"type":"paragraph","attrs":{"bid":"b7a"},"content":[
         {"type":"text","text":"La penalidad sera del "},
         {"type":"text","text":"15%","marks":[{"type":"suggestion","attrs":{"sid":"s_1","kind":"del","author":{"kind":"agent","id":"legal"},"ts":"...","reason":"Excede politica","proposal_id":"prop_4"}}]},
         {"type":"text","text":"10%","marks":[{"type":"suggestion","attrs":{"sid":"s_2","kind":"ins","author":{"kind":"agent","id":"legal"},"ts":"...","proposal_id":"prop_4"}}]}]}]},
    {"type":"embed","attrs":{"bid":"b9","artifact_id":"art_chart1","view":{"height":260},"pinned_version":null}}]},
  "comments":[{"cid":"c_1","bid":"b7a","quote":"15%"}] }       // el cuerpo del comentario vive en artifact_comments

// aiw.table/1
{ "schema":"aiw.table/1","columns":[{"key":"risk","label":"Riesgo","type":"text"},{"key":"sev","label":"Severidad","type":"select","options":["Alta","Media","Baja"]}],
  "rows":[{"id":"r1","cells":{"risk":"Penalidad desproporcionada","sev":"Alta"}}] }

// aiw.board/1
{ "schema":"aiw.board/1","view":"kanban",           // kanban|checklist
  "columns":[{"id":"todo","title":"Por hacer","wip":null},{"id":"doing","title":"En curso"},{"id":"done","title":"Hecho"}],
  "cards":[{"id":"k1","col":"todo","title":"Contratar 2 personas","assignee":{"kind":"agent","id":"hr"},
            "labels":["capacidad"],"due":"2026-11-01","checklist":[{"t":"Perfil","done":false}],"link":{"artifact_id":"art_x"}}] }

// aiw.chart/1
{ "schema":"aiw.chart/1","title":"Margen por mes","mark":"line",      // bar|line|area|pie|scatter|combo
  "source":{"artifact_id":"art_sheet1","sheet":"Margen","range":"A1:C13"},   // o {"inline":{"columns":[],"rows":[]}}
  "encoding":{"x":"mes","y":["ingresos","costos"],"series":null},"format":{"y":"$#,##0"},"stack":false }

// aiw.pdf/1   { "schema":"aiw.pdf/1","blob_id":"blob_...","pages":12,"annotations":[{"id":"a1","page":3,"rect":[x,y,w,h],"text":"Revisar","author":{...}}] }
// aiw.form/1  { "schema":"aiw.form/1","bound":{"approval_id":"appr_1"},"fields":[{"key":"discount","type":"money","label":"Descuento autorizado","required":true,"max":10}, {"key":"doc","type":"readonly_artifact","artifact_id":"art_d","version":4}],"answers":null,"answered_by":null }
// aiw.inbox/1 { "schema":"aiw.inbox/1","items":[{"id":"m1","from":"ana@acme.com","subject":"...","snippet":"...","received_at":"...","source":"email","triage":{"label":"cliente","priority":"alta","suggested_action":"responder","draft_artifact_id":"art_9"}}] }
// aiw.agenda/1{ "schema":"aiw.agenda/1","events":[{"id":"e1","title":"Revision Acme","start":"...","end":"...","attendees":["ana@acme.com"],"status":"proposed"}] }
```

**ChartSpec/FormSpec/etc. son datos, nunca codigo.** Cualquier campo fuera de la lista blanca se descarta (como `StructuredOutput.Normalize`).

**Operaciones (`ops`)**: el agente (y el cliente humano para cambios pequeños) edita con ops tipadas en lugar de reenviar el contenido completo. `content` completo solo en `create` y para guardado humano de documentos.

| Tipo | Ops |
|---|---|
| sheet | `set_cells{sheet,range:"B2:C3",values:[[{"v":1},{"f":"=B2*2"}],[...]]}`, `clear{sheet,range}`, `insert_rows/delete_rows{sheet,at,count}`, `insert_cols/delete_cols`, `add_sheet{name}`, `rename_sheet`, `set_format{sheet,range,fmt,style}`, `set_name{name,ref}` |
| doc | `insert_after{bid,nodes[]}`, `replace_block{bid,node}`, `delete_block{bid}`, `suggest_replace{bid,find,replace,reason}`, `suggest_insert{bid,after_text,text,reason}`, `suggest_delete{bid,find,reason}`, `comment{bid,quote,text}`, `set_attrs{bid,attrs}` |
| table | `add_rows`, `update_cells{row_id,cells}`, `delete_rows`, `add_column`, `set_column` |
| board | `add_card`, `move_card{id,col,index}`, `update_card`, `delete_card`, `add_column`, `rename_column` |
| chart | `set_spec{patch}` (merge del spec) |
| form | `set_answers{answers}` (solo humano) |
| inbox/agenda | `set_triage{item,triage}`, `add_event`, `update_event`, `set_event_status` |
| pdf | `add_annotation`, `remove_annotation` |

### 5.2 Tool `artifacts` (el agente decide el tipo)

El runtime sigue devolviendo `output` + `tool_requests`. Se agrega la herramienta **`artifacts`** al `tools.Registry` (`backend/internal/tools/artifacts.go`), sujeta al mismo `Executor`/`policy`:

| Accion | read_only | Riesgo por defecto | Notas |
|---|---|---|---|
| `list`, `read{id,version?,range?,format?}`, `search` | si | low | `read` devuelve texto renderizado (sec. 6.1) |
| `create{kind,title,content?,template_id?,client_ref,open_in_workspace,customer_id?}` | no | low | siempre permitido: crea `draft` propiedad del agente (adjunto `edit`) |
| `apply_ops{artifact,ops[],summary}` | no | medium | Go decide directo vs propuesta (sec. 6.2) |
| `propose_ops{artifact,ops[],summary}` | no | low | siempre propuesta |
| `comment{artifact,anchor,text,mention?}` | no | low | |
| `embed{into,after_block,artifact,view,pinned_version?}` | no | low | valida ciclos/permiso |
| `link{from,to,relation}` | no | low | |
| `set_status{artifact,status:"in_review"}` | no | medium | `approved`/`sent` son solo humano |
| `set_progress{artifact,progress,build_state?,note?}` | no | low | progreso 0-100 del entregable; Go lo acota (monotono salvo reinicio explicito) y lo cruza con `task.progress`; emite `artifact.progress_changed` |
| `bind{artifact,project_id?,deliverable_id?}` | no | low | liga/desliga un artefacto a un entregable (valida que el entregable pertenezca a la tarea/proyecto del agente) |
| `link` (ya listada) con `relation:"source_of"` y `anchor` | no | low | declara dependencia de datos (hoja que lee otra hoja, nota que enlaza a una hoja); ver sec. 5.9 |
| `request_input{form_spec,title}` | no | low | crea `form` + espera respuesta (reusa flujo de aprobacion) |
| `export{artifact,format,version?}` | no | **high** | docx/xlsx/pdf/csv; salida fuera de la plataforma o envio sigue en `documents.send_*` con aprobacion |

Ejemplo de respuesta del runtime (`POST /v1/run-task`), con referencias temporales `client_ref` que Go resuelve **en orden**:

```json
{
  "output": {
    "summary": "El margen real es 24% (no 31%).",
    "findings": ["Costos indirectos omitidos en la propuesta"],
    "metrics": {"margen_real": 0.24},
    "artifact_refs": [
      {"ref": "$sheet", "role": "deliverable", "note": "Calculo del margen"},
      {"ref": "art_prop_valeria", "role": "input"}
    ],
    "confidence": 0.86, "hypotheses": [], "evidence": [], "recommendations": [], "suggested_tasks": []
  },
  "tool_requests": [
    {"tool":"artifacts","action":"create","risk":"low","args":{"client_ref":"$sheet","kind":"sheet","title":"Margen Acme","open_in_workspace":true,
      "content":{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Margen","cells":{"A1":{"v":"Ingresos"},"B1":{"v":50000},"A2":{"v":"Costos"},"B2":{"v":38000},"A3":{"v":"Margen"},"B3":{"f":"=(B1-B2)/B1","fmt":"0.0%"}}}]}}},
    {"tool":"artifacts","action":"create","risk":"low","args":{"client_ref":"$chart","kind":"chart","title":"Margen propuesto vs real",
      "content":{"schema":"aiw.chart/1","mark":"bar","source":{"artifact_id":"$sheet","sheet":"Margen","range":"A1:B3"},"encoding":{"x":"A","y":["B"]}}}},
    {"tool":"artifacts","action":"embed","risk":"low","args":{"into":"art_prop_valeria","after_block":"b4","artifact":"$chart","view":{"height":240}}}
  ],
  "consults": [], "usage": {"model":"sim","input_tokens":0,"output_tokens":0,"cost_usd":0,"duration_ms":0}
}
```
`domain.StructuredOutput` gana `ArtifactRefs []ArtifactRef` (`json:"artifact_refs"`, normalizado a `[]`); Go reemplaza cada `ref` temporal por el `id` real antes de guardar `task.output` y de mandarlo por WS. La herramienta se registra con `ArgResolver` para fijar hechos confiables a la politica: `kind`, `status` actual, `tainted`, tamano del cambio (`cells_changed`, `blocks_changed`), `has_human_versions`.

### 5.3 Versionado, diffs y concurrencia

**Versionado**: `artifacts.head_version` + `artifact_versions` inmutable (sin UPDATE/DELETE para `app_user`, igual que `audit_logs`). Cada version guarda `content` (JSONB; si pasa de 256 KB, en blob con `blob_id`) y las `ops` que la produjeron. Retencion: todas 30 dias; despues se compactan conservando la ultima por dia y las que tengan `named`/estado `approved`/`sent`. Restaurar = nueva version `source:"restore"`.

**Diffs** (`GET /artifacts/{id}/diff?from=&to=`): estructurales por tipo, no texto crudo.

```json
{ "kind":"sheet","from":6,"to":7,
  "changes":[{"sheet":"Margen","cell":"B3","before":{"v":0.31},"after":{"f":"=(B1-B2)/B1","v":0.24},"author":{"kind":"agent","id":"accounting"}}] }
```
doc: lista por `bid` (`added|removed|modified` con diff de palabras); board: tarjetas movidas/creadas; chart: campos del spec; table: filas/celdas.

**Por que no CRDT (recomendacion)**:
1. Los editores del agente son **lotes discretos y validados** (ops), no pulsaciones; un CRDT aporta convergencia a nivel de tecla que no se necesita y sacrifica control: Go debe validar, aplicar politica/autonomia, auditar y poder **rechazar** un lote completo.
2. Go es la fuente de verdad: un CRDT (Yjs) exigiria un servidor Yjs (Hocuspocus en Node) o bindings de `yrs` por cgo y duplicar la validacion de esquema.
3. Hojas con formulas y tablas no tienen buen CRDT de "intencion"; el merge por celda/fila es suficiente y explicable al usuario.
4. Los casos reales son 1 humano + N agentes que trabajan por turnos; los conflictos son raros y se resuelven por region.
Cuando revisar: co-edicion simultanea de varios **humanos** sobre el mismo documento. Entonces (Fase 6) Yjs + `y-prosemirror` + Hocuspocus (todo MIT) solo para `doc`, con snapshots a `artifact_versions` como checkpoint.

**Algoritmo (merge de 3 vias, por tipo, en `backend/internal/artifacts/kinds/<kind>/merge.go`)**:

1. Al iniciar una tarea, Go registra `snapshot[artifact_id] = head_version` de cada artefacto en contexto. Las ops del agente se aplican con `base_version = snapshot` (Go lo impone; el runtime no puede mentir).
2. `head == base` -> aplica.
3. `head > base` -> **rebase**: unidad de conflicto = celda (sheet), `bid` (doc), `row_id+columna` (table), `card id` (board), campo (chart), `event/item id` (agenda/inbox). Unidades tocadas por las ops del agente vs. unidades cambiadas en `(base, head]`: disjuntas -> se aplican sobre el head (version `source:"rebase"`); solapadas -> esas ops se convierten en una **propuesta `conflicted`** con ambos valores; el resto se aplica.
4. Guardado humano (`POST /artifacts/{id}/versions` con `base_version` y `ops` o `content`): mismo merge en servidor. Conflicto -> `409`:
```json
{"code":"conflict","head_version":9,"merged":false,
 "conflicts":[{"unit":"Margen!B3","mine":{"v":0.3},"theirs":{"f":"=(B1-B2)/B1"},"theirs_author":{"kind":"agent","id":"accounting"}}]}
```
   La UI muestra el aviso "Tomas cambio B3 mientras editabas" con "Quedarme con la mia / con la suya".
5. Autoguardado humano: debounce 1.5 s o al perder foco; indicador `art-save-state` (`saved|saving|conflict|offline`). Un guardado sin cambios no crea version. Los guardados humanos consecutivos del mismo autor en menos de 2 min se **coalescen** (actualizan la ultima version si esta es de ese usuario y no hay version de otro autor posterior) para no inflar el historial.
6. **Presencia cosmetica** (no bloquea): mientras una tarea del agente escribe, Go emite `artifact.agent_focus` (efimero) con la region; la UI resalta con el color del rol y banner `art-agent-badge`. Bloqueo duro solo cuando `locked` (estado `approved`/`sent`): los agentes reciben `deny`/propuesta; un humano que edita devuelve el estado a `draft` (con confirmacion).

### 5.4 Eventos WebSocket (aditivos; envelope de 03)

Los datos de contenido **no** viajan completos por WS (>16 KB se envia `ops_ref`/se pide REST). Persistidos (outbox, `seq`) salvo los marcados efimeros.

| Tipo | Payload | Efimero |
|---|---|---|
| `artifact.created` | `{artifact: ArtifactMeta, open_in_workspace: bool}` (`agent_id` en envelope si lo creo un agente) | no |
| `artifact.version_created` | `{artifact_id, version, base_version, author, source, summary, ops?: Op[], ops_ref?: string, changed_units: string[]}` | no |
| `artifact.updated` | `{artifact: ArtifactMeta}` (titulo, estado, adjuntos, bloqueo) | no |
| `artifact.status_changed` | `{artifact_id, from, to, by}` | no |
| `artifact.proposal_created` | `{proposal: ArtifactProposal}` | no |
| `artifact.proposal_resolved` | `{proposal_id, artifact_id, status, resolved_by, version?}` | no |
| `artifact.comment_added` / `artifact.comment_resolved` | `{comment}` / `{comment_id,artifact_id}` | no |
| `artifact.link_added` | `{from, to, relation}` | no |
| `artifact.export_ready` | `{artifact_id, format, blob_id, url, expires_at}` | no |
| `artifact.deleted` | `{artifact_id}` | no |
| `artifact.progress_changed` | `{artifact_id, project_id?, deliverable_id?, task_id?, progress, build_state, note?}` | no (throttle 2/s por artefacto; los cambios de `build_state` nunca se descartan) |
| `artifact.dependency_changed` | `{artifact_id, source_id, source_version, stale: bool}` (el dependiente quedo desactualizado o ya fue recalculado) | no |
| `artifact.recalculated` | `{artifact_id, version, source_id, changed_units}` (version `source:"recalc"`) | no |
| `artifact.agent_focus` | `{artifact_id, agent_id, region?: "Margen!B2:D9"\|"b7", ttl_ms}` | **si** |

Cada `artifact.version_created` y propuesta genera tambien `activity.logged` (texto legible: "Tomas actualizo 'Margen Acme' (v7)") y `audit_logs` (`entity='artifact'`). Topics nuevos para `subscribe`: `artifacts`, `artifact:<id>`. Filtrado por permisos en servidor (03 sec. 5): quien no tiene `artifacts:read` no recibe el evento. `hello` y `resync_required` no cambian; tras resync el cliente recarga `GET /artifacts?open=1` y las pestañas abiertas.

El payload de `task.completed` ya incluye `task.output`, que ahora lleva `artifact_refs` (ids reales).

### 5.5 Endpoints REST (`/api/v1`; permisos entre corchetes)

```
GET    /artifact-kinds                              catalogo + schema_version + suggested del agente (?agent_id=)   [artifacts:read]
GET    /artifact-templates?agent_id=&kind=                                                                         [artifacts:read]
GET    /artifacts?kind=&agent_id=&customer_id=&task_id=&status=&q=&limit=&cursor=                                   [artifacts:read]
POST   /artifacts                {kind,title,content?|template_id?|import_blob_id?,agent_id?,customer_id?,task_id?}  [artifacts:create]
GET    /artifacts/{id}?version=&format=json|text|criticmarkup                                                       [artifacts:read]
PATCH  /artifacts/{id}           {title?,status?,tags?}  If-Match: <head_version>                                  [artifacts:write; status approved/sent: artifacts:approve]
DELETE /artifacts/{id}           (archiva, soft)                                                                    [artifacts:delete]
POST   /artifacts/{id}/versions  {base_version, ops?|content?, summary?}  -> 201 {version, merged, rebased?} | 409   [artifacts:write]
GET    /artifacts/{id}/versions  ; GET /artifacts/{id}/versions/{n} ; GET /artifacts/{id}/diff?from=&to=            [artifacts:read]
POST   /artifacts/{id}/restore   {version}                                                                          [artifacts:write]
GET    /artifacts/{id}/proposals?status=
POST   /artifacts/{id}/proposals {ops,summary,base_version}                (el humano tambien puede "sugerir")      [artifacts:comment]
POST   /artifacts/{id}/proposals/{pid}/decision {decision:"accept"|"reject"|"accept_partial", op_ids?:[], note?}   [artifacts:approve]
GET    /artifacts/{id}/comments ; POST {anchor,text,mention?} ; PATCH /artifacts/{id}/comments/{cid} {resolved}      [artifacts:comment]
PUT    /artifacts/{id}/attachments/{agent_id}  {mode:"none"|"read"|"propose"|"edit"}                                [artifacts:write]
POST   /artifacts/{id}/links     {to,relation,anchor?}                                                              [artifacts:write]
POST   /artifacts/{id}/ask       {agent_id,text?,mode:"review_my_changes"|"free"}  -> 202 {request_id}              [requests:create]
POST   /artifacts/{id}/export    {format:"docx"|"xlsx"|"csv"|"pdf"|"png"|"json",version?} -> 200 blob | 202 {job}    [artifacts:export]
POST   /artifact-blobs           multipart (limite 25 MB) -> {blob_id,sha256,size,mime}                            [artifacts:create]
GET    /artifact-blobs/{id}      redirect/URL firmada TTL 60 s, nosniff, attachment excepto pdf/png                 [artifacts:read]
POST   /artifacts/import         {blob_id, kind?:"auto"} -> 201 artifact (xlsx/csv->sheet|table, docx->doc, pdf->pdf)
GET    /workspaces/{desk}        desk = agent id | "me"   -> {tabs,layout,active}                                    (usuario)
PUT    /workspaces/{desk}        {tabs:[{artifact_id,pinned}],layout:{cols:1|2,split:[id,id]},active}
GET    /projects/{project_id}/workspace  -> {project_ref, deliverables:[{deliverable_id,title,agent_id,status,progress,build_state,artifacts:[ArtifactMeta]}], links:[{from,to,relation,anchor}]}   [artifacts:read]
                                          (el recurso `project` y sus campos los define workflow-visualization.md; aqui solo se agrega esta vista de artefactos)
GET    /artifacts/{id}/links?direction=in|out|both   referencias entrantes/salientes con ancla y estado stale          [artifacts:read]
POST   /artifacts/{id}/refresh-dependencies   fuerza el recalculo de valores cacheados de un dependiente               [artifacts:write]
PATCH  /artifacts/{id}           ahora acepta tambien {project_id?, deliverable_id?}
```
Cambios a endpoints existentes (aditivos): `POST /approvals/{id}/decision` acepta `answers?` (formularios) y `Approval` gana `artifact_ref:{artifact_id,version,proposal_id?}` y `content_hash`; `GET /agents/{id}/detail` agrega `artifacts` (adjuntos recientes). Errores en `application/problem+json` con codigos `conflict`, `invalid_state`, `forbidden`, `validation_failed`, `payload_too_large`.

### 5.6 Tablas Postgres (`backend/internal/infrastructure/postgres/migrations/204_artifacts.sql`)

Convencion real del repo (ids `TEXT`, `org_id TEXT REFERENCES organizations(id)`, RLS `app_current_org()`).

```sql
CREATE TABLE IF NOT EXISTS artifacts (
    org_id          TEXT NOT NULL REFERENCES organizations(id),
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('sheet','doc','table','board','chart','pdf','form','inbox','agenda')),
    schema_version  TEXT NOT NULL,
    title           TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','in_review','approved','sent','archived')),
    head_version    INT  NOT NULL DEFAULT 1,
    created_by_kind TEXT NOT NULL CHECK (created_by_kind IN ('agent','user','system')),
    created_by_id   TEXT NOT NULL,
    task_id         TEXT, request_id TEXT, customer_id TEXT,
    project_id      TEXT,                        -- opacos, sin FK: los define workflow-visualization.md
    deliverable_id  TEXT,
    progress        INT NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    build_state     TEXT NOT NULL DEFAULT 'done' CHECK (build_state IN ('queued','building','ready_for_review','done','blocked')),
    locale          TEXT NOT NULL DEFAULT 'es' CHECK (locale IN ('es','en')),
    trust           TEXT NOT NULL DEFAULT 'internal' CHECK (trust IN ('internal','user','external_untrusted')),
    tainted         BOOLEAN NOT NULL DEFAULT false,
    locked          BOOLEAN NOT NULL DEFAULT false,
    size_bytes      INT NOT NULL DEFAULT 0,
    tags            JSONB NOT NULL DEFAULT '[]',
    document_id     TEXT,                       -- vinculo opcional con documents (archivos externos)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at     TIMESTAMPTZ,
    UNIQUE (org_id, id)
);
CREATE INDEX IF NOT EXISTS artifacts_org_kind_idx ON artifacts (org_id, kind, updated_at DESC);
CREATE INDEX IF NOT EXISTS artifacts_org_task_idx ON artifacts (org_id, task_id);
CREATE INDEX IF NOT EXISTS artifacts_org_project_idx ON artifacts (org_id, project_id, deliverable_id);

CREATE TABLE IF NOT EXISTS artifact_versions (   -- inmutable
    org_id TEXT NOT NULL REFERENCES organizations(id),
    artifact_id TEXT NOT NULL,
    version INT NOT NULL,
    base_version INT,
    author_kind TEXT NOT NULL CHECK (author_kind IN ('agent','user','system')),
    author_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('agent_task','human_edit','accept_proposal','import','restore','rebase','recalc','create')),
    task_id TEXT,
    content JSONB,                 -- NULL si va en blob
    blob_id TEXT,
    ops JSONB,                     -- ops que produjeron la version (para diffs y eventos)
    content_hash TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    has_pending_suggestions BOOLEAN NOT NULL DEFAULT false,
    named TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_id, version),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE,
    CHECK (content IS NOT NULL OR blob_id IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS artifact_proposals (
    org_id TEXT NOT NULL REFERENCES organizations(id),
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL,
    base_version INT NOT NULL,
    author_kind TEXT NOT NULL, author_id TEXT NOT NULL, task_id TEXT,
    summary TEXT NOT NULL DEFAULT '',
    ops JSONB NOT NULL,            -- cada op lleva "op_id" para aceptacion parcial
    render TEXT NOT NULL DEFAULT 'overlay' CHECK (render IN ('overlay','marks')),   -- marks = doc con suggestion marks
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','partial','superseded','conflicted')),
    conflicts JSONB,
    resolved_by TEXT, resolved_note TEXT, resolved_version INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), resolved_at TIMESTAMPTZ,
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifact_comments (
    org_id TEXT NOT NULL REFERENCES organizations(id),
    id TEXT PRIMARY KEY, artifact_id TEXT NOT NULL, parent_id TEXT,
    anchor JSONB NOT NULL,         -- {"bid":"b7a","quote":"15%"} | {"sheet":"Margen","cell":"B3"} | {"card":"k1"} | {"page":3,"rect":[..]}
    author_kind TEXT NOT NULL, author_id TEXT NOT NULL,
    body TEXT NOT NULL, mentions JSONB NOT NULL DEFAULT '[]',
    resolved BOOLEAN NOT NULL DEFAULT false, resolved_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifact_links (       -- incrustaciones, fuentes y derivaciones (grafo sin ciclos)
    org_id TEXT NOT NULL REFERENCES organizations(id),
    from_id TEXT NOT NULL, to_id TEXT NOT NULL,
    relation TEXT NOT NULL CHECK (relation IN ('embeds','source_of','derived_from','refers_to')),
    anchor JSONB,                              -- {"sheet":"Result","range":"B12"} | {"bid":"b7"} | {"card":"k1"}
    alias TEXT,                                -- alias usado por AIW_REF("<alias>", ...)
    pinned_version INT,                        -- NULL = vivo
    stale BOOLEAN NOT NULL DEFAULT false,      -- el origen avanzo y el dependiente aun no se recalculo
    last_source_version INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (from_id, to_id, relation),
    FOREIGN KEY (org_id, from_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, to_id)   REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifact_attachments ( -- que agente ve/edita que (capacidad por adjunto)
    org_id TEXT NOT NULL REFERENCES organizations(id),
    artifact_id TEXT NOT NULL, agent_id TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('read','propose','edit')),
    attached_by TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_id, agent_id),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifact_agent_cursors ( -- ultima version que el agente "vio" (para el contexto de ediciones humanas)
    org_id TEXT NOT NULL REFERENCES organizations(id),
    artifact_id TEXT NOT NULL, agent_id TEXT NOT NULL,
    last_seen_version INT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_id, agent_id)
);

CREATE TABLE IF NOT EXISTS artifact_blobs (
    org_id TEXT NOT NULL REFERENCES organizations(id),
    id TEXT PRIMARY KEY, sha256 TEXT NOT NULL, size_bytes BIGINT NOT NULL, mime TEXT NOT NULL,
    storage_uri TEXT NOT NULL,     -- local://<org>/<sha[:2]>/<sha> | s3://bucket/<org>/<sha[:2]>/<sha>
    scan_status TEXT NOT NULL DEFAULT 'pending' CHECK (scan_status IN ('pending','clean','rejected')),
    created_by TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, sha256)
);

CREATE TABLE IF NOT EXISTS workspace_layouts (
    org_id TEXT NOT NULL REFERENCES organizations(id),
    user_id TEXT NOT NULL, desk TEXT NOT NULL,         -- agent id | 'me'
    layout JSONB NOT NULL DEFAULT '{}', updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id, desk)
);

-- RLS (mismo patron que 202_rls_policies.sql)
DO $$
DECLARE t TEXT;
BEGIN
  FOREACH t IN ARRAY ARRAY['artifacts','artifact_versions','artifact_proposals','artifact_comments',
                           'artifact_links','artifact_attachments','artifact_agent_cursors','artifact_blobs','workspace_layouts']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
  END LOOP;
END $$;

-- Las versiones son append-only para la aplicacion (la compactacion corre con rol de mantenimiento)
DO $$ BEGIN
  EXECUTE format('REVOKE UPDATE, DELETE ON TABLE %I.artifact_versions FROM app_user', current_schema());
END $$;
```
Migracion `down` en `migrations/down/204_artifacts_down.sql`. Tests: ampliar `pg_integration_test` con aislamiento (org A no ve `artifacts` de org B; sin `app.org_id` => 0 filas) y un test de que `UPDATE artifact_versions` falla. Tambien hay que implementar los metodos en `infrastructure/memory/store.go` (modo sin Postgres) para que el demo y los tests sigan funcionando.

### 5.7 Almacenamiento de blobs

```go
// backend/internal/artifacts/blobs.go
type BlobStore interface {
    Put(ctx context.Context, orgID string, r io.Reader, maxBytes int64) (BlobInfo, error) // calcula sha256 en streaming, dedupe por (org, sha256)
    Open(ctx context.Context, orgID, sha256 string) (io.ReadCloser, error)
    SignedURL(ctx context.Context, orgID, sha256 string, ttl time.Duration, disposition string) (string, error)
    Delete(ctx context.Context, orgID, sha256 string) error
}
```
Implementaciones: `FSBlobStore` (dev; volumen Docker `blobs:/data/blobs`, ruta `/{org}/{sha[:2]}/{sha}`), `S3BlobStore` (`minio-go`, compatible S3/MinIO/R2). Config: `BLOB_STORE=fs|s3`, `BLOB_DIR`, `S3_ENDPOINT/S3_BUCKET/...`. Los blobs son **inmutables y por contenido**; version de artefacto referencia `blob_id`. Origen: imports originales (se conserva el xlsx/docx subido), PDFs, imagenes de docs (solo por `blob_id`, nunca URL remota), exports. Cuota por org (`ARTIFACT_STORAGE_QUOTA_MB`) y recoleccion de blobs huerfanos por job nocturno. Los PDF/docx subidos pasan a `scan_status=pending` (hook para ClamAV opcional) y no se sirven hasta `clean`.

Servicios nuevos (Fase 2/4, compose): `export-worker` (Node, TypeScript, comparte `adapters` con el frontend; recibe contenido canonico por HTTP interno y devuelve docx/png via `docx` y ECharts SSR; sin red externa ni secretos) y `gotenberg` (docx->pdf). Fase 1 exporta docx **en el navegador** (sin worker) y xlsx en Go.

### 5.8 Contrato con proyectos y entregables (paralelismo)

Se asume (terminologia final en workflow-visualization.md): un **proyecto** agrupa **entregables**; cada entregable lo produce una tarea/paso de un agente y se materializa en uno o mas artefactos. Esta spec solo exige:

1. **Ligadura**: `artifacts.project_id` / `deliverable_id` (opacos). Al crear con `artifacts.create` dentro de una tarea que ya tiene entregable, Go las **hereda de la tarea** (el runtime no las elige); `bind` solo sirve para reasignar. El plan (planner o DSL de `05-workflows.md`) puede declarar por paso `"deliverable": {"id": "...", "artifact_kind": "sheet", "template_id": "balance_sheet"}`; si lo hace, Go **crea el artefacto vacio en `build_state=queued`** al crearse la tarea (aparece de inmediato en el escritorio, con progreso 0).
2. **Paralelismo**: cada tarea paralela escribe en su propio artefacto con su propio `snapshot` de versiones (5.3), asi que balance, estado de resultados y flujo de efectivo se construyen sin conflicto. Si la politica de concurrencia del scheduler serializa por agente, los entregables restantes se ven `queued`; el workspace no cambia.
3. **Progreso por pestaña**, en orden de prioridad: (a) `set_progress` del agente (acotado por Go), (b) estimacion de Go = proporcion de secciones/hojas completadas segun `template` (checklist del entregable), (c) `task.progress` (SPEC `agent.progress`) si hay una sola tarea; al terminar la tarea `build_state` pasa a `ready_for_review` (si requiere revision humana) o `done`. Las pestañas leen `artifact.progress_changed`.
4. **Contexto del proyecto al runtime** (aditivo en `RunContext`): `project: {id, deliverables:[{id,title,agent_id,status,progress,artifact_ids}]}` y, en cada artefacto de contexto, `deliverable_id` y `depends_on_artifacts` — el agente sabe que su hoja depende de otra aun en construccion (`build_state:"building"`) y puede dejar la celda con `AIW_REF` pendiente en lugar de inventar el valor.
5. **Finalizacion**: el entregable pasa a `done` cuando su artefacto principal alcanza `build_state=done` y no tiene propuestas pendientes ni dependencias `stale`; ese cambio se reporta al motor de workflows para avanzar pasos que dependan de el (`artifact.progress_changed` + el `task.completed` ya existente; la integracion exacta con el estado del entregable es de workflow-visualization.md).

### 5.9 Referencias entre artefactos

Tres mecanismos, todos registrados en `artifact_links` (grafo **sin ciclos**, profundidad de cadena <= 5, validado en `link`/`apply_ops`):

1. **Celda -> hoja** (balance lee estado de resultados). El contenido de la hoja declara importaciones y usa la funcion segura `AIW_REF`:
```jsonc
{ "schema":"aiw.sheet/1",
  "imports":[{"alias":"IS","artifact_id":"art_income_stmt","pinned_version":null}],
  "sheets":[{"id":"s1","name":"Balance","cells":{
     "B20":{"f":"=AIW_REF(\"IS\",\"Result!B12\")","v":120000,"fmt":"$#,##0"},     // utilidad neta del estado de resultados
     "B22":{"f":"=B18+B20","v":2120000}}}] }
```
   - `imports` genera/actualiza filas `artifact_links (relation='source_of', alias='IS', anchor={sheet,range})`. `AIW_REF(alias, "Sheet!A1")` (o rango, devuelve matriz) esta en la lista blanca del `formula_guard`; sigue prohibido todo acceso a archivos/URLs/`INDIRECT` (el alias es estatico, no una cadena calculada).
   - Resolucion: el frontend registra `AIW_REF` como funcion personalizada de Univer (`univerAdapter`) leyendo los valores cacheados del artefacto origen; Go, antes de `excelize.CalcCellValue`, **pre-sustituye** cada `AIW_REF` por el valor cacheado del origen (version viva o `pinned_version`). Requiere `artifacts:read` sobre el origen; sin permiso => `#REF!` y `403` al declarar el import.
   - **Propagacion**: cuando el origen crea version, Go marca `stale=true` en los enlaces `source_of` no fijados, emite `artifact.dependency_changed` y encola un **recalculo del dependiente** (version `source:"recalc"`, autor `system`, solo cambia valores cacheados, `artifact.recalculated`). Si el dependiente esta en edicion humana, no se pisa: queda `stale` y la UI ofrece "Refrescar" (`POST /artifacts/{id}/refresh-dependencies`). Un dependiente en estado `approved|sent` no se recalcula solo (aviso).
2. **Nota -> hoja/otro artefacto** (documento enlaza a hoja): nodo/mark `artifact_link` en `aiw.doc/1`:
```json
{"type":"text","text":"ver balance general","marks":[{"type":"artifact_link","attrs":{"artifact_id":"art_balance","anchor":{"sheet":"Balance","range":"B20:B22"},"pinned_version":null}}]}
```
   Registra `artifact_links (refers_to)`; clic abre/enfoca la pestaña y selecciona el ancla. Diferencia con `embed`: `embed` renderiza el contenido dentro del doc; `artifact_link` solo navega.
3. **Tarjeta/entregable -> artefacto**: `board.cards[].link` y el panel del proyecto (5.8) usan la misma tabla.

Exportacion: `AIW_REF` se exporta a xlsx como **valor** (con comentario "origen: <titulo>"), nunca como formula externa; `artifact_link` exporta como texto + nota al pie.

---

## 6. Humano y agente: edicion, contexto, sugerencias y aprobaciones

### 6.1 El agente ve lo que edito el humano

Antes de cada `/v1/run-task`, `application/artifact_context.go` arma `context.artifacts` con los artefactos que cumplan cualquiera de: referenciados por la tarea (`task.artifact_ids`/dependencias), adjuntos del agente (`artifact_attachments`), o vinculados. **Aditivo** a `RunContext`:

```json
"artifacts": [{
  "id": "art_sheet1", "kind": "sheet", "title": "Margen Acme", "version": 9, "status": "draft",
  "access": "edit", "trust": "user", "tainted": false,
  "content_text": "Hoja Margen (A1:C13)\n| Concepto | Valor |\n| Ingresos | 50000 |\n| Margen | =(B1-B2)/B1 -> 0.24 |",
  "truncated": false,
  "human_changes_since_last_seen": {
    "from_version": 7, "to_version": 9,
    "changes": [{"author":{"kind":"user","id":"u_1"},"at":"...","summary":"Cambio B2 de 38000 a 41000","units":["Margen!B2"]}],
    "diff_text": "B2: 38000 -> 41000 (usuario)"
  },
  "open_proposals": [{"id":"prop_4","author":"legal","summary":"..."}],
  "comments_open": [{"id":"c_1","anchor":"b7a","author":"user","text":"@Elena revisa este monto"}]
}]
```
- Se serializa como **dato delimitado y no confiable** (`<untrusted_data source="artifact:art_sheet1" trust="user">`, 07 sec. 2.1), con escape del delimitador. Si `tainted` o `trust=external_untrusted` => heuristica de inyeccion y aprobacion elevada (sec. 7).
- Renderizado a texto por tipo: `sheet`/`table` -> tabla Markdown del rango usado con formulas y valor calculado; `doc` -> Markdown con **CriticMarkup** (`{++ins++}`, `{--del--}`, `{>>comentario<<}`) para que el modelo vea cambios sugeridos y comentarios; `board` -> lista por columna; `chart` -> spec + datos; `form` -> campos y respuestas; `inbox`/`agenda` -> lista.
- Presupuesto de tokens: `ARTIFACT_CONTEXT_TOKENS` (defecto 6000 por tarea, 2500 por artefacto); si excede: esquema/indice + zonas cambiadas + `truncated:true` (en fases posteriores el agente pide mas con `artifacts.read{range}` en un ciclo de herramientas).
- Tras ejecutarse la tarea Go avanza `artifact_agent_cursors.last_seen_version` al `head` visto. Los costos de ese contexto entran en `usage` como cualquier otro.
- UI: banner "Tomas vera tus cambios en su proxima tarea" (si hay `head > last_seen`) y boton **Pedir revision** (`art-ask-agent`) que llama `POST /artifacts/{id}/ask` => crea una `Request` normal cuyo plan incluye ese artefacto (modo `review_my_changes` prellena "Revisa mis cambios desde la v7").
- Un comentario con `@Agente` crea una tarea `reply_to_comment` para ese agente (sujeta a presupuesto/permisos como cualquier otra tarea). Un rechazo con nota escribe una memoria `kind:'lesson'` del agente (04) para que no repita el error.

### 6.2 Escritura directa vs. propuesta (lo decide Go)

`artifacts.apply_ops` pasa por `policy.Engine` con hechos confiables del `ArgResolver`. Resultado:

| Condicion | Resultado |
|---|---|
| `autonomy = suggest` | Siempre **propuesta** en artefactos existentes; `create` permitido (borrador propio) |
| `autonomy = approve_each` | Propuesta si el artefacto tiene versiones humanas o estado != `draft`; directo en borradores propios del agente |
| `autonomy = rules` | Directo si `draft`, no `locked`, adjunto `edit`, cambio pequeno (<=200 celdas / <=30 % de bloques / <=20 tarjetas) y no `tainted`; si no, propuesta |
| `autonomy = autonomous` | Directo salvo `locked`/`approved`/`sent`/adjunto `read|propose` (entonces propuesta o `deny`) |
| Adjunto `read` | `deny`; `propose` => siempre propuesta; `edit` => segun filas anteriores |
| Conflicto de merge | Propuesta `conflicted` |
| Formula/ops invalidas | `deny` + `findings` (nunca se ejecuta parcialmente un lote invalido: validacion atomica) |

Propuesta = fila `artifact_proposals` + evento `artifact.proposal_created`. Render:
- **doc**: se materializa como marcas `suggestion` (ins/del con autor, `sid`, motivo) en una nueva version con `has_pending_suggestions=true`; `ProposalBar` ofrece "Aceptar todo / Rechazar todo / Revisar una por una"; aceptar = quitar marca (conservar `ins`, borrar `del`), rechazar = inverso; cada accion crea version `accept_proposal` y la propuesta pasa a `accepted|rejected|partial`.
- **sheet/table/board/chart/agenda/inbox**: **overlay** fantasma sobre la vista (celdas con valor propuesto en ambar, tarjetas punteadas, eventos punteados) con aceptar/rechazar por region u operacion (`op_ids`); al aceptar se aplican las ops (con merge si el head avanzo).
- Una propuesta cuyo `base_version` quedo obsoleto y se solapa pasa a `conflicted`; si el humano la ignora tras reescribir esa zona, a `superseded`.

### 6.3 Estados y aprobaciones

- `draft -> in_review -> approved -> sent`; solo humano con `artifacts:approve` aprueba; `sent` lo fija la accion ya existente `documents.send_proposal/send_contract` tras aprobarse. `approved|sent` => `locked`; editar crea nueva version y regresa a `draft` (aviso).
- **Aprobacion de acciones sobre artefactos**: `send_proposal`, `send_contract`, `artifacts.export` (high) generan `Approval` con `artifact_ref {artifact_id, version}` y `content_hash` incluido en el `args_hash` de 06 (si el contenido cambia tras pedir la aprobacion, la aprobacion se invalida con `409 invalid_state`). La tarjeta muestra el artefacto en esa version exacta.
- **Formularios**: `request_input` crea un `form` + `Approval` con `action:"request_input"`; `POST /approvals/{id}/decision {decision:"approve", answers:{...}}` guarda `answers` en una nueva version del form y reanuda la tarea con las respuestas como contexto. Es el unico camino de "aprobacion rica" (montos, checklist).
- La bandeja actual (`ApprovalsTray`) no cambia; las propuestas de edicion se cuentan aparte (`ws-proposals-count` en la cabecera del escritorio y en `Dashboard > files`), sin mezclarse con `approval-<id>`.

---

## 7. Seguridad y permisos

### 7.1 Permisos por capacidad

Nuevos permisos en `auth/rbac.go` (`<recurso>:<accion>`):

| Permiso | Humanos (rol) | Agentes |
|---|---|---|
| `artifacts:read` | viewer, member, admin, owner | `agents.permissions` |
| `artifacts:create` | member+ | por defecto si |
| `artifacts:write` (editar/versionar/restaurar) | member+ | segun adjunto y autonomia |
| `artifacts:comment` (comentar y sugerir) | member+ (viewer no) | por defecto si |
| `artifacts:approve` (aceptar propuestas, `approved/sent`) | admin, owner (y `approver`) | **nunca** (los agentes no se aprueban a si mismos ni entre si) |
| `artifacts:export` | member+ | via `tool:artifacts.export` (riesgo alto => politica/aprobacion) |
| `artifacts:delete` | admin+ (autor puede archivar el suyo) | no |

La autoridad efectiva de una accion del agente es la **interseccion** de: permisos del agente, `artifact_attachments.mode`, permisos del usuario `OnBehalfOf` (06 sec. 1) y las reglas de autonomia. **Ningun permiso depende del tipo de artefacto ni del rol del agente.** Restriccion opcional por org: `agent_tools.constraints.artifact_kinds` / `customer_scope`. Visibilidad por cliente: un artefacto con `customer_id` solo entra al contexto de tareas de ese cliente (regla de 04: "cliente A nunca en contexto de B"). ACL fina por artefacto (grants a usuarios) queda en Fase 5; antes, visibilidad = org.

### 7.2 Sanitizacion y validacion de contenido (en Go, siempre; el cliente no es confiable)

| Riesgo | Control |
|---|---|
| XSS en documentos | Nunca se guarda HTML. `aiw.doc/1` es JSON validado contra lista blanca de nodos (`paragraph, heading, bulletList, orderedList, listItem, blockquote, codeBlock, table*, hardBreak, clause, embed, image`) y marcas (`bold, italic, underline, strike, code, link, suggestion, comment`). Nodo/mark desconocido => rechazo del lote. `link.href` solo `https:`/`http:`/`mailto:` (nada de `javascript:`/`data:`), `rel="noopener noreferrer nofollow"`, se muestra el dominio visible. |
| Exfiltracion por imagen/URL remota (07 T7) | `image.src` solo `blob:<id>` propio; sin `<img>` remoto ni iframes ni CSS arbitrario; el visor de importaciones pasa por `DOMPurify` (solo navegador) y luego por el esquema. |
| Formulas peligrosas | `formula_guard.go` tokeniza cada formula y aplica **lista blanca** (~150 funciones: matematicas, texto, logicas, busqueda, fecha, financieras). Bloqueadas siempre: `HYPERLINK`, `WEBSERVICE`, `FILTERXML`, `IMPORT*` (`IMPORTDATA/XML/HTML/RANGE/FEED`), `INDIRECT` y `OFFSET` (volatiles/dinamicas; opcional por org), **excepto** `AIW_REF(alias,"Sheet!A1")` con alias estatico declarado en `imports` (5.9), `CALL`, `REGISTER.ID`, `EXEC`, `RTD`, `DDE`, referencias externas (`[libro]Hoja!A1`, `'C:\...'`, `file:`, URLs) y nombres definidos que las oculten. Limite de longitud 8 192, profundidad de anidacion 64, referencias circulares marcadas `#CIRC!`. |
| Inyeccion de formulas al exportar (CSV/xlsx) | Celdas de **texto** que empiecen con `= + - @ \t \r` se exportan como string con prefijo `'`/tipo string; las formulas legitimas se exportan como formula solo si pasaron el guard. |
| Importacion xlsx/docx hostil | Magic bytes + extension coherentes; rechazo de `.xlsm/.docm/.xlsb`, `vbaProject.bin`, `xl/externalLinks/*`, OLE/embeddings, `w:altChunk`, relaciones externas (`TargetMode="External"`), DDE; ZIP-bomb: <=2 000 entradas, <=50 MB descomprimido, ratio <=100:1; XML sin entidades externas (DTD deshabilitado); tiempo maximo de parseo 10 s. |
| PDF | Solo visor; sin JS (`isEvalSupported:false`, sin acciones), limite 25 MB / 500 paginas, worker aislado; enlaces mostrados con dominio y confirmacion. |
| Servir blobs | URL firmada de 60 s, `Content-Disposition: attachment` (excepto pdf/png inline), `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox`, dominio/ruta separado de la app. |
| Prompt injection via artefactos | Todo contenido que sube al runtime va delimitado (6.1). `tainted` se propaga: si una tarea recibio `external_content` o artefactos `external_untrusted`, los artefactos que crea heredan `tainted=true`; `tainted` + exportar/enviar => `require_approval` obligatorio y `security.alert` si la heuristica puntua alto. El texto de un documento jamas se interpreta como orden. |
| Abuso de escritura del agente | Atomicidad por lote (todo o nada), maximo 500 ops y 5 000 celdas por llamada, 20 `apply_ops` por tarea, token bucket por agente en Redis (clave con `org_id`); fuera de limite => `deny` + `findings`. |

### 7.3 Limites de tamano (configurables por entorno)

| Recurso | Limite |
|---|---|
| `sheet` | 5 MB JSON, 200 000 celdas no vacias, 50 hojas, 1 048 576 filas lógicas |
| `doc` | 1 MB JSON, 5 000 bloques, 20 embeds |
| `table` | 50 000 filas x 100 columnas (>10 000 filas: servidas por paginas) |
| `board` | 2 000 tarjetas, 50 columnas |
| `chart` | 50 000 puntos tras resolver `source` |
| Upload (blob) | 25 MB (xlsx/docx/pdf/csv), imagenes 5 MB |
| Versiones | contenido >256 KB va a blob; historial: ver 5.3 |
| Cuota por org | `ARTIFACT_STORAGE_QUOTA_MB` (defecto 1024) |

Toda accion de escritura/aprobacion/exportacion se audita (`audit_logs`, `entity='artifact'`) con autor, version y `content_hash`; el redactor central de 07 enmascara secretos y patrones sensibles en logs (no en el contenido del usuario).

---

## 8. Plan de fases

Estimacion en **dias-persona** (1 dev backend Go + 1 frontend; las tareas FE y BE de una fase corren en paralelo). Orden: valor temprano primero.

### Fase 0 - Cimientos (5-6 d) [BE 3 / FE 3]
Objetivo: plomeria sin UI visible nueva salvo el boton "Archivos".
- BE: `domain/artifact.go` (tipos, constantes de eventos `artifact.*`); `artifacts/service.go` (create/get/list/version/restore, merge3 interfaz); `artifacts/blobs.go` (`FSBlobStore`); migracion `204_artifacts.sql` + down; metodos en `infrastructure/postgres` y `memory/store.go`; permisos `artifacts:*` en `auth/rbac.go`; `api/artifacts_handlers.go` + rutas en `router.go` (`GET/POST /artifacts`, `GET /artifacts/{id}`, versions); publicacion de eventos via bus existente.
- FE: `lib/types.ts` (tipos), `lib/artifacts.ts` (slice, api, eventos en `ws.ts`), `workspace/{WorkspaceHost,WorkspaceTabs,ArtifactPane,registry}.tsx`, pestaña **Archivos** en `AgentPanel.tsx`, `--panel-w` en `Shell.tsx`, mock en `lib/mock/engine.ts`.

### Fase 1 - Hoja + documento + entregables en paralelo (valor inmediato) (22-26 d) [BE 10-11 / FE 12-15]
Entregable: agentes crean/editan **hojas** y **documentos**, varios a la vez (un artefacto por entregable, con progreso por pestaña); el humano los abre, edita (autoguardado) y ve versiones; docx/xlsx import-export; el agente ve las ediciones humanas.
- BE (8-9 d): `artifacts/kinds/sheet` (validacion, ops, merge celda, texto) + `formula_guard.go` + recalculo `excelize`; `kinds/doc` (esquema ProseMirror whitelisted, ops, merge por `bid`, render CriticMarkup/Markdown); `tools/artifacts.go` (`create/read/apply_ops/list`) + `ArgResolver` + registro en el `Registry` + hechos de politica (`policy/facts.go`); `application/artifact_context.go` + `RunContext.artifacts`; `StructuredOutput.ArtifactRefs`; cursores `last_seen`; `POST /artifacts/import` (xlsx/csv) y `/export` xlsx; `POST /artifacts/{id}/ask`; `PUT attachments`; adaptar `tools/documents.go`/`spreadsheet_calc.go` para que `create/draft/write` creen artefactos.
- Runtime (2 d, dentro de BE): `models.py` (`ArtifactRef`, request/response), `simulation.py`/`sim_content.py` para el escenario de $50,000 (ver 8.1).
- FE (10-13 d): `SheetEditor` + `univerAdapter` (spike tema 2 d), `DocEditor` (TipTap v3 sin sugerencias; edicion simple, tablas, clausulas) + `tiptapAdapter`, import docx (`mammoth`+`dompurify`), export docx (`docx`, carga diferida), `NewArtifactMenu`, `AttachControl`, `VersionHistory` basico, banner "vera tus cambios", `AgentPresence`, camara y animacion `dock->expanded`, chip `ws-chip-<id>`, seccion **Archivos** del dashboard.
- Proyectos/entregables (incluido arriba, +3-4 d): columnas `project_id/deliverable_id/progress/build_state/locale` (ya en la migracion), herencia desde la tarea, creacion de artefactos `queued` desde el plan, tool `set_progress`/`bind`, evento `artifact.progress_changed`, `TabProgressRing`, `ProjectSummary` basico (lista de entregables + progreso) y `DeliverableNav`; i18n (`es`/`en`) y plantillas localizables (`balance_sheet`, `income_statement`, `cash_flow`, `accounting_policies`, `bank_reconciliation`).
- Paquetes: `@univerjs/presets`, `@univerjs/preset-sheets-core`, `@univerjs/preset-sheets-formula`, `@univerjs/preset-sheets-numfmt` (versiones **exactas**), `@tiptap/react@^3`, `@tiptap/pm@^3`, `@tiptap/starter-kit@^3`, `@tiptap/extension-table@^3`, `@tiptap/extension-link@^3`, `@tiptap/extension-underline@^3`, `mammoth@^1.8`, `docx@^9`, `dompurify@^3.2`, `lucide-react@^0.4xx`. Go: `github.com/xuri/excelize/v2`.
- **Criterio de salida**: demo del escenario con hoja de margen creada por Tomas y contrato por Elena abiertos en sus escritorios; edicion humana de una celda aparece como contexto en la siguiente tarea.

### Fase 2 - Propuestas, cambios sugeridos, comentarios, aprobaciones y referencias entre artefactos (18-22 d) [BE 8-10 / FE 10-12]
- BE: `artifact_proposals` + `/proposals` + `/decision`; autonomia -> directo/propuesta; `kinds/doc` suggestion marks (ins/del) y aceptacion parcial; comentarios + menciones -> tarea; estados `draft..sent` + `locked`; `Approval.artifact_ref`/`content_hash` + `args_hash`; `export-worker` (Node: docx con `w:ins/w:del`, PNG de graficas) y export docx server-side.
- Referencias (+4 d): `AIW_REF` (guard, pre-sustitucion en Go, funcion personalizada en Univer), `artifact_links` con `alias/stale`, propagacion (`artifact.dependency_changed`, recalculo `source:"recalc"`), `ArtifactLinkMark`, `LinkedArtifacts`, insignia `ws-dependency-badge-*`, `GET /artifacts/{id}/links`.
- FE: extensiones TipTap `SuggestionMark`, `CommentMark`, `ClauseNode` (spike de 2 d vs Plate), `ProposalBar`, `CommentsRail`, overlay de propuesta en hoja, `VersionHistory` con diff, conflicto 409 (UI "mia/suya"), tarjeta de aprobacion con version exacta.
- **Criterio de salida**: el balance lee el estado de resultados con `AIW_REF` y se recalcula al cambiar este; Legal propone redlines en el contrato; el humano acepta/rechaza una por una; exportar a docx conserva cambios sugeridos y comentarios.

### Fase 3 - Tabla, tablero, grafica, incrustacion, pestañas multiples/split (14-18 d) [BE 5-6 / FE 9-12]
- BE: `kinds/{table,board,chart}`, `artifact_links` + validacion de ciclos y permisos, `embed`, export CSV.
- FE: `TableEditor` (TanStack), `BoardEditor` (dnd-kit), `ChartEditor` + `echartsAdapter` (ECharts), nodo TipTap `Embed` con vistas por tipo, split de 2 columnas, `WorkspaceTabs` completo, persistencia `workspace_layouts`, `ProjectSummary` completo (grafo de dependencias reutilizando el componente de workflow-visualization.md, `GET /projects/{id}/workspace`), agrupacion por proyecto en el dashboard.
- Paquetes: `@tanstack/react-table@^8.20`, `@tanstack/react-virtual@^3`, `@dnd-kit/core@^6.3`, `@dnd-kit/sortable@^10`, `echarts@^5.6`.
- **Criterio de salida**: una grafica de la hoja del Contador incrustada en la propuesta de Ventas, viva (cambia al editar la hoja); un agente tiene 3 pestañas de tipos distintos.

### Fase 4 - PDF, formularios, bandeja y agenda (+ PDF export) (10-13 d) [BE 4-5 / FE 6-8]
- BE: `kinds/{pdf,form,inbox,agenda}`, `request_input` + `decision.answers`, Gotenberg en compose, export PDF, escaneo de blobs `scan_status`, `S3BlobStore`.
- FE: `PdfViewer` (`react-pdf@^9`), `FormRenderer` (`react-hook-form@^7.54`, `zod@^3.24`), `InboxView`, `AgendaView` (`@fullcalendar/react@^6.1`, `daygrid`, `timegrid`, `interaction`), plantillas por rol.
- **Criterio de salida**: la Asistente triagea la bandeja y propone eventos; un agente pide datos con un formulario y reanuda la tarea.

### Fase 5 - Pulido, colaboracion y gobierno (12-15 d)
- Notebooks (bloques `cell` en `doc`: markdown/consulta/grafica), parser propio de cambios sugeridos docx (`jszip`, `fast-xml-parser`), ACL por artefacto, retencion/compactacion, busqueda full-text (`pg_trgm`), metricas de uso, accesibilidad (teclado en tablero y propuestas), rendimiento (virtualizacion, carga diferida).

### Fase 6 (opcional) - Co-edicion multi-humano
Yjs + `y-prosemirror` + Hocuspocus solo para `doc`, con checkpoints a `artifact_versions` (decision en 5.3).

Resumen: F0 5-6 d, F1 22-26 d, F2 18-22 d, F3 14-18 d, F4 10-13 d, F5 12-15 d => aprox. 82-100 dias-persona (≈ 11-13 semanas con 2 personas, F1 entregable a las ~5-6 semanas). Estimaciones de orden de magnitud.

### 8.1 Escenario de simulacion (demo sin API key)

Para que el demo y los E2E sean deterministas, `agent-runtime/app/simulation.py` emite `tool_requests` de `artifacts` en el escenario de $50,000 y demuestra la mezcla de tipos entre roles:
- Valeria (ventas): `doc` "Propuesta Acme - Rediseno" (con `embed` de tabla de inversion) y `board` "Pipeline" (tarjeta Acme).
- Tomas (contador): `sheet` "Margen Acme" (31 % -> 24 %) **y** `doc` "Nota de margen" que incrusta el rango de la hoja.
- Nadia (analista): `chart` "Rentabilidad" fuente = la hoja de Tomas, incrustada en la propuesta de Valeria (`embed`).
- Elena (legal): `apply_ops` sobre el contrato con `suggest_replace` en la clausula 7 (propuesta con marcas) **y** `table` "Matriz de riesgos".
- Ivan (operaciones): `board` checklist "Contratar 2 personas" **y** `sheet` de capacidad.
- Sofia (asistente): `doc` reporte final con embeds de grafica y hoja; `inbox` del dia.

**Proyecto paralelo "Cierre financiero"** (segundo escenario guionado, p. ej. "prepara el cierre del trimestre"): el planificador crea 3 entregables del Contador en **paralelo** mas 1 de sintesis, todos con `deliverable_id`:
- `balance_sheet` (`sheet`, `build_state` building 0 -> 100), `income_statement` (`sheet`) y `cash_flow` (`sheet`) avanzan a la vez con `artifact.progress_changed` guionados (p. ej. 20/45/70/100 con latencias distintas).
- El balance usa `AIW_REF("IS","Result!B12")` hacia el estado de resultados; mientras el origen esta en construccion su celda queda pendiente (`stale`), y al llegar la version final del estado de resultados se recalcula (`artifact.recalculated`).
- Un `doc` "Politicas contables" (nota) con `artifact_link` hacia `Balance!B20:B22` y un `chart` incrustado del estado de resultados.
- Una tarea de sintesis (Asistente) depende de los tres entregables; el panel del proyecto muestra 4 entregables con progreso.
Todos los textos del simulador existen en `es` y `en` (localizables) y las plantillas usan los ids `balance_sheet`, `income_statement`, `cash_flow`, `accounting_policies`.

Los ids de artefactos de simulacion son estables (`art_demo_*`) y `POST /demo/reset` los limpia y resiembra.

---

## 9. Criterios de aceptacion (Playwright, `e2e/tests/`)

Convencion: testids nuevos con prefijo `ws-`, `art-`, `dash-` (**nunca** `agent-`/`approval-`). Los editores canvas (Univer) no exponen celdas en el DOM: se usan `art-sheet-formulabar`/`art-sheet-namebox` (DOM de Univer) y el hook `window.__AIW_E2E__.sheet(id).getCell("B3")` activo solo con `?e2e=1`; las asserts de contenido van contra la API (`GET /artifacts/{id}`). Cada test llama `POST /demo/reset`.

| testid | Elemento |
|---|---|
| `ws-chip-<agentId>` | chip "N archivos" bajo la etiqueta del agente |
| `ws-host` (`data-size`: dock/expanded/full, `data-desk`) | contenedor del escritorio |
| `ws-open-desk` | boton "Abrir escritorio" en `AgentPanel` |
| `ws-tab-<artifactId>` (`data-kind`, `data-active`), `ws-tab-close-<id>` | pestaña y cerrar |
| `ws-tab-new`, `ws-new-kind-<kind>` (9 tipos), `ws-new-template-<templateId>` (p. ej. `bank_reconciliation`), `ws-import-input`, `ws-locale-select` | menu "+" e idioma |
| `ws-split-toggle`, `ws-pane-<artifactId>` | split / paneles |
| `ws-proposals-count` | contador de propuestas del escritorio |
| `art-<kind>` (`art-sheet`, `art-doc`, `art-table`, `art-board`, `art-chart`, `art-pdf`, `art-form`, `art-inbox`, `art-agenda`) | raiz de cada editor |
| `art-title`, `art-status` (`data-status`), `art-version`, `art-save-state` (`data-state`) | cabecera |
| `art-agent-badge` (`data-agent`) | "agente editando" |
| `art-attach-<agentId>` | selector de adjunto (`read/propose/edit/none`) |
| `art-ask-agent`, `art-ask-submit` | pedir revision |
| `art-history-toggle`, `art-version-<n>`, `art-restore-<n>`, `art-diff` | historial |
| `art-proposal-bar`, `art-proposal-<id>`, `art-proposal-accept-all`, `art-proposal-reject-all`, `art-proposal-accept-<id>`, `art-proposal-reject-<id>` | propuestas |
| `art-suggestion-<sid>` (`data-kind`: ins/del), `art-suggestion-accept-<sid>`, `art-suggestion-reject-<sid>` | marcas del doc |
| `art-comment-input`, `art-comment-submit`, `art-comment-<cid>`, `art-comment-resolve-<cid>` | comentarios |
| `art-embed-<bid>` (`data-target-kind`, `data-target-id`) | incrustacion |
| `art-export-<format>`, `art-import-input` | exportar / importar |
| `art-conflict`, `art-conflict-mine`, `art-conflict-theirs` | resolucion de conflicto |
| `dash-nav-files`, `dash-file-<id>`, `dash-project-<projectId>` | seccion `files` del dashboard (agrupada por proyecto) |
| `ws-tab-progress-<artifactId>` (`data-progress`, `data-build-state`) | anillo de progreso por pestaña |
| `ws-project-summary`, `ws-deliverable-<deliverableId>` (`data-progress`), `ws-deliverable-nav`, `ws-deliverable-select`, `ws-deliverable-prev`, `ws-deliverable-next` | panel del proyecto y navegacion |
| `ws-dependency-badge-<artifactId>`, `ws-linked-artifacts`, `art-link-<linkId>` | referencias entre artefactos |
| `art-sheet-ref-<cell>` | chip de celda con `AIW_REF` (el hook E2E expone la celda) |

Pruebas probables (Given/When/Then):

1. **`workspace-open.spec.ts` - escritorio.** Dado el demo, al hacer clic en `agent-accounting` aparece el `AgentPanel` (`ws-host[data-size=dock]`); clic en `ws-open-desk` => `data-size=expanded`, el nodo `agent-accounting` sigue visible y los 7 `agent-*` siguen siendo 7 (no se rompe el contrato de testids).
2. **`artifacts-sheet.spec.ts` - hoja del agente.** Al correr la solicitud de $50,000 y aprobar: existe `ws-chip-accounting` con >= 1; abrir la hoja => `art-sheet` visible, `art-sheet-formulabar` muestra `=(B1-B2)/B1` al seleccionar B3 (via namebox), `GET /artifacts/{id}` devuelve `kind:"sheet"` con `sheets[0].cells.B3.f`; `art-version` >= 1.
3. **`artifacts-human-edit.spec.ts` - humano edita, agente lo ve.** Editar B2 por API de UI/hook `?e2e=1` => `art-save-state` pasa a `saved` y `head_version` sube; lanzar `art-ask-agent` => la `Request` creada; el `POST /v1/run-task` capturado (via mock del runtime en E2E o logs del backend de simulacion) contiene `context.artifacts[].human_changes_since_last_seen` con `Margen!B2`.
4. **`artifacts-doc.spec.ts` - documento y docx.** Abrir el contrato de Legal => `art-doc` visible; escribir texto => `art-save-state=saved`; `art-export-docx` descarga un `.docx` valido (ZIP con `word/document.xml`); importar un `.docx` de fixture via `art-import-input` crea una pestaña `data-kind=doc`.
5. **`workspace-tabs.spec.ts` - multi-tipo y cualquier agente.** En el escritorio de Legal: `ws-tab-new` -> `ws-new-kind-sheet` crea una hoja; luego `ws-new-kind-chart`, `ws-new-kind-board`; resultado: 3+ pestañas de tipos distintos con `data-kind`; cambiar a `ws-split-toggle` muestra 2 `ws-pane-*`; recargar la pagina conserva las pestañas (`GET /workspaces/legal`).
6. **`artifacts-catalog.spec.ts` - sin restriccion por rol.** Para cada agente (7) y cada tipo (9), `ws-new-kind-<kind>` esta presente y habilitado; el tipo sugerido del rol aparece primero (`data-suggested="true"`).
7. **`artifacts-embed.spec.ts` - incrustacion.** La propuesta de Ventas contiene `art-embed-<bid>[data-target-kind=chart]`; editar la hoja origen por API cambia el dato visible de la grafica incrustada (`pinned_version:null`) y no cambia la fijada (`pinned_version:n`); intentar incrustar creando un ciclo devuelve 409/422.
8. **`artifacts-proposals.spec.ts` - propuestas y redlines.** Con Elena en `suggest|approve_each`, tras la tarea aparece `art-proposal-bar`, con `art-suggestion-*[data-kind=del|ins]` en la clausula 7; `art-proposal-reject-<id>` restaura el texto original (nueva version `source=accept_proposal`), `art-proposal-accept-all` aplica el cambio; `ws-proposals-count` baja a 0.
9. **`artifacts-conflict.spec.ts` - concurrencia.** Humano y agente cambian celdas **distintas** => ambas sobreviven (version `rebase`); cambian la **misma** => aparece `art-conflict` con `art-conflict-mine/theirs`; API devuelve 409 con `conflicts[]`.
10. **`artifacts-approval.spec.ts` - aprobacion ligada a version.** La aprobacion `send_proposal` (`approval-<id>`, contrato existente) muestra el artefacto con `data-version`; modificar el documento despues de pedir aprobacion y aprobar => 409 `invalid_state` (hash cambio) y la UI lo informa.
11. **`artifacts-form.spec.ts` - formulario.** Un agente emite `request_input`; aparece `art-form`; completar y enviar reanuda la tarea (la tarea pasa de `awaiting_approval` a `done`); `answers` persistidas en la version del form.
12. **`dashboard-files.spec.ts` - dashboard.** `mode-toggle` -> dashboard -> `dash-nav-files`; las tarjetas `dash-file-<id>` listan los artefactos del escenario con filtros por tipo; abrir una monta `ws-host[data-size=full]`; volver a oficina conserva las pestañas abiertas.
13. **`ws-contract.spec.ts` (ampliado).** Tras el escenario llegan: `artifact.created` (>= 5), `artifact.version_created` con `{artifact_id, version, author, source}`, `artifact.proposal_created`, `artifact.agent_focus` (efimero: sin `seq`), con envelope valido y `org_id`; nunca contenido > 16 KB en el frame.
14. **`artifacts-security.spec.ts` - API.** `POST /artifacts/{id}/versions` con (a) formula `=HYPERLINK("http://x")` => 422 `validation_failed`; (b) nodo `script` o `link` `javascript:` => 422; (c) `image` con URL remota => 422; (d) upload `.xlsm` => 415/422; (e) payload > limite => 413; (f) usuario `viewer` => 403 en `POST .../versions` y `.../decision`; (g) sin `app.org_id`/otra org => 404 (aislamiento).
15. **`artifacts-permissions.spec.ts`.** Adjunto `read` => una `apply_ops` del agente (runtime malicioso simulado) recibe `deny` y no cambia `head_version`; adjunto `propose` => crea propuesta; un agente intentando `set_status:"approved"` => `deny`.

16. **`workspace-parallel.spec.ts` - entregables en paralelo.** Con el escenario "Cierre financiero": el escritorio de `accounting` muestra >= 3 pestañas `ws-tab-*` con `ws-tab-progress-*[data-build-state=building]` **a la vez**; sus `data-progress` aumentan de forma independiente (se observan al menos dos valores distintos por pestaña) y terminan en `data-build-state=ready_for_review|done`; el chip `ws-chip-accounting` pasa de "3 building" a 0.
17. **`workspace-project.spec.ts` - panel del proyecto.** `ws-project-summary` lista 4 `ws-deliverable-*` con progreso coherente con las pestañas; clic en uno enfoca/abre su pestaña; `ws-deliverable-next` navega al siguiente entregable del mismo proyecto; el dashboard (`dash-project-<id>`) muestra el mismo progreso agregado; cambiar de modo no pierde las pestañas.
18. **`artifacts-references.spec.ts` - referencias.** El balance contiene `AIW_REF` hacia el estado de resultados (`GET /artifacts/{id}/links` devuelve `relation:"source_of"`); al crearse una version nueva del estado de resultados llega `artifact.dependency_changed` -> `ws-dependency-badge-<balance>` y despues `artifact.recalculated` (version `source:"recalc"`) con el valor actualizado; `Ctrl+clic` en `art-sheet-ref-B20` abre la hoja origen; el enlace `artifact_link` de la nota abre el balance con el rango `B20:B22` seleccionado; declarar un ciclo (A lee B y B lee A) => 422; `AIW_REF` hacia un artefacto sin `artifacts:read` => 403.
19. **`i18n.spec.ts` - idioma.** Con `locale=es` (defecto) el menu "+" y las plantillas (`ws-new-template-bank_reconciliation`) muestran espanol; al cambiar a `en` se muestran en ingles sin recargar datos; el artefacto creado desde plantilla guarda `locale` y cadenas planas; los `data-testid` no cambian entre idiomas.

Pruebas unitarias Go que acompanan (no Playwright): `formula_guard_test.go` (corpus de formulas hostiles), `merge_test.go` por tipo (propiedad: aplicar ops disjuntas en cualquier orden converge), `artifact_context_test.go` (delimitado, truncado, `last_seen`), `pg_integration_test` (RLS y append-only), `tools_test.go` (acciones de `artifacts` bajo cada autonomia), corpus de ZIP-bomb/xlsx hostil para importadores.

---

## 10. Riesgos y preguntas abiertas

| Riesgo / pregunta | Mitigacion / propuesta |
|---|---|
| Univer 0.x cambia API y el tema crema puede no quedar perfecto | Version exacta + adaptador unico + spike de 2 dias en Fase 1; Plan B FortuneSheet (MIT). |
| Univer Pro seria tentador (xlsx/colaboracion) pero es de pago y no redistribuible | Mantenerlo fuera; import/export en Go (`excelize`). |
| Construir cambios sugeridos y comentarios en TipTap es trabajo propio | Spike de 2 dias (Fase 2) contra Plate; el modelo canonico (`aiw.doc/1`) permite cambiar de editor sin migrar datos. |
| Fidelidad docx/xlsx parcial | Documentar limites, aviso al importar, conservar el archivo original como blob. |
| Formulas: diferencias entre Univer y excelize | El valor cacheado `v` lo escribe quien edita; Go recalcula solo para ediciones del agente; discrepancias se marcan, no bloquean. |
| Contexto grande sube el costo del runtime | Presupuesto de tokens por artefacto, solo cambios desde `last_seen`, truncado con indice. |
| El humano puede abrir "cualquier tipo en cualquier agente": confusion de que ve el agente | Control explicito `AttachControl` (visible/oculto) y banner de "que vera <agente>". |
| Adjuntos y "agente aprobandose" | `artifacts:approve` jamas concedible a agentes (validado en `rbac`/`policy`). |
| Ejecucion de codigo en notebooks | Fuera de alcance: solo bloques declarativos (consulta a dataset, grafica, markdown). |
| Terminologia de proyecto/entregable aun no fijada | `project_id`/`deliverable_id` opacos y sin FK; al publicarse workflow-visualization.md se ajustan nombres (sec. 5.8) y, si procede, se agregan FKs en una migracion posterior. |
| Recalculo en cascada y ciclos entre artefactos | Grafo sin ciclos validado al declarar, profundidad <= 5, recalculo encolado y acotado (token bucket), versiones `recalc` agrupables en el historial. |
| Agente con varias tareas paralelas y `Agent` con un solo `current_task_id` | La UI se apoya en artefactos (`build_state`/`progress`), no en `current_task_id`; el scheduler decide la concurrencia por agente. |
| Decision pendiente | Almacenamiento de blobs en produccion (S3 vs disco) y si se aplica ClamAV; politica de retencion por org; si `inbox`/`agenda` se alimentan solo de herramientas reales (Fase 4 del roadmap 09) o tambien de importacion manual. |

---

## 11. Estado de implementacion del frontend (v1)

Implementado contra el mock (`frontend/src/lib/mock/artifacts-mock.ts`, enganchado en `engine.ts`) y la capa `lib/api.ts` (`call` exportado) + `lib/artifacts.ts` (tipos, `artifactApi`, slice Zustand, eventos `artifact.*`; `store.apply()` solo reenvia los frames `artifact.*` y recarga en `hello`). Codigo en `frontend/src/components/workspace/*`.

**Hecho (Fases 0-1 y parte de 3-4):**
- Escritorio por agente: pestaña **Archivos** en `AgentPanel`, `ws-open-desk`, `WorkspaceHost` con tamanos `dock|expanded|full` (variable CSS `--panel-w` leida por `Shell`), pestañas con icono de tipo, anillo de progreso por pestaña (`ws-tab-progress-*`), menu **+** (todos los tipos, sugeridos por rol primero con `data-suggested`, plantillas, importar CSV), split de 2 columnas, cerrar pestañas, estado de pestañas en el store + `sessionStorage`.
- Editores: **hoja** (celdas, barra de formulas, formulas SUM/AVERAGE/MIN/MAX/COUNT/ROUND/ABS/IF y `AIW_REF`, formatos `$`/`%`, cache `v` recalculado), **documento** (bloques editables, enlaces `artifact_link` como `[[id|texto]]`, incrustaciones `embed` vivas), **tabla** (orden, filtro, edicion tipada). Vistas de solo lectura: tablero, grafica (SVG propio: barras/linea/area/pie, fuente viva desde hoja/tabla), PDF (paginas + anotaciones, sin visor real), formulario, bandeja, agenda.
- Progreso/paralelismo: `artifact.progress_changed`, `build_state`, panel **Project** (`ws-project-summary`), `DeliverableNav` (anterior/siguiente/selector, `Alt+[` / `Alt+]`), escenario mock "Cierre financiero" (3 hojas en paralelo; se relanza con una solicitud que mencione "cierre"/"financial close").
- Vinculos entre artefactos: `AIW_REF` con `Ctrl+clic`, marks `artifact_link`, panel `ws-linked-artifacts`, `ws-dependency-badge-*`, `artifact.dependency_changed` / `artifact.recalculated`, ancla (rango/bloque) al abrir.
- Edicion humana: autoguardado 1.5 s, `art-save-state`, merge de 3 vias por celda en el mock, conflicto 409 con `art-conflict-*`, historial con restaurar, adjuntos `none|read|propose|edit` (sin modo `approve`; solo el humano aprueba: `art-approve`), exportar JSON/CSV (con guardia de inyeccion de formulas).
- Dashboard: seccion **Archivos** (`dash-nav-files`, `dash-file-*`, `dash-project-*`) y desk en `full`.
- i18n: claves `workspace.*`, `art.*`, `artifact.*`, `files.*`, etc. en `es.json`/`en.json` (paridad verificada); `ws-locale-select` cambia el idioma.

**No hecho (pendiente):** propuestas/redlines y comentarios (`ProposalBar`, `CommentsRail`, marks `suggestion`, aceptacion parcial; `ws-proposals-count` existe pero siempre refleja `pending_proposals` del meta), edicion de tablero/agenda/bandeja/formulario (solo lectura), visor PDF real (react-pdf) y subida de blobs, import/export xlsx/docx, persistencia de layout en el servidor (hoy `sessionStorage`), chip `ws-chip-<agentId>`, camara/animacion del personaje en `OfficeScene`/`LabelLayer` (fuera de alcance: `components/office/` lo editan otros), grafo mini de dependencias del proyecto, banner "vera tus cambios" (requiere `last_seen_version`), Univer/TipTap/ECharts (v1 usa motores propios ligeros sin dependencias nuevas; el contenido canonico `aiw.*/1` ya es el del spec, por lo que cambiar de libreria es un cambio de adaptador).

**Backend implementado** (paquete `backend/internal/artifacts`, rutas en `internal/api/artifacts.go`, migración `280_artifacts.sql` con down en `backend/migrations/down/280_artifacts_down.sql`, RLS por organización; contrato exacto en [08-api.md](08-api.md) sec. 17). Ya existen: `GET /artifact-kinds`, `GET /artifact-templates`, `GET|POST /artifacts`, `GET|PATCH|DELETE /artifacts/{id}` (DELETE archiva), `GET|POST /artifacts/{id}/versions` (+ `GET .../versions/{n}`), `POST /artifacts/{id}/restore`, `PUT /artifacts/{id}/attachments/{agent_id}`, `GET|POST /artifacts/{id}/links`, `POST /artifacts/{id}/refresh-dependencies`, `POST /artifacts/{id}/ask`, `GET /projects/{id}/workspace` y los eventos WS `artifact.created|updated|status_changed|version_created|progress_changed|dependency_changed|recalculated|agent_focus|deleted|link_added`. Los contratos que asumía el frontend se cumplen tal cual: la lista devuelve `{items}` solo con `ArtifactMeta`; `POST .../versions` acepta `{base_version, content, summary}` y responde `201 {version, merged, content?}` o `409 {code:"conflict", head_version, conflicts:[{unit, mine, theirs, theirs_author}]}`; versiones y vínculos devuelven `{items}`.

- **Contenido y límites**: `aiw.<kind>/1` validado en Go (esquema exacto, hasta 1 MB el documento y 5 MB el resto, 200 000 celdas, 5 000 bloques, 20 embeds, 50 000 filas, 2 000 tarjetas). Hoja: guard de fórmulas con lista blanca (bloquea `HYPERLINK`, `WEBSERVICE`, `IMPORT*`, `INDIRECT`, `OFFSET`, `CALL`, `EXEC`, referencias externas...); `AIW_REF` solo con un alias declarado en `imports`. Documento: nodos y marcas en lista blanca (incluye la marca `artifact_link` que usa el editor), enlaces solo `http|https|mailto`, imágenes solo `blob:`. Lo que no cumple rechaza todo el guardado (`400`), nada se "limpia" en silencio.
- **Versiones y conflictos**: cada cambio es una versión inmutable (en Postgres, `artifact_versions` es solo-insertar para `app_user` y con trigger). El guardado se basa en `base_version`; si la cabeza avanzó se hace merge de 3 vías por unidad (celda de hoja, bloque `bid` de documento, fila+columna de tabla, tarjeta de tablero, evento/ítem de agenda/bandeja, campo del resto): unidades distintas se combinan (`merged:true` + contenido fusionado), la misma unidad en ambos lados devuelve `409` con la unidad y el autor del otro cambio y no escribe nada. Un guardado sin cambios no crea versión. Restaurar crea una versión nueva (`source:"restore"`).
- **Agentes**: el modo de adjunto es `none|read|propose|edit` (`approve` se rechaza con `400`); solo una persona lo concede. `artifacts.Service.ApplyAgent` escribe únicamente con `edit` (`propose` aún no tiene propuestas: se rechaza); un agente nunca puede aprobar, enviar, archivar ni cambiar adjuntos (solo puede pedir `in_review`). `approved`/`sent` exigen el permiso `artifacts:approve` (admin/owner) y bloquean el contenido (`locked`) hasta que alguien con ese permiso lo devuelve a `draft`. Permisos nuevos en `auth/rbac.go`: `artifacts:read` (viewer+), `create|write|comment|export` (member+), `approve|delete` (admin+).
- **Vínculos y dependencias**: los vínculos `source_of` (imports de hoja), `embeds`/`refers_to` (documento), gráficas y tarjetas se derivan del contenido en cada versión; `depends_on_artifacts` sale de ahí. Cuando una fuente cambia, el dependiente recibe `artifact.dependency_changed` y, por defecto, se recalcula solo (versión `source:"recalc"`, autor `system/recalc`, evento `artifact.recalculated`) con un evaluador de fórmulas pequeño (`+ - * / ^`, comparaciones, `SUM AVERAGE MIN MAX COUNT ROUND ABS IF`, rangos y `AIW_REF`); las fórmulas que no entiende conservan su valor en caché y se listan en `unresolved`. `POST .../refresh-dependencies` fuerza el recálculo. `stale` de un vínculo = la fuente avanzó desde el último recálculo.
- **Proyectos**: cada nodo con agente de un proyecto lanzado recibe un entregable (artefacto del agente: tipo sugerido por su rol, `project_id`, `deliverable_id` = id del nodo); su `progress`/`build_state` siguen al nodo (`queued -> building -> ready_for_review`, `blocked`) con `artifact.progress_changed`, y al terminar se escribe la salida de la tarea como versión del agente (documento, hoja o tabla). `GET /projects/{id}/workspace` los agrupa por entregable.
- **Preguntar a un agente**: `POST /artifacts/{id}/ask` envía una solicitud normal al orquestador (presupuesto, aprobaciones y auditoría aplican) y concede lectura al agente si no la tenía.
- **Pruebas**: `internal/artifacts/service_test.go` (validación, merge y conflictos, restaurar, permisos de agente y aprobación, dependencias y recálculo, aislamiento por organización, espacio de proyecto), `internal/api/workspaces_api_test.go` (HTTP y roles) y `internal/infrastructure/postgres/workspaces_pg_test.go` (con `TEST_DATABASE_URL`: compare-and-swap, inmutabilidad, RLS).

**Sigue sin backend** (el frontend no lo llama o es solo lectura): propuestas/redlines y comentarios (`/proposals`, `/comments`, `pending_proposals` siempre 0), `GET /artifacts/{id}/diff`, `POST /artifacts/{id}/export`, blobs (`/artifact-blobs`) e `import`, `GET|PUT /workspaces/{desk}` (el layout sigue en `sessionStorage`), la tool `artifacts` del runtime (los agentes escriben hoy a través del servicio Go y de los entregables de proyecto, no por tool-calls), la coalescencia de guardados humanos (por la inmutabilidad), la retención/compactación de versiones y la cuota por organización.
`GET /artifact-kinds`, `GET /artifact-templates`, `GET|POST /artifacts`, `GET|PATCH /artifacts/{id}`, `GET|POST /artifacts/{id}/versions`, `POST /artifacts/{id}/restore`, `PUT /artifacts/{id}/attachments/{agent_id}`, `GET /artifacts/{id}/links`, `POST /artifacts/{id}/refresh-dependencies`, `POST /artifacts/{id}/ask`, `GET /projects/{id}/workspace`, `GET|PUT /workspaces/{desk}` (el frontend aun no los llama; el layout vive en `sessionStorage`), mas los eventos WS `artifact.created|updated|status_changed|version_created|progress_changed|dependency_changed|recalculated|agent_focus|deleted`. Contratos adicionales que el frontend asume: la lista `GET /artifacts` devuelve solo `ArtifactMeta` (el contenido se pide con `GET /artifacts/{id}`); `POST /artifacts/{id}/versions` acepta `{base_version, content, summary}` y responde `{version, merged, content?}` o `409`; `GET /artifacts/{id}/versions` y `/links` devuelven `{items: [...]}`.

## 12. Estado A7a: funciones de artefactos terminadas

Estado verificado en código (rama `feat/a7a-artifacts`):

- **Exportar** (`backend/internal/artifacts/export.go`, ruta en `backend/internal/api/artifacts.go`): `GET /api/v1/artifacts/{id}/export?format=docx|xlsx[&version=n]`. `doc` solo a `docx` y `sheet`/`table` solo a `xlsx`; cualquier otro formato responde `400`. Exige `artifacts:read` y `artifacts:export` (viewer no exporta), se limita a la organización del token (otra organización recibe `404`) y escribe la entrada de auditoría `artifact.exported` (formato, versión, bytes, hash del contenido, `tainted`). El paquete OOXML se arma con la librería estándar (`archive/zip`), sin dependencias nuevas. En `xlsx` el texto que empieza con `=`, `+`, `-`, `@`, tabulador o retorno de carro se escribe como cadena en línea con el estilo `quotePrefix`, de modo que sigue siendo texto al abrirlo o guardarlo como CSV; las fórmulas solo se exportan si pasan el guard de fórmulas y no usan `AIW_REF` (si no, se exporta el valor calculado). En `docx` los cambios sugeridos pendientes salen como `w:ins`/`w:del`. El frontend agrega los botones `art-export-docx` y `art-export-xlsx` (ocultos en modo demo, que no tiene backend).
- **Visor PDF** (`frontend/src/components/workspace/editors/PdfView.tsx`): `pdfjs-dist` se carga de forma perezosa al abrir un PDF; el worker se empaqueta como recurso local (`new URL(..., import.meta.url)`), sin CDN, y cada página se dibuja cuando entra en pantalla. Pendiente: el backend aún no tiene `POST /artifact-blobs` ni `GET /artifact-blobs/{id}`; mientras `blob_id` sea nulo (o en modo demo) se muestran los marcos de página y las anotaciones como antes.
- **Comentarios y propuestas** (`backend/internal/artifacts/collab.go`, migración `340_artifact_comments.sql`, down en `backend/migrations/down/340_artifact_comments_down.sql`, RLS por organización y sin `DELETE` para `app_user`): `GET|POST /artifacts/{id}/comments`, `PATCH .../comments/{cid}` (`resolved`), `GET|POST /artifacts/{id}/proposals`, `POST .../proposals/{pid}/decision`. Una propuesta lleva el contenido completo propuesto, la `base_version` y un resumen. Un agente con adjunto `propose` o `edit` puede crearla (y comentar); **nunca** aceptarla ni rechazarla ni resolver comentarios: `Decide` rechaza a cualquier agente aunque tenga `edit`, y la ruta exige `artifacts:approve`. Aceptar escribe una versión con `source: accept_proposal` usando el guardado versionado (merge de 3 vías); si la misma unidad cambió después, responde `409` con los conflictos y la propuesta sigue pendiente. `pending_proposals` se recalcula en cada alta o decisión. Eventos: `artifact.proposal_created|proposal_resolved|comment_added|comment_resolved`. El texto de comentarios y propuestas es dato: se muestra como texto plano (`CollabRail.tsx`, `art-collab-toggle`). Límite de esta primera versión: la propuesta es un reemplazo completo; las operaciones granulares, la aceptación parcial, las marcas `suggestion` en TipTap y las menciones que crean tareas siguen pendientes.
- **Tablero y agenda editables** (`BoardView.tsx`, `AgendaView.tsx`): arrastrar y soltar, más alternativa de teclado (`Alt` + flechas sobre la tarjeta o el evento enfocado, botones de mover, `Supr` elimina) con región `aria-live`. Guardan con `edit` del store, es decir, el guardado versionado con `base_version`, autoguardado, estado `conflict` y merge por `id` de tarjeta o evento. Se vuelven de solo lectura si el artefacto está bloqueado (`approved`/`sent`).

Pruebas: `backend/internal/artifacts/export_test.go`, `collab_test.go` y `TestArtifactExportPermissionsAndTenants`, `TestArtifactCommentsAndProposalsOverHTTP` en `backend/internal/api/workspaces_api_test.go`. Sin pruebas automáticas de UI (Playwright) para el visor PDF, el tablero ni la agenda.
