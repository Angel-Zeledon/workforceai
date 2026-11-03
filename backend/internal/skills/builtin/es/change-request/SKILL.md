---
name: change-request
description: "Redacta una solicitud de cambio en un sistema o proceso: motivo, impacto, riesgos, plan de reversa y aprobaciones necesarias. Úsala antes de implementar un cambio que pueda afectar al servicio."
metadata:
  category: it
  builtin: "true"
---

# Solicitud de cambio

## Cuándo usarla
Antes de modificar un sistema, una configuración o un proceso que usan personas de la organización, para que el cambio se revise, se programe y pueda revertirse si algo sale mal.

## Pasos
1. Describe el cambio y su motivo: qué se modifica, por qué y qué problema o necesidad resuelve.
2. Identifica el impacto: sistemas, procesos, usuarios y áreas afectadas, y el momento propuesto dentro de la ventana de mantenimiento.
3. Evalúa los riesgos con probabilidad e impacto, incluyendo dependencias y lo que no se puede probar antes de implementar.
4. Escribe el plan de implementación en pasos verificables y el plan de reversa: cómo volver al estado anterior y cuánto tiempo toma.
5. Define las pruebas de validación y los criterios para dar el cambio por bueno o para revertirlo.
6. Lista las aprobaciones requeridas según la clase de cambio (normal, urgente o estándar) y deja los espacios para las firmas (usa `templates/solicitud_cambio.md`).

## Criterios de calidad
- El plan de reversa es ejecutable y tiene un tiempo estimado.
- Cada riesgo alto tiene una mitigación o una aceptación explícita.
- La ventana y el impacto coinciden con los usuarios y procesos afectados.
- Las aprobaciones necesarias están identificadas antes de la implementación.

## Límites
Prepara la solicitud. No implementa cambios, no despliega ni modifica configuraciones y no aprueba el cambio. Las aprobaciones y la ejecución las dan las personas responsables del sistema y el comité de cambios, si existe.
