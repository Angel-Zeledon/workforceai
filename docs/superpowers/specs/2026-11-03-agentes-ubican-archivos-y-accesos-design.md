# Agentes que se ubican en los archivos y accesos por agente

Fecha: 2026-11-03. Estado: aprobado por el dueño en el chat ("si correcto").

## Por qué

El dueño quiere poder decir "puse las facturas del mes en PDF, hazme el estado de resultados" y que el agente encuentre
solo esos archivos, los lea bien y entregue números correctos. Y quiere dar accesos distintos a agentes distintos:
no todos deben ver las mismas carpetas.

## Lo que ya existe

- Herramientas de archivos del agente (listar, leer, buscar, escribir, mover) con auditoría `file.activity`; búsqueda
  por palabras y por significado (embeddings OpenAI encendidos en producción); lectura de texto de PDF (sin OCR),
  docx y xlsx; páginas como imagen con visión solo si el sandbox está encendido (apagado en producción);
  `fs.write_document` para xlsx/docx/pdf/csv (`docs/architecture/archivos-agentes.md` §1, `agent-search.md`).
- Zonas del agente (su mesa, proyectos asignados, `/Shared`, `/Clients`) y permisos de lectura por carpeta **solo por
  API**, sin pantalla (`archivos-agentes.md` §1 y §7, `equipos-oficinas.md` §5).
- Skills de fábrica de contabilidad (conciliación, cierre de mes, cuentas por cobrar/pagar…).

## Parte 1: el agente se ubica solo

Cuando la persona menciona archivos sin ruta ("las facturas del mes", "el contrato que subí ayer"):

1. **Orden de búsqueda:** (a) lo adjuntado en el mensaje; (b) lo que **esa persona** subió o movió en los últimos días
   (herramienta nueva de "archivos recientes": por persona, por tipo, por carpeta, con fecha); (c) búsqueda por palabras
   y por significado en las carpetas que el agente puede ver.
2. **Confirmar solo si hay duda:** si el conjunto es claro (p. ej. 23 PDF subidos hoy en una sola carpeta), sigue. Si hay
   ambigüedad (dos meses mezclados, dos carpetas candidatas, muy pocos o demasiados archivos), confirma en una línea
   antes de gastar: "Encontré 23 PDF en /Compartido/Facturas/Octubre, subidos hoy. ¿Uso esos?". Esa confirmación usa el
   mecanismo de "Necesita tu ayuda" del diseño `2026-11-03-proyectos-por-cliente-y-ayuda-design.md` cuando ocurre dentro
   de una tarea, y una pregunta normal de chat cuando ocurre en el chat.
3. **Sin acceso:** si lo que busca está en una carpeta que ese agente no puede ver, lo dice y ofrece "Darle acceso a
   esta carpeta" (para quien puede darlo) o "Pedir acceso" (para quien no). Nunca lo salta en silencio.

## Parte 2: números correctos (facturas → estado de resultados)

1. **Extraer, no sumar con el modelo:** el agente pasa cada factura a una fila estructurada: fecha, número, emisor,
   receptor, concepto, moneda, subtotal, impuestos, total, tipo (ingreso o gasto, según si la empresa emite o recibe),
   y la página de origen.
2. **El sistema calcula:** sumas, agrupaciones por cuenta o categoría y el estado de resultados (ingresos, costo,
   gastos, utilidad) se calculan de forma determinista en código, no en el modelo. Monedas distintas no se suman sin
   tipo de cambio indicado.
3. **Entrega:** un Excel con la hoja de detalle (una fila por factura, con enlace al archivo), la hoja del estado de
   resultados y una hoja de "Revisar" con las facturas ilegibles, duplicadas (mismo número y emisor), con totales que no
   cuadran (subtotal + impuestos ≠ total) o con dudas de clasificación. El resumen del chat dice cuántas se leyeron, el
   resultado y cuántas hay que revisar.
4. **PDF escaneados:** se leen como imagen con visión (gpt-5-mini acepta imágenes) **cuando el sandbox de render está
   encendido** en producción. Mientras no lo esté, esas facturas van a "Revisar" con el motivo "escaneada: no se pudo
   leer el texto", nunca se inventan.
5. La skill de fábrica correspondiente (estado de resultados desde facturas) describe el método y se asigna a
   contabilidad y finanzas.

## Parte 3: accesos por agente

1. **Pantalla:** en el panel de cada agente, sección "Carpetas que puede ver": lista de carpetas con nivel **Ninguno,
   Leer o Editar**, que vale para todo lo que hay dentro (herencia hacia abajo). Agregar carpeta con un selector del
   Drive. También desde Archivos: en una carpeta, "Qué agentes la ven".
2. **Por defecto** para un agente nuevo: leer `/Compartido` (`/Shared`), editar su propia carpeta y la de los proyectos
   donde trabaja. Lo demás se da a mano. Los agentes existentes conservan lo que ven hoy.
3. **Quién puede cambiarlos:** administradores de la empresa y el responsable del equipo del agente. Cada cambio queda
   en la auditoría.
4. **Se aplica en cada herramienta:** listar, leer, buscar (incluida la búsqueda por significado y los archivos
   recientes) y escribir filtran por estos accesos en cada llamada, no solo al empezar la tarea.
5. **Para después (E3):** el acceso efectivo será lo que el agente tiene ∩ lo que puede ver la persona que le pide.

## Metas, no metas, anti-metas

- **Metas:** decir qué archivos en lenguaje normal y que el agente los encuentre; números que cuadran; accesos claros
  por agente.
- **No metas:** contabilidad completa con plan de cuentas configurable; OCR propio; permisos por persona (E3).
- **Anti-metas:** sumas inventadas o mal hechas; leer archivos de otra carpeta sin permiso; preguntar de más cuando
  todo está claro; que una factura ilegible desaparezca del resultado sin aviso.

## Restricciones

- Reutilizar Drive, docindex, herramientas de archivos, `fs.write_document`, skills y auditoría existentes.
- Encender el sandbox en producción requiere instalar gVisor en el VPS compartido: es una operación aparte que el dueño
  coordina; este diseño funciona sin ella (escaneadas a "Revisar").
- Textos es/en/pt, estilo de la app, pruebas sin llamadas reales a proveedores.

## A criterio de quien construye

Nombres de herramientas y tablas, cuántos días cuentan como "recientes" (por defecto 14), umbrales de ambigüedad, el
formato exacto del Excel y el diseño de la sección de accesos dentro del estilo de la app.
