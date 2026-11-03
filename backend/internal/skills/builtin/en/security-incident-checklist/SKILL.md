---
name: security-incident-checklist
description: "Guides the first steps after a security incident: contain, record, notify the right people and preserve evidence. Use it as soon as unauthorized access or a data leak is suspected."
metadata:
  category: it
  builtin: "true"
---

# Security incident first-steps checklist

## When to use it
As soon as unauthorized access, data loss, malware or a compromised account is suspected, to organize the response while the responsible team takes control.

## Steps
1. Record the time of detection, who reports it, what was observed and which systems or data may be affected. Write down only verified facts and clearly separate suspicions.
2. Propose containment actions for the IT team to carry out: isolate the affected device or account, change compromised credentials or suspend access. Do not shut down devices or delete files before the evidence is preserved.
3. Preserve evidence: logs, screenshots, emails and suspicious files, with date, time and the name of the person who collected them.
4. Notify according to the organization's incident response plan: the security owner, management and legal. If applicable, identify the affected parties and the notification deadlines required by current law.
5. Assess the scope and severity: systems involved, number of records, whether personal or confidential data is involved, and assign a priority.
6. Keep a log of each action, its owner and its time, and schedule the post-incident review (use `templates/incident_checklist.md`).

## Quality criteria
- Each containment action has a recorded owner and time.
- Evidence is preserved before any cleanup or restoration.
- Confirmed facts are separated from hypotheses.
- It is recorded who was notified, when and through which channel.

## Limits
This skill guides and records. It does not execute actions on systems, does not decide on its own whether a notification obligation exists, and does not draft external statements without legal review. Containment and notification decisions are made by the responsible people. Do not include real passwords or personal data in the log. This checklist does not replace the organization's incident response plan.
