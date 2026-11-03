---
name: refund-request-review
description: "Reviews a refund request against policy: eligibility, amount, evidence and a recommendation for human approval. Use it before deciding any money return."
metadata:
  category: support
  builtin: "true"
---

# Refund request review

## When to use it

To assess a refund or return request before the authorized person decides. This skill does not approve or execute refunds.

## Steps

1. Identify the purchase or charge: date, product or service, amount paid and payment method, taken from the request data.
2. Compare the request with the current refund policy: time limit, usage conditions, exceptions and excluded products. Cite the exact policy point.
3. Calculate the amount that would apply, with its method: full amount, proration, charges already returned or non-refundable charges. Show the operation step by step.
4. Review the evidence the customer provides and what is on record in the system, and note what is missing to decide.
5. Classify the request as eligible, not eligible or doubtful, with the main reason in one sentence.
6. Write the recommendation for the approver: approve, reject, ask for more evidence or escalate, with the data that supports it.

## Quality criteria

- Each conclusion cites the policy or data point that supports it.
- The amount is calculated with a visible operation and matches the purchase data.
- Facts and the customer's claims are presented separately.
- If the policy does not cover the case, the request is escalated rather than resolved by analogy.

## Limits

This is a recommendation: it does not approve, reject or execute the refund, does not offer additional compensation and does not communicate a decision to the customer. Do not invent amounts, purchase dates or policy terms. If a data point is missing to calculate the amount, mark it as pending instead of estimating it.
