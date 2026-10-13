# Edit by chat: `fs.edit`

A person says "change the title of the table" or "make the second paragraph shorter" about a file. An agent edits it, a NEW VERSION is saved, and the chat shows what changed with **Keep** and **Undo**.

## The flow

1. In the file viewer, the person types in **Ask for a change** (agent picker, Enter sends) or turns on **Edit in chat** and writes in the normal chat. Both are ordinary chat messages (`sendChat`) with the file attached (path + version) and an instruction to use `fs.edit`. There is no new transport.
2. The agent reads the file (`fs.read`, which returns its version), then calls `fs.edit`.
3. The backend applies the operations to the current bytes, saves a new version attributed to the agent, and returns a short diff to the agent.
4. The chat message of the task shows a `FileEditCard`: file, `v3 to v4`, `+N -M`, a collapsible diff, **Keep** and **Undo**.

## The action

```
tool=fs  action=edit
args: {
  path | file_id,
  expected_version,          // required: the version the agent read
  ops: [ {op: ...}, ... ],   // 1..50 operations
  message?                   // version message (default "Edit by agent: N operation(s)")
}
```

- **Atomic**: all operations apply, in order (each sees the result of the previous), or none does. Nothing is written on any failure.
- **Bound to `expected_version`**: if the file is at another version the call fails with `version_conflict` and `current_version` (read again, retry).
- **Same zones and approvals as `fs.write`**: own desk and assigned projects run free; `/Shared` and granted folders need a human approval card (`fs.edit <path>`, risk medium, reversibility versioned). An edit also needs read access: a file the agent cannot read (another desk, another project, `/Clients` without a grant) is refused with `not_granted`.
- Every edit is a new version, so it is reversible. Standing approvals may cover `fs.edit` like `fs.write`.
- Result for the agent: summary `edited <path> (version 3 -> 4): +2 -1 lines`, a unified diff (max 4000 characters, delimited untrusted data) and data `file_id, path, version, previous_version, added, removed, ops`. An edit that produces identical content creates no version (`unchanged: true`).

### Failure codes (stable)

| code | meaning | data |
| --- | --- | --- |
| `invalid_args` | missing/malformed field, unknown op, too many ops | `op` (1-based, 0 = whole call) |
| `edit_not_found` | the `old`/anchor text does not exist | `op`, `matches: 0` |
| `edit_ambiguous` | it exists more times than `count` (default 1) | `op`, `matches` |
| `edit_count_mismatch` | it exists fewer times than `count` | `op`, `matches` |
| `edit_invalid_result` | the edit would leave a JSON file invalid | |
| `version_conflict` | someone saved in between | `current_version` |
| `unsupported_type` | pdf, images, pptx, binary | |
| `file_too_large` | text file > 2 MB, document > 8 MB, or result over the drive limit | |
| `not_granted` / `not_found` / `approval_required` | zone and existence rules | |

Through the Tool Gateway the failure `summary` carries the code, the op number and the match count (never the quoted text), so the agent can retry with more context.

### Caps

50 operations per call; 20,000 characters per string; 200,000 characters of arguments in total; text files up to 2 MB; docx and xlsx up to 8 MB; at most 10,000 regex matches; regex source up to 500 characters (RE2, linear time).

## Operations per format

### Text: md, txt, csv, json, code

| op | args |
| --- | --- |
| `replace` | `old`, `new`, `count?` (integer, default 1, or `"all"`), `regex?` (default false) |
| `insert_after` / `insert_before` | `anchor`, `text`, `count?` |
| `delete` | `text`, `count?` |
| `append` | `text` (adds a newline first if the file does not end with one) |

`old`, `anchor` and `text` are matched exactly (case sensitive). With `regex: true` the pattern is RE2, must not match the empty string, and `new` may use `$1`. CRLF files are matched with LF and written back with CRLF. A `.json` file that was valid must still be valid after the edit.

### docx (edited in place in `word/document.xml`)

| op | args |
| --- | --- |
| `replace` | `old`, `new`, `count?`: text inside a paragraph (headings and table cells included); a match cannot span two paragraphs; single line |
| `set_heading` | `text` and `old` (current heading text, must be unique; the Title also matches) or `index` (1-based among Heading 1-9, the Title not counted) |
| `append_paragraph` | `text` (new paragraphs before the page settings, default style) |

Every other part of the package (styles, numbering, images, headers, footers, settings) is copied untouched, and so is the markup around the text. **Formatting that can be lost or changed**: a replacement takes the formatting of the first run it touches (text that was bold in the middle of a replaced span becomes the style of its first run); `set_heading` puts the new text in the first run of the heading; appended paragraphs use the default style. Headers, footers, footnotes and text boxes are not reached. Tracked changes and comments are not created.

### xlsx (workbook opened, changed and written back)

| op | args |
| --- | --- |
| `set_cell` | `cell` (`B3`), `sheet?` (default first), and `value` (text, number, boolean, null clears) or `formula` |
| `add_row` | `values` (list, up to 30 columns), `sheet?`; written under the last used row, copying the cell styles of the row above |
| `rename_sheet` | `sheet?`, `name` (max 31 characters, no `[ ] : * ? / \`) |

Text is always stored as text, never evaluated (`=cmd...` stays a string). Formulas are limited, like in `write_document`, to `SUM(range)` or `AVERAGE(range)` over a range of the same sheet; anything else is refused. Values, number formats, column widths, merged cells, frozen panes and cell styles survive. **May be lost**: details of charts and drawings that the library does not model, macros, pivot caches, external links. Formulas that depend on an edited cell keep their old cached value until the file is opened in a spreadsheet application (the workbook is flagged to recalculate on open). `add_row` goes below a totals row if there is one; use `set_cell` to place data elsewhere. Rows are limited to the same 5,000 and columns to 30 as generated sheets.

Not editable with operations: pdf (regenerate it with `write_document` from the text), images, pptx.

## Diff and undo

The diff is computed on the text of the file (text types) or on the extracted text (docx paragraphs, first sheet rows), with the same line diff as the drive (`DiffText`). The chat card loads the stored diff with `GET /drive/files/:id/diff?from=N&to=N+1`, which exists for text types; for docx and xlsx the card shows the counts and says the line diff is not available, and the person opens the file to see the change.

- **Keep** is a no-op that dismisses the buttons: the edit already is the newest version.
- **Undo** calls the existing restore-version API with the previous version. That writes a NEW version with the old bytes (history is never rewritten). It is offered only while the edited version is still the latest one; the card checks again right before restoring. If newer versions exist the card says so and the person uses the version list in the viewer.

## How the card reaches the chat

`fs.edit` calls `NotifyEdit(path, from, to, added, removed)`; the orchestrator keeps it per task (`NoteTaskEdit`); when the task posts its file message, the file reference gets `action: "edit"`, `from_version`, `added`, `removed`, and the frontend renders `FileEditCard` instead of the plain file card. Several edits of one file in one task become one card from the first version to the last.

## Safety

File content is untrusted data: the agent prompt says so, the diff returned to the agent is sanitized and delimited like any `fs.read`, and an agent edit is only as powerful as `fs.write` (same zones, same approvals, every change versioned and restorable).
