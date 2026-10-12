---
name: bug-report-triage
description: "Triage a bug report by severity, impact, reproducibility and likely owner area, and request the missing information. Use it when user or test failure reports arrive."
metadata:
  category: engineering
  builtin: "true"
---
# Bug report triage

## When to use it
To sort incoming bug reports and decide their priority.

## Steps
1. Summarize the failure in one sentence: what happens, where and who is affected.
2. Check whether the report includes reproduction steps, version, environment and evidence.
3. Assign severity and impact based on affected users and whether a key function is blocked.
4. Name the likely area and list the information still needed to move forward.

## Quality criteria
- Severity is justified with a concrete detail from the report.
- Duplicates are linked to the original case reference, if the data includes it.
- No root cause is stated without evidence.

## Limits
Leave the triage as a proposal for the engineering owner. Do not change priorities or assign people without approval.
