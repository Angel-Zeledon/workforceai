---
name: refund-request-review
description: "Revisa una solicitud de reembolso contra la política: elegibilidad, monto, evidencia y una recomendación para aprobación humana. Úsala antes de decidir cualquier devolución de dinero."
metadata:
  category: support
  builtin: "true"
---

# Revisión de solicitud de reembolso

## Cuándo usarla

Para evaluar una solicitud de reembolso o devolución antes de que la persona autorizada decida. Esta skill no aprueba ni ejecuta reembolsos.

## Pasos

1. Identifica la compra o el cargo: fecha, producto o servicio, monto pagado y medio de pago, tomados de los datos de la solicitud.
2. Compara la solicitud con la política de reembolsos vigente: plazo, condiciones de uso, excepciones y productos excluidos. Cita el punto exacto de la política.
3. Calcula el monto que correspondería, con su método: monto total, prorrateo, cargos ya devueltos o cargos no reembolsables. Muestra la operación paso a paso.
4. Revisa la evidencia que aporta el cliente y lo que consta en el sistema, y señala lo que falta para decidir.
5. Clasifica la solicitud como elegible, no elegible o dudosa, con el motivo principal en una frase.
6. Redacta la recomendación para quien aprueba: aprobar, rechazar, pedir más evidencia o escalar, con los datos que la sustentan.

## Criterios de calidad

- Cada conclusión cita la política o el dato que la respalda.
- El monto se calcula con una operación visible y coincide con los datos de la compra.
- Los hechos y las afirmaciones del cliente se presentan por separado.
- Si la política no cubre el caso, la solicitud se escala en lugar de resolverse por analogía.

## Límites

Es una recomendación: no aprueba, no rechaza ni ejecuta el reembolso, no ofrece compensaciones adicionales y no comunica una decisión al cliente. No inventes montos, fechas de compra ni condiciones de la política. Si falta un dato para calcular el monto, márcalo como pendiente en lugar de estimarlo.
