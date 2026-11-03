---
name: menu-costing
description: "Calcula el costo de un platillo o producto a partir de su receta, con merma, costo por ingrediente, precio sugerido y margen. Úsala al definir o revisar el precio de la carta."
metadata:
  category: hospitality
  builtin: "true"
---

# Costeo de platillo

## Cuándo usarla
Al crear un platillo nuevo, cuando cambian los precios de los insumos o antes de modificar el precio de venta de la carta.

## Pasos
1. Lista la receta con la cantidad bruta de cada ingrediente, su unidad de medida y el rendimiento final del platillo en porciones.
2. Asigna a cada ingrediente su costo unitario vigente, con la fuente del precio y la fecha de cotización.
3. Aplica la merma de cada ingrediente (limpieza, corte, cocción) para obtener la cantidad neta usable y su costo real.
4. Suma los costos de todos los ingredientes para obtener el costo total del platillo y divídelo entre las porciones para el costo por porción.
5. Calcula el precio sugerido dividiendo el costo por porción entre el porcentaje de costo objetivo que defina el negocio, y el margen como precio menos costo. Incluye impuestos y comisiones de plataformas si aplican.
6. Completa la plantilla `templates/costeo-platillo.csv` y marca como pendiente cada ingrediente sin precio confirmado.

## Criterios de calidad
- El costo total es la suma exacta de los costos por ingrediente.
- La merma aparece como porcentaje aplicado, no oculta dentro de la cantidad.
- Cada precio de insumo tiene fecha y fuente.
- El precio sugerido y el margen se calculan con las mismas fórmulas en cada revisión.

## Límites
Es una estimación para revisión del dueño o del chef antes de cambiar la carta. No inventes precios de insumos, porcentajes de merma ni precios de la competencia. No cambies precios publicados sin aprobación humana. La información nutricional y de alérgenos requiere verificación aparte y no se incluye aquí. Los impuestos deben confirmarse con tu contador.
