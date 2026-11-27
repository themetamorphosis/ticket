# Kacheem Comms ticketing

Buyer site, organizer dashboard and gate scanner for one concert. Next.js
static exports (`web/buyer`, `web/staff`), a Go API and worker (`backend/`),
PostgreSQL as the only source of truth and Redis for the waiting room, rate
limits and display counters. The build plan is the design reference.

## Requirements

Docker with Compose, and GNU make. Go, Node and every other tool run in the
pinned containers named at the top of the [Makefile](Makefile), so local runs
match CI exactly.

## Common commands

```bash
make up
```

```bash
make migrate
```

```bash
make verify
```

`make help` lists every target. `make up` starts PostgreSQL 17 and Redis 8,
`make migrate` applies the schema, and `make reset` deletes the local data.

If ports 5432 or 6379 are already taken on your machine, copy `.env.example` to
`.env` and change `DB_HOST_PORT` and `REDIS_HOST_PORT`. The tests talk to the
services inside the Compose network, so host ports never affect them.

## Layout

| Path | Contents |
| --- | --- |
| `backend/cmd/api` | Public and staff API listeners (Unix sockets behind NGINX) |
| `backend/cmd/worker` | Background jobs, run on separate worker hosts |
| `backend/cmd/migrate` | `up`, `down` and `status` for the embedded Goose migrations |
| `backend/internal/` | Shared packages: config, HTTP, PostgreSQL, Redis, test helpers |
| `backend/migrations/` | Goose SQL migrations, embedded into the binaries |
| `web/buyer`, `web/staff` | Next.js apps built with `output: 'export'` |
| `api/openapi.yaml` | Versioned API contract shared by Go and both frontends |
| `infra/ansible/` | Host roles (staging and production) |

## Working rules

- **Test first.** Write the failing test, make it pass, then refactor.
- **100% coverage gate.** Go statement coverage, and frontend lines,
  statements, functions and branches, must be 100%. Nothing is excluded:
  untestable code is restructured (see the `exit` and `newPool` seams), and
  `make verify` rejects coverage-ignore and lint-disable comments.
- **Real PostgreSQL.** Database and concurrency tests run against the Compose
  server through `internal/pgtest`, which creates a throwaway database per test.
  SQLite and mocks are never used for them.
- **Commit small, never push automatically.** Commit locally after each
  test → code → refactor chunk with a Conventional Commit message. Pushing to
  GitHub is a separate, manual step after `make verify` passes.
