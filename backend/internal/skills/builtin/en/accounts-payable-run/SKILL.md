---
name: accounts-payable-run
description: "Prepares the supplier payment run: sorts due dates, assigns priorities and compares the total with the cash available. Use it before each payment run, weekly or biweekly."
metadata:
  category: finance
  builtin: "true"
---

# Supplier payment run

## When to use it
Before each payment run, to decide which invoices are paid, in what order, and with how much cash available.

## Steps
1. Gather the open supplier invoices at the cut-off date: supplier, invoice, due date, amount, currency, payment terms, and whether an early-payment discount applies.
2. Exclude what should not be paid yet: disputed invoices, invoices without confirmed receipt of goods or services, invoices without an approved purchase order, and duplicates.
3. Assign priorities: 1 for overdue invoices or invoices at risk of supply suspension, 2 for invoices due before the next run, and 3 for the rest. Flag invoices with an early-payment discount that is worth paying early.
4. Compare the run total with the cash available for the period: projected cash balance, expected collections and the minimum cash buffer set by finance.
5. If cash is not enough, propose payment by priority and list the invoices to be postponed, with a new date to agree with each supplier.
6. Deliver the payment run proposal (use `templates/payment_run.csv`) with totals by priority and by currency, ready for review.

## Quality criteria
- The proposal total equals the sum of its rows and includes no excluded invoices.
- No invoice appears twice, and none is paid without a purchase order or confirmed receipt.
- The total does not exceed the cash available, or any shortfall is shown with its amount.
- Each payee account matches the vendor master file; any change of bank account is verified separately.

## Limits
This skill prepares the proposal. It does not execute payments, change supplier bank details or release transfers. Approval and execution are human steps, under the segregation of duties the company has defined.
