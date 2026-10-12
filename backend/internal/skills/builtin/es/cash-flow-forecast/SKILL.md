---
name: cash-flow-forecast
description: "Proyecta el flujo de caja semanal o mensual con saldo inicial, cobros y pagos previstos, y alerta de saldos mínimos. Úsala para anticipar necesidades de liquidez."
metadata:
  category: finance
  builtin: "true"
---
# Previsión de flujo de caja

## Cuándo usarla
Para anticipar la posición de liquidez de la organización en las próximas semanas o meses.

## Pasos
1. Parte del saldo inicial verificado en la fecha de corte.
2. Lista los cobros y pagos previstos por fecha, separando los ya comprometidos de las estimaciones.
3. Calcula el saldo acumulado por periodo y marca cuándo cae por debajo del mínimo de seguridad.
4. Presenta un escenario base y uno pesimista que cambie solo los cobros inciertos, con sus supuestos.

## Criterios de calidad
- El saldo final de cada periodo equivale al inicial más cobros menos pagos.
- Cada cobro o pago indica si es confirmado o estimado.
- El escenario pesimista muestra qué partida lo provoca.

## Límites
La proyección es orientativa y debe validarla la persona responsable de tesorería. No completes cobros o pagos que no estén en los datos de la tarea.
