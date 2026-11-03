---
name: legal-deadline-tracker
description: "Tracks legal and contractual deadlines (expiries, renewals, prior notices and response periods) with alert dates. Use it so no contract, filing or notification date is missed."
metadata:
  category: legal
  builtin: "true"
---

# Legal and contractual deadline tracker

## When to use it
When several contracts or procedures have dates to meet and a single register with alerts is needed.

## Steps
1. List each document or procedure with its source and the clause or rule that sets the deadline.
2. Calculate the due date from the event that starts it, for example receipt of a notice. Note the counting rule (business or calendar days, and from which day counting starts) and check the rules in your country.
3. Calculate the prior-notice date, for example 30 or 60 days before expiry, and an internal alert date.
4. Assign an owner and a status (pending, in progress, completed or overdue).
5. Sort the register by due date and highlight anything due in the next 30 days.
6. Complete `templates/deadline-register.csv` and review it weekly with the owners.

## Quality criteria
- Each deadline cites its exact source (clause or article).
- Dates are calculated with an explicit counting rule; if the rule is doubtful, it is flagged.
- No deadline is left without an owner or an alert date.
- Overdue deadlines appear first, with a proposed action.

## Limits
It does not replace a backed-up deadline system or a lawyer's confirmation of procedural deadlines. Dates must be checked against the original document before acting.
