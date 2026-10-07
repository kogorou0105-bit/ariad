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
| `ARIAD_ADMIN_TOKEN` | unset | Whitespace-free Bearer token for management endpoints; unset rejects all management requests |
| `ARIAD_CONVERSATION_HISTORY_TURN_LIMIT` | `5` | Recent turns included in model context |

## Product constraints

- Every factual answer must be traceable to evidence supplied by the server.
- Every tenant-owned record is scoped by `workspace_id`.
- Published Agent and ready Knowledge versions are immutable.
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
