# Archivos para los agentes: importar, entender, editar y devolver

Investigación y diseño del 2026-11-02. Objetivo del dueño: que los agentes trabajen con los archivos de la empresa estén
donde estén, con la misma experiencia que el editor de Claude (subo un PDF, pido un cambio, me devuelve un PDF nuevo),
con memoria larga y memoria RAG, y que lo editado pueda volver a su origen. Solo entran servicios que sirven para
importar o manejar archivos.

Estado: **propuesta**. Nada de esto está construido salvo lo que la sección 1 marca como "ya existe". Las decisiones que
son del dueño están en la sección 10.

---

## 0. La idea en una frase

**Nuestro Drive es la mesa de trabajo; todo lo demás es una fuente.** Cada archivo, venga de donde venga, se copia a
nuestro Drive (con su origen y su versión de origen anotados), se entiende una vez (texto, páginas, tablas, índice), los
agentes lo editan creando **versiones nuevas** que se pueden ver, comparar y deshacer, y solo cuando una persona lo
aprueba se **publica de vuelta** en el origen, sin pisar cambios ajenos.

```
 Fuentes                      Mesa de trabajo (Drive)                 Agentes
 ───────                      ───────────────────────                 ───────
 subida / carpeta / zip  ─┐   archivo + versiones inmutables   ◄──►   leer (texto, páginas, imagen)
 enlace / buzón (ya hay) ─┤   ├─ extracción (texto, tablas, OCR)      buscar (RAG con citas)
 Git (GitHub, GitLab,    ─┤   ├─ índice (palabras + significado)      editar en el sandbox → versión nueva
   Bitbucket, cualquiera) │   ├─ origen: fuente, id, rev/etag         recordar (memoria larga)
 WebDAV / S3 / SFTP      ─┤   └─ estado: igual / cambiado aquí /
 Google Drive, Dropbox,  ─┘          cambiado en origen / conflicto
   OneDrive (decisión §10)                     │
                                               └──► "Publicar en origen" (aprobación + diff + rev) ──► fuente
```

---

## 1. Lo que ya existe (inventario del 2026-11-02)

| Pieza | Estado | Dónde |
|---|---|---|
| Drive con carpetas, versiones inmutables, papelera, diff de texto, bloqueo optimista | Hecho | `backend/internal/drive`, `drive.md` |
| Subida | `PUT` crudo, todo en memoria, 25 MB, sin reanudable ni multipart | `api/drive.go:234` |
| Tipos | texto/código, pdf, docx, xlsx, pptx (solo guardado), imágenes. Sin zip, html, xls | `drive/names.go:167` |
| Extraer texto | PDF (sin OCR), docx (solo cuerpo), xlsx (1.ª hoja). pptx e imágenes: nada | `drive/extract.go`, `pdftext` |
| Ver páginas con visión | Sí, 4 páginas por lectura, con la imagen de render (poppler) | `drive/vision.go`, `sandbox/render` |
| Crear documentos | `fs.write_document`: xlsx/docx/pdf/md/csv desde un modelo simple. PDF solo Latin-1, sin imágenes ni estilo | `officedoc` |
| Editar documentos | `fs.edit`: texto, docx (reemplazar, títulos, párrafos), xlsx (celdas, filas). **PDF, pptx e imágenes no** | `drive/edit.go`, `officedoc/edit*.go` |
| Sandbox de Python | Apagado por defecto, aprobación por ejecución, solo numpy/pandas/openpyxl/matplotlib, no deja salir docx, sus salidas van a `/outputs` y nunca son versión del original | `sandbox/`, `code-sandbox.md` |
| Búsqueda | Palabras (tsvector) + significado (embeddings OpenAI-compatibles, apagados en todos los compose) fusionados por RRF; coseno en Go; sin pgvector, sin reranker | `docindex`, `agent-search.md` |
| Memoria larga (M1) | Notas, resultados e informes por organización/agente/cliente; FTS + semántica; ≤6 recuerdos por tarea | `memory`, `memory.md` |
| Importar | Enlace (pull programado), buzones (push con token), `custom_api` (lectura en vivo) | `link-imports.md`, `data-inboxes.md` |
| Conectores de marca | Google Drive solo lectura en vivo, solo texto, 256 KiB, sin sync. GitHub solo issues. Todo apagado (`VENDOR_INTEGRATIONS`) | `connections/drive` |
| Devolver archivos a un origen | **No existe** | — |
| Chat | Adjuntar archivos y carpetas, elegir del Drive, tarjetas de archivo y de edición con Conservar/Deshacer, "Pedir un cambio". El front deja 10 adjuntos y el back se queda con 6 | `chat/attachments.tsx`, `application/chat.go:367` |

**Producción hoy** (`docker-compose.prod.yml`): sin S3 (los bytes van a Postgres como bytea), sin sandbox (sin
`code.run` ni páginas como imagen), sin embeddings (solo búsqueda por palabras). Antes de cualquier cosa nueva hay que
cerrar esto (fase 0).

Conclusión: la base (Drive versionado, índice, memoria, aprobaciones, marca de contenido no confiable) es buena. Faltan
tres cosas grandes: **editar de verdad cualquier formato** (el ida y vuelta tipo Claude), **entender bien documentos
difíciles** (escaneados, tablas, pptx) y **fuentes con sincronización y vuelta**.

---

## 2. Fuentes: de dónde vienen los archivos

### 2.1 Tres niveles

1. **Genéricas (encajan en la regla de v1, sin marca):**
   - **Subida mejorada:** multipart y reanudable (tus), carpetas y `.zip` (desempaquetado seguro en el sandbox), hasta
     ~500 MB por archivo con S3 (directo al bucket con URL firmada, nunca en memoria del backend).
   - **Git por protocolo** (cualquier URL https + token): cubre **GitHub, GitLab y Bitbucket** y cualquier servidor Git
     sin usar la API de ninguno. Clonar o actualizar una rama; la vuelta es un **commit en una rama nueva** que se empuja
     (`workforceai/<tarea>`), nunca a la rama principal. Abrir el PR es el único paso que necesita la API de cada marca:
     en v1 devolvemos el enlace "crear PR" de la propia forja.
   - **WebDAV / Nextcloud, S3 compatible, SFTP** con la biblioteca de rclone en Go (MIT, `librclone` en proceso). Sirve
     para listar y copiar; la detección de cambios y la escritura condicionada (etag) las hacemos nosotros.
   - **Enlace y buzón** (ya existen): siguen igual; los archivos que traen pasan por la misma tubería.

2. **Almacenamiento de marca solo para archivos** (requiere la excepción de §10.1):

   | Servicio | Cómo entrar sin pedir de más | Permiso | Revisión del proveedor | Cambios | Vuelta |
   |---|---|---|---|---|---|
   | Google Drive | **Google Picker**: la persona elige archivos o carpetas | `drive.file` (no sensible) | Verificación básica. **Evitar** `drive`/`drive.readonly`: son restringidos y piden auditoría CASA anual (~540 USD+ y semanas) | `changes.list` + canales `watch` (≤7 días, hay que renovarlos) + sondeo | `files.update` = revisión nueva del mismo archivo; comparar `headRevisionId` antes |
   | Dropbox | **Chooser / Saver** (sin OAuth) y OAuth con alcances finos para sincronizar | `files.content.read` / `.write` | Aprobación de producción al pasar 50 usuarios (plazo de 2 semanas) | cursor de `list_folder` + webhooks | `upload` con `mode: update(rev)` → conflicto si cambió |
   | OneDrive / SharePoint | File Picker v8 (comodidad, no limita permisos) | `Files.Read` / `Files.ReadWrite`; empresas: `Files.SelectedOperations.Selected` (lo concede un admin, por archivo) | Verificación de editor (gratis, necesita ID de partner y dominio) | delta + suscripciones (≤30 días, hasta 6 h de retraso: sondear también) | `PUT` con `If-Match: eTag` → 412 si cambió |
   | Box | Más tarde (empresas) | OAuth de usuario | Aprobación del admin | eventos + webhooks | versión nueva con `If-Match` |

   Google Docs/Sheets/Slides se importan exportados (docx/xlsx/pptx o pdf; la exportación tiene tope de 10 MB) y se
   devuelven como revisión del mismo archivo.

3. **Lo que no hacemos:** conectores que no son de archivos (correo, chat, CRM). Tampoco pedir acceso a todo el Drive
   de nadie.

### 2.2 Modelo de datos

- `sources`: organización, tipo (`upload`, `git`, `webdav`, `s3`, `sftp`, `gdrive`, `dropbox`, `onedrive`, `link`,
  `inbox`), configuración sellada con el vault (`CONNECTIONS_KEK`, ya existe), raíz elegida, modo (`una vez`, `espejo`
  = sincroniza, `en vivo` = solo lectura bajo demanda), cursor de cambios, estado y último error.
- `source_items`: fuente, id externo, ruta externa, **rev/etag/sha de origen**, archivo del Drive, versión del Drive que
  corresponde a esa rev, estado (`igual`, `cambiado aquí`, `cambiado en origen`, `conflicto`, `borrado en origen`).
- En el Drive aparece una raíz nueva **`/Fuentes/<nombre>/`**, espejo de solo lectura para las personas por defecto.
  Los agentes leen ahí; para editar, crean versión nueva en el mismo archivo (queda "cambiado aquí").

### 2.3 Sincronización

Un trabajador por fuente (mismo patrón de lease + `SKIP LOCKED` que `docindex`): webhook o canal cuando el proveedor lo
tiene, **siempre** con sondeo de respaldo (los avisos de Drive y Graph se pierden o llegan tarde). Cada cambio en origen
crea una versión nueva en nuestro Drive solo si cambió el hash. Si el archivo también cambió aquí → `conflicto` (nunca
se pisa nada; se muestran las dos versiones). Un borrado en origen lleva el archivo a la papelera y **borra sus trozos
del índice, sus embeddings y los recuerdos que salieron solo de él** (§5.4).

### 2.4 Publicar en origen (la vuelta)

1. La persona (o el agente, pidiendo aprobación) pulsa **Publicar en origen** sobre una versión.
2. Se muestra el **diff** (visual para pdf/docx/pptx, de texto para lo demás) y a dónde va.
3. Al aprobar: escritura **condicionada** a la rev que teníamos (`If-Match`, `rev`, `sha`, `headRevisionId`). Si el
   origen cambió → no se escribe; queda `conflicto` y se ofrece "guardar como archivo nuevo junto al original"
   (`Contrato (editado por WorkforceAI).pdf`) o, en Git, siempre una rama nueva.
4. Se audita (quién aprobó, qué versión, qué rev quedó) y la aprobación se ejecuta una sola vez (ya existe).

Los permisos de escritura se piden **solo cuando alguien activa la vuelta** en esa fuente (OAuth incremental).

---

## 3. Entender los archivos (ingesta)

Tubería única para todo lo que entra (subida, fuente, adjunto de chat, salida de un agente):

1. **Control de entrada:** tipo por bytes mágicos (no por extensión), tamaño, páginas, tamaño descomprimido y número de
   entradas (docx/xlsx/pptx son zip; un PDF puede ser una bomba), XML con `defusedxml`, macros fuera (salvo que se pida
   conservarlas), **ClamAV**. Todo el análisis corre en el sandbox sin red, con tope de CPU, memoria y tiempo.
2. **Extracción estructurada** con **Docling** (MIT, local): PDF, DOCX, PPTX, XLSX, HTML e imágenes → JSON con orden de
   lectura, títulos, tablas, fórmulas, **número de página y caja** de cada bloque. OCR con Tesseract para escaneados.
   Las páginas que Docling no resuelve bien (escaneo malo, tablas complejas, gráficos) se mandan una por una a
   **Gemini con visión** ("transcribe a Markdown con tablas"). Gemini lee PDFs de hasta 1000 páginas; en Gemini 3 el
   texto incrustado no se cobra y las páginas imagen se cobran como imagen.
3. **Trozos por estructura** (sección, tabla entera, diapositiva), ~400-800 tokens, cada uno con ruta de secciones,
   página y caja para citar. Código: por función o clase (tree-sitter) con ruta y símbolo.
4. **Contexto por trozo** ("contextual retrieval"): 1-2 frases generadas con Flash que sitúan el trozo en su documento,
   antes de indexarlo. En las pruebas publicadas reduce los fallos de búsqueda ~49% junto con BM25 y ~67% con reranking.
   Es opcional por organización porque cuesta una llamada por documento (aprovechando la caché implícita de Gemini).
5. **Índice:** el que ya existe (tsvector) + embeddings (§4). Progreso visible en la pantalla de Archivos ("indexando 34
   de 120 páginas").
6. **pptx:** extracción de texto y notas, vista previa por diapositiva (render con LibreOffice → PNG).

Licencias a evitar en el servidor: **PyMuPDF** (AGPL salvo licencia comercial), pg_search de ParadeDB (AGPL). Revisar
la licencia de MinerU y los pesos de Marker antes de usarlos.

---

## 4. RAG: buscar con citas

- **Embeddings con Gemini:** `gemini-embedding-2` (texto, imagen y PDFs cortos; dimensiones 128-3072; se usa **1536**),
  0,20 USD por millón de tokens (0,10 en lote). Hoy `docindex/embed.go` solo habla el formato OpenAI: añadir Gemini
  como proveedor de embeddings para que una organización con Gemini no se quede solo con búsqueda por palabras.
  Cambiar de modelo obliga a reindexar todo: el modelo queda guardado en cada trozo.
- **Postgres con pgvector:** cambiar la imagen a `pgvector/pgvector:pg16` y guardar `halfvec(1536)` con índice HNSW e
  `iterative_scan` (para que filtrar por organización no pierda resultados). El `real[]` + coseno en Go queda como
  respaldo para instalaciones sin la extensión (ya está así diseñado en `04-memoria.md` §6).
- **Fusión híbrida:** la que ya existe (RRF de palabras + significado). BM25 de verdad con `pg_textsearch` es opcional:
  sus estadísticas se calculan sobre toda la tabla y filtrarían frecuencias entre organizaciones si no se particiona.
- **Reordenar:** los 50 mejores se reordenan con Flash ("del 0 al 3, ¿responde esto a la pregunta?") cuando la búsqueda
  es para una respuesta importante; si no, se omite.
- **Citas:** cada trozo lleva archivo, versión, página, sección y caja. El agente cita ids de trozo; la interfaz los
  convierte en "Contrato.pdf, pág. 4" que abre el visor **en esa página con el párrafo resaltado**.
- **Archivo grande en una conversación activa:** en vez de RAG, el documento entero en la **caché de contexto** de
  Gemini mientras dure la conversación (más barato que reenviarlo y mejor que trozos para "revisa todo el contrato").
  RAG queda para el conjunto de documentos de la empresa.

---

## 5. Memoria larga

Hoy hay M1 (notas, resultados e informes con FTS + semántica). Se amplía a una sola tabla de recuerdos con:

- **Ámbito:** organización, equipo, proyecto, cliente, agente, persona.
- **Tipo:** *episódica* (qué se hizo y cómo salió, resumida al cerrar cada tarea o conversación), *semántica* (hechos de
  la empresa, de clientes, de documentos, con su fuente) y *procedimental* (cómo le gusta a esta empresa que se hagan las
  cosas; se conecta con las habilidades).
- **Procedencia** (versión de archivo, tarea o conversación), confianza, `valido_desde` / `valido_hasta`, último uso.

Funcionamiento:

1. **Memoria núcleo por agente:** unas pocas líneas fijas siempre en contexto (quién es, para quién trabaja,
   preferencias firmes), editables por la persona.
2. **Recuerdo automático** antes de cada tarea (como hoy, con presupuesto de tokens) + **herramienta de memoria** para el
   agente (ver, crear, reemplazar y borrar entradas en su ámbito, el mismo patrón que la herramienta de memoria de Claude,
   como herramienta propia sobre Gemini).
3. **Consolidación nocturna:** junta duplicados, resume episodios viejos y **cierra** (`valido_hasta`) los hechos que una
   información más nueva contradice, sin borrarlos.
4. **Reglas:**
   - La memoria privada de un agente se escribe libre; la **compartida de la organización necesita aprobación**.
   - **El texto de un documento importado nunca escribe memoria por sí solo** (evita que un PDF malicioso "enseñe" algo
     a todos los agentes).
   - La persona ve, corrige y borra recuerdos en una pantalla; borrar una fuente borra lo que salió solo de ella.

---

## 6. Editar como en Claude: el banco de documentos

Es la pieza central de la experiencia: *subo un PDF, pido "cambia la fecha y el monto de la cláusula 3", me devuelve un
PDF nuevo, veo qué cambió y lo conservo o lo deshago*.

### 6.1 Cómo lo hace Claude

Un sandbox por usuario donde el modelo **escribe y ejecuta código**, más unas guías ("skills") por formato: docx (editar
el XML del documento y validar), xlsx (openpyxl y recalcular con LibreOffice), pptx (XML y miniaturas) y pdf (pypdf,
pdfplumber, reportlab, qpdf). **No edita el texto de un PDF en su sitio:** superpone o regenera. Después **renderiza
las páginas a imagen y las mira** para comprobar el resultado. Esas guías son de código visible pero no abiertas: las
usamos como referencia, no las copiamos.

### 6.2 Cómo lo hacemos

1. **Imagen de sandbox "documentos"** (además de la de análisis): LibreOffice sin interfaz, poppler, pypdf, pikepdf,
   pdfplumber, pypdfium2, reportlab, WeasyPrint, Typst, python-docx, python-pptx, openpyxl, Docling, Tesseract, fuentes
   tipográficas comunes. Sin red. Salidas permitidas: también docx y pptx. Tope por archivo ~30 MB.
2. **Nueva herramienta `doc.edit` (o modo de `code.run`):** recibe los archivos de la tarea, el agente escribe Python
   guiado por **nuestras propias guías por formato** (guardadas como habilidades revisadas en `/Shared/Skills/`), y
   **la salida se guarda como versión nueva del mismo archivo** (o archivo nuevo si se pidió uno nuevo). Hoy las salidas
   del sandbox van a `/outputs` y no son versión del original: eso cambia.
3. **Escalera para PDF** (lo menos invasivo que funcione):
   1. páginas, sellos, formularios, comentarios → pypdf / pikepdf, sin pérdida;
   2. cambio pequeño de texto en un PDF digital → tachar y escribir en el mismo rectángulo (avisando si falta la fuente
      original);
   3. reescritura → si tenemos el **original editable** (docx, Google Doc, pptx, muy común con fuentes), se edita ese y
      se vuelve a exportar a PDF: es lo más fiel. Si no, PDF → docx/HTML → PDF, avisando que el diseño queda "parecido";
   4. escaneado → OCR y regenerar.
4. **Comprobación antes de entregar:** render antes/después a PNG, mapa de diferencias por página y diff del texto; se
   le pasa a Gemini con visión: "lista cada cambio y marca los que no se pidieron". Si hay cambios no pedidos, el agente
   corrige o lo dice.
5. **Entrega en el chat:** la tarjeta de edición que ya existe (Conservar / Deshacer) gana un **diff visual** (páginas
   antes y después lado a lado, cambios resaltados) para pdf, docx y pptx; xlsx muestra las celdas cambiadas.
6. **Varios agentes sobre el mismo archivo:** el bloqueo optimista por versión ya existe; para binarios se añade un
   turno corto por archivo (lease) para que dos agentes no editen el mismo PDF a la vez; el texto mantiene la fusión.
7. **Aprobaciones:** como solo crea versiones (reversibles) y corre sin red, editar archivos de la propia mesa o del
   proyecto no pide aprobación por ejecución (decisión §10.4). Publicar en origen, borrar o compartir fuera sí.

`fs.write_document` y `fs.edit` se quedan para lo simple y rápido (sin sandbox); el banco de documentos es para todo lo
demás.

---

## 7. Experiencia en pantalla

- **Chat:** arrastrar archivos, carpetas o un zip; botones "Desde Google Drive / Dropbox / OneDrive" (picker de cada uno)
  y "Desde un repositorio Git"; progreso de lectura; respuestas con citas que abren el archivo en la página; tarjeta de
  versión nueva con diff visual y Conservar / Deshacer / Descargar / Publicar en origen. Corregir el límite de adjuntos
  (10 en el front, 6 en el back).
- **Archivos:** raíz `/Fuentes` con el estado de cada fuente (última sincronización, errores, "cambiado aquí",
  "conflicto"), insignia de origen en cada archivo, vista previa de pptx, permisos de agentes sobre carpetas (hoy solo
  por API).
- **Ajustes → Fuentes:** conectar, elegir carpetas, activar la vuelta (pide el permiso de escritura en ese momento),
  desconectar (revoca el token y borra lo importado en un plazo dicho).
- **Ajustes → Memoria:** ver, corregir y borrar recuerdos por ámbito; memoria núcleo de cada agente.

---

## 8. Seguridad

- **Inyección por documentos:** el contenido importado sigue siendo `<untrusted_data>` y marca la tarea (ya existe). Un
  agente que leyó contenido no confiable no tiene red en el sandbox, y publicar en origen o enviar fuera **siempre** pasa
  por una persona. Se detecta texto oculto (blanco, diminuto) comparando el render con el texto extraído. Las respuestas
  no cargan imágenes ni enlaces remotos (evita fuga de datos).
- **Aislamiento:** organización en cada fila + filtros obligatorios en la búsqueda; prefijo por organización en S3; un
  contenedor de sandbox por trabajo.
- **Tokens:** sellados con el vault, solo en el backend, nunca en el sandbox; lectura primero, escritura solo si se
  activa la vuelta; GitHub y similares con tokens de vida corta cuando el proveedor lo permite.
- **Gemini:** en el nivel gratuito Google usa los datos para mejorar sus productos. **Los documentos de clientes solo
  pueden ir por el nivel de pago** (decisión §10.2). Además el precio de `gemini-3.6-flash` se duplica el 2027-01-01.
- **Borrado:** quitar un archivo o desconectar una fuente borra trozos, embeddings, versiones derivadas, objetos subidos
  a Gemini (caducan a las 48 h, pero se borran antes) y recuerdos que dependen solo de él.

---

## 9. Fases

Tamaños relativos: S (días), M (1-2 semanas), L (varias semanas).

| Fase | Qué | Tamaño | Resultado para el usuario |
|---|---|---|---|
| **0. Cimientos en producción** | S3 en prod; sandbox en prod; embeddings encendidos (con Gemini, §4); subida multipart/reanudable y directa a S3; arreglar 10 vs 6 adjuntos | M | Lo que ya existe funciona en producción como en local |
| **1. Banco de documentos** | Imagen "documentos", `doc.edit` con salida como versión, guías propias por formato, escalera PDF, comprobación visual, diff visual en la tarjeta, pptx (texto y vista previa) | L | **El ida y vuelta tipo Claude**: subo PDF/docx/xlsx/pptx, pido un cambio, recibo la versión nueva con diff |
| **2. Entender mejor** | Docling + OCR + Gemini por página, trozos por estructura con página y caja, pgvector, contexto por trozo, citas a la página, caché de contexto para documentos grandes | M-L | Respuestas con citas exactas también de escaneados, tablas y presentaciones |
| **3. Memoria v2** | Ámbitos y tipos, memoria núcleo, herramienta de memoria, resúmenes de conversación, consolidación, pantalla de memoria | M | Los agentes recuerdan proyectos, clientes y preferencias, y la persona lo controla |
| **4. Fuentes genéricas** | `sources`/`source_items`, `/Fuentes`, sincronización, Git (GitHub, GitLab, Bitbucket por protocolo), WebDAV/Nextcloud, S3, SFTP, publicar en origen con rev | L | Conectar un repositorio o un servidor de archivos y trabajar sobre él, con vuelta |
| **5. Almacenamiento de marca** (si §10.1) | Google Drive (Picker + `drive.file`), Dropbox (Chooser/Saver + OAuth), OneDrive (picker + `Files.ReadWrite`, verificación de editor). Después: SharePoint `*.Selected`, Box | M por proveedor | Elegir archivos de Drive/Dropbox/OneDrive y devolverlos editados |

**Estado de la fase 0 (2026-11-02):** hecho en el código, sin desplegar.
- S3 (RustFS interno) en los dos compose de producción, con `server ctl objects-to-s3` para mover los bytes de Postgres
  y el respaldo del volumen (`drive.md`).
- Sandbox y render de páginas en producción con el perfil `sandbox`, apagado por defecto (`code-sandbox.md`).
- Embeddings con Gemini (`gemini-embedding-2@1536`), apagados en producción hasta tener Gemini de pago
  (`agent-search.md`).
- Subida en streaming, multipart y con progreso, hasta 500 MB con S3 (`drive.md`).

Pendiente de la fase 0:
- subida reanudable o directa a S3 con URL firmada;
- el límite de 10 adjuntos en el front frente a 6 en el back (`application/chat.go`, en manos de otro trabajo).

Orden recomendado: 0 → 1 → 2 → 3 → 4 → 5. La fase 1 es la que más se nota; la 4 cubre Bitbucket sin romper la regla de
v1; la 5 depende de la decisión del dueño.

---

## 10. Decisiones del dueño

2026-11-02: el dueño aceptó las recomendaciones ("la que recomiendes"). Quedan así: 1 sí, en la fase 5; 3 sin PyMuPDF;
4 sin aprobación por ejecución dentro de la mesa o el proyecto; 5 pgvector, cuando toque la fase 2 y con copia de
seguridad antes. La 2 (Gemini de pago) la tiene que activar el dueño en Google AI Studio; hasta entonces, nada de
documentos reales de clientes en producción por Gemini.

1. **¿Excepción a "v1 sin integraciones de marca" solo para almacenamiento de archivos (Google Drive, Dropbox,
   OneDrive)?** Recomendación: sí, pero en la fase 5, con picker y permisos mínimos. Git genérico ya cubre GitHub,
   GitLab y Bitbucket sin excepción.
2. **Gemini de pago para documentos de clientes.** Sin esto no se debería encender en producción nada que mande
   documentos de clientes a Gemini. El nivel gratuito vale para desarrollo.
3. **PyMuPDF:** licencia comercial (edición de texto de PDF en su sitio más fina) o no usarlo (recomendado al principio:
   pypdf/pikepdf + regenerar).
4. **El banco de documentos sin aprobación por ejecución** cuando solo crea versiones en la mesa del agente o del
   proyecto (recomendado; publicar fuera siempre con aprobación).
5. **Cambiar la imagen de Postgres a pgvector** (recomendado; un cambio de imagen en el VPS con copia de seguridad antes).

## Fuentes de la investigación

Google Drive: scopes y verificación (developers.google.com/workspace/drive/api/guides/api-specific-auth), notificaciones
(…/guides/push), límites (…/guides/limits). Microsoft Graph: File Picker v8, permisos `Selected`
(learn.microsoft.com/graph/permissions-selected-overview), verificación de editor, suscripciones. Dropbox: guía de
desarrolladores, detección de cambios, rendimiento. Box: límites, subidas, aprobación. GitHub: límites de la API REST y
Contents API. GitLab: límites de GitLab.com. Bitbucket: límites de la API y retiro de contraseñas de aplicación. rclone
(`librclone`). Claude: soporte de creación de archivos y repositorio `anthropics/skills`. Docling, Marker, MinerU.
Gemini: procesamiento de documentos, Files API, embeddings, precios, caché. pgvector 0.8, pg_textsearch, ParadeDB.
Anthropic "Contextual Retrieval" (2024-09-19). Herramienta de memoria de Claude. OWASP Top 10 para LLM 2025.
Sin verificar del todo: coste y plazo actuales de CASA; si una carpeta elegida con `drive.file` da acceso a su contenido;
cifras de throttling de Graph; tope de sesiones de subida de Dropbox; cuotas del nivel gratuito de Gemini.
