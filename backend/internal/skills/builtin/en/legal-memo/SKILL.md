---
name: legal-memo
description: "Drafts an internal legal memo with question, facts, analysis, conclusion and risks, using a fixed template. Use it to put a legal position in writing before a business decision."
metadata:
  category: legal
  builtin: "true"
---

# Internal legal memo

## When to use it
When a business decision needs a documented legal position, such as a hiring matter, a compliance issue or an ongoing dispute.

## Steps
1. Define the legal question in one sentence, with the period and jurisdiction that bound it.
2. List the relevant facts with their source (contract, email, minutes). Separate what is verified from what is only asserted.
3. Identify the applicable rules and criteria. If no verified sources are available, write "pending verification" instead of citing from memory.
4. Analyze each point: arguments in favor, arguments against, and how strong each one is.
5. Write the conclusion as a direct answer to the question, with the level of certainty and the conditions that could change it.
6. List the risks and recommended actions, with an owner and a suggested date, using `templates/legal-memo.md`.

## Quality criteria
- The question and the conclusion each fit in one sentence.
- Every fact has a source, and assumptions are marked as such.
- Rules and criteria cited are checked against an official source before use; references are never invented.
- Risks are ordered by impact and likelihood.

## Limits
This is an internal draft for review by a person with legal competence. It does not replace a formal legal opinion and is not shared outside the company without that review.
