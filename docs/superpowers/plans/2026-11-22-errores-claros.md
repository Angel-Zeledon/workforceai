# Errores claros (2026-11-22)

Objetivo: que ninguna pantalla muestre texto de código; cada fallo dice qué pasa y qué hacer.

1. Backend: `internalFail` (500 sin texto interno, con `ref` en el log) y `badRequestError` (JSON mal formado). Pruebas en `api/errors_clear_test.go`.
2. Catálogo central `lib/errorsCore.ts` + `lib/errors.ts` + claves `err.*` (es/en/pt). Pruebas `lib/errors.test.mjs`.
3. `ErrorNote`, `toastError`, límites de error de React (página, raíz, global).
4. Sustituir los sitios que mostraban `e.message` / `String(e)` / `({code})`; la guarda del test impide la regresión.
5. Pendiente: migrar del todo los traductores sueltos de cada módulo (`deptLogic`, `goalErrorKind`, `slack.test.*`) al catálogo; hoy ya dicen frases humanas y se mantienen.
