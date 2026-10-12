# Memoria a largo plazo (M1)

Objetivo: que los agentes recuerden trabajo de semanas atrás y en proyectos grandes. Complementa `04-memoria.md` (la memoria corta `ctx.memory` sigue igual).

## Qué se indexa

| Fuente | Cómo | Quién la ve |
|---|---|---|
| Salidas de tareas terminadas (resumen, hallazgos, recomendaciones, métricas y la solicitud) | tabla `memory_docs`, al terminar la tarea (`completeTask`) | la organización |
| Informes | `memory_docs`, al crearse el informe | la organización |
| Lecciones (`output.memory_notes[]`) | `memory_docs` (`kind=note`, ámbito `agent`) | solo el agente que las escribió |
| Archivos de texto del drive | vector `drive_files.search_mem` (columna generada: Postgres lo mantiene en cada escritura; mover o borrar nunca lo deja viejo) | solo lo que el agente puede leer |

Migración **550** (`550_memory.sql`, bajada en `backend/migrations/down/`). No se copian los archivos: el drive ya guarda su texto extraído. Un *backfill* idempotente al arrancar indexa las tareas y los informes anteriores a la memoria. `memory_docs` tiene RLS por organización. El reset de demo la vacía.

Búsqueda de texto completo en español (raíces y stop words), sin acentos: `mem_fold()` (SQL) e `internal/textfold` (Go) pliegan igual, también las cifras (`$50,000` = `50000`). El inglés coincide por forma exacta.

## Permisos

* Archivos: solo bajo `drive.AgentFS.ReadableRoots` (escritorio propio, proyectos asignados, `/Shared` y carpetas con permiso), la misma regla que `fs.search`. Un error al calcular el alcance no devuelve nada.
* Notas: privadas del agente. Documentos (tareas, informes): de la organización; los informes ya los ve toda la organización. Los archivos de otros se marcan `foreign` y manchan la tarea (`tainted`) como cualquier lectura.
* Borrar (UI o API) deja una marca (`suppressed`) que ninguna reindexación revierte; queda auditado.

## Recuperación

`memory.Retriever` es la interfaz: `Retrieve(ctx, org, Query) ([]Item, error)`. Hoy hay uno, `FTS` (documentos + archivos): ranking por texto, cobertura de términos, mismo proyecto o su carpeta ×1,4, notas ×1,15, recencia suave y descarte de lo muy débil. `Multi` combina retrievers con fusión por rango recíproco: **la costura para pgvector** es un segundo `Retriever` (con su migración) añadido a `Multi`, sin tocar al orquestador, la herramienta ni la API. Todavía no hay proveedor de embeddings.

En cada tarea el orquestador consulta con título + descripción + solicitud (excluye la propia solicitud) con presupuesto de ≤ 6 elementos y ≈ 2,5k caracteres, y los entrega en `recalled`: datos **no confiables**, delimitados, saneados y con secretos redactados, cada uno con su `ref`: `task:<id>`, `report:<id>`, `file:<ruta>` (se abre con `fs.read`) o `memory:<id>`. El agente puede pedir más con `memory.search`.

## Lecciones

El agente puede declarar `memory_notes[]` en su salida (opcional, retrocompatible): como máximo 4 notas de ≤ 280 caracteres. Las que contengan secretos o parezcan una inyección se descartan (no se guardan redactadas).

## UI y simulación

Pestaña **Memoria** del panel del agente: lista con fuentes, buscador (lo que recordaría el agente) y olvidar. La simulación escribe una nota por tarea y, con la marca `[sim:memory]` en el texto, pide `memory.search` una vez; así el e2e (`e2e/tests/memory.spec.ts`) funciona sin LLM.
