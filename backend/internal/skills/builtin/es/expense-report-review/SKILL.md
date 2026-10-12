---
name: expense-report-review
description: "Revisa un informe de gastos: comprueba recibos, categorías, políticas, importes y duplicados antes de aprobarlo. Úsala al validar reembolsos o rendiciones de gastos."
metadata:
  category: finance
  builtin: "true"
---
# Revisión de informe de gastos

## Cuándo usarla
Para validar gastos presentados por una persona antes de aprobar su reembolso.

## Pasos
1. Cruza cada gasto con su comprobante: fecha, importe, proveedor y moneda deben coincidir.
2. Clasifica cada partida según la política de gastos y marca las que excedan límites o no tengan justificación.
3. Busca duplicados por importe, fecha y proveedor, y señala los casos dudosos.
4. Resume el total aprobable, el total observado y las preguntas pendientes para la persona responsable.

## Criterios de calidad
- Cada partida observada indica el motivo concreto y la política aplicable.
- Los totales coinciden con la suma de partidas aprobadas y observadas.
- Ningún gasto sin comprobante queda aprobado sin marcarlo.

## Límites
La aprobación final y cualquier reembolso corresponden a la persona aprobadora. No supongas políticas ni importes que no estén en los datos.
