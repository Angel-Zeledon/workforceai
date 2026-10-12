---
name: bank-reconciliation
description: "Concilia un extracto bancario contra el libro mayor o los registros de cobros y pagos y deja las diferencias explicadas. Úsala cuando haya que cuadrar un banco, una caja o un cierre de efectivo."
metadata:
  category: finance
  builtin: "true"
---

# Conciliación bancaria

## Cuándo usarla
Cuando recibas un extracto (csv, xlsx o texto) y deba cuadrarse contra los registros contables.

## Pasos
1. Identifica el periodo, la cuenta y la moneda. Si algo falta, dilo y pregunta antes de seguir.
2. Normaliza ambos lados a columnas: fecha, concepto, referencia, importe (con signo) y saldo.
3. Empareja por referencia exacta; después por importe y fecha con tolerancia de 3 días; al final por importe agrupado.
4. Clasifica lo que no empareja: partidas en tránsito, comisiones o intereses, errores de registro, duplicados o pendientes de identificar.
5. Calcula el saldo conciliado: saldo del banco + depósitos en tránsito - cheques o pagos pendientes = saldo contable ajustado.
6. Entrega la tabla de diferencias (usa `templates/conciliacion.csv`) y propón el asiento de ajuste, sin registrarlo.

## Criterios de calidad
- Cada diferencia tiene causa y responsable propuesto.
- La suma de las partidas explicadas cuadra con la diferencia total; si no cuadra, dilo y muestra el residuo.
- Las cifras citan la fila de origen.

## Límites
Prepara el análisis y el borrador del ajuste. Registrar asientos o mover dinero requiere aprobación humana.
