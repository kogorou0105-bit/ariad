# Database

`migrations/` owns append-only PostgreSQL schema migrations. `queries/` owns the explicit SQL consumed by sqlc. Domain packages call generated repositories through their own interfaces; HTTP handlers never construct SQL.
