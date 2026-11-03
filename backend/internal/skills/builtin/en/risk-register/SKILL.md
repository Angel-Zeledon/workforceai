---
name: risk-register
description: "Build a risk register with cause, effect, probability, impact, owner, mitigation and review date. Use it when starting a project or reviewing risks on a regular schedule."
metadata:
  category: management
  builtin: "true"
---

# Risk register
## When to use it
When starting a project or initiative, or when periodically reviewing risks that could affect timelines, costs, quality or reputation.

## Steps
1. Identify the risks with the team or from the project information: what could happen, why, and with what effect.
2. Describe each risk in one sentence, separating the cause from the effect.
3. Rate probability and impact from 1 to 5 using written criteria. The score is probability times impact (1 to 25): low from 1 to 6, medium from 7 to 14 and high from 15 to 25.
4. Assign an owner to each risk and define the mitigation: avoid, reduce, transfer or accept, with the concrete action.
5. Sort the register by score and flag high risks for immediate attention.
6. Fill in the template `templates/risk-register.csv` with review date and status.

## Quality criteria
- Each risk has a cause, an effect, an owner and a review date.
- The score is calculated with the stated formula and not adjusted by hand.
- Mitigations are concrete actions, not intentions: "monitor" is not enough.
- The whole register uses the same rating scale.

## Limits
This is a draft; the project owner validates the ratings. Do not assign owners without confirming with each person. It does not replace a financial or legal analysis of a specific risk.
