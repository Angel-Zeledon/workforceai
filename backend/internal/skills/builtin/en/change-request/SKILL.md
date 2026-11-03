---
name: change-request
description: "Drafts a change request for a system or process: reason, impact, risks, rollback plan and required approvals. Use it before implementing a change that may affect the service."
metadata:
  category: it
  builtin: "true"
---

# Change request

## When to use it
Before modifying a system, a configuration or a process that people in the organization use, so the change is reviewed, scheduled and can be reverted if something goes wrong.

## Steps
1. Describe the change and its reason: what is modified, why, and which problem or need it solves.
2. Identify the impact: systems, processes, users and areas affected, and the proposed timing within the maintenance window.
3. Assess the risks with probability and impact, including dependencies and what cannot be tested before implementation.
4. Write the implementation plan in verifiable steps and the rollback plan: how to return to the previous state and how long it takes.
5. Define the validation tests and the criteria to accept the change or revert it.
6. List the approvals required according to the change class (normal, urgent or standard) and leave spaces for signatures (use `templates/change_request.md`).

## Quality criteria
- The rollback plan is executable and has an estimated time.
- Each high risk has a mitigation or an explicit acceptance.
- The window and impact match the users and processes affected.
- The required approvals are identified before implementation.

## Limits
This skill prepares the request. It does not implement changes, deploy or modify configurations, and does not approve the change. Approvals and execution are given by the people responsible for the system and by the change board, if one exists.
