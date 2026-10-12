---
name: variance-analysis
description: "Analyze variances between actual and budget or between periods, split price, volume and mix, and explain causes with figures. Use it in management reports and results reviews."
metadata:
  category: analysis
  builtin: "true"
---

# Variance analysis

## When to use it
When actual figures are compared against budget, the previous period or the previous year.

## Steps
1. Fix the comparison base and the units (currency, thousands, percentage).
2. Compute absolute and percentage variance per line; sort by impact.
3. For the three largest, break down into price, volume and mix when unit data exists.
4. Look for the cause in the available data; if it is a hypothesis, label it as such.
5. Deliver the table (use `templates/variance-table.csv`) and three conclusions with a suggested action.

## Quality criteria
- Figures add up: the sum of line variances equals the total variance.
- Always separate fact (figure) from interpretation.
- No invented figures: what is not in the data stays pending.
