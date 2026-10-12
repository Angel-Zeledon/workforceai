---
name: bank-reconciliation
description: "Reconcile a bank statement against the ledger or the receipts and payments records and leave the differences explained. Use it to balance a bank account, a cash box or a cash close."
metadata:
  category: finance
  builtin: "true"
---

# Bank reconciliation

## When to use it
When you receive a statement (csv, xlsx or text) that must be matched against the accounting records.

## Steps
1. Identify the period, the account and the currency. If something is missing, say so and ask before going on.
2. Normalize both sides to columns: date, description, reference, signed amount and balance.
3. Match by exact reference first; then by amount and date within 3 days; finally by grouped amount.
4. Classify what does not match: items in transit, fees or interest, posting errors, duplicates or unidentified items.
5. Compute the reconciled balance: bank balance + deposits in transit - outstanding payments = adjusted book balance.
6. Deliver the difference table (use `templates/reconciliation.csv`) and propose the adjusting entry without posting it.

## Quality criteria
- Every difference has a cause and a proposed owner.
- Explained items add up to the total difference; if not, say so and show the remainder.
- Figures cite the source row.

## Limits
You prepare the analysis and the draft adjustment. Posting entries or moving money needs human approval.
