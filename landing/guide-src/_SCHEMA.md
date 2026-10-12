# Guide page source (one JSON file per page: guide-src/<id>.json)

```json
{
  "id": "getting-started",
  "slug": { "es": "primeros-pasos", "en": "getting-started" },
  "image": { "file": "office.webp", "alt": { "es": "...", "en": "..." } },
  "related": ["the-office", "approvals-and-security"],
  "es": {
    "title": "H1 of the page",
    "metaTitle": "<= 60 chars, ends with ' | AI Workforce OS' only if it fits",
    "description": "<= 155 chars, what the reader will be able to do",
    "intro": "2-3 sentences",
    "sections": [
      { "h": "Section heading (a task or a question)", "paras": ["paragraph", "..."], "steps": ["do this", "then this"], "tip": "optional one-sentence tip" }
    ]
  },
  "en": { "...same structure and same number/order of sections..." }
}
```
`paras` and `steps` are both optional per section (use whichever fits; a how-to section usually has `steps`). Plain text only (no HTML/markdown); mention UI labels exactly as written in the app.
