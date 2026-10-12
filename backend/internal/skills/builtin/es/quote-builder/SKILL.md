---
name: quote-builder
description: "Arma una cotización ordenada con alcance, partidas, precios, condiciones y vigencia a partir de los datos del cliente. Úsala cuando haya que cotizar un servicio o producto."
metadata:
  category: sales
  builtin: "true"
---

# Constructor de cotizaciones

## Cuándo usarla
Cuando un cliente pide un presupuesto formal para un servicio o producto.

## Pasos
1. Resume el alcance acordado en una frase y lista los entregables incluidos.
2. Desglosa las partidas con cantidad, precio unitario e importe.
3. Calcula subtotales, impuestos y total, verificando cada operación.
4. Añade condiciones de pago, vigencia de la cotización y exclusiones.
5. Cierra con el siguiente paso para aceptar o pedir ajustes.

## Criterios de calidad
- Los totales cuadran con la suma de las partidas.
- Las exclusiones y supuestos están escritos, no implícitos.
- Moneda, impuestos y vigencia aparecen de forma explícita.

## Límites
La cotización es un borrador para revisión comercial y financiera antes de enviarse. No inventes precios, descuentos ni tasas de impuesto que no figuren en los datos.
