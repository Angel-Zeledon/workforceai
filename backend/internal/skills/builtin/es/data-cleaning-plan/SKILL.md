---
name: data-cleaning-plan
description: "Diseña un plan para limpiar un conjunto de datos: problemas detectados, reglas de corrección, validaciones y orden de ejecución. Úsala antes de analizar datos de fuentes desordenadas."
metadata:
  category: data
  builtin: "true"
---
# Plan de limpieza de datos

## Cuándo usarla
Para ordenar el trabajo de depuración de un conjunto de datos antes de analizarlo.

## Pasos
1. Describe el conjunto: fuente, número de filas, columnas y propósito del análisis.
2. Lista los problemas detectados: duplicados, vacíos, formatos mixtos y valores atípicos.
3. Para cada problema define la regla de corrección y qué hacer con los casos dudosos.
4. Añade validaciones de control y el orden en que se aplican las reglas.

## Criterios de calidad
- Cada regla indica cuántos registros afecta, según los datos disponibles.
- Ninguna corrección elimina datos sin dejar constancia del criterio usado.
- Otra persona puede seguir el plan sin preguntas adicionales.

## Límites
Deja el plan como propuesta para validación de la persona responsable de datos. No inventes cifras de registros afectados que no se conozcan.
