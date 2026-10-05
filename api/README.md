# API contracts

This directory is reserved for the versioned OpenAPI definition and SSE event
JSON Schemas planned in implementation task 080. No API contract has been
published yet, so this directory is not currently a contract source of truth.

Once task 080 lands, generated clients and server types must derive from files
here and must not be edited by hand.

The initial contract must apply the context, principal, terminal-disposition and
public-ingress gates from
[`ADR-0002`](../docs/decisions/0002-product-context-and-capability-ownership.md).
Usage-producing operations must also identify the durable facts required by
[`ADR-0003`](../docs/decisions/0003-reliable-business-events-and-metering.md).
