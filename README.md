# Ariad

> Follow the thread. Trust the answer.

Ariad is a traceable AI support-agent platform. Teams connect websites and knowledge sources, test grounded answers, inspect evidence and execution traces, and publish a customer-facing widget.

The repository is a modular monolith with independently started browser, API, and worker processes. Domain packages stay behind explicit boundaries so they can evolve without prematurely splitting into services.

## Repository map

```text
ariad/
├── apps/
│   ├── console/          # Authenticated React administration SPA
│   └── widget/           # Public embeddable React client
├── packages/contracts/   # Reviewed, runtime-free frontend contracts
├── cmd/
│   ├── api/              # Go HTTP process entry point
│   └── worker/           # Go asynchronous worker entry point
├── internal/             # Go domain and platform packages
├── db/                   # SQL migrations, sqlc queries and generation config
├── api/                  # OpenAPI and SSE event contracts
├── tests/                # Integration, security and eval tests
├── deploy/compose/       # Local dependency environment
└── docs/                 # Architecture decisions and development guides
```

See [module boundaries](docs/architecture/module-boundaries.md) for ownership and dependency rules.
Contribution and commit-message rules are documented in [CONTRIBUTING.md](CONTRIBUTING.md).

## Prerequisites

- Node.js 24.19.x and npm 12.x
- Go 1.26.5

The supported baseline is pinned by `.nvmrc`, `.go-version`, `go.mod`, and the CI workflow. See [ADR-0001](docs/decisions/0001-toolchain-baseline.md) before changing it.

PostgreSQL, Redis and S3-compatible storage are introduced by the next Foundation task; the current API and worker bootstrap do not require them.

## Local development

Install frontend dependencies once:

```bash
npm install
```

Start each process in a separate terminal:

```bash
npm run dev:console
npm run dev:widget
go run ./cmd/api
go run ./cmd/worker
```

The Console runs on `http://localhost:5173`, the Widget preview on `http://localhost:5174`, and the API on `http://localhost:8080`. The API exposes `GET /healthz`.

Run the complete repository quality gate:

```bash
npm run check
```

This single command checks frontend lint, types and production builds, Go
formatting, `go vet`, the pinned `golangci-lint` tool, architecture boundaries,
and Go tests.

Run the Go-only gate with either equivalent command:

```bash
npm run check:go
make check-go
```

Do not run repository-wide wildcard commands such as `go test ./...`,
`go vet ./...`, or `go list ./...` from the repository root. Some npm packages
ship Go source files under `node_modules`, which is also beneath the Go module
root. `make check-go` uses the canonical package set from the Makefile and
excludes frontend dependencies.

| Variable | Default | Purpose |
| --- | --- | --- |
| `ARIAD_API_ADDR` | `:8080` | Go API listen address |
| `ARIAD_LOG_LEVEL` | `info` | API and Worker log level |
| `ARIAD_MODEL_CONFIG_ENCRYPTION_KEY` | unset | Base64-encoded 32-byte AES key required to persist workspace BYOK configuration |
| `ARIAD_CONVERSATION_HISTORY_TURN_LIMIT` | `5` | Recent turns included in model context |
| `ARIAD_EMBEDDING_MODEL` | unset | System-default OpenAI-compatible embedding model; when unset, retrieval uses lexical matching unless a workspace embedding model is configured in Console |

On the first API startup after migrations, Ariad creates the `admin` account and
prints its randomly generated initial password once in the startup log. Sign in
through the Console and create a separate administrator account for each operator.
In a multi-instance deployment, collect startup logs centrally: only the instance
that wins first-account creation prints the password. If every administrator is
locked out, stop all API instances, back up the database, delete the rows from
`administrator_sessions` and `administrators`, then start exactly one API instance
and capture its newly generated password. This is a destructive break-glass reset
of administrator identities; it does not affect workspace or visitor data.

The widget creates an anonymous visitor identity on first use. An unpredictable
refresh credential preserves that identity in browser `localStorage`; short-lived
Bearer sessions are renewed on activity for 30 days and safely reissued through
the refresh credential after expiry. The current conversation ID is kept in
`sessionStorage`, so reopening the widget starts a new conversation for the same
anonymous visitor. Apply database
migration `000006_visitor_sessions.sql` before deploying this version. Invalid or
expired visitor credentials receive `visitor_session_invalid` or
`visitor_session_expired`; the widget automatically obtains a new identity while
keeping already-rendered messages visible.

Generate the workspace model-configuration encryption key with `openssl rand -base64 32`
and keep it stable across API restarts. Losing or changing it makes persisted workspace API keys undecryptable.

Apply database migration `000007_knowledge_embeddings.sql` to persist chunk vectors and the
workspace embedding configuration. In Console, open **Model configuration** and provide an
OpenAI-compatible embedding Base URL, model name, API key, and minimum similarity threshold.
Saving takes effect immediately; clearing the embedding model disables semantic retrieval and
keeps lexical retrieval active. API keys are encrypted with
`ARIAD_MODEL_CONFIG_ENCRYPTION_KEY` and are returned only as masks.

New knowledge is embedded during ingestion. For knowledge created before this migration, use
**Backfill existing knowledge** on the same Console page. The job runs in the background and its
running/completed state, counts, and per-chunk failure reasons are persisted and available from
`GET /api/v1/knowledge/embeddings/backfill?workspace_id=...`; failed chunks are retried by starting
the backfill again. Provider errors are reflected by the most recent embedding health status in
Console and retrieval errors are logged while lexical matching remains available.

### Knowledge sources and file upload

Open **Knowledge** in Console to see text, URL, and file sources in one list. Each source exposes
its processing status, chunk count, creation time, and any parsing failure. Sources can be deleted
(their chunks and retrieval visibility are removed immediately); failed files and URL sources can
be reprocessed from the detail page.

The upload area accepts one or more `.pdf`, `.docx`, `.txt`, `.md`, or `.markdown` files. Parsing
runs on the API server. Each file is limited to **10 MiB**, and one multipart upload is limited to
**50 MiB**. Unsupported, empty, malformed, or oversized files are retained with a failed status and
a visible error when possible, so they do not block other files in the same upload. Successfully
parsed files use the normal chunking path and automatically receive embeddings whenever semantic
retrieval is configured. PDF extraction supports composite CID fonts, embedded `ToUnicode` CMaps,
and the standard Adobe Chinese, Japanese, and Korean character collections; image-only scans still
require OCR and are reported as having no extractable text.

File uploads are idempotent within a workspace. Ariad derives the idempotency identity from the
normalized file extension and a SHA-256 digest of the original bytes, so retrying the same upload—or
uploading the same content under another filename—returns the existing source instead of duplicating
its chunks. A different file format is treated separately because it follows a different parser.

### Playground

Open **Playground** in Console to test a single question without creating a conversation. Each run
shows the generated answer, ranked knowledge chunks, source titles, relevance scores, hit count,
and retrieval/model/total latency. `top-k` and minimum relevance are request-local controls and do
not change workspace retrieval settings. The browser keeps the latest 20 runs in local storage for
comparison; clearing browser storage or using another browser removes that history.

As a system-default alternative, set `ARIAD_EMBEDDING_MODEL` together with
`ARIAD_MODEL_BASE_URL` and `ARIAD_MODEL_API_KEY`. A local OpenAI-compatible provider works too;
for example, Ollama can use a Base URL such as `http://localhost:11434/v1`, an installed embedding
model such as `nomic-embed-text`, and any non-empty API-key placeholder. Ariad combines cosine
similarity with lexical overlap and excludes semantic-only results below the configured threshold.

## Product constraints

- Every factual answer must be traceable to evidence supplied by the server.
- Every tenant-owned record is scoped by `workspace_id`.
- Published Agent versions are immutable; knowledge sources change only through explicit delete or reprocess actions.
- Uncertainty produces clarification, refusal, or human handoff—not invented facts.
- API, Worker and browser runtimes have separate lifecycle and permission boundaries.
- Contact PII stays separate from conversation transcripts and telemetry.
- Unreconstructable usage facts are recorded durably before production traffic.
- Channel adapters render server-owned conversation and answer state; they do not own it.

These boundaries are executable: Go tests reject domain-to-platform imports and
imports of another domain's implementation subpackages.

## Product documentation

- [Product Requirements Document](https://ocn0kpf8lgg6.feishu.cn/docx/AFued3kVnoPzSQxDbSNcuZ6Gnze)
- [Technical Design](https://ocn0kpf8lgg6.feishu.cn/docx/PZ6YdU5bToWWzsxyKhacs0iYnwh)
- [Implementation Order](https://ocn0kpf8lgg6.feishu.cn/base/Ea9FbzN3caLzfZsSLO7cc9bNnge?table=tblx96PQIrqpTEJo&view=vew5fuD0GJ)
- [ADR-0002: Product context and capability ownership](docs/decisions/0002-product-context-and-capability-ownership.md)
- [ADR-0003: Reliable business events and metering](docs/decisions/0003-reliable-business-events-and-metering.md)

## License

No license has been selected yet.
