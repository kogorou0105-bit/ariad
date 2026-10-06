# Local dependency environment

Compose runs PostgreSQL only. Application processes remain outside Compose for
fast reload and debugging.

Start PostgreSQL from the repository root:

```sh
docker compose -f deploy/compose/compose.yaml up -d postgres
```

Use this local connection string:

```sh
export ARIAD_DATABASE_URL='postgres://ariad:ariad@localhost:5432/ariad?sslmode=disable'
```

Apply pending append-only migrations through goose. Goose maintains the
`goose_db_version` table and applies each migration once:

```sh
make migrate-up
```

Stop the dependency without deleting its data:

```sh
docker compose -f deploy/compose/compose.yaml down
```

To explicitly discard the local database as well, add `--volumes` to the
`down` command.
