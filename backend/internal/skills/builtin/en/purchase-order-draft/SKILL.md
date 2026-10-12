---
name: purchase-order-draft
description: "Draft a purchase order with vendor, items, quantities, prices, taxes, delivery, terms and approvals. Use it when preparing a purchase before authorization."
metadata:
  category: finance
  builtin: "true"
---
# Purchase order draft

## When to use it
To prepare a formal purchase to a vendor before it is authorized.

## Steps
1. Identify the vendor, delivery address, cost center and requesting person.
2. List each item with code, description, quantity, unit price and subtotal.
3. Add taxes, total, currency, required delivery date and the agreed payment terms.
4. Include the approval block with the levels required by the purchasing policy.

## Quality criteria
- Totals and subtotals match the item lines.
- Each price comes from the quote or from the data given in the task.
- The order includes a reference number and no mandatory field is left out.

## Limits
Leave the order as a draft; it is not issued or sent without approval from the authorized person. Never invent prices, item codes or reference numbers.
