# ADR-0002: Product context and capability ownership

- Status: Accepted
- Date: 2026-10-06

## Context

Ariad starts with a deliberately small set of domains for the traceable-answer
path. Product capabilities such as leads, feedback, analytics, billing,
notifications, human support and additional channels will arrive later. Creating
one package for every anticipated feature would establish boundaries before the
rules and data ownership are understood. Deferring every decision, however,
would make public contracts, tenant authorization and privacy expensive to
change after real conversations exist.

The architecture therefore needs stable interaction context and ownership rules
without speculative domain implementations.

## Decision

### Stable interaction context

Public contracts, application commands and durable business events carry the
applicable identifiers from this vocabulary:

- `workspace_id` scopes every tenant-owned operation;
- `agent_id` identifies the configured support agent;
- `conversation_id` identifies the customer session;
- `message_id` and `answer_id` provide stable feedback, citation and audit
  targets;
- `visitor_id` is an opaque, workspace-scoped identifier and is not contact
  information;
- `channel` records the ingress adapter, initially `widget`;
- `locale` records the requested or detected language without implying that
  automatic translation occurred;
- `request_id` correlates logs and events;
- `idempotency_key` is required for retryable public mutations.

Identifiers are not trusted merely because a client supplied them. The server
derives or validates tenant and agent scope before invoking domain behavior.

### Principal and authorization boundary

Authentication distinguishes administrator users, workspace members, anonymous
visitors and, when introduced, service principals. Domain operations receive an
authorized principal and workspace context; they do not infer authorization
from an unvalidated role string or a resource identifier alone.

The first product release needs only the smallest role/capability set required
by its use cases. Custom roles, SSO and SCIM do not belong in the foundation.

### Capability ownership

- `tenant` owns workspaces, membership and authorization context.
- `conversation` owns sessions, messages, citations and answer history. It owns
  actor references, not a visitor's contact profile.
- `runtime` owns the answer state machine and the terminal disposition:
  `answered`, `clarify`, `refused` or `handoff`.
- `ingestion` owns source synchronization attempts and retryable processing;
  `knowledge` owns source revisions, normalized chunks and immutable ready
  knowledge versions.
- A future contact or lead capability owns email addresses, phone numbers,
  consent and CRM synchronization. Conversations may hold an opaque
  `contact_ref`, but must not duplicate contact fields in transcript metadata.
- Feedback references a stable `answer_id`. It becomes a separate domain only
  when its moderation, retention or workflow rules justify independent
  ownership.
- Analytics is a projection of business facts, never an alternative source of
  truth for conversations, feedback or usage.
- Billing consumes immutable usage facts and applies versioned plan and price
  rules. It does not own model-provider telemetry.
- Notifications consume committed business events. A notification failure does
  not roll back the originating business operation.

### Channel boundary

The Widget, HTTP/SSE API and future external channels are protocol adapters.
They normalize ingress into application commands and render channel-neutral
runtime results. Conversation state and answer policy must not live in a
browser or channel adapter.

No generic `channel` package is created for the first Widget implementation.
An abstraction is extracted only when a second channel demonstrates a shared
lifecycle or policy beyond transport DTOs.

### Privacy boundary

Contact information, consent and other directly identifying data are separated
from conversation transcripts and have explicit access and retention policies.
Durable events contain opaque references instead of PII unless the event's
documented purpose requires it. Logs, traces, prompts and analytics projections
must not become accidental contact stores.

## Implementation gates

Before the first public conversation contract is published:

- request and event schemas use the stable identifiers above;
- runtime responses expose a terminal disposition, including handoff;
- the public edge defines origin policy, request-size limits, rate-limit keys
  and idempotency behavior;
- SSE reconnect and replay behavior is documented.

Before the first business migration is accepted:

- conversation tables keep visitor identity separate from contact PII;
- ingestion models distinguish source, source revision and synchronization run;
- tenant ownership and authorization constraints are testable at the storage
  boundary;
- the reliable business-event decision in ADR-0003 is implemented for facts
  that cannot be reconstructed later.

## Domain creation rule

A new top-level domain package is added only when there is implemented behavior
with stable ownership, invariants or a distinct lifecycle. A roadmap item, UI
screen or vendor integration alone is not sufficient. When a domain is added,
the architecture boundary registry, commit scope, tests and documentation are
updated in the same change.

## Consequences

Future commercial and multi-channel capabilities have explicit attachment
points without imposing empty packages or generalized plugin infrastructure on
the first release. Some future extractions will still require migrations; the
stable identifiers and ownership rules keep those migrations additive and
prevent PII, analytics and billing concerns from becoming coupled to the answer
path.
