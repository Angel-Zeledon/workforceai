---
name: security-incident-checklist
description: "Guía los primeros pasos ante un incidente de seguridad: contener, registrar, avisar a quien corresponda y preservar la evidencia. Úsala en cuanto se sospeche de un acceso o fuga no autorizados."
metadata:
  category: it
  builtin: "true"
---

# Lista de primeros pasos ante un incidente de seguridad

## Cuándo usarla
En cuanto haya sospecha de acceso no autorizado, pérdida de datos, malware o una cuenta comprometida, para ordenar la respuesta mientras el equipo responsable toma el control.

## Pasos
1. Registra la hora de detección, quién lo reporta, qué se observó y qué sistemas o datos podrían estar afectados. Anota solo hechos verificados y separa claramente las sospechas.
2. Propón las acciones de contención para que el equipo de TI las ejecute: aislar el equipo o la cuenta afectada, cambiar credenciales comprometidas o suspender accesos. No apagues equipos ni borres archivos antes de preservar la evidencia.
3. Preserva la evidencia: registros, capturas, correos y archivos sospechosos, con fecha, hora y nombre de quien los recogió.
4. Avisa según el plan de respuesta de la organización: a la persona responsable de seguridad, a dirección y a legal. Si aplica, identifica a los afectados y los plazos de notificación que exija la ley vigente.
5. Evalúa el alcance y la gravedad: sistemas involucrados, cantidad de registros, si hay datos personales o confidenciales, y asigna una prioridad.
6. Lleva una bitácora con cada acción, su responsable y su hora, y agenda la revisión posterior al incidente (usa `templates/checklist_incidente.md`).

## Criterios de calidad
- Cada acción de contención tiene responsable y hora registradas.
- La evidencia se preserva antes de cualquier limpieza o restauración.
- Los hechos confirmados están separados de las hipótesis.
- Queda identificado a quién se avisó, cuándo y por qué canal.

## Límites
Guía y registra. No ejecuta acciones en sistemas, no decide por sí solo si existe obligación de notificar y no redacta comunicados externos sin revisión de legal. Las decisiones de contención y de notificación las toman las personas responsables. No incluyas contraseñas ni datos personales reales en la bitácora. Esta lista no sustituye el plan de respuesta a incidentes de la organización.
