---
name: income-statement-from-invoices
description: "Arma el estado de resultados de un periodo a partir de las facturas (PDF, xml o imagen) que la persona dejó en el Drive: encuentra los archivos, pasa cada factura a una fila y deja que el sistema haga las cuentas. Úsala cuando pidan \"hazme el estado de resultados con las facturas del mes\"."
metadata:
  category: finance
  builtin: "true"
---

# Estado de resultados desde facturas

## Cuándo usarla
Cuando la persona menciona facturas emitidas y recibidas de un periodo y quiere ingresos, costos, gastos y utilidad.

## Pasos
1. **Encuentra los archivos sin pedir la ruta:** primero lo adjuntado; después `recent` (lo que esa persona subió o movió en los últimos 14 días, por carpeta y tipo, por ejemplo `kind: "pdf"`); al final `search` por palabras y por significado. Si el conjunto es claro (todos en una carpeta, del mismo periodo), sigue. Si hay dos meses mezclados, dos carpetas candidatas o muy pocos o demasiados archivos, confirma en una línea antes de seguir.
2. **Sin acceso:** si `recent` dice "no access" o una lectura responde `not_granted`, dilo y ofrece "Darle acceso a esta carpeta" o "Pedir acceso". No lo saltes.
3. **Extrae, no sumes:** lee cada factura y pásala a una fila: fecha (AAAA-MM-DD), número, emisor y su RFC/NIF, receptor y su RFC/NIF, concepto, categoría sugerida (Ventas, Costo de ventas, Renta, Servicios, Nómina...), moneda, subtotal, impuestos, total, tipo (ingreso o gasto) y archivo con página. Copia los importes como aparecen.
4. **Escaneadas e ilegibles:** si una factura no tiene texto y no se pudo ver como imagen, agrégala con `status: "scanned"`; si no se entiende, `status: "unreadable"` con el motivo. Nunca completes un importe.
5. **Deja que el sistema calcule:** pide `income_statement` con la empresa (nombre y RFC/NIF: lo emitido es ingreso, lo recibido es gasto), la moneda base, los tipos de cambio solo si la persona los dio, y todas las filas. El sistema suma, agrupa por categoría, detecta duplicados (mismo número y emisor), totales que no cuadran (subtotal + impuestos ≠ total) y monedas sin tipo de cambio, y escribe el Excel con las hojas Detalle, Estado de resultados y Revisar.
6. **Entrega:** en el resumen di cuántas facturas se leyeron, la utilidad antes de impuestos y cuántas hay que revisar, con las cifras exactas que devolvió el sistema, y la ruta del Excel.

## Criterios de calidad
- Ninguna factura desaparece: está en el resultado o en Revisar con su motivo.
- Las cifras del resumen son las del sistema, sin redondeos propios.
- Monedas distintas nunca se suman sin tipo de cambio indicado por una persona.

## Límites
No es contabilidad completa: no lleva plan de cuentas configurable ni calcula impuestos sobre la renta. Registrar asientos o presentar declaraciones requiere a una persona.
