---
name: inventory-reorder-plan
description: "Calcula el punto de reorden y la cantidad a pedir de cada artículo según consumo, plazo de entrega y stock de seguridad. Úsala al revisar inventario o antes de preparar las compras del periodo."
metadata:
  category: operations
  builtin: "true"
---

# Plan de punto de reorden

## Cuándo usarla
Cuando necesites saber qué artículos están por debajo del nivel de reposición y cuánto pedir de cada uno, sin depender de la intuición de quien compra.

## Pasos
1. Reúne por artículo: consumo promedio diario o semanal de los últimos meses, plazo de entrega del proveedor en días, stock disponible, pedidos abiertos en tránsito y stock de seguridad vigente. Si falta un dato, márcalo como pendiente y no inventes el valor.
2. Calcula el consumo durante el plazo de entrega y súmale el stock de seguridad. Ese resultado es el punto de reorden.
3. Compara el stock disponible más lo que está en tránsito con el punto de reorden. Si está igual o por debajo, el artículo entra al plan de compra.
4. Define la cantidad a pedir: cubre el consumo esperado hasta la siguiente revisión más el stock de seguridad, y ajústala al lote mínimo o múltiplo de compra del proveedor cuando exista.
5. Marca como urgentes los artículos con cobertura menor a la mitad del plazo de entrega, y como sobrestock los que superan 90 días de cobertura.
6. Entrega la tabla por artículo (usa `templates/plan_reorden.csv`) y una lista corta con los cinco artículos más urgentes.

## Criterios de calidad
- Cada punto de reorden muestra el consumo, el plazo y el stock de seguridad usados en el cálculo.
- Las cantidades respetan el lote mínimo y el múltiplo de compra cuando ese dato existe.
- Los artículos sin dato de consumo o de plazo aparecen como pendientes, no con cifras supuestas.
- El stock en tránsito se suma una sola vez y solo si tiene fecha de llegada.

## Límites
Calcula y propone. No genera órdenes de compra, no contacta proveedores y no ajusta existencias en el sistema. Cualquier compra requiere aprobación humana. El cálculo usa el consumo histórico; no anticipa campañas ni cambios de demanda que no estén en la información proporcionada.
