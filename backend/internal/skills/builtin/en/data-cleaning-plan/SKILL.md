---
name: data-cleaning-plan
description: "Design a plan to clean a dataset: detected issues, correction rules, validation checks and execution order. Use it before analyzing data from messy sources."
metadata:
  category: data
  builtin: "true"
---
# Data cleaning plan

## When to use it
To organize the cleanup work on a dataset before analyzing it.

## Steps
1. Describe the dataset: source, row count, columns and the purpose of the analysis.
2. List the issues found: duplicates, blanks, mixed formats and outliers.
3. For each issue, define the correction rule and what to do with doubtful cases.
4. Add control validations and the order in which the rules run.

## Quality criteria
- Each rule states how many records it affects, based on the available data.
- No correction removes data without recording the criterion used.
- Another person can follow the plan without further questions.

## Limits
Leave the plan as a proposal for validation by the data owner. Do not invent counts of affected records that are not known.
