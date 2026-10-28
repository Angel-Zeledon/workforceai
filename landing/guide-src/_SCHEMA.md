# Guide page source (one JSON file per page: guide-src/<id>.json)

The user guide uses the same page shape as the technical docs and the trust center: see `../docs-src/_SCHEMA.md`
for every field (`paras`, `list`, `steps`, `code`, `table`, `note`, `tip`, `warning`, inline `backticks`).

Guide-specific conventions
- A how-to section usually has `steps`; each step is one action in the imperative ("Open Settings", not "You can open").
- Mention UI labels exactly as the app writes them (verify them in `frontend/`), in the page's language.
- `image` is a screenshot from `/img/` (`office.webp`, `approvals.webp`, `dashboard.webp`, `projects.webp`).
- Keep `tip` to one sentence. Use `warning` only for something that loses work, leaks data or spends money.
- Plain text only, no HTML/markdown (backticks for inline code are the one exception).
