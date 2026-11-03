---
name: accounts-payable-run
description: "Prepara la corrida de pagos a proveedores: ordena vencimientos, asigna prioridades y compara el total con el flujo de caja disponible. Úsala antes de cada corrida de pagos, semanal o quincenal."
metadata:
  category: finance
  builtin: "true"
---

# Corrida de pagos a proveedores

## Cuándo usarla
Antes de cada corrida de pagos, para decidir qué facturas se pagan, en qué orden y con qué efectivo disponible.

## Pasos
1. Reúne las facturas de proveedores pendientes a la fecha de corte: proveedor, factura, fecha de vencimiento, importe, moneda, condiciones de pago y si existe descuento por pronto pago.
2. Excluye lo que no debe pagarse todavía: facturas en disputa, sin recepción confirmada de bienes o servicios, sin orden de compra aprobada o duplicadas.
3. Asigna prioridad: 1 para facturas vencidas o con riesgo de suspensión de suministro, 2 para las que vencen antes de la próxima corrida y 3 para el resto. Marca las facturas con descuento por pronto pago que conviene pagar antes.
4. Compara el total de la corrida con el flujo disponible del periodo: saldo de caja proyectado, cobros previstos y el colchón mínimo que defina finanzas.
5. Si el flujo no alcanza, propone el pago por prioridad y lista las facturas que se posponen, con la nueva fecha por acordar con cada proveedor.
6. Entrega la propuesta de corrida (usa `templates/corrida_pagos.csv`) con totales por prioridad y por moneda, lista para revisión.

## Criterios de calidad
- El total de la propuesta es igual a la suma de sus filas y no incluye facturas excluidas.
- Ninguna factura aparece dos veces ni sin orden de compra o recepción confirmada.
- El total no supera el flujo disponible, o el faltante aparece marcado con su importe.
- La cuenta destino de cada pago coincide con el maestro de proveedores; un cambio de cuenta se verifica por separado.

## Límites
Prepara la propuesta. No ejecuta pagos, no modifica datos bancarios de proveedores y no libera transferencias. La aprobación y la ejecución son humanas, con la separación de funciones que tenga definida la empresa.
