---
name: sql-query-explainer
description: "Explain in plain language what a SQL query does: tables, filters, joins, aggregations and expected result. Use it to document or review legacy or complex queries."
metadata:
  category: data
  builtin: "true"
---
# SQL query explainer

## When to use it
To understand or document a query someone else wrote or that is hard to read.

## Steps
1. Identify the source tables and the general purpose of the query.
2. Explain each clause in order: joins, filters, groupings and sorting.
3. Describe what each result row represents and its level of detail.
4. Flag possible problems: join-driven duplication, date filters or null handling.

## Quality criteria
- The explanation does not change the meaning of the original query.
- Table and column names are cited exactly as they appear.
- Any part that cannot be confirmed with the available information is flagged.

## Limits
Leave the explanation as documentation for team review. Do not run or modify the query against real data.
