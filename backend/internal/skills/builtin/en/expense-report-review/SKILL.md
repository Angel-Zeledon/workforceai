---
name: expense-report-review
description: "Review an expense report: check receipts, categories, policy limits, amounts and duplicates before approval. Use it when validating reimbursements or expense claims."
metadata:
  category: finance
  builtin: "true"
---
# Expense report review

## When to use it
To validate expenses submitted by a person before approving their reimbursement.

## Steps
1. Match each expense to its receipt: date, amount, vendor and currency must agree.
2. Classify each line against the expense policy and flag items over limits or lacking justification.
3. Look for duplicates by amount, date and vendor, and mark doubtful cases.
4. Summarize the approvable total, the flagged total and the open questions for the responsible person.

## Quality criteria
- Each flagged line gives the specific reason and the policy that applies.
- Totals match the sum of approved and flagged lines.
- No expense without a receipt is approved without being marked.

## Limits
Final approval and any reimbursement belong to the approver. Do not assume policies or amounts that are not in the data.
