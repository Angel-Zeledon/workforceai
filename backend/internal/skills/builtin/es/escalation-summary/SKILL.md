---
name: escalation-summary
description: "Resume un caso escalado para quien lo recibe: cliente, historial, lo ya intentado, impacto y el pedido concreto, sin preguntas básicas. Úsala al pasar un caso a otro equipo."
metadata:
  category: support
  builtin: "true"
---

# Resumen de escalamiento

## Cuándo usarla

Cuando un caso pasa a un segundo nivel, a producto, a ingeniería o a dirección, y quien lo recibe debe entenderlo sin leer todo el hilo. Usa `templates/resumen-escalamiento.md` como estructura.

## Pasos

1. Identifica al cliente, su plan o tipo de cuenta y el canal por el que contactó, sin incluir datos personales que no ayuden a resolver el caso.
2. Escribe el historial en orden cronológico, con fecha y una línea por contacto relevante.
3. Enumera lo que ya se intentó y el resultado de cada intento, para que nadie repita los mismos pasos.
4. Describe el impacto con hechos observables: qué no puede hacer el cliente, desde cuándo, a cuántos usuarios u operaciones afecta y si hay una fecha límite.
5. Separa los hechos confirmados de las hipótesis y marca cada hipótesis como tal.
6. Termina con el pedido concreto: qué se necesita del equipo receptor, en qué plazo, si el cliente o la política lo exigen, y quién responde al cliente mientras tanto.

## Criterios de calidad

- Quien lo recibe puede actuar sin preguntar datos básicos.
- El impacto se expresa con cifras o hechos del caso, no con adjetivos.
- Cada intento anterior tiene su resultado.
- El pedido es una acción concreta, no una frase general como "por favor ayuda".

## Límites

El resumen es interno y no se envía al cliente sin revisión. No cambia el estado del caso ni asigna responsables que no consten en los datos. No promete tiempos de respuesta ni soluciones al cliente.
