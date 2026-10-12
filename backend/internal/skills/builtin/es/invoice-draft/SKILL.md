---
name: invoice-draft
description: "Redacta un borrador de factura con datos del cliente, conceptos, importes, impuestos, vencimiento y condiciones de pago. Úsala al preparar facturas antes de su revisión y emisión."
metadata:
  category: finance
  builtin: "true"
---
# Borrador de factura

## Cuándo usarla
Para preparar una factura a partir de datos de venta o de un pedido ya acordado.

## Pasos
1. Verifica los datos fiscales del emisor y del cliente: razón social, identificación fiscal y domicilio.
2. Enumera cada concepto con cantidad, precio unitario, descuento y subtotal, sin agrupar partidas distintas.
3. Calcula impuestos y total, y muestra el desglose por tipo de impuesto.
4. Indica fecha de emisión, fecha de vencimiento y condiciones de pago acordadas.

## Criterios de calidad
- Los totales cuadran con la suma de conceptos, descuentos e impuestos.
- Cada línea corresponde a un dato de la tarea, sin partidas inventadas.
- Numeración, moneda y referencia al pedido o contrato están presentes.

## Límites
Deja la factura como borrador; su emisión y validez fiscal corresponden a una persona responsable. No inventes números de factura ni datos fiscales ausentes.
