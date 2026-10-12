---
name: code-review-checklist
description: "Generate a code review checklist: correctness, tests, security, readability and performance, tailored to the change. Use it when reviewing a pull request before approval."
metadata:
  category: engineering
  builtin: "true"
---
# Code review checklist

## When to use it
To review a code change in an orderly way before approving it.

## Steps
1. Summarize the change in one sentence and check it matches its description.
2. Review correctness: edge cases, handled errors and side effects.
3. Review tests, security (inputs, permissions, secrets) and obvious performance issues.
4. Review readability and consistency with project style, marking each point as blocking or a suggestion.

## Quality criteria
- Each comment names the file or line and explains why.
- Blocking issues are clearly separated from optional improvements.
- Code that meets the requirements is not asked to be rewritten without a technical reason.

## Limits
Leave the comments as a draft for the reviewer to publish. Do not approve or merge changes on your own.
