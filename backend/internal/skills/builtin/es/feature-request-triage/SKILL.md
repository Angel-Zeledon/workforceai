---
name: feature-request-triage
description: "Clasifica pedidos de funciones con criterios comunes: problema, frecuencia, valor, esfuerzo y decisión. Úsala para ordenar la lista de solicitudes antes de planear producto."
metadata:
  category: product
  builtin: "true"
---

# Clasificación de pedidos de funciones

## Cuándo usarla

Cuando hay una lista acumulada de solicitudes de funciones y el equipo de producto necesita decidir cuáles evaluar. Usa `templates/clasificacion-pedidos.csv` como estructura.

## Pasos

1. Reescribe cada pedido como un problema del usuario, no como una función: qué intenta lograr y qué se lo impide hoy.
2. Une los pedidos duplicados o muy parecidos y cuenta cuántos clientes o casos distintos los piden, con cifras reales del registro. Si no existen, deja el campo vacío.
3. Asigna valor de 1 a 5 según el impacto para el usuario y para el negocio, con una frase que justifique la calificación.
4. Asigna esfuerzo de 1 a 5 según lo que indique el equipo técnico. Si no se ha estimado, escribe "por estimar".
5. Ordena por valor y frecuencia, y usa el esfuerzo como desempate. Cambia el orden solo si hay una razón escrita.
6. Asigna una decisión a cada pedido: evaluar en la próxima planeación, mantener en lista o descartar, con el motivo del descarte.

## Criterios de calidad

- Cada pedido tiene un problema escrito y una decisión.
- Las calificaciones de valor y esfuerzo tienen una justificación de una frase.
- Los descartes indican el motivo para poder revisarlos después.
- No se usan cifras de frecuencia que no existan en los datos.

## Límites

Deja la clasificación como propuesta para el equipo de producto. No fija fechas, no asigna responsables ni cambia el roadmap. No promete a quienes hicieron el pedido que la función se construirá.
