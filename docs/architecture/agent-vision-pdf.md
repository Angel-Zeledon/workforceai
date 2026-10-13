# Agent vision and PDF reading (capabilities 1 and 2)

Agents SEE images (photos, screenshots, charts) and READ PDFs the way people expect from Claude or ChatGPT:
the text layer first, and scanned pages as pixels. This page follows the shared contract in
`docs/architecture/agent-capabilities.md`.

## What happens

| The agent does | It gets |
|---|---|
| `fs.read {path: "x.pdf"}` | The text layer with page markers (`[page 1]`, `[page 2]`...), `pages: N`, and `scanned: true` when the text layer is empty or very thin. |
| `fs.read {path: "x.pdf", pages: "3-5"}` | Only those pages' text. |
| `fs.read {path: "foto.png"}` (png, jpg, gif, webp) | The image, normalized, attached as an image part for the next pass. |
| `fs.read {path: "x.pdf", view: "image", pages: "3-5"}` | Those pages rendered as images (at most 4 per read). |
| `fs.search {query}` | PDFs are indexed with their text, so they are found like any document. |

A person who attaches images to a chat message gets them in front of the agent on its **first** pass (at most 4).
Each attachment is read as the agent (`fs.read view:"image"` through the Tool Gateway): an attachment outside what
the agent may read is not attached.

## Flow

1. **Backend, drive** (`backend/internal/drive`). `AgentFS.Run` re-plans every call first (zones, grants, tool
   labels), then `readVisual` (`vision.go`) reads the bytes of the same file version the text read would use. Images
   are normalized; PDF pages are rendered by the sandbox. The result is a `RunResult` with a delimited text block (the
   image is described as data) plus `Images`. There is no path that reads pixels without the permission checks.
2. **Gateway** (`internal/gateway/native.go`). `NativeResult.Images` becomes `GatewayOutcome.Images`, next to the
   sanitized `Blocks` (same controls, kill switch, audit).
3. **Orchestrator** (`internal/application/vision.go`, `connections_flow.go`). Second pass: images of the reads are
   added to `RunTaskRequest.Images` (newest kept, capped). First pass: `firstPassImages` reads the image attachments of
   the chat request as the agent. Runtime notices are turned into task evidence and, once per request, a
   `provider.no_vision` event.
4. **Runtime** (`agent-runtime/app/vision.py`, `providers.py`, `crewai_engine.py`). Images are validated and capped
   again, and the provider is chosen: `vision_candidates()` = the data-policy candidates (`allowed_providers`,
   `ALLOWED_PROVIDERS`, `byo_only`) restricted to vision-capable providers, in the same order and with the same
   failover. The live engine sends one multimodal call (OpenAI-style `image_url` data URLs; CrewAI's native
   Anthropic provider converts them to Anthropic `image` blocks), each image preceded by its untrusted-data label.
   Simulation mode only acknowledges the images in the evidence.
5. **No vision allowed.** When no allowed provider can see images, nothing is sent anywhere with the images: the
   runtime returns the notice `vision_unavailable`, the agent is told to say plainly that it could not see them, and
   the task continues with text. The UI shows an amber banner (`provider.noVision.*`, es/en/pt).

## Contract (all additive, omitted when empty)

Backend -> runtime, `POST /v1/run-task`:

```json
"images": [{"name": "foto.png", "mime": "image/png", "data_b64": "<base64>", "source_path": "/Shared/foto.png",
            "page": 3, "width": 1568, "height": 1100}]
```

Runtime -> backend: `"notices": ["vision_unavailable" | "images_dropped"]`; `usage.image_count`, `usage.image_tokens`
(approximate, already included in `input_tokens` and in `cost_usd`).

Drive text API (`GET /api/v1/drive/files/{id}/text`, `TextResult.info`): `kind: "pdf"`, `pages`, `scanned`,
`image_pages`, `code` (only when extraction failed).

Go types: `application.ImagePart`, `RunTaskRequest.Images`, `RunTaskResponse.Notices`, `NativeResult.Images`,
`GatewayOutcome.Images`, `drive.RunResult.Images`, `drive.Renderer`, `sandbox.RenderRequest/RenderResponse`.

## Stable codes

| Code | Where | Meaning |
|---|---|---|
| `pdf_too_large` | text read, render | over 20 MB |
| `pdf_encrypted` | text read, render | needs a password |
| `pdf_invalid` | text read, render | not a PDF, corrupt, unsupported structure (also PDF 2.0 headers in the Go reader) |
| `pdf_timeout`, `pdf_busy` | text read | extraction over 8 s, or both extraction slots busy |
| `pdf_pages_invalid` | read with `pages` | bad range or no such page |
| `render_unavailable` | `view:"image"` | no render sandbox configured or reachable |
| `render_failed`, `image_invalid`, `image_too_large`, `image_unsupported` | image / page view | could not prepare it |
| `vision_unavailable` | runtime notice | no allowed provider can see images; text only |
| `images_dropped` | runtime notice | some images were over the per-call caps |
| `no_vision_provider` | `providers.ERR_NO_VISION_PROVIDER` | reserved constant for callers that must fail instead of degrading |

## Limits and settings

| Setting | Default | Where |
|---|---|---|
| PDF text: size / pages / chars / time | 20 MB / 300 pages / 1,000,000 chars / 8 s, 2 concurrent | `internal/pdftext` |
| `VISION_MAX_IMAGES` | 4 (max 8) | backend and runtime, per call |
| `VISION_MAX_IMAGE_MB` | 5 (max 10) | per image after normalization |
| `VISION_MAX_TOTAL_MB` | 16 (max 32) | per call |
| `VISION_MAX_SIDE` | 1568 px | long side of every image sent |
| `VISION_PDF_DPI` | 110 (50-200) | page raster, bounded so the long side never exceeds `VISION_MAX_SIDE` |
| `VISION_MAX_PDF_PAGES` | 4 (max 8) | pages per `fs.read view:"image"` |
| `VISION_ATTACH_FIRST_PASS` | true | offer attached images on the first pass |
| `KIMI_VISION`, `CUSTOM_LLM_VISION` | false | the endpoint accepts images (`DEEPSEEK_VISION` also exists, default false) |
| `DRIVE_RENDER=off` | on when the sandbox is configured | backend: never call the render sandbox |
| `SANDBOX_RENDER_IMAGE` | unset (compose: `wfai-sandbox-render:1`) | runner: the render image; empty = `/v1/render` answers 503 |

Vision by provider: anthropic yes (Claude 3 and later), openai yes (gpt-4o / gpt-4.1 / gpt-5 / o-series, not
gpt-3.5), deepseek-chat no, kimi and custom configurable (default no).

## Turning on PDF page rendering (operator)

Text extraction and image viewing (PNG/JPEG/GIF) work with no setup. To let agents see scanned PDF pages (and WebP):

1. Build the render image: `docker build -t wfai-sandbox-render:1 sandbox/render` (or `make up-sandbox`, which
   builds it with the `sandbox` compose profile).
2. Run the sandbox runner with `SANDBOX_RENDER_IMAGE=wfai-sandbox-render:1` (compose sets it by default).
3. Give the backend `SANDBOX_URL` and `SANDBOX_TOKEN` (the same pair code runs use). Logs say
   `render sandbox: agents can see PDF pages and images`.

Without step 3 the backend prepares PNG/JPEG/GIF images with the Go decoders and tells the agent plainly that PDF
pages cannot be rendered here.

## Security notes

- **Data, never instructions.** The text block of every visual read says that what is seen is data; in the runtime
  each image is preceded by a `wrap_untrusted` label and the prompt repeats the rule. A read of someone else's file
  marks the task tainted (as any read), so later side effects still go through approval. OCR-like text the model
  reads from an image cannot be redacted before the model sees it: the pixels are the content.
- **Permissions.** Pixels come only from `AgentFS.Run` after `Plan` (zones, grants, `fs`/`fs_read` labels, deleted
  files refused), through the Tool Gateway (kill switch, pause, per-tool switch, audit). Tested in
  `drive/vision_test.go` (`TestImagePartFollowsPermissions`) and `application/vision_flow_test.go`.
- **Data policy.** Images only go to providers in the policy-filtered candidate list; there is no fallback to a
  non-vision or non-allowed provider with the images (`tests/test_vision.py`).
- **Metadata.** Every image is decoded and re-encoded (EXIF/GPS, XMP, ICC, comments dropped) before it leaves the
  backend.
- **Untrusted formats.** PDF rasterization and WebP decoding run only in the render container (no network,
  read-only root, non-root, no capabilities, 768 MB, 64 pids, 45 s), and the runner re-validates names, counts,
  sizes, types, pages and dimensions of what comes back. The Go side uses the memory-safe standard decoders with a
  pixel cap checked from the header before decoding (40 MP, 20000 px per side).
- **PDF text in the backend** uses `github.com/ledongthuc/pdf` (BSD-3-Clause, no dependencies). It panics on bad
  input and has no deadline, so every call is wrapped (recover, timeout, two concurrent slots, page/char/byte caps).
  A parser stuck on a hostile file keeps its slot until it ends; a decompression bomb inside a content stream could
  still use memory until the timeout fires (bounded by the 20 MB input, not by the parser). Moving text extraction
  into the sandbox is the stronger option if that matters.
- **Tenant isolation.** No new tables, caches or indexes: images live only in the request of one task.

## Tests

- Go: `internal/pdftext` (page markers, ranges, scanned detection, limits, corrupt and encrypted input, timeout),
  `internal/imagenorm` (downscale, metadata stripped, pixel bombs), `internal/drive/vision_test.go`,
  `internal/application/vision_*_test.go`, `internal/sandbox/render_test.go`,
  `internal/sandbox/runnerd/render_test.go` (the integration test runs only when Docker and the render image exist).
- Python: `agent-runtime/tests/test_vision.py` (caps, multimodal messages, capability table, data policy, failover
  among vision providers, text-only fallback, simulation/API).
