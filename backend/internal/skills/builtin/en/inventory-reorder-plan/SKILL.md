---
name: inventory-reorder-plan
description: "Calculates the reorder point and order quantity for each item from usage, lead time and safety stock. Use it when reviewing inventory or before preparing the purchases for the period."
metadata:
  category: operations
  builtin: "true"
---

# Reorder point plan

## When to use it
When you need to know which items are below their replenishment level and how much to order of each, without relying on the buyer's judgment.

## Steps
1. Gather per item: average daily or weekly usage over recent months, supplier lead time in days, stock on hand, open orders in transit and current safety stock. If a data point is missing, mark it as pending and do not invent the value.
2. Calculate the usage over the lead time and add the safety stock. That result is the reorder point.
3. Compare stock on hand plus what is in transit against the reorder point. If it is equal to or below, the item goes into the purchase plan.
4. Set the order quantity: cover the expected usage until the next review plus the safety stock, and adjust it to the supplier's minimum lot or purchase multiple when one exists.
5. Mark items with less than half of the lead time in coverage as urgent, and items with more than 90 days of coverage as overstock.
6. Deliver the table per item (use `templates/reorder_plan.csv`) and a short list of the five most urgent items.

## Quality criteria
- Each reorder point shows the usage, lead time and safety stock used in the calculation.
- Order quantities respect the minimum lot and purchase multiple when that data exists.
- Items without usage or lead time data appear as pending, not with assumed figures.
- Stock in transit is counted once and only when it has an arrival date.

## Limits
This skill calculates and proposes. It does not generate purchase orders, contact suppliers or adjust stock in the system. Any purchase requires human approval. The calculation uses historical usage; it does not anticipate campaigns or demand changes that are not in the information provided.
