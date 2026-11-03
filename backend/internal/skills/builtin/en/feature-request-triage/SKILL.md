---
name: feature-request-triage
description: "Triages feature requests with common criteria: problem, frequency, value, effort and decision. Use it to sort the request backlog before product planning."
metadata:
  category: product
  builtin: "true"
---

# Feature request triage

## When to use it

When there is an accumulated list of feature requests and the product team needs to decide which ones to evaluate. Use `templates/feature-request-triage.csv` as the structure.

## Steps

1. Rewrite each request as a user problem, not as a feature: what the user is trying to achieve and what stops them today.
2. Merge duplicate or very similar requests and count how many distinct customers or cases ask for them, using real figures from the record. If none exist, leave the field empty.
3. Assign value from 1 to 5 based on the impact for the user and for the business, with one sentence justifying the score.
4. Assign effort from 1 to 5 based on what the engineering team indicates. If it has not been estimated, write "to be estimated".
5. Sort by value and frequency, and use effort as a tiebreaker. Change the order only if there is a written reason.
6. Assign a decision to each request: evaluate at the next planning, keep on the list or discard, with the reason for discarding.

## Quality criteria

- Each request has a written problem and a decision.
- Value and effort scores have a one-sentence justification.
- Discarded requests state the reason so they can be reviewed later.
- No frequency figures are used that do not exist in the data.

## Limits

Leave the triage as a proposal for the product team. Do not set dates, assign owners or change the roadmap. Do not promise the people who made a request that the feature will be built.
