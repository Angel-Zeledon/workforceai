---
name: product-launch-checklist
description: "Checklist for launching a feature: technical readiness, support, communication, metrics and rollback plan with owners. Use it before publishing a change for customers."
metadata:
  category: product
  builtin: "true"
---

# Product launch checklist

## When to use it

Before publishing a feature or major change for customers, to confirm that each area is ready and that a plan exists if something fails. Use `templates/product-launch-checklist.md` as the structure.

## Steps

1. Define the scope: what changes, for which customer segment, on what publish date and what is left out.
2. Confirm technical readiness: tests completed, performance reviewed, migrations applied in the correct order and permissions verified, each item with its owner.
3. Prepare support: help articles published, answers to expected questions and training for the team that handles customers.
4. Review communication: internal message, customer notes, in-product notice and brand or legal approval where applicable.
5. Define the success and alert metrics, with their baseline and the threshold that triggers a launch review.
6. Write the rollback plan: how the feature is turned off, who decides, how long it takes and what happens to data and accounts already affected.
7. Set the exit criterion: the conditions that must hold during the observation window to declare the launch stable.

## Quality criteria

- Each checklist item has an owner and a status.
- The rollback plan can be executed without the person who built the feature.
- Each metric has a baseline and a threshold, not just a name.
- No item is marked as ready without verifiable evidence.

## Limits

This is a checklist for product team review and does not by itself decide whether the feature launches. It does not set dates that are not in the data or mark as complete what was not verified. It does not publish the feature, send customer communications or execute the rollback plan.
