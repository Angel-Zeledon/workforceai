---
name: cash-flow-forecast
description: "Project weekly or monthly cash flow with opening balance, expected receipts and payments, and alerts when balances fall below minimums. Use it to anticipate liquidity needs."
metadata:
  category: finance
  builtin: "true"
---
# Cash flow forecast

## When to use it
To anticipate the organization's liquidity position over the coming weeks or months.

## Steps
1. Start from the opening balance verified at the cut-off date.
2. List expected receipts and payments by date, separating committed items from estimates.
3. Calculate the running balance per period and mark when it drops below the safety minimum.
4. Present a base scenario and a pessimistic one that changes only the uncertain receipts, with their assumptions.

## Quality criteria
- The closing balance of each period equals opening balance plus receipts minus payments.
- Each receipt or payment states whether it is confirmed or estimated.
- The pessimistic scenario shows which line item causes it.

## Limits
The forecast is indicative and must be validated by the person responsible for treasury. Do not complete receipts or payments that are not in the task data.
