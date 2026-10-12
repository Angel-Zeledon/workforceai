---
name: bug-report-triage
description: "Clasifica un reporte de error por severidad, impacto, reproducibilidad y área responsable, y pide la información que falta. Úsala al recibir reportes de fallos de usuarios o de pruebas."
metadata:
  category: engineering
  builtin: "true"
---
# Triaje de reporte de error

## Cuándo usarla
Para ordenar los reportes de error que llegan y decidir su prioridad.

## Pasos
1. Resume el fallo en una frase: qué ocurre, dónde y a quién afecta.
2. Verifica si el reporte incluye pasos para reproducirlo, versión, entorno y evidencia.
3. Asigna severidad e impacto según los usuarios afectados y si bloquea una función clave.
4. Indica el área probable y enumera la información que falta para avanzar.

## Criterios de calidad
- La severidad se justifica con un dato concreto del reporte.
- Los duplicados se marcan con la referencia del caso original, si está en los datos.
- Ninguna causa raíz se afirma sin evidencia.

## Límites
Deja la clasificación como propuesta para la persona responsable de ingeniería. No cambies prioridades ni asignes personas sin aprobación.
