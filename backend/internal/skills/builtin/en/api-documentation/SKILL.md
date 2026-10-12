---
name: api-documentation
description: "Document an endpoint or API: purpose, parameters, responses, errors and a usage example with fictional data. Use it when publishing or updating developer documentation."
metadata:
  category: engineering
  builtin: "true"
---
# API documentation

## When to use it
To explain to other developers how to use an endpoint or a set of operations.

## Steps
1. Describe the endpoint's purpose and when it should be used.
2. Tabulate parameters: type, whether they are required and allowed values.
3. Document success responses and error codes with their causes.
4. Add a request and response example using fictional data.

## Quality criteria
- Names, types and paths match the task specification exactly.
- Each error explains how to resolve it from the client side.
- Examples use fictional data and contain no real credentials.

## Limits
Leave the documentation as a draft for API team review. Do not invent fields, limits or behaviors that are not specified.
