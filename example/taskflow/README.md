# Taskflow — the reference craftgo application

A production-shaped, deploy-ready team task-management API, built to be the
**starting point you copy from**. It exercises most of the craftgo DSL in a
realistic domain and ships the full operational story — Docker, docker-compose,
OpenTelemetry, health probes, graceful shutdown, config, and API versioning.

> The other examples (`todo`, `upload`, `raw`, `ecommerce`) are focused feature
> demos. Taskflow is the one to clone for a new service.

---

## Quick start (zero dependencies)

The reference build uses an in-memory store, so it runs with nothing installed:

```bash
cd example/taskflow
go run .
# API   → http://localhost:8080/api/v1/projects
# Docs  → http://localhost:8080/docs
# Ready → http://localhost:8080/readyz
```

```bash
# create a project, then a task in it
curl -X POST localhost:8080/api/projects/v1 \
  -H 'content-type: application/json' \
  -d '{"key":"acme","name":"ACME Website"}'

curl localhost:8080/api/projects/v1
```

## Deploy with Docker

```bash
# from the REPO ROOT (build context is the root because of the module replace):
docker compose -f example/taskflow/docker-compose.yml up --build
```

This brings up the API plus an OpenTelemetry collector; traces and metrics print
to `docker compose logs -f otel-collector`. Container config lives in
[`deploy/config.docker.yaml`](deploy/config.docker.yaml); the image is a static,
non-root, distroless binary that self-healthchecks via `taskflow -healthcheck`.

---

## What it demonstrates

**Architecture**
- Multi-package design with cross-package refs (`shared` scalars/mixins/errors/
  generics reused everywhere).
- **API versioning in one package**: the `project` package holds a single
  `ProjectService`; the primary block is v1 and an `extend service` block adds v2
  (inheriting v1's `@prefix`, `@middlewares`, `@security`, `@tags`). `@group`
  lays the generated code out under `project/v1` and `project/v2`; the URL version
  comes from the method paths (`/v1`, `/v2`), because an `extend` block can't
  redeclare `@prefix`. Result: `/api/projects/v1/...` and `/api/projects/v2/...`
  (v2 evolves the shape with `ownerId` + `description`) from one binary.
- Generic pagination envelope `Page<T>` returned by every list endpoint.
- `@group("admin")` nests the admin service's generated code under `admin/`.

**DSL surface** — scalars (`ID`, `Slug`, `Email`, `HexColor`, `Points`), string
and int enums, mixins (`Timestamps`, `PageParams`), typed errors with bodies,
the full validator set (`@length`/`@minLength`/`@maxLength`, `@gt`/`@gte`/`@lt`/
`@lte`/`@range`, `@multipleOf`, `@pattern`, `@format`, `@minItems`/`@maxItems`/
`@uniqueItems`, `@default`, `@nullable`), every binding (`@path`/`@query`/
`@header`/`@cookie`/`@form`/`@body`), multipart file upload (`@maxSize`/
`@mimeTypes`), `@security` + declared `@middlewares` (+ `@ignoreSecurity`/
`@ignoreMiddleware`), `@sensitive` server-only fields, and OpenAPI controls
(`@status`, `@operationId`, `@summary`, `@deprecated`, `@example`, `@tags`,
`@version`).

**Runtime guards** — `@timeout` on `Reindex` and `@maxBodySize` on the bulk /
upload endpoints, both of which **override** the global defaults set in
`config.yaml` (a per-method value is used as-is, not clamped to the default).

## Project layout

```
design/                 the DSL (source of truth)
  shared/               scalars, mixins, generics, errors, middlewares
  project/              ONE package: v1 primary service + v2 `extend` block
  tasks/  attachments/  admin/
internal/
  types/                generated (project/, tasks/, ... — one dir per package)
  transport/ routes/ service/   generated; project/ is nested project/{v1,v2}
  store/                ← hand-written: in-memory persistence (swap for Postgres)
svccontext/             ← DI container (holds *store.Store)
config/  main.go        ← scaffolded once, then yours to edit
Dockerfile  docker-compose.yml  deploy/  migrations/
```

Regenerate after editing the DSL: `craftgo gen -f design` (types/transport/
routes/openapi are always rewritten; `main.go`, `config/`, `svccontext/`, and the
`internal/service/**` logic files are scaffold-once and never overwritten).

## Swapping in Postgres

The in-memory store keeps the example dependency-free. To go to Postgres:
1. Uncomment the `postgres` service in `docker-compose.yml`.
2. Implement `internal/store` against [`migrations/0001_init.sql`](migrations/0001_init.sql),
   keeping the exact method set (`CreateProject`, `ListTasks`, …) and `Ping`.
3. Add a DSN to `config` and dial the pool in `svccontext.NewServiceContext`.

Nothing else changes — handlers depend only on `*store.Store`, and `/readyz`
already calls `Store.Ping()`.

---

## Notes from building this (craftgo DX log)

Building Taskflow doubled as a dogfooding pass. Findings:

| Area | Observation |
|------|-------------|
| `@default` on an enum | Must reference the **member by name** (`@default(Medium)`, not `@default(2)`) — clear design-time error. 👍 |
| `@default` on required field | Warns to add `?` (the default only fires when absent) — helpful diagnostic. 👍 |
| `@tags(projects-v2)` | A **hyphen** in a bare tag arg fails to parse with a misleading *"expected number after '-'"* (the `-` is read as arithmetic minus). Workaround: `@tags(projectsV2)`. Worth a clearer diagnostic. |
| ID casing | `ownerId` → Go `OwnerID`, but `assigneeIds` → `AssigneeIds` (trailing `Id`→`ID`, plural `Ids` kept). Consistent with Go initialisms. 👍 |
| Versioning (URL) | `@group` sets the on-disk **layout only**, never the URL — the URL is `@prefix` + method path. So version-first URLs (`/api/v1/projects`) need distinct `@prefix` = separate services; one service with `extend` shares the prefix and puts the version in the method path (`/api/projects/v1`). |
| `extend service` + `@prefix` | An `extend` block **can't redeclare `@prefix`** (craftgo: *"not valid; move it to the primary service"*) — it inherits the primary's prefix (and middlewares/security/tags). Clear error, but it's the reason version-in-method-path is how one-service versioning works. |
