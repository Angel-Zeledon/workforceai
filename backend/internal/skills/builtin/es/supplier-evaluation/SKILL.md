---
name: supplier-evaluation
description: "Evalúa periódicamente a un proveedor por calidad, puntualidad, precio, servicio y riesgos, con una calificación ponderada y acciones de mejora. Úsala cada trimestre o tras un problema relevante."
metadata:
  category: operations
  builtin: "true"
---

# Evaluación de proveedor

## Cuándo usarla
Para revisar el desempeño de un proveedor activo en un periodo definido, decidir si se mantiene, si necesita un plan de mejora o si conviene buscar una alternativa.

## Pasos
1. Define el periodo de evaluación y reúne los datos: entregas recibidas y a tiempo, devoluciones o no conformidades, facturas comparadas contra la tarifa acordada, incidencias de atención y cambios en su situación financiera, legal o de capacidad.
2. Calcula cada criterio con su indicador: porcentaje de entregas a tiempo, tasa de no conformidad, variación de precio contra contrato y tiempo de respuesta a incidencias.
3. Asigna una puntuación de 1 a 5 a cada criterio según una escala definida antes de evaluar (por ejemplo, 5 supera lo pactado, 3 cumple, 1 incumple de forma reiterada). Los pesos sugeridos son calidad 30 %, puntualidad 25 %, precio 20 %, servicio 15 % y riesgos 10 %; la persona responsable puede cambiarlos al inicio.
4. Calcula la calificación ponderada y clasifica al proveedor como preferente, aprobado, en observación o a reemplazar.
5. Registra los riesgos detectados y, para cada uno, una acción de mitigación con responsable y fecha.
6. Entrega la ficha de evaluación (usa `templates/evaluacion_proveedor.csv`) y una lista de mejoras para revisar con el proveedor.

## Criterios de calidad
- Cada puntuación tiene su indicador o su evidencia; no se califica por impresión.
- Los pesos y la escala se indican al inicio y son los mismos para todos los proveedores comparados.
- La clasificación final se deriva de la calificación ponderada, no de una opinión aparte.
- Las incidencias aisladas se distinguen de un patrón que se repite.

## Límites
Evalúa y recomienda. No suspende proveedores, no negocia precios ni envía avisos formales. Una decisión de terminar un contrato o cambiar de proveedor la toma la persona responsable de compras con apoyo de legal, sobre datos verificados.
