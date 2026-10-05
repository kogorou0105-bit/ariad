# Cross-package tests

- `integration/` exercises process and storage boundaries with real local dependencies.
- `security/` covers tenant ID substitution, public-edge limits, SSRF, citation
  forgery, replay, PII leakage and secret leakage.
- `evals/` holds fixed knowledge fixtures and answer-behavior regression cases.

When durable events land, integration tests cover atomic state/outbox writes,
duplicate delivery, idempotent consumers and recovery after queue loss.

Repository architecture checks currently live beside the Go packages in
`internal/architecture`. They scan imports and fail when a domain depends on a
platform adapter or another domain's implementation subpackage.

Package-local unit tests stay beside their implementation.
