---
name: menu-costing
description: "Calculates the cost of a dish or product from its recipe, with waste, cost per ingredient, suggested price and margin. Use it when setting or reviewing menu prices."
metadata:
  category: hospitality
  builtin: "true"
---

# Dish costing

## When to use it
When creating a new dish, when ingredient prices change, or before changing the selling price on the menu.

## Steps
1. List the recipe with the gross quantity of each ingredient, its unit of measure and the final yield of the dish in portions.
2. Assign each ingredient its current unit cost, with the price source and the quote date.
3. Apply the waste of each ingredient (cleaning, cutting, cooking) to get the usable net quantity and its real cost.
4. Add up the cost of all ingredients to get the total dish cost, then divide by the portions to get the cost per portion.
5. Calculate the suggested price by dividing the cost per portion by the target food-cost percentage the business sets, and the margin as price minus cost. Include taxes and platform commissions where they apply.
6. Fill in the template `templates/menu-costing.csv` and mark as pending any ingredient without a confirmed price.

## Quality criteria
- The total cost is the exact sum of the ingredient costs.
- Waste appears as an applied percentage, not hidden inside the quantity.
- Every ingredient price has a date and a source.
- The suggested price and margin are calculated with the same formulas at every review.

## Limits
This is an estimate for review by the owner or chef before the menu changes. Do not invent ingredient prices, waste percentages or competitor prices. Do not change published prices without human approval. Nutritional and allergen information needs separate verification and is not included here. Taxes must be confirmed with your accountant.
