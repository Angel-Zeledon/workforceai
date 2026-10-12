# Technical docs page source (one JSON file per page: docs-src/<id>.json)

Same shape as the user guide (see ../guide-src/_SCHEMA.md) with three extra optional fields per section:
`code` (a preformatted block: {"lang": "bash|json|yaml|http|go|text", "text": "..."}) and `table` ({"head": ["Col A","Col B"], "rows": [["a","b"]]}) INSIDE each language's section, and `lead`-less intros.

```json
{
  "id": "architecture",
  "slug": { "es": "arquitectura", "en": "architecture" },
  "related": ["api-reference", "security-model"],
  "es": {
    "title": "H1",
    "metaTitle": "<= 60 chars",
    "description": "<= 155 chars",
    "intro": "2-3 sentences",
    "sections": [
      { "h": "Heading", "paras": ["..."], "steps": ["..."], "code": { "lang": "bash", "text": "docker compose up -d" }, "table": { "head": ["Variable", "Default", "Meaning"], "rows": [["PORT", "8080", "HTTP port"]] }, "tip": "optional" }
    ]
  },
  "en": { "same structure, same number and order of sections; code identical; table rows identical in count" }
}
```
Plain text only (no HTML/markdown) inside strings. `paras`, `steps`, `code`, `table`, `tip` are all optional per section; a section renders them in that order.
