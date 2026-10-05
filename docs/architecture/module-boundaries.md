# Module boundaries

## Runtime boundaries

Ariad starts as a modular monolith with independent process lifecycles:

- `apps/console` is the authenticated administration SPA.
- `apps/widget` is the public, least-privilege customer client. It must never import Console business code.
- `cmd/api` serves management and public HTTP/SSE contracts.
- `cmd/worker` executes retryable asynchronous jobs. It reuses domain services, not API process state.

The two browser applications may import `packages/contracts`, which contains runtime-free transport types. Sharing feature state, API clients, routes, or authentication code across the Console and Widget requires an explicit boundary review.

## Go dependency direction

```text
cmd/* -> internal/domain packages -> domain-owned interfaces
   \-> internal/platform adapters -> external systems
```

Domain packages are `auth`, `tenant`, `agent`, `knowledge`, `ingestion`, `retrieval`, `runtime`, `conversation`, and `evaluation`. A domain may call another domain only through an exported service interface. It must not import another domain's storage implementation or reach into its tables.

`internal/platform` owns database, queue, storage, model-provider, telemetry, and HTTP infrastructure. It implements interfaces required by domains; it does not decide answer policy, tenant authorization, publication, or evaluation outcomes.

A roadmap capability does not become a domain merely because it may need a UI
or vendor integration. New top-level domains require stable ownership,
invariants or a distinct lifecycle and follow
[ADR-0002](../decisions/0002-product-context-and-capability-ownership.md).

## Application and channel boundary

HTTP, SSE, Widget and future external channels are adapters. They translate
protocol input into application commands and render channel-neutral results.
Conversation state, retrieval and answer policy remain on the server. Runtime
terminates each answer as `answered`, `clarify`, `refused` or `handoff`; a full
human-support inbox may be implemented independently later.

Public requests use the stable context vocabulary in ADR-0002. Client-supplied
workspace, agent, visitor and conversation identifiers are authorized or
validated before business lookup. Public ingress also owns origin policy,
request-size limits, rate limiting and idempotency enforcement; those controls
do not belong in the Widget.

## Data and event boundary

Conversation transcripts store opaque actor references, not duplicated email or
phone fields. A future contact capability owns PII, consent and retention.
Analytics is derived state and cannot become an alternative source of truth.

Required business facts and unreconstructable usage are persisted according to
[ADR-0003](../decisions/0003-reliable-business-events-and-metering.md). Domain
mutations and their outbox records share a PostgreSQL transaction. Redis and
Asynq may deliver work but are not the only record of a confirmed fact.

## Non-negotiable invariants

1. Every business lookup receives `workspace_id`; tenant-owned rows are never fetched by resource ID alone.
2. A published Agent version and a ready Knowledge version are immutable.
3. Citations are server-mapped from Evidence IDs provided during the same answer turn.
4. PostgreSQL is the source of truth for business state; Redis loss cannot erase confirmed facts.
5. External side effects are idempotent and leave an auditable result.
6. HTTP handlers translate protocols and delegate; they do not contain SQL or model prompts.
7. Model providers translate vendor protocols; they do not retrieve evidence or persist business records.
8. Contact PII is not duplicated into conversation metadata, logs, traces or analytics projections.
9. Required usage facts cannot be silently discarded by a best-effort or no-op production recorder.
10. Channel adapters do not own conversation state or answer policy.

## Enforcement

`make check-go` runs `internal/architecture/dependencies_test.go` as part of the
canonical Go package set. The test scans Ariad-owned repository imports and
enforces two rules before domain implementations grow:

- domain packages cannot import `internal/platform` adapters;
- code outside a domain may import that domain's root package, but not its
  implementation subpackages.

The scan includes package-local `_test.go` files, so unit tests obey the same
boundaries as production code. Only `tests/integration` is allowed to assemble
platform adapters and domain implementations directly; security and evaluation
tests remain behind public domain surfaces.

Root wildcard commands such as `go test ./...` are intentionally not canonical.
The repository's npm dependencies may themselves contain Go sources beneath
`node_modules`; `GO_PACKAGES` in the Makefile limits checks to `cmd`, `internal`
and `tests`.

`npm run check` is the canonical local and CI gate. It also runs ESLint, frontend
type checks and production builds, `gofmt` in check-only mode, `go vet`, the Go
module's pinned `golangci-lint` tool, and Go tests. Textual rules that cannot yet be checked
mechanically remain review requirements until a reliable executable rule exists.

## HTTP deadlines

Request/response handlers opt into bounded middleware groups. A timeout is never
attached to the root router because SSE and other streaming handlers are
long-lived. Streaming routes own separate handshake, idle, and business-level
deadlines, while `http.Server.ReadHeaderTimeout` protects request headers at the
process boundary.

## Test ownership

Unit tests live beside their packages. Tests that cross a process, database, tenant, or provider boundary live under `tests/`. Fixed evaluation fixtures are versioned and cannot use production conversations or metrics.
