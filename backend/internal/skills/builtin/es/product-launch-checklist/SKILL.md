---
name: product-launch-checklist
description: "Lista de verificación para lanzar una función: listo técnico, soporte, comunicación, métricas y plan de reversa con responsables. Úsala antes de publicar un cambio para clientes."
metadata:
  category: product
  builtin: "true"
---

# Lista de lanzamiento de producto

## Cuándo usarla

Antes de publicar una función o un cambio importante para clientes, para confirmar que cada área está lista y que existe un plan si algo falla. Usa `templates/lista-lanzamiento.md` como estructura.

## Pasos

1. Define el alcance: qué cambia, para qué segmento de clientes, en qué fecha de publicación y qué queda fuera.
2. Confirma el listo técnico: pruebas completadas, rendimiento revisado, migraciones aplicadas en el orden correcto y permisos verificados, cada punto con su responsable.
3. Prepara soporte: artículos de ayuda publicados, respuestas a las preguntas esperadas y capacitación del equipo que atiende clientes.
4. Revisa la comunicación: mensaje interno, notas para clientes, aviso dentro del producto y aprobación de marca o legal cuando aplique.
5. Define las métricas de éxito y de alerta, con su línea base y el umbral que obliga a revisar el lanzamiento.
6. Escribe el plan de reversa: cómo se desactiva la función, quién decide, cuánto tarda y qué pasa con los datos y cuentas ya afectadas.
7. Fija el criterio de salida: las condiciones que deben cumplirse durante la ventana de observación para declarar el lanzamiento estable.

## Criterios de calidad

- Cada elemento de la lista tiene un responsable y un estado.
- El plan de reversa se puede ejecutar sin la persona que construyó la función.
- Cada métrica tiene línea base y umbral, no solo nombre.
- Ningún elemento se marca como listo sin evidencia verificable.

## Límites

Es una lista para revisión del equipo de producto y no decide por sí sola si la función se lanza. No fija fechas que no consten en los datos ni marca como completado lo que no se verificó. No publica la función, no envía comunicaciones a clientes y no ejecuta el plan de reversa.
