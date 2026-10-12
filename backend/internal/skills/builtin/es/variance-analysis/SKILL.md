---
name: variance-analysis
description: "Analiza variaciones entre real y presupuesto o entre periodos, separa precio, volumen y mezcla, y explica las causas con cifras. Úsala en informes de gestión y revisiones de resultados."
metadata:
  category: analysis
  builtin: "true"
---

# Análisis de variaciones

## Cuándo usarla
Cuando se comparan cifras reales contra presupuesto, contra el periodo anterior o contra el año anterior.

## Pasos
1. Fija la base de comparación y las unidades (moneda, miles, porcentaje).
2. Calcula variación absoluta y porcentual por línea; ordénalas por impacto.
3. En las tres mayores, descompón en precio, volumen y mezcla cuando haya datos de unidades.
4. Busca la causa en los datos disponibles; si es una hipótesis, márcala como hipótesis.
5. Entrega la tabla (usa `templates/tabla-variaciones.csv`) y tres conclusiones con acción sugerida.

## Criterios de calidad
- Las cifras suman: la suma de variaciones por línea es igual a la variación total.
- Diferencia siempre el hecho (cifra) de la interpretación.
- Sin cifras inventadas: lo que no esté en los datos queda como pendiente.
