---
name: it-access-request-review
description: "Reviews a request for access to a system: need, least privilege, approver, expiry and risks. Use it before granting, expanding or renewing user permissions."
metadata:
  category: it
  builtin: "true"
---

# Access request review

## When to use it
When someone asks for new access, more permissions or the renewal of access to a system, and a decision is needed on whether it is justified and at what scope.

## Steps
1. Identify who is asking for access, to which system, at what level (read, edit or administration) and for what specific task. If any data is missing, the request stays pending.
2. Verify the need: that the task requires that access and that the requester's role or project justifies it.
3. Apply least privilege: propose the most limited role that covers the task and avoid administrator or broad permissions for convenience.
4. Identify the approver according to the system's access matrix (line manager and system owner), and check for segregation-of-duties conflicts, for example someone who records payments and also approves them.
5. Define the validity: expiry or review date, and if the access is temporary, when it will be removed.
6. Deliver the review summary with a recommendation: approve, approve with conditions, reduce the scope or reject, with the reason in one sentence.

## Quality criteria
- Each proposed access has a written task and justification.
- The requested level is the minimum that covers the task, or the reduction is explained.
- There is an identified approver and no unmitigated segregation-of-duties conflict.
- Every temporary access has an expiry or review date.

## Limits
This skill reviews and recommends. It does not grant, modify or remove access, does not create accounts and does not handle credentials or passwords. Approval and execution are carried out by the IT team or the system owner. Do not record passwords or unnecessary personal data in the review.
