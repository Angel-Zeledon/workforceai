---
name: internal-control-test
description: "Documenta la prueba de un control interno: objetivo, muestra, procedimiento, resultado y hallazgos. Úsala para probar el diseño o la operación de un control en una auditoría o revisión."
metadata:
  category: audit
  builtin: "true"
---

# Prueba de control interno

## Cuándo usarla
Cuando una auditoría o revisión necesite evidencia de que un control existe (prueba de diseño) o funcionó durante el periodo (prueba de operación).

## Pasos
1. Define el control: descripción, objetivo, frecuencia, responsable y el riesgo que mitiga. Si el control no está descrito por escrito, indícalo: no puede concluirse sobre su diseño.
2. Define el tipo de prueba, de diseño u operación, y el periodo cubierto.
3. Define la población y la muestra con un método explícito (aleatorio, por juicio o por atributos) y justifica su tamaño según la frecuencia del control.
4. Ejecuta el procedimiento sobre cada elemento de la muestra: revisa la evidencia, la fecha, la aprobación y la coincidencia con el registro. Anota cada excepción con su referencia.
5. Concluye el resultado: efectivo, efectivo con observaciones o no efectivo. Analiza la causa de cada excepción antes de extender la conclusión a toda la población.
6. Si hay excepciones, documenta lo necesario para un hallazgo (usa `templates/prueba_control.md`) y pásalo al informe de hallazgos.

## Criterios de calidad
- La población está completa y la muestra y su tamaño están justificados.
- Cada elemento probado tiene su evidencia referenciada; ningún resultado se marca sin evidencia.
- Cada excepción describe el elemento, la fecha y la causa observada.
- La conclusión coincide con las excepciones encontradas.

## Límites
La prueba documenta lo observado en la evidencia disponible. No certifica el control ni sustituye la opinión del responsable de la auditoría. No modifica registros ni corrige excepciones durante la prueba. No supongas que la evidencia existe.
