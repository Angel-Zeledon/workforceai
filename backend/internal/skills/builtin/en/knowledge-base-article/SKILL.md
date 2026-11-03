---
name: knowledge-base-article
description: "Writes a step-by-step help article for customers, with a searchable title, requirements and troubleshooting. Use it to document a fix that keeps coming up in support."
metadata:
  category: support
  builtin: "true"
---

# Knowledge base article

## When to use it

To turn a fix that repeats across several tickets into a help article the customer can find and follow on their own. Use `templates/help-article.md` as the structure.

## Steps

1. Write a title with the words a customer would search for, such as the symptom or the task, not the internal name of the feature.
2. Summarize in two or three sentences who the article applies to, what it solves and what the customer needs before starting.
3. List the steps in order, one action per step, with the expected result at the end of each. Use no more than seven steps; if there are more, split them into two articles.
4. Add a troubleshooting section with the two or three most frequent errors, each with its visible sign and its fix.
5. Check that each step can be followed without reading the rest of the article and without access the customer does not have.
6. Close with when to contact support and what data to send, such as the time of the error or a screenshot.

## Quality criteria

- The title matches the way the customer describes the problem.
- Each step has a single action and an observable result.
- The steps and errors come from the ticket, the knowledge base or the product owner, not from assumptions.
- The article can be read in three minutes or less.

## Limits

Leave the article as a draft for review by the support team or the product owner before publishing it. Do not invent features, menus, prices or timelines; if a fact is missing, mark it as pending. Do not use personal data of real customers in examples.
