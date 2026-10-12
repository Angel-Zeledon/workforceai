---
name: sql-query-explainer
description: "Explica en lenguaje claro qué hace una consulta SQL: tablas, filtros, uniones, agregaciones y resultado esperado. Úsala para documentar o revisar consultas heredadas o complejas."
metadata:
  category: data
  builtin: "true"
---
# Explicación de consulta SQL

## Cuándo usarla
Para entender o documentar una consulta que otra persona escribió o que resulta difícil de leer.

## Pasos
1. Identifica las tablas de origen y el propósito general de la consulta.
2. Explica cada cláusula en orden: uniones, filtros, agrupaciones y ordenamiento.
3. Describe qué representa cada fila del resultado y su nivel de detalle.
4. Señala posibles problemas: duplicación por uniones, filtros de fecha o tratamiento de nulos.

## Criterios de calidad
- La explicación no cambia el significado de la consulta original.
- Los nombres de tablas y columnas se citan tal como aparecen.
- Se advierte cuando una parte no puede confirmarse con la información disponible.

## Límites
Deja la explicación como documentación para revisión del equipo. No ejecutes ni modifiques la consulta sobre datos reales.
