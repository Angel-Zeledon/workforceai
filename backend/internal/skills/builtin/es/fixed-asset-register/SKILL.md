---
name: fixed-asset-register
description: "Mantiene el registro de activos fijos y calcula la depreciación lineal mensual y acumulada de cada activo. Úsala al dar de alta equipo, al cerrar el mes o al preparar estados financieros."
metadata:
  category: finance
  builtin: "true"
---

# Registro de activos fijos

## Cuándo usarla
Cuando haya altas, bajas o traslados de activos, o cuando haya que calcular la depreciación del periodo y el valor neto en libros.

## Pasos
1. Lista cada activo con su identificador interno, descripción, categoría, fecha de adquisición, fecha de inicio de uso, costo, valor residual estimado y vida útil en meses según la política de la empresa.
2. Calcula la depreciación mensual en línea recta: (costo menos valor residual) dividido entre la vida útil en meses. La depreciación empieza el mes en que el activo entra en uso.
3. Calcula la depreciación acumulada a la fecha de corte y el valor neto en libros: costo menos depreciación acumulada.
4. Marca los activos totalmente depreciados, dados de baja, vendidos, no localizados o con cambio de ubicación o de responsable.
5. Compara el costo y la depreciación acumulada del registro con los saldos del mayor de activos fijos y de depreciación acumulada. Explica cualquier diferencia.
6. Entrega el registro (usa `templates/registro_activos_fijos.csv`) y el cargo de depreciación del periodo por categoría, como propuesta de asiento sin registrarlo.

## Criterios de calidad
- Costo menos depreciación acumulada da el valor neto de cada activo, y el total cuadra con el mayor.
- Ningún activo se deprecia por debajo de su valor residual ni antes de entrar en uso.
- Cada activo tiene identificador, responsable y ubicación.
- Las altas y bajas del periodo están respaldadas por factura, acta o documento de baja.

## Límites
Aplica el método lineal descrito. Si la política usa otro método, o si la vida útil fiscal difiere de la contable, verifica la normativa aplicable. No da de alta, baja ni vende activos y no registra asientos. Los deterioros o valores de mercado requieren una evaluación separada.
