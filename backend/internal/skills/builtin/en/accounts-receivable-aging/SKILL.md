---
name: accounts-receivable-aging
description: "Ages open customer invoices into buckets (0-30, 31-60, 61-90 and over 90 days) and proposes a collection action per bucket. Use it at month close or before a collections meeting."
metadata:
  category: finance
  builtin: "true"
---

# Accounts receivable aging

## When to use it
When you need to know which customer invoices are overdue, for how long, and which customers to collect from first.

## Steps
1. Take the list of open invoices at the cut-off date: customer, invoice, issue date, due date, open amount and currency. If any field is missing, say so before going on.
2. Calculate the days past due for each invoice as the difference between the cut-off date and the due date. Invoices not yet due go in a separate "current" bucket.
3. Assign each invoice to its bucket: current, 0-30, 31-60, 61-90 or over 90 days. Add the amounts by bucket and by customer.
4. Check that the buckets add up to the total accounts receivable balance in the ledger at the cut-off date.
5. For each customer with balances in the 31-day buckets or older, propose an action: friendly reminder, phone call, meeting with the customer, pause on new orders, or a review of a bad-debt allowance, which finance decides.
6. Deliver the bucket table (use `templates/accounts_receivable_aging.csv`) and a short list of the five customers with the largest overdue balances.

## Quality criteria
- The buckets add up exactly to the ledger's total balance at the cut-off date.
- Each invoice appears once, in one bucket, with its days past due calculated.
- Each customer with a balance over 60 days has a proposed action and a suggested owner.
- Currencies are not added together without conversion; they are shown separately or with the stated exchange rate.

## Limits
This skill classifies and proposes. It does not send collection messages, issue credit notes or post allowances. Any reminder, suspension or adjustment needs human approval. Actions must respect the payment terms agreed with each customer.
