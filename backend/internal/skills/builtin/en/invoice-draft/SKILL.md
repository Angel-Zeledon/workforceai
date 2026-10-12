---
name: invoice-draft
description: "Draft an invoice with client details, line items, amounts, taxes, due date and payment terms. Use it when preparing invoices before human review and issue."
metadata:
  category: finance
  builtin: "true"
---
# Invoice draft

## When to use it
To prepare an invoice from agreed sales data or an accepted order.

## Steps
1. Verify the issuer and client legal details: company name, tax ID and address.
2. List each item with quantity, unit price, discount and subtotal, without merging distinct items.
3. Calculate taxes and the total, showing the breakdown by tax type.
4. State the issue date, due date and the agreed payment terms.

## Quality criteria
- Totals match the sum of items, discounts and taxes.
- Every line maps to a figure in the task data, with no invented items.
- Invoice number, currency and order or contract reference are present.

## Limits
Leave the invoice as a draft; issuing it and its tax validity belong to a responsible person. Never invent invoice numbers or missing tax details.
