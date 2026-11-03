---
name: legal-deadline-tracker
description: "Registra plazos legales y contractuales (vencimientos, renovaciones, avisos previos y periodos de respuesta) con fechas de alerta. Úsala para no perder fechas de contratos, trámites o notificaciones."
metadata:
  category: legal
  builtin: "true"
---

# Registro de plazos legales y contractuales

## Cuándo usarla
Cuando haya varios contratos o trámites con fechas que cumplir y se necesite un registro único con alertas.

## Pasos
1. Lista cada documento o trámite con su fuente y la cláusula o norma que fija el plazo.
2. Calcula la fecha límite a partir del evento que la inicia, por ejemplo la recepción de una notificación. Anota el criterio de cómputo (días hábiles o naturales y desde qué día se cuenta) y verifica las reglas de tu país.
3. Calcula la fecha de aviso previo, por ejemplo 30 o 60 días antes del vencimiento, y una fecha de alerta interna.
4. Asigna un responsable y un estado (pendiente, en curso, cumplido o vencido).
5. Ordena el registro por fecha límite y destaca lo que vence en los próximos 30 días.
6. Completa `templates/registro-plazos.csv` y revísalo cada semana con los responsables.

## Criterios de calidad
- Cada plazo cita su fuente exacta (cláusula o artículo).
- Las fechas se calculan con un criterio explícito; si el criterio es dudoso, se marca.
- Ningún plazo queda sin responsable ni sin fecha de alerta.
- Los plazos vencidos aparecen al inicio, con una acción propuesta.

## Límites
No sustituye un sistema de vencimientos con respaldo ni la confirmación de un abogado para plazos procesales. Las fechas se verifican en el documento original antes de actuar.
