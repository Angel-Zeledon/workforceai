---
name: business-continuity-plan
description: "Builds a business continuity plan: critical processes, maximum recovery times, alternatives and owners. Use it to prepare operations for outages, disasters or supplier failures."
metadata:
  category: operations
  builtin: "true"
---

# Business continuity plan

## When to use it
To prepare operations for serious disruptions, such as system outages, loss of facilities, failure of a single-source supplier or absence of key staff, and to know what to do, in which order and who does it.

## Steps
1. List the business processes and prioritize the critical ones according to the impact of their interruption on customers, revenue, legal obligations and safety.
2. For each critical process define the recovery time objective (RTO), meaning how long it can be down, and the maximum acceptable data loss (RPO).
3. Identify plausible threats, such as system failure, a single supplier, facilities, key staff or security incidents, and estimate their likelihood.
4. Define an alternative for each critical process: manual procedure, alternate supplier, backup site or equipment, or cross-trained staff.
5. Assign an owner and a backup per process, identified by role. Do not include personal data in the document.
6. Establish how and how often the plan is tested, and deliver the document (use `templates/continuity_plan.md`).

## Quality criteria
- Each critical process has an RTO, an RPO, an alternative and an owner with a backup.
- The alternatives are realistic with the available resources and are described step by step.
- The plan states when and how it is tested.
- Single-source supplier dependencies appear as an explicit risk.

## Limits
This is a draft to be validated with management and with the owners of each process. It does not contract suppliers, buy equipment, activate backup resources or assign budget. Times and alternatives that are not in the information provided are marked as to be confirmed.
