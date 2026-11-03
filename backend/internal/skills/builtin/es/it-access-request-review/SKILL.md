---
name: it-access-request-review
description: "Revisa una solicitud de acceso a un sistema: necesidad, mínimo privilegio, aprobador, vencimiento y riesgos. Úsala antes de conceder, ampliar o renovar permisos de usuarios."
metadata:
  category: it
  builtin: "true"
---

# Revisión de solicitud de acceso

## Cuándo usarla
Cuando alguien pide acceso nuevo, más permisos o la renovación de un acceso a un sistema, y hace falta decidir si se justifica y con qué alcance.

## Pasos
1. Identifica quién pide el acceso, a qué sistema, con qué nivel (lectura, edición o administración) y para qué tarea concreta. Si falta algún dato, la solicitud queda pendiente.
2. Verifica la necesidad: que la tarea requiera ese acceso y que el puesto o proyecto del solicitante lo justifique.
3. Aplica el mínimo privilegio: propone el rol más limitado que cubra la tarea y evita permisos de administrador o amplios por comodidad.
4. Identifica al aprobador según la matriz del sistema (jefatura y dueño del sistema) y revisa si hay conflicto de funciones, por ejemplo quien registra pagos y también los aprueba.
5. Define la vigencia: fecha de vencimiento o de revisión, y si el acceso es temporal, cuándo se retira.
6. Entrega el resumen de revisión con una recomendación: aprobar, aprobar con condiciones, reducir el alcance o rechazar, con la razón en una frase.

## Criterios de calidad
- Cada acceso propuesto tiene una tarea y una justificación escritas.
- El nivel pedido es el mínimo que cubre la tarea, o la reducción está explicada.
- Hay un aprobador identificado y ningún conflicto de funciones sin mitigar.
- Todo acceso temporal tiene fecha de vencimiento o de revisión.

## Límites
Revisa y recomienda. No concede, modifica ni retira accesos, no crea cuentas y no maneja credenciales ni contraseñas. La aprobación y la ejecución las realiza el equipo de TI o el dueño del sistema. No registres contraseñas ni datos personales innecesarios en la revisión.
