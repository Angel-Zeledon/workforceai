---
name: fixed-asset-register
description: "Maintains the fixed asset register and calculates the monthly and accumulated straight-line depreciation of each asset. Use it when equipment is added, at month close or when preparing financial statements."
metadata:
  category: finance
  builtin: "true"
---

# Fixed asset register

## When to use it
When assets are added, disposed of or moved, or when the period's depreciation and the net book value must be calculated.

## Steps
1. List each asset with its internal identifier, description, category, acquisition date, date put into use, cost, estimated residual value and useful life in months, according to company policy.
2. Calculate the monthly straight-line depreciation: (cost minus residual value) divided by the useful life in months. Depreciation starts in the month the asset is put into use.
3. Calculate the accumulated depreciation at the cut-off date and the net book value: cost minus accumulated depreciation.
4. Flag assets that are fully depreciated, disposed of, sold, not located, or with a change of location or owner.
5. Compare the register's cost and accumulated depreciation with the balances in the fixed asset and accumulated depreciation ledger accounts. Explain any difference.
6. Deliver the register (use `templates/fixed_asset_register.csv`) and the period's depreciation charge by category, as a proposed entry without posting it.

## Quality criteria
- Cost minus accumulated depreciation gives the net book value of each asset, and the total agrees with the ledger.
- No asset is depreciated below its residual value or before it is put into use.
- Each asset has an identifier, an owner and a location.
- The period's additions and disposals are supported by an invoice, a minute or a disposal document.

## Limits
This skill applies the straight-line method described. If policy uses another method, or if the tax useful life differs from the book useful life, verify the rules that apply. It does not add, retire or sell assets and does not post entries. Impairments or market values need a separate assessment.
