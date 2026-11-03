---
name: income-statement-from-invoices
description: "Build the income statement of a period from the invoices (PDF, xml or image) a person left in the Drive: find the files, turn each invoice into a row and let the system do the math. Use it when someone asks \"make me the income statement from this month's invoices\"."
metadata:
  category: finance
  builtin: "true"
---

# Income statement from invoices

## When to use it
When the person mentions the issued and received invoices of a period and wants revenue, costs, expenses and profit.

## Steps
1. **Find the files without asking for the path:** first what was attached; then `recent` (what that person uploaded or moved in the last 14 days, by folder and type, for example `kind: "pdf"`); last `search` by words and by meaning. If the set is clear (all in one folder, same period), go on. If two months are mixed, there are two candidate folders, or too few or too many files, confirm in one line before going on.
2. **No access:** if `recent` says "no access" or a read answers `not_granted`, say so and offer "Give it access to this folder" or "Ask for access". Never skip it.
3. **Extract, do not add up:** read each invoice and turn it into a row: date (YYYY-MM-DD), number, issuer and its tax id, receiver and its tax id, concept, suggested category (Sales, Cost of sales, Rent, Services, Payroll...), currency, subtotal, taxes, total, type (income or expense) and file with page. Copy the amounts as they appear.
4. **Scanned and unreadable:** if an invoice has no text and could not be seen as an image, add it with `status: "scanned"`; if it cannot be understood, `status: "unreadable"` with the reason. Never fill in an amount.
5. **Let the system compute:** ask for `income_statement` with the company (name and tax id: issued is income, received is expense), the base currency, exchange rates only if the person gave them, and every row. The system adds up, groups by category, finds duplicates (same number and issuer), totals that do not add up (subtotal + taxes ≠ total) and currencies without an exchange rate, and writes the Excel with the sheets Detail, Income statement and To review.
6. **Deliver:** in the summary say how many invoices were read, the profit before taxes and how many must be reviewed, with the exact figures the system returned, and the path of the Excel.

## Quality criteria
- No invoice disappears: it is in the result or in To review with its reason.
- The figures of the summary are the system's, with no rounding of your own.
- Different currencies are never added without an exchange rate given by a person.

## Limits
This is not full accounting: no configurable chart of accounts and no income tax. Booking entries or filing returns needs a person.
