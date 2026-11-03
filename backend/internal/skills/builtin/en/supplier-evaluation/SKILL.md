---
name: supplier-evaluation
description: "Periodically evaluates a supplier on quality, timeliness, price, service and risks, with a weighted rating and improvement actions. Use it each quarter or after a significant problem."
metadata:
  category: operations
  builtin: "true"
---

# Supplier evaluation

## When to use it
To review the performance of an active supplier over a defined period, decide whether to keep it, whether it needs an improvement plan, or whether to look for an alternative.

## Steps
1. Define the evaluation period and gather the data: deliveries received and on time, returns or nonconformities, invoices compared against the agreed rates, service incidents, and changes in its financial, legal or capacity situation.
2. Calculate each criterion with its indicator: percentage of on-time deliveries, nonconformity rate, price variance against contract and response time to incidents.
3. Assign a score from 1 to 5 to each criterion using a scale defined before evaluating (for example, 5 exceeds what was agreed, 3 meets it, 1 repeatedly fails). Suggested weights are quality 30%, timeliness 25%, price 20%, service 15% and risk 10%; the responsible person can change them at the start.
4. Calculate the weighted rating and classify the supplier as preferred, approved, under observation or to be replaced.
5. Record the risks detected and, for each one, a mitigation action with an owner and a date.
6. Deliver the evaluation sheet (use `templates/supplier_evaluation.csv`) and a list of improvements to review with the supplier.

## Quality criteria
- Each score has its indicator or its evidence; nothing is rated on impression.
- Weights and scale are stated at the start and are the same for all suppliers being compared.
- The final classification follows from the weighted rating, not from a separate opinion.
- Isolated incidents are distinguished from a pattern that repeats.

## Limits
This skill evaluates and recommends. It does not suspend suppliers, negotiate prices or send formal notices. A decision to end a contract or change supplier is taken by the procurement owner with legal support, based on verified data.
