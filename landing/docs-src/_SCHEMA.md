# Content page source (one JSON file per page)

Used by all three collections built by `scripts/build-guide.mjs`:
`docs-src/` (technical docs), `guide-src/` (user guide) and `trust-src/` (trust center). Same shape everywhere.

```json
{
  "id": "architecture",
  "slug": { "es": "arquitectura", "en": "architecture" },
  "image": { "file": "office.webp", "alt": { "es": "...", "en": "..." } },
  "related": ["api-reference", "security-model"],
  "es": {
    "title": "H1",
    "metaTitle": "<= 60 chars",
    "description": "<= 155 chars, what the reader will be able to do",
    "intro": "2-3 sentences",
    "sections": [
      {
        "h": "Heading (a task or a question)",
        "paras": ["paragraph", "..."],
        "list": ["bullet", "bullet"],
        "steps": ["do this", "then this"],
        "code": { "lang": "bash", "title": "optional caption, e.g. a file name", "text": "docker compose up -d" },
        "table": { "head": ["Variable", "Default", "Meaning"], "rows": [["`PORT`", "8080", "HTTP port"]] },
        "note": "optional neutral aside",
        "tip": "optional one-sentence tip",
        "warning": "optional: something that can break, leak or cost money"
      }
    ]
  },
  "en": { "same structure, same number and order of sections; code identical; table rows identical in count" }
}
```

Rules
- `image` and `related` are optional. `related` ids must exist in the same collection.
- Every section key except `h` is optional. A section renders them in this order:
  `paras`, `list`, `steps`, `code`, `table`, `note`, `tip`, `warning`.
- `code` is one block or an array of blocks. `lang` is one of `bash`, `json`, `yaml`, `http`, `go`, `python`, `sql`, `text`
  (`bash`, `json`, `yaml`, `http` and `go` get build-time syntax highlighting). Code text is identical in es and en
  except for comments.
- Text is plain (no HTML, no markdown) with ONE exception: wrap identifiers (endpoints, env vars, file names,
  commands, JSON fields) in backticks to render them as inline code, e.g. "Set `RUNTIME_TOKEN` on both sides".
  Backticks are allowed in `intro`, `paras`, `list`, `steps`, table cells and callouts; never in `title`,
  `metaTitle` or `description` (they go into meta tags).
- The first column of a table is the row header. It is not monospaced unless you wrap it in backticks.
- es and en must have the same sections in the same order (check.mjs and the build enforce the count).
- Never state an endpoint, env var, limit or feature the code does not have. Mention UI labels exactly as the app writes them.
