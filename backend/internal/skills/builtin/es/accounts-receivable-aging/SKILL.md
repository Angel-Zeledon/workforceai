---
name: accounts-receivable-aging
description: "Clasifica las cuentas por cobrar por antigüedad (0-30, 31-60, 61-90 y más de 90 días) y propone una acción de cobro por tramo. Úsala en el cierre mensual o antes de una reunión de cobranza."
metadata:
  category: finance
  builtin: "true"
---

# Antigüedad de cuentas por cobrar

## Cuándo usarla
Cuando necesites saber qué facturas de clientes están vencidas, cuánto tiempo llevan así y a qué clientes conviene cobrar primero.

## Pasos
1. Toma el listado de facturas abiertas a la fecha de corte: cliente, factura, fecha de emisión, fecha de vencimiento, importe pendiente y moneda. Si falta algún dato, dilo antes de seguir.
2. Calcula los días de atraso de cada factura como la diferencia entre la fecha de corte y la fecha de vencimiento. Las facturas que aún no vencen van en un tramo aparte, "por vencer".
3. Asigna cada factura a su tramo: por vencer, 0-30, 31-60, 61-90 o más de 90 días. Suma los importes por tramo y por cliente.
4. Verifica que la suma de los tramos sea igual al saldo total de cuentas por cobrar del mayor a la fecha de corte.
5. Para cada cliente con saldo en tramos de 31 días o más, propone una acción: recordatorio amistoso, llamada, reunión con el cliente, suspensión de nuevos pedidos o evaluación de provisión para incobrables, que decide finanzas.
6. Entrega la tabla por tramos (usa `templates/antiguedad_cuentas_por_cobrar.csv`) y una lista corta con los cinco clientes de mayor saldo vencido.

## Criterios de calidad
- Los tramos suman exactamente el saldo total del mayor a la fecha de corte.
- Cada factura aparece una sola vez, en un tramo, con sus días de atraso calculados.
- Cada cliente con saldo de más de 60 días tiene una acción propuesta y un responsable sugerido.
- Las monedas no se suman entre sí sin conversión; se muestran por separado o con el tipo de cambio indicado.

## Límites
Clasifica y propone. No envía cobros, no emite notas de crédito y no registra provisiones. Cualquier recordatorio, suspensión o ajuste requiere aprobación humana. Las acciones deben respetar las condiciones de pago pactadas con cada cliente.
