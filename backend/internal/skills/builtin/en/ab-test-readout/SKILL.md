---
name: ab-test-readout
description: "Summarize A/B test results: hypothesis, metrics, difference, confidence level and recommendation. Use it when closing a test to decide whether to ship the change."
metadata:
  category: data
  builtin: "true"
---
# A/B test readout

## When to use it
To communicate whether a variant worked and which decision to take.

## Steps
1. Restate the hypothesis, the primary metric and the control metrics.
2. Report sample size, duration and the observed difference between variants.
3. Report the confidence level or interval, and whether the planned power was reached.
4. End with a recommendation: ship, discard or repeat, and the reason.

## Quality criteria
- Figures match the task data exactly.
- Statistical significance is distinguished from practical relevance.
- Segments and side effects are mentioned only if the data includes them.

## Limits
Leave the report as a draft for analysis and product review. Do not declare a winner or ship changes without human approval.
