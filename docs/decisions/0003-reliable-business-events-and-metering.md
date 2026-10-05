# ADR-0003: Reliable business events and metering

- Status: Accepted
- Date: 2026-10-06

## Context

Usage metering, analytics, notifications and billing all need facts produced by
conversation, model-provider and ingestion workflows. Some values, such as
message counts or current storage size, can be reconstructed from durable
business state. Exact provider token usage, failed or interrupted invocations
and the price context at the time of consumption often cannot.

A synchronous best-effort recorder or a no-op implementation would allow the
business operation to succeed while silently losing facts needed for quotas and
billing. Direct calls from every domain to a future billing package would also
reverse the intended dependency direction.

## Decision

### PostgreSQL is the event source of truth

When a successful domain mutation produces a required business fact, the state
change and an outbox record are committed in the same PostgreSQL transaction.
Redis or Asynq may wake workers and deliver work, but neither is the only copy of
a confirmed fact.

The outbox publisher provides at-least-once delivery. Consumers deduplicate by
`event_id` and must be safe to retry. Publication status is operational state;
deleting Redis data cannot erase an unpublished or unconsumed fact.

### Event envelope

Versioned durable events use an envelope containing:

- `event_id`, `event_type` and `event_version`;
- `occurred_at` in UTC;
- `workspace_id` for tenant-owned events;
- `aggregate_type`, `aggregate_id` and, when applicable, aggregate version;
- `request_id` or `correlation_id`;
- an idempotency or causation reference when the producer handles retries;
- a minimal, schema-versioned payload that follows ADR-0002's PII rules.

Event names describe completed facts rather than commands. Consumers do not
mutate another domain's tables to acknowledge an event.

### Usage facts

Metering stores raw, immutable units rather than only incrementing mutable
counters. Depending on the workflow, a usage fact records:

- the workspace, agent and originating request or answer;
- provider and model identifiers;
- input, cached-input and output units reported by the provider;
- completion, failure or interruption status when it affects cost or quota;
- knowledge or storage units whose historical value cannot be reconstructed;
- the provider occurrence time and a unique deduplication key.

Provider-reported units remain raw facts. A future billing capability applies a
versioned price or plan independently so that pricing changes do not rewrite
historical consumption.

Required production facts may not be silently discarded. If a fact cannot be
recorded atomically, the operation either fails or records an explicit
`unmetered` outcome that raises an operational alert; the owning workflow must
document which behavior it uses. No-op recorders are limited to tests or local
development paths that cannot be mistaken for production metering.

### Projections and consumers

Analytics dashboards, quota counters and notification deliveries are disposable
projections or side effects derived from committed facts. They may be rebuilt or
replayed and are never authoritative for the original conversation or usage.
Billing consumes an immutable usage ledger, not an analytics aggregate.

This decision does not require a distributed event broker, event sourcing, a
generic event framework or a billing domain in the Foundation phase.

## Implementation gates

Before unreconstructable production usage is accepted:

- migrations exist for the outbox and immutable usage facts;
- business mutation and outbox insertion are covered by one transaction;
- duplicate delivery and consumer retry tests exist;
- the publisher exposes backlog age, failure count and last-success telemetry;
- event schema compatibility and retention rules are documented;
- reconciliation can compare provider reports with stored usage facts.

Before billing is introduced:

- raw units and price application are separate records;
- price and plan versions are effective-dated;
- quota and invoice consumers are idempotent;
- backfill and reconciliation procedures are exercised against fixed fixtures.

## Consequences

The write path carries a small persistence and operational cost before billing
exists. In return, later analytics and commercial features do not need to call
back into domain internals or trust lossy counters. Replayable projections keep
product reporting flexible while PostgreSQL remains the authoritative record of
confirmed business facts.
