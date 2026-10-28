# Cadena de evidencia (B6)

Clic en una cifra de un informe para llegar a la celda, el archivo (en la versión leída), el correo u otro
elemento de una conexión, o la tarea de donde salió. Pensado para el cierre mensual de despachos contables: cada
cifra debe poder rastrearse.

Código: `backend/internal/application/evidence.go` (verificación y read model), `backend/internal/domain/domain.go`
(`Citation`, `CitationSource`), `backend/internal/artifacts/context_source.go` (`ResolveCell`), fuentes de lectura
en `gateway/gateway.go`, `drive/gwtool.go` y `memory/gwtool.go` (`ReadSource`); runtime en `agent-runtime/app/models.py`
(`Citation`), `security.py` (`CITATION_RULE`), `simulation.py` y `review.py`; UI en `frontend/src/components/evidence/`,
`frontend/src/lib/evidenceLogic.ts` y `ReportView.tsx`. Capturas: `docs/screenshots/b6-evidence/`.

## Contrato

El agente propone, en su salida, `citations: [{value, label, ref}]` (opcional; máx. 30; textos acotados; los
elementos mal formados se descartan sin invalidar la salida). `ref` es uno de:

| ref | Qué es | Se verifica contra |
|---|---|---|
| `task:<id>` | Salida de otra tarea | Dependencias e índice del proyecto que recibió **esta** tarea, o memorias `task:` recordadas |
| `report:<id>`, `memory:<id>` | Memoria de la organización | `recalled` de la tarea o resultados de `memory.search` de **esta** tarea |
| `file:<ruta o file_id>` | Archivo del drive | Lecturas `fs.read` de **esta** tarea (se guarda `file_id`, ruta y **versión** leída); o memoria `file:/ruta` recordada |
| `cell:<artefacto>!<hoja>!<A1>` | Celda de una hoja | El artefacto debe haber llegado a la tarea como contexto; la celda debe existir y su valor (`v`) **coincidir** con la cifra |
| `conn:<herramienta>:<id>` | Elemento de una conexión (correo, evento…) | Ids de los elementos que devolvieron las lecturas por conector de **esta** tarea (el mismo id del bloque `untrusted_data`) |

Ajuste respecto al diseño propuesto: el formato de conector es `conn:<herramienta>:<id>` (p. ej. `conn:email:…`) y no
`conn:<alias>:<id>`, porque el runtime nunca ve el alias ni el id de conexión: solo ve la herramienta y el id del
elemento. El proveedor se resuelve en el backend.

## Anti-fabricación

- El backend **descarta** `status`/`source` que mande el runtime y los recalcula; una cita solo es `verified` si su
  referencia está en lo que la tarea vio (registro en memoria por ejecución, `run.ext.seen`, alimentado por los ids
  estructurados `GatewayOutcome.Sources`, que nunca llegan al runtime).
- Lo no verificable queda `unverified` con `reason` (`not_seen`, `not_found`, `value_mismatch`, `malformed`,
  `unknown_kind`) y **sin** `source`: la UI nunca lo enlaza; lo lista como advertencia. Valores sin dígitos se descartan.
- `value_checked=true` cuando el backend encontró la cifra exacta en la fuente (siempre en celdas; en tareas y
  memorias si aparece en su texto). `false` en una cita verificada = la fuente sí se leyó pero la cifra no aparece
  literal (cálculo derivado, o contenido que el backend no conserva: archivos y conectores).
- Cada salida con citas deja una auditoría `evidence.citations_checked` (verificadas, no verificadas, descartadas,
  motivos). La verificación ocurre tras cada respuesta del runtime (`runTask`, rehacer de la revisión de calidad y
  seguimiento de `code.run`), antes del checkpoint durable.
- No cambia permisos, aprobaciones, kill switch, modo solo lectura, pausa ni topes: no ejecuta nada, solo compara
  ids. Una celda se lee con la misma regla de acceso que el contexto de artefactos.

## Almacenamiento y API

Las citas viven en `tasks.output` (JSONB): **sin migración** (no hizo falta la 795). Read model:
`GET /api/v1/reports/{id}/evidence` (`reports:read`) →
`{report_id, request_id, verified, total, citations: [{value, label, ref, kind, status, reason, value_checked, source, task_id, agent_id}]}`,
verificadas primero; las no verificadas nunca traen `source`. Un backend anterior sin la ruta responde 404 y la UI
simplemente no enlaza nada.

## UI

`ReportView` pide la evidencia y enlaza las cifras del resumen y de las secciones que **coinciden numéricamente**
con una cita verificada (ambas convenciones decimales; los porcentajes solo con porcentajes; números sueltos menores a
10 sin moneda ni % no se enlazan por ambiguos). El panel de origen muestra: la salida de la tarea con la cifra
marcada; la hoja con la celda resaltada y desplazada a la vista; el archivo con botón para abrir **la versión leída**
en el visor; o la tarjeta de metadatos del conector (proveedor, herramienta, id, cuándo se leyó; sin contenido de
terceros). Textos en es/en/pt.

Modo mock (`NEXT_PUBLIC_MOCK=true`): el escenario de $50,000 trae citas de ejemplo (hoja `art_demo_margin`, archivo
versionado `/Shared/Finanzas/costo-capacidad.csv`, correo simulado de Gmail y una cifra a propósito no verificada).

## Límites conocidos (no verificado / pendiente)

- Probado con el runtime simulado y con Gmail simulado; **no** con un modelo real ni con un buzón real: no se midió
  con qué frecuencia un LLM real emite citas correctas.
- La simulación solo cita `task:` (métricas de dependencias) y el auditor sus cifras comparadas; las citas `cell:`,
  `file:` y `conn:` de punta a punta están probadas en tests del backend y en el mock del frontend.
- Lecturas web (`web.search`/`web.fetch`) e imágenes aún no son fuentes citables.
- Las celdas de archivos `.xlsx` del drive no son `cell:` (solo hojas-artefacto); se citan como `file:`.
- El informe sintetizado no cita por sí mismo: sus cifras se enlazan si coinciden con citas de las tareas.
