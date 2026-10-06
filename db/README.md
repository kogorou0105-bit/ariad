# Database

`migrations/` owns append-only PostgreSQL schema migrations managed by goose.
Never edit a migration after it has been applied; add the next numbered
migration instead. Goose records applied versions in `goose_db_version`.
`queries/` owns the explicit, workspace-scoped SQL consumed by sqlc. Domain
packages call generated repositories through their own interfaces; HTTP
handlers never construct SQL.

Generated Go code lives in `generated/` and must not be edited by hand. This
repository pins sqlc as a Go tool. Regenerate it from the repository root with:

```sh
go tool sqlc generate -f db/sqlc.yaml
```

With `ARIAD_DATABASE_URL` set, inspect and apply migrations with:

```sh
make migrate-status
make migrate-up
```

The initial schema deliberately keeps opaque visitor identifiers in
`conversation_messages`; it has no contact, email, phone or other PII columns.
Knowledge and conversation records, plus raw usage facts, are immutable at the
database boundary. Outbox event content is immutable too; only a one-time
`published_at` transition is allowed.
